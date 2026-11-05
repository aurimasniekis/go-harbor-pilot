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

// UsageSummary is a usage report.
type UsageSummary struct {
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	GroupBy    string            `json:"group_by"`
	Groups     []UsageGroup      `json:"groups"`
	Totals     []CostAmount      `json:"totals" doc:"Per category and currency; categories are never summed together."`
	Fees       []SubscriptionFee `json:"subscription_fees"`
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

// Summarize builds a usage summary.
func Summarize(ctx context.Context, q store.Execer, snap *cp.Snapshot, from, to time.Time, by string) (*UsageSummary, error) {
	col, ok := groupCols[by]
	if !ok {
		return nil, api.Invalid("unknown group_by", api.FieldError{Location: "query.group_by", Message: "must be one of account, provider, model, binding, project, workspace, billing"})
	}
	type row struct {
		key, kind, cur, billing           string
		n, in, out, cr, cw, rs, cost, unk int64
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (row, error) {
		var x row
		var key, cur, billing sql.NullString
		err := r.Scan(&key, &x.kind, &cur, &billing, &x.n, &x.in, &x.out, &x.cr, &x.cw, &x.rs, &x.cost, &x.unk)
		x.key, x.cur, x.billing = key.String, cur.String, billing.String
		return x, err
	}, `SELECT `+col+`, cost_kind, currency, MIN(billing), COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(cost_e10),0),
		SUM(CASE WHEN kind = 'unknown_usage' THEN 1 ELSE 0 END)
		FROM ledger WHERE at >= ? AND at < ? AND kind IN ('usage','unknown_usage','correction')
		GROUP BY `+col+`, cost_kind, currency`, from.UnixMilli(), to.UnixMilli()) //nolint:gosec // col comes from the static map
	if err != nil {
		return nil, err
	}
	groups := map[string]*UsageGroup{}
	gcosts := map[string]map[costKey]money.Amount{}
	totals := map[costKey]money.Amount{}
	for _, r := range rows {
		g := groups[r.key]
		if g == nil {
			g = &UsageGroup{Key: r.key, Label: label(snap, by, r.key), Billing: r.billing}
			groups[r.key] = g
			gcosts[r.key] = map[costKey]money.Amount{}
		}
		g.Attempts += r.n
		g.UsageTokens.Input += r.in
		g.UsageTokens.Output += r.out
		g.UsageTokens.CacheRead += r.cr
		g.UsageTokens.CacheWrite += r.cw
		g.UsageTokens.Reasoning += r.rs
		g.UsageTokens.UnknownUsage += r.unk
		if r.kind != "none" && r.cur != "" {
			k := costKey{r.kind, r.cur}
			gcosts[r.key][k] += money.Amount(r.cost)
			totals[k] += money.Amount(r.cost)
		}
	}
	s := &UsageSummary{From: from, To: to, GroupBy: by, Groups: []UsageGroup{}, Fees: []SubscriptionFee{}, Categories: categories}
	// Subscription fees prorated to the range and allocated by tokens.
	alloc := map[string]map[costKey]money.Amount{}
	if snap != nil {
		for _, e := range snap.Entitlements {
			fee, err := money.ParseDecimal(e.Spec.Fee)
			if err != nil || fee <= 0 {
				continue
			}
			pr := prorate(e.Spec, fee, from, to)
			if pr <= 0 {
				continue
			}
			s.Fees = append(s.Fees, SubscriptionFee{EntitlementID: e.ID, AccountID: e.Spec.AccountID, PlanName: e.Spec.PlanName, Period: e.Spec.Period,
				Fee: fee.String(), Prorated: pr.String(), Currency: e.Spec.Currency})
			totals[costKey{"subscription_fee", e.Spec.Currency}] += pr
			shares, err := tokenShares(ctx, q, col, e.Spec.AccountID, from, to)
			if err != nil {
				return nil, err
			}
			var total int64
			for _, v := range shares {
				total += v
			}
			if total == 0 {
				continue
			}
			for key, v := range shares {
				if alloc[key] == nil {
					alloc[key] = map[costKey]money.Amount{}
				}
				alloc[key][costKey{"allocation", e.Spec.Currency}] += money.Amount(float64(pr) * float64(v) / float64(total))
			}
		}
	}
	for key, g := range groups {
		g.Costs = moneyList(gcosts[key])
		g.Allocated = moneyList(alloc[key])
		s.Groups = append(s.Groups, *g)
	}
	sort.Slice(s.Groups, func(i, j int) bool {
		ti := s.Groups[i].UsageTokens.Input + s.Groups[i].UsageTokens.Output
		tj := s.Groups[j].UsageTokens.Input + s.Groups[j].UsageTokens.Output
		if ti != tj {
			return ti > tj
		}
		return s.Groups[i].Key < s.Groups[j].Key
	})
	sort.Slice(s.Fees, func(i, j int) bool { return s.Fees[i].EntitlementID < s.Fees[j].EntitlementID })
	s.Totals = moneyList(totals)
	return s, nil
}

func tokenShares(ctx context.Context, q store.Execer, col, accountID string, from, to time.Time) (map[string]int64, error) {
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (struct {
		k string
		v int64
	}, error) {
		var x struct {
			k string
			v int64
		}
		var k sql.NullString
		err := r.Scan(&k, &x.v)
		x.k = k.String
		return x, err
	}, `SELECT `+col+`, COALESCE(SUM(COALESCE(input_tokens,0) + COALESCE(output_tokens,0) + COALESCE(cache_write_tokens,0)), 0)
		FROM ledger WHERE account_id = ? AND at >= ? AND at < ? AND kind = 'usage' GROUP BY `+col, accountID, from.UnixMilli(), to.UnixMilli()) //nolint:gosec // static column
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
		From    string `query:"from" doc:"RFC 3339; default 30 days before to."`
		To      string `query:"to" doc:"RFC 3339; default now."`
		GroupBy string `query:"group_by" enum:"account,provider,model,binding,project,workspace,billing," required:"false"`
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
		s, err := Summarize(ctx, d.DB().R, d.Snapshot(), from, to, by)
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
