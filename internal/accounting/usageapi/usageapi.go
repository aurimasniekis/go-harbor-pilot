// Package usageapi reports usage and cost from the ledger. Every money
// figure carries its category; categories are never added together:
//
//   - gateway-observed consumption (tokens and requests seen by the gateway)
//   - estimated_api_charge (metered usage priced at configured list prices)
//   - actual_api_charge (charges reported by a provider's billing API)
//   - list_equivalent (subscription usage priced at list rates; not an invoice)
//   - subscription fees (from entitlements, prorated to the range)
//   - internal allocation (subscription fees split by observed tokens)
//   - provider-reported quota (see the quota board)
package usageapi

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Deps are the API's dependencies.
type Deps struct {
	DB       func() *store.DB
	Snapshot func() *cp.Snapshot
	Clock    clock.Clock
}

// UsageTokens sums token counts. Unknown counts are not included; UnknownUsage
// counts attempts whose usage was never reported.
type UsageTokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite   int64 `json:"cache_write"`
	Reasoning    int64 `json:"reasoning"`
	UnknownUsage int64 `json:"unknown_usage_attempts" doc:"Attempts whose usage was not reported (counted, never assumed zero)."`
}

// CostAmount is an amount in one category and currency.
type CostAmount struct {
	Kind     string `json:"kind" enum:"estimated_api_charge,actual_api_charge,list_equivalent,subscription_fee,allocation"`
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	amount   money.Amount
}

// UsageGroup is one row of a usage summary.
type UsageGroup struct {
	Key         string       `json:"key"`
	Label       string       `json:"label"`
	Billing     string       `json:"billing,omitempty"`
	Attempts    int64        `json:"attempts"`
	UsageTokens UsageTokens  `json:"tokens"`
	Costs       []CostAmount `json:"costs"`
	// Allocated is this group's share of subscription fees.
	Allocated []CostAmount `json:"allocated"`
}

// SubscriptionFee is a subscription fee prorated to the range.
type SubscriptionFee struct {
	EntitlementID string `json:"entitlement_id"`
	AccountID     string `json:"account_id"`
	PlanName      string `json:"plan_name"`
	Period        string `json:"period"`
	Fee           string `json:"fee" doc:"SubscriptionFee per period."`
	Prorated      string `json:"prorated" doc:"Share of the fee that falls in the range."`
	Currency      string `json:"currency"`
}

// SummaryFilters echoes the filters a summary was computed with.
type SummaryFilters struct {
	Refs      map[string]string `json:"refs,omitempty" doc:"Trusted-reference filters (ANDed)."`
	BindingID string            `json:"binding_id,omitempty"`
}

// UsageSummary is a usage report.
type UsageSummary struct {
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	GroupBy    string            `json:"group_by"`
	Filters    *SummaryFilters   `json:"filters,omitempty" required:"false" doc:"Present when a filter was applied."`
	Groups     []UsageGroup      `json:"groups"`
	NextCursor string            `json:"next_cursor,omitempty" required:"false" doc:"With group_by=ref:<key>: present when more groups are available."`
	Totals     []CostAmount      `json:"totals" doc:"Per category and currency over every matching row (all pages); categories are never summed together. subscription_fee is the whole-range fee and ignores filters."`
	Fees       []SubscriptionFee `json:"subscription_fees" doc:"Whole-range fees; filters do not apply to them."`
	Categories []string          `json:"categories"`
}

// UsagePoint is one bucket of a time series.
type UsagePoint struct {
	T           time.Time    `json:"t"`
	Attempts    int64        `json:"attempts"`
	UsageTokens UsageTokens  `json:"tokens"`
	Costs       []CostAmount `json:"costs"`
}

var groupCols = map[string]string{
	"account": "account_id", "provider": "provider_id", "model": "model", "binding": "binding_id",
	"project": "project_id", "workspace": "workspace_id", "billing": "billing",
}

var categories = []string{
	"gateway_observed: tokens and attempts seen by the gateway",
	"estimated_api_charge: metered usage priced at configured list prices",
	"actual_api_charge: charges reported by provider billing APIs",
	"list_equivalent: subscription usage valued at list prices (not an invoice)",
	"subscription_fee: entitlement fees prorated to the range",
	"allocation: subscription fees split across groups by observed tokens (internal)",
}

func label(s *cp.Snapshot, by, key string) string {
	if s == nil || key == "" {
		return key
	}
	switch by {
	case "account":
		if a := s.Accounts[key]; a != nil {
			return a.Name
		}
	case "provider":
		if p := s.Providers[key]; p != nil {
			return p.Name
		}
	case "binding":
		if b := s.Bindings[key]; b != nil {
			return b.Name
		}
	case "project":
		if p := s.Projects[key]; p != nil {
			return p.Name
		}
	case "workspace":
		if w := s.Workspaces[key]; w != nil {
			return w.Name
		}
	}
	return key
}

type costKey struct{ kind, cur string }

func moneyList(m map[costKey]money.Amount) []CostAmount {
	out := make([]CostAmount, 0, len(m))
	for k, v := range m {
		out = append(out, CostAmount{Kind: k.kind, Currency: k.cur, Amount: v.String(), amount: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Currency < out[j].Currency
	})
	return out
}

// SummaryQuery selects what a usage summary covers.
type SummaryQuery struct {
	From, To time.Time
	// By is a static dimension (account, provider, model, binding, project,
	// workspace, billing) or ref:<key> for a trusted reference key.
	By        string
	Refs      []api.RefFilter
	BindingID string
	// Cursor and Limit page the groups of a ref:<key> grouping, ordered by
	// group key. Limit 0 returns every group.
	Cursor string
	Limit  int
}

// refGroupPrefix marks a group_by on a trusted reference key.
const refGroupPrefix = "ref:"

// groupSpec is how a group_by dimension maps onto the ledger (aliased l).
// Column names and SQL come from static code; the ref key is a bound
// argument of join.
type groupSpec struct {
	expr  string
	join  string
	args  []any
	isRef bool
}

func resolveGroup(by string) (groupSpec, error) {
	if col, ok := groupCols[by]; ok {
		return groupSpec{expr: `COALESCE(l.` + col + `, '')`}, nil
	}
	if key, ok := strings.CutPrefix(by, refGroupPrefix); ok && cp.ValidRefKey(key) {
		return groupSpec{expr: `COALESCE(gr.value, '')`, join: ` LEFT JOIN ledger_refs gr ON gr.ledger_id = l.id AND gr.key = ?`,
			args: []any{key}, isRef: true}, nil
	}
	return groupSpec{}, api.Invalid("unknown group_by", api.FieldError{Location: "query.group_by",
		Message: "must be one of account, provider, model, binding, project, workspace, billing or ref:<key>"})
}

// filterWhere builds the ledger conditions shared by every summary query:
// the time range plus the binding and trusted-reference filters.
func filterWhere(sq SummaryQuery) (string, []any) {
	from, to := sq.From.UnixMilli(), sq.To.UnixMilli()
	where := `l.at >= ? AND l.at < ?`
	args := []any{from, to}
	if sq.BindingID != "" {
		where += ` AND l.binding_id = ?`
		args = append(args, sq.BindingID)
	}
	for _, f := range sq.Refs {
		where += ` AND l.id IN (SELECT ledger_id FROM ledger_refs WHERE key = ? AND value = ? AND at >= ? AND at < ?)`
		args = append(args, f.Key, f.Value, from, to)
	}
	return where, args
}

const groupCursorPrefix = "g|"

// Summarize builds a usage summary.
func Summarize(ctx context.Context, q store.Execer, snap *cp.Snapshot, sq SummaryQuery) (*UsageSummary, error) {
	g, err := resolveGroup(sq.By)
	if err != nil {
		return nil, err
	}
	fwhere, fargs := filterWhere(sq)
	usageKinds := ` AND l.kind IN ('usage','unknown_usage','correction')`

	// Page the groups of a ref grouping by key. Totals still cover every
	// matching row.
	var pageCond string
	var pageArgs []any
	var next string
	if g.isRef && sq.Limit > 0 {
		cond, args := fwhere+usageKinds, append([]any{}, fargs...)
		if sq.Cursor != "" {
			after, ok := strings.CutPrefix(sq.Cursor, groupCursorPrefix)
			if !ok {
				return nil, api.Invalid("invalid cursor", api.FieldError{Location: "query.cursor", Message: "malformed cursor"})
			}
			cond += ` AND ` + g.expr + ` > ?`
			args = append(args, after)
		}
		keys, err := store.Query(ctx, q, func(r *sql.Rows) (string, error) {
			var k string
			return k, r.Scan(&k)
		}, `SELECT DISTINCT `+g.expr+` FROM ledger l`+g.join+` WHERE `+cond+` ORDER BY 1 LIMIT ?`, //nolint:gosec // expr and join are static
			append(append(append([]any{}, g.args...), args...), sq.Limit+1)...)
		if err != nil {
			return nil, err
		}
		if len(keys) > sq.Limit {
			keys = keys[:sq.Limit]
			next = api.EncodeCursor(groupCursorPrefix + keys[len(keys)-1])
		}
		if len(keys) == 0 {
			pageCond = ` AND 0`
		} else {
			pageCond = ` AND ` + g.expr + ` IN (` + strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",") + `)`
			for _, k := range keys {
				pageArgs = append(pageArgs, k)
			}
		}
	}

	type row struct {
		key, kind, cur, billing           string
		n, in, out, cr, cw, rs, cost, unk int64
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (row, error) {
		var x row
		var cur, billing sql.NullString
		err := r.Scan(&x.key, &x.kind, &cur, &billing, &x.n, &x.in, &x.out, &x.cr, &x.cw, &x.rs, &x.cost, &x.unk)
		x.cur, x.billing = cur.String, billing.String
		return x, err
	}, `SELECT `+g.expr+`, l.cost_kind, l.currency, MIN(l.billing), COUNT(*), COALESCE(SUM(l.input_tokens),0), COALESCE(SUM(l.output_tokens),0),
		COALESCE(SUM(l.cache_read_tokens),0), COALESCE(SUM(l.cache_write_tokens),0), COALESCE(SUM(l.reasoning_tokens),0), COALESCE(SUM(l.cost_e10),0),
		SUM(CASE WHEN l.kind = 'unknown_usage' THEN 1 ELSE 0 END)
		FROM ledger l`+g.join+` WHERE `+fwhere+usageKinds+pageCond+`
		GROUP BY 1, l.cost_kind, l.currency`, append(append(append([]any{}, g.args...), fargs...), pageArgs...)...) //nolint:gosec // expr and join are static
	if err != nil {
		return nil, err
	}
	groups := map[string]*UsageGroup{}
	gcosts := map[string]map[costKey]money.Amount{}
	for _, r := range rows {
		gr := groups[r.key]
		if gr == nil {
			gr = &UsageGroup{Key: r.key, Label: label(snap, sq.By, r.key), Billing: r.billing}
			groups[r.key] = gr
			gcosts[r.key] = map[costKey]money.Amount{}
		}
		gr.Attempts += r.n
		gr.UsageTokens.Input += r.in
		gr.UsageTokens.Output += r.out
		gr.UsageTokens.CacheRead += r.cr
		gr.UsageTokens.CacheWrite += r.cw
		gr.UsageTokens.Reasoning += r.rs
		gr.UsageTokens.UnknownUsage += r.unk
		if r.kind != "none" && r.cur != "" {
			gcosts[r.key][costKey{r.kind, r.cur}] += money.Amount(r.cost)
		}
	}
	// Totals over every matching row, independent of the group page.
	totals := map[costKey]money.Amount{}
	type trow struct {
		kind, cur string
		cost      int64
	}
	trows, err := store.Query(ctx, q, func(r *sql.Rows) (trow, error) {
		var x trow
		var cur sql.NullString
		err := r.Scan(&x.kind, &cur, &x.cost)
		x.cur = cur.String
		return x, err
	}, `SELECT l.cost_kind, l.currency, COALESCE(SUM(l.cost_e10),0) FROM ledger l WHERE `+fwhere+usageKinds+` GROUP BY 1, 2`, fargs...) //nolint:gosec // static SQL
	if err != nil {
		return nil, err
	}
	for _, r := range trows {
		if r.kind != "none" && r.cur != "" {
			totals[costKey{r.kind, r.cur}] += money.Amount(r.cost)
		}
	}
	s := &UsageSummary{From: sq.From, To: sq.To, GroupBy: sq.By, Groups: []UsageGroup{}, NextCursor: next, Fees: []SubscriptionFee{}, Categories: categories}
	if len(sq.Refs) > 0 || sq.BindingID != "" {
		s.Filters = &SummaryFilters{Refs: api.RefMap(sq.Refs), BindingID: sq.BindingID}
	}
	// Subscription fees prorated to the range (whole-range figures, never
	// filtered) and allocated by tokens. Under a filter each group gets its
	// share of the account's unfiltered tokens, not the whole fee.
	alloc := map[string]map[costKey]money.Amount{}
	if snap != nil {
		for _, e := range snap.Entitlements {
			fee, err := money.ParseDecimal(e.Spec.Fee)
			if err != nil || fee <= 0 {
				continue
			}
			pr := prorate(e.Spec, fee, sq.From, sq.To)
			if pr <= 0 {
				continue
			}
			s.Fees = append(s.Fees, SubscriptionFee{EntitlementID: e.ID, AccountID: e.Spec.AccountID, PlanName: e.Spec.PlanName, Period: e.Spec.Period,
				Fee: fee.String(), Prorated: pr.String(), Currency: e.Spec.Currency})
			totals[costKey{"subscription_fee", e.Spec.Currency}] += pr
			total, err := accountTokens(ctx, q, e.Spec.AccountID, sq.From, sq.To)
			if err != nil {
				return nil, err
			}
			if total == 0 {
				continue
			}
			shares, err := tokenShares(ctx, q, g, fwhere, fargs, pageCond, pageArgs, e.Spec.AccountID)
			if err != nil {
				return nil, err
			}
			for key, v := range shares {
				if alloc[key] == nil {
					alloc[key] = map[costKey]money.Amount{}
				}
				alloc[key][costKey{"allocation", e.Spec.Currency}] += money.Amount(float64(pr) * float64(v) / float64(total))
			}
		}
	}
	for key, gr := range groups {
		gr.Costs = moneyList(gcosts[key])
		gr.Allocated = moneyList(alloc[key])
		s.Groups = append(s.Groups, *gr)
	}
	if g.isRef {
		// Stable key order, which is also the page order.
		sort.Slice(s.Groups, func(i, j int) bool { return s.Groups[i].Key < s.Groups[j].Key })
	} else {
		sort.Slice(s.Groups, func(i, j int) bool {
			ti := s.Groups[i].UsageTokens.Input + s.Groups[i].UsageTokens.Output
			tj := s.Groups[j].UsageTokens.Input + s.Groups[j].UsageTokens.Output
			if ti != tj {
				return ti > tj
			}
			return s.Groups[i].Key < s.Groups[j].Key
		})
	}
	sort.Slice(s.Fees, func(i, j int) bool { return s.Fees[i].EntitlementID < s.Fees[j].EntitlementID })
	s.Totals = moneyList(totals)
	return s, nil
}

// allocationSum sums the tokens a subscription allocation is split by.
const allocationSum = `COALESCE(SUM(COALESCE(l.input_tokens,0) + COALESCE(l.output_tokens,0) + COALESCE(l.cache_write_tokens,0)), 0)`

// accountTokens is an account's unfiltered allocation denominator.
func accountTokens(ctx context.Context, q store.Execer, accountID string, from, to time.Time) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT `+allocationSum+` FROM ledger l WHERE l.account_id = ? AND l.at >= ? AND l.at < ? AND l.kind = 'usage'`,
		accountID, from.UnixMilli(), to.UnixMilli()).Scan(&n)
	return n, err
}

// tokenShares sums an account's filtered tokens per group.
func tokenShares(ctx context.Context, q store.Execer, g groupSpec, fwhere string, fargs []any, pageCond string, pageArgs []any, accountID string) (map[string]int64, error) {
	type share struct {
		k string
		v int64
	}
	args := append(append(append(append([]any{}, g.args...), fargs...), pageArgs...), accountID)
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (share, error) {
		var x share
		err := r.Scan(&x.k, &x.v)
		return x, err
	}, `SELECT `+g.expr+`, `+allocationSum+` FROM ledger l`+g.join+` WHERE `+fwhere+pageCond+` AND l.kind = 'usage' AND l.account_id = ? GROUP BY 1`, args...) //nolint:gosec // static SQL
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		if r.v > 0 {
			out[r.k] = r.v
		}
	}
	return out, nil
}

// prorate returns the share of a periodic fee that falls in [from, to),
// bounded by the entitlement's start and end dates.
func prorate(e cp.EntitlementSpec, fee money.Amount, from, to time.Time) money.Amount {
	start := from
	if t, err := time.Parse("2006-01-02", e.StartsOn); err == nil && t.After(start) {
		start = t
	}
	end := to
	if e.EndsOn != "" {
		if t, err := time.Parse("2006-01-02", e.EndsOn); err == nil && t.Before(end) {
			end = t
		}
	}
	if !end.After(start) {
		return 0
	}
	period := 30.4375 * 24 * float64(time.Hour)
	if e.Period == "yearly" {
		period = 365.25 * 24 * float64(time.Hour)
	}
	return money.Amount(float64(fee) * float64(end.Sub(start)) / period)
}

// Series builds a time series over hour or day buckets.
func Series(ctx context.Context, q store.Execer, from, to time.Time, step string, filterCol, filterVal string) ([]UsagePoint, error) {
	var cond string
	var cargs []any
	if filterCol != "" {
		cond, cargs = filterCol+` = ?`, []any{filterVal}
	}
	return seriesWhere(ctx, q, from, to, step, cond, cargs)
}

// stepMillis is the bucket size of a series step.
func stepMillis(step string) int64 {
	switch step {
	case "day":
		return int64(24 * time.Hour / time.Millisecond)
	case "week":
		return int64(7 * 24 * time.Hour / time.Millisecond)
	}
	return int64(time.Hour / time.Millisecond)
}

// seriesWhere buckets ledger usage matching an extra condition (column
// names come from static maps, never from input).
func seriesWhere(ctx context.Context, q store.Execer, from, to time.Time, step, cond string, cargs []any) ([]UsagePoint, error) {
	size := stepMillis(step)
	where := `at >= ? AND at < ? AND kind IN ('usage','unknown_usage','correction')`
	args := []any{size, size, from.UnixMilli(), to.UnixMilli()}
	if cond != "" {
		where += ` AND ` + cond
		args = append(args, cargs...)
	}
	type row struct {
		b                                 int64
		kind, cur                         string
		n, in, out, cr, cw, rs, cost, unk int64
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (row, error) {
		var x row
		var cur sql.NullString
		err := r.Scan(&x.b, &x.kind, &cur, &x.n, &x.in, &x.out, &x.cr, &x.cw, &x.rs, &x.cost, &x.unk)
		x.cur = cur.String
		return x, err
	}, `SELECT (at / ?) * ?, cost_kind, currency, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(cost_e10),0),
		SUM(CASE WHEN kind = 'unknown_usage' THEN 1 ELSE 0 END)
		FROM ledger WHERE `+where+` GROUP BY 1, cost_kind, currency ORDER BY 1`, args...) //nolint:gosec // filterCol is from a static map
	if err != nil {
		return nil, err
	}
	byT := map[int64]*UsagePoint{}
	costs := map[int64]map[costKey]money.Amount{}
	var keys []int64
	for _, r := range rows {
		p := byT[r.b]
		if p == nil {
			p = &UsagePoint{T: time.UnixMilli(r.b).UTC()}
			byT[r.b] = p
			costs[r.b] = map[costKey]money.Amount{}
			keys = append(keys, r.b)
		}
		p.Attempts += r.n
		p.UsageTokens.Input += r.in
		p.UsageTokens.Output += r.out
		p.UsageTokens.CacheRead += r.cr
		p.UsageTokens.CacheWrite += r.cw
		p.UsageTokens.Reasoning += r.rs
		p.UsageTokens.UnknownUsage += r.unk
		if r.kind != "none" && r.cur != "" {
			costs[r.b][costKey{r.kind, r.cur}] += money.Amount(r.cost)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]UsagePoint, 0, len(keys))
	for _, k := range keys {
		p := byT[k]
		p.Costs = moneyList(costs[k])
		out = append(out, *p)
	}
	return out, nil
}

// SeriesGroup is the series of one group value.
type SeriesGroup struct {
	Key    string       `json:"key" doc:"Group value (id or name); \"other\" for the remaining groups, \"\" when unattributed."`
	Points []UsagePoint `json:"points"`
}

func seriesGroups(ctx context.Context, q store.Execer, from, to time.Time, step, filterCol, filterVal, byCol string, top int) ([]SeriesGroup, error) {
	if top <= 0 {
		top = 6
	}
	base := `at >= ? AND at < ? AND kind IN ('usage','unknown_usage','correction')`
	args := []any{from.UnixMilli(), to.UnixMilli()}
	var fcond string
	var fargs []any
	if filterCol != "" {
		fcond, fargs = filterCol+` = ?`, []any{filterVal}
		base += ` AND ` + fcond
		args = append(args, fargs...)
	}
	keys, err := store.Query(ctx, q, func(r *sql.Rows) (string, error) {
		var k sql.NullString
		var n int64
		err := r.Scan(&k, &n)
		return k.String, err
	}, `SELECT COALESCE(`+byCol+`, ''), COUNT(*) FROM ledger WHERE `+base+` GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT ?`, append(args, top+1)...) //nolint:gosec // byCol is from a static map
	if err != nil {
		return nil, err
	}
	more := len(keys) > top
	if more {
		keys = keys[:top]
	}
	and := func(c string) string {
		if fcond == "" {
			return c
		}
		return fcond + ` AND ` + c
	}
	out := make([]SeriesGroup, 0, len(keys)+1)
	for _, k := range keys {
		pts, err := seriesWhere(ctx, q, from, to, step, and(`COALESCE(`+byCol+`, '') = ?`), append(append([]any{}, fargs...), k))
		if err != nil {
			return nil, err
		}
		out = append(out, SeriesGroup{Key: k, Points: pts})
	}
	if more {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
		oargs := append([]any{}, fargs...)
		for _, k := range keys {
			oargs = append(oargs, k)
		}
		pts, err := seriesWhere(ctx, q, from, to, step, and(`COALESCE(`+byCol+`, '') NOT IN (`+ph+`)`), oargs)
		if err != nil {
			return nil, err
		}
		out = append(out, SeriesGroup{Key: "other", Points: pts})
	}
	return out, nil
}

func parseRange(c clock.Clock, from, to string, def time.Duration) (time.Time, time.Time, error) {
	now := c.Now().UTC()
	t1 := now
	t0 := now.Add(-def)
	if to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return t0, t1, api.Invalid("invalid to", api.FieldError{Location: "query.to", Message: "must be RFC 3339"})
		}
		t1 = t
	}
	if from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return t0, t1, api.Invalid("invalid from", api.FieldError{Location: "query.from", Message: "must be RFC 3339"})
		}
		t0 = t
	} else {
		t0 = t1.Add(-def)
	}
	if !t1.After(t0) || t1.Sub(t0) > 400*24*time.Hour {
		return t0, t1, api.Invalid("invalid range", api.FieldError{Location: "query.from", Message: "range must be positive and at most 400 days"})
	}
	return t0, t1, nil
}

// Register mounts the usage endpoints.
func Register(a *api.API, d Deps) {
	tags := []string{"Usage"}
	type sumIn struct {
		From      string   `query:"from" doc:"RFC 3339; default 30 days before to."`
		To        string   `query:"to" doc:"RFC 3339; default now."`
		GroupBy   string   `query:"group_by" required:"false" pattern:"^(account|provider|model|binding|project|workspace|billing|ref:[a-z0-9][a-z0-9._-]{0,63})?$" doc:"account (default), provider, model, binding, project, workspace, billing, or ref:<key> to group by a trusted reference key (rows without the key form the \"\" group)."`
		Refs      []string `query:"ref,explode" required:"false" maxItems:"4" doc:"Trusted-reference filter <key>:<value>, split at the first colon. Repeatable (at most 4, ANDed)."`
		BindingID string   `query:"binding_id" maxLength:"64" doc:"Only usage of this client binding."`
		Cursor    string   `query:"cursor" doc:"With group_by=ref:<key>: opaque cursor from a previous page's next_cursor."`
		Limit     int      `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"With group_by=ref:<key>: maximum groups to return."`
	}
	type sumOut struct{ Body *UsageSummary }
	api.Register(a, api.Op{ID: "usage-summary", Method: http.MethodGet, Path: "/usage/summary", Summary: "Usage and cost by category",
		Tags: tags, Permission: auth.PermUsageRead}, func(ctx context.Context, in *sumIn) (*sumOut, error) {
		from, to, err := parseRange(d.Clock, in.From, in.To, 30*24*time.Hour)
		if err != nil {
			return nil, err
		}
		by := in.GroupBy
		if by == "" {
			by = "account"
		}
		refs, err := api.ParseRefFilters(in.Refs)
		if err != nil {
			return nil, err
		}
		cursor, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		s, err := Summarize(ctx, d.DB().R, d.Snapshot(), SummaryQuery{From: from, To: to, By: by, Refs: refs, BindingID: in.BindingID,
			Cursor: cursor, Limit: limit})
		if err != nil {
			return nil, err
		}
		return &sumOut{Body: s}, nil
	})
	type serIn struct {
		From    string `query:"from"`
		To      string `query:"to"`
		Step    string `query:"step" enum:"hour,day,week," required:"false"`
		Filter  string `query:"filter" enum:"account,provider,model,binding,project,workspace,billing," required:"false"`
		FilterV string `query:"value" maxLength:"200"`
		By      string `query:"by" enum:"account,provider,model,binding,project,workspace,billing," required:"false" doc:"Also split the series by this dimension (top groups by attempts, the rest as other)."`
		Top     int    `query:"top" minimum:"0" maximum:"20" doc:"Groups returned with by (default 6)."`
	}
	type serBody struct {
		From   time.Time     `json:"from"`
		To     time.Time     `json:"to"`
		Step   string        `json:"step"`
		Points []UsagePoint  `json:"points"`
		Groups []SeriesGroup `json:"groups,omitempty" required:"false" doc:"Present with by: one series per group; key \"other\" holds the remaining groups."`
	}
	type serOut struct{ Body serBody }
	api.Register(a, api.Op{ID: "usage-series", Method: http.MethodGet, Path: "/usage/series", Summary: "Usage and cost over time",
		Tags: tags, Permission: auth.PermUsageRead}, func(ctx context.Context, in *serIn) (*serOut, error) {
		def := 7 * 24 * time.Hour
		step := in.Step
		if step == "" {
			step = "hour"
		}
		switch step {
		case "day":
			def = 30 * 24 * time.Hour
		case "week":
			def = 90 * 24 * time.Hour
		}
		from, to, err := parseRange(d.Clock, in.From, in.To, def)
		if err != nil {
			return nil, err
		}
		if step == "hour" && to.Sub(from) > 31*24*time.Hour {
			return nil, api.Invalid("range too long for hourly steps", api.FieldError{Location: "query.step", Message: "use day for ranges over 31 days"})
		}
		col := ""
		if in.Filter != "" {
			col = groupCols[in.Filter]
		}
		pts, err := Series(ctx, d.DB().R, from, to, step, col, in.FilterV)
		if err != nil {
			return nil, err
		}
		body := serBody{From: from, To: to, Step: step, Points: pts}
		if in.By != "" {
			if body.Groups, err = seriesGroups(ctx, d.DB().R, from, to, step, col, in.FilterV, groupCols[in.By], in.Top); err != nil {
				return nil, err
			}
		}
		return &serOut{Body: body}, nil
	})
}
