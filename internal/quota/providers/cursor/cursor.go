// Package cursor implements the Cursor Admin API adapter: current billing
// cycle team spend (POST /teams/spend), per-member spending limits and daily
// request counts (POST /teams/daily-usage-data).
//
// Member identities are never exported: emails and names returned by the API
// are discarded and members are referred to by a short one-way hash of their
// Cursor user ID.
package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID identifies the adapter.
const ProviderID domain.ProviderID = "cursor"

// DefaultBaseURL is the documented Admin API origin.
const DefaultBaseURL = "https://api.cursor.com"

const (
	pageSize = 100
	// maxPages bounds pagination per endpoint (pageSize × maxPages members).
	maxPages = 20
	// maxMemberBudgets bounds the number of per-member limit buckets.
	maxMemberBudgets = 50
)

// Options configure the adapter.
type Options struct {
	BaseURL string `json:"base_url"`
	// LookbackDays of daily usage to (re)fetch each poll (1..30; the API
	// allows at most a 30-day range).
	LookbackDays int `json:"lookback_days"`
	// MemberBudgets emits one budget bucket per member that has a monthly
	// spending limit (default true).
	MemberBudgets *bool `json:"member_budgets"`
}

type parsedOpts struct {
	Options
	base     string
	lookback int
}

func parse(cfg providers.AccountConfig) (*parsedOpts, error) {
	var o Options
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return nil, err
	}
	p := &parsedOpts{Options: o, base: DefaultBaseURL, lookback: 7}
	if o.BaseURL != "" {
		b, err := providers.ValidateBaseURL(o.BaseURL)
		if err != nil {
			return nil, err
		}
		p.base = b
	}
	if o.LookbackDays != 0 {
		if o.LookbackDays < 1 || o.LookbackDays > 30 {
			return nil, providers.NewError(domain.ErrConfiguration, "options.lookback_days must be 1..30 (the API limits ranges to 30 days)")
		}
		p.lookback = o.LookbackDays
	}
	if _, err := providers.RequireSecret(cfg, "api_key"); err != nil {
		return nil, err
	}
	return p, nil
}

// Adapter implements providers.Adapter.
type Adapter struct {
	http *providers.HTTPClient
}

var _ providers.Adapter = (*Adapter)(nil)

// New creates the adapter.
func New(deps providers.Deps) *Adapter { return &Adapter{http: deps.HTTP} }

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "Cursor (Admin API)", Family: "cursor", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes: []string{"Admin API key (team admin)"},
		// The Admin API allows 20 requests/minute per team and daily usage
		// data is recommended to be polled at most hourly.
		DefaultPollInterval: time.Hour,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapMonetarySpend:     p("current billing cycle team spend only (total and on-demand); no per-day cost"),
			domain.CapRequestCounters:   p("daily request counts (subscription-included, usage-based, API key); raw events, not billable units"),
			domain.CapBudget:            p("per-member monthly spending limits; no team-wide limit"),
			domain.CapHistoricalBackfil: p("daily usage ≤30 days per request"),
			domain.CapOrgProjectScope:   s("team scope"),
			domain.CapTokenCounters:     u("token counts are not exposed by these endpoints"),
			domain.CapModelUsage:        u("only a per-member most-used model is reported, not counts per model"),
			domain.CapQuotaRemaining:    u("no included-request allowance remaining is exposed"),
			domain.CapMonetaryBalance:   u("no balance API"),
		}),
		Limitations: []string{
			"Spend covers the current billing cycle only; the next cycle start is not reported.",
			"Daily request counts are raw events, not billable units.",
			"Member emails and names are discarded; members are identified by a hashed user ID.",
			"Rate limited to 20 requests/minute per team; polled hourly by default.",
		},
		DocsURLs: []string{"https://cursor.com/docs/account/teams/admin-api"},
	}
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	_, err := parse(cfg)
	return err
}

func headers(key string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key+":")))
	return h
}

// memberRef returns a stable, non-reversible reference to a Cursor user ID.
func memberRef(id string) string {
	if id == "" {
		return ""
	}
	h := sha256.Sum256([]byte("cursor-member:" + id))
	return "m_" + hex.EncodeToString(h[:4])
}

// rawID renders a JSON string or number ID as text.
func rawID(m json.RawMessage) string {
	var s string
	if json.Unmarshal(m, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(m, &n) == nil {
		return n.String()
	}
	return ""
}

type memberSpend struct {
	UserID                       json.RawMessage `json:"userId"`
	SpendCents                   *json.Number    `json:"spendCents"`
	OverallSpendCents            *json.Number    `json:"overallSpendCents"`
	FastPremiumRequests          *json.Number    `json:"fastPremiumRequests"`
	HardLimitOverrideDollars     *json.Number    `json:"hardLimitOverrideDollars"`
	MonthlyLimitDollars          *json.Number    `json:"monthlyLimitDollars"`
	EffectivePerUserLimitDollars *json.Number    `json:"effectivePerUserLimitDollars"`
}

type spendPage struct {
	TeamMemberSpend        []memberSpend `json:"teamMemberSpend"`
	SubscriptionCycleStart *json.Number  `json:"subscriptionCycleStart"`
	TotalMembers           int           `json:"totalMembers"`
	TotalPages             int           `json:"totalPages"`
}

type dailyRow struct {
	UserID                   json.RawMessage `json:"userId"`
	Day                      string          `json:"day"`
	Date                     *json.Number    `json:"date"`
	ComposerRequests         *json.Number    `json:"composerRequests"`
	ChatRequests             *json.Number    `json:"chatRequests"`
	AgentRequests            *json.Number    `json:"agentRequests"`
	SubscriptionIncludedReqs *json.Number    `json:"subscriptionIncludedReqs"`
	UsageBasedReqs           *json.Number    `json:"usageBasedReqs"`
	APIKeyReqs               *json.Number    `json:"apiKeyReqs"`
}

type dailyPage struct {
	Data       []dailyRow `json:"data"`
	Pagination *struct {
		Page        int  `json:"page"`
		TotalPages  int  `json:"totalPages"`
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pagination"`
}

// cents parses a (possibly fractional) cent amount exactly.
func cents(n *json.Number) (money.Amount, bool) {
	if n == nil || *n == "" {
		return 0, false
	}
	a, err := money.ParseScaledDecimal(n.String(), 2)
	return a, err == nil
}

func dollars(n *json.Number) (money.Amount, bool) {
	if n == nil || *n == "" {
		return 0, false
	}
	a, err := money.ParseDecimal(n.String())
	return a, err == nil
}

func count(n *json.Number) (int64, bool) {
	if n == nil || *n == "" {
		return 0, false
	}
	v, err := n.Int64()
	if err != nil {
		f, ok := providers.NumFloat(*n)
		if !ok {
			return 0, false
		}
		v = int64(*f + 0.5)
	}
	return v, true
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	p, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	key, _ := providers.RequireSecret(req.Config, "api_key")
	h := headers(key)
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}

	members, cycleStart, truncated, err := a.spend(ctx, h, p.base)
	if err != nil {
		return nil, err
	}
	if truncated {
		res.Notes = append(res.Notes, fmt.Sprintf("team spend truncated to the first %d members", pageSize*maxPages))
	}
	res.Buckets = append(res.Buckets, spendBuckets(members, cycleStart, now, p.MemberBudgets == nil || *p.MemberBudgets, res)...)

	usage, truncated, err := a.daily(ctx, h, p.base, now, p.lookback)
	if err != nil {
		return nil, err
	}
	if truncated {
		res.Notes = append(res.Notes, "daily usage truncated by the pagination bound")
	}
	res.Usage = usage
	return res, nil
}

func (a *Adapter) spend(ctx context.Context, h http.Header, base string) ([]memberSpend, *time.Time, bool, error) {
	var (
		out        []memberSpend
		cycleStart *time.Time
	)
	for page := 1; page <= maxPages; page++ {
		var sp spendPage
		body := map[string]any{"page": page, "pageSize": pageSize}
		if _, err := a.http.DoJSON(ctx, providers.Request{Method: http.MethodPost, URL: base + "/teams/spend", Header: h, Body: body}, &sp); err != nil {
			return nil, nil, false, err
		}
		out = append(out, sp.TeamMemberSpend...)
		if cycleStart == nil && sp.SubscriptionCycleStart != nil {
			if ms, err := sp.SubscriptionCycleStart.Int64(); err == nil && ms > 0 {
				t := time.UnixMilli(ms).UTC()
				cycleStart = &t
			}
		}
		if page >= sp.TotalPages || len(sp.TeamMemberSpend) == 0 {
			return out, cycleStart, false, nil
		}
	}
	return out, cycleStart, true, nil
}

func spendBuckets(members []memberSpend, cycleStart *time.Time, now time.Time, memberBudgets bool, res *providers.FetchResult) []domain.BucketObservation {
	var overall, onDemand money.Amount
	var haveOverall, haveOnDemand bool
	for _, m := range members {
		if v, ok := cents(m.OverallSpendCents); ok {
			overall, _ = money.Add(overall, v)
			haveOverall = true
		}
		if v, ok := cents(m.SpendCents); ok {
			onDemand, _ = money.Add(onDemand, v)
			haveOnDemand = true
		}
	}
	src := now
	if cycleStart != nil {
		res.Notes = append(res.Notes, "current billing cycle started "+cycleStart.Format(time.DateOnly))
	}
	obs := func(key, label string, used money.Amount, scope domain.BucketScope) domain.BucketObservation {
		f := used.Float64()
		u := used
		return domain.BucketObservation{
			Spec: domain.BucketSpec{Key: key, Label: label, Scope: scope, Kind: domain.KindBudget, Unit: domain.UnitCurrency,
				Currency: "USD", ResetSemantics: domain.ResetUnknown, Official: true},
			Used: &f, UsedAmount: &u, Quality: domain.QualityProviderReported, Source: "POST /teams/spend", SourceTime: &src,
		}
	}
	var out []domain.BucketObservation
	if haveOverall {
		out = append(out, obs("cycle_spend", "Team spend this billing cycle (incl. included usage)", overall, domain.ScopeOrganization))
	}
	if haveOnDemand {
		out = append(out, obs("cycle_on_demand_spend", "Team on-demand spend this billing cycle", onDemand, domain.ScopeOrganization))
	}
	if !memberBudgets {
		return out
	}
	var limited []domain.BucketObservation
	for _, m := range members {
		limit, ok := dollars(m.EffectivePerUserLimitDollars)
		if !ok || limit <= 0 {
			limit, ok = dollars(m.MonthlyLimitDollars)
		}
		if !ok || limit <= 0 {
			continue
		}
		ref := memberRef(rawID(m.UserID))
		if ref == "" {
			continue
		}
		used, ok := cents(m.SpendCents)
		if !ok {
			continue
		}
		o := obs("member_budget:"+ref, "Member "+ref+" monthly on-demand limit", used, domain.ScopeAccount)
		lf := limit.Float64()
		l := limit
		rem, _ := money.Add(limit, -used)
		rf := rem.Float64()
		o.Limit, o.LimitAmount, o.Remaining, o.RemainingAmount = &lf, &l, &rf, &rem
		limited = append(limited, o)
	}
	sort.Slice(limited, func(i, j int) bool { return limited[i].Spec.Key < limited[j].Spec.Key })
	if len(limited) > maxMemberBudgets {
		res.Notes = append(res.Notes, fmt.Sprintf("member budgets limited to %d of %d members with limits", maxMemberBudgets, len(limited)))
		limited = limited[:maxMemberBudgets]
	}
	return append(out, limited...)
}

type daySums struct {
	requests                     int64
	included, usageBased, apiKey int64
	agent, chat, composer        int64
	known                        bool
}

func (d *daySums) add(r dailyRow) {
	for _, f := range []struct {
		n   *json.Number
		dst *int64
	}{
		{r.SubscriptionIncludedReqs, &d.included}, {r.UsageBasedReqs, &d.usageBased}, {r.APIKeyReqs, &d.apiKey},
		{r.AgentRequests, &d.agent}, {r.ChatRequests, &d.chat}, {r.ComposerRequests, &d.composer},
	} {
		if v, ok := count(f.n); ok {
			*f.dst += v
			d.known = true
		}
	}
	d.requests = d.included + d.usageBased + d.apiKey
}

func (a *Adapter) daily(ctx context.Context, h http.Header, base string, now time.Time, lookback int) ([]domain.UsageAggregate, bool, error) {
	today := now.Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(lookback - 1))
	var rows []dailyRow
	truncated := true
	for page := 1; page <= maxPages; page++ {
		var dp dailyPage
		body := map[string]any{"startDate": start.UnixMilli(), "endDate": now.UnixMilli(), "page": page, "pageSize": pageSize}
		if _, err := a.http.DoJSON(ctx, providers.Request{Method: http.MethodPost, URL: base + "/teams/daily-usage-data", Header: h, Body: body}, &dp); err != nil {
			return nil, false, err
		}
		rows = append(rows, dp.Data...)
		if dp.Pagination == nil || !dp.Pagination.HasNextPage || len(dp.Data) == 0 {
			truncated = false
			break
		}
	}
	total := map[time.Time]*daySums{}
	perMember := map[[2]string]*daySums{}
	for _, r := range rows {
		day, ok := rowDay(r)
		if !ok || day.Before(start) || day.After(today) {
			continue
		}
		if total[day] == nil {
			total[day] = &daySums{}
		}
		total[day].add(r)
		ref := memberRef(rawID(r.UserID))
		if ref == "" {
			continue
		}
		k := [2]string{day.Format(time.DateOnly), ref}
		if perMember[k] == nil {
			perMember[k] = &daySums{}
		}
		perMember[k].add(r)
	}
	agg := func(breakdown string, d domain.UsageDimensions, day time.Time, s *daySums) domain.UsageAggregate {
		req := s.requests
		return domain.UsageAggregate{
			Origin: domain.OriginProviderReported, Breakdown: breakdown, Dimensions: d, IntervalStart: day,
			IntervalEnd: day.Add(24 * time.Hour), Granularity: domain.GranDay, Requests: &req, Quality: domain.QualityProviderReported,
			Partial: !now.Before(day) && now.Before(day.Add(24*time.Hour)), CurrencyVerified: true,
		}
	}
	days := make([]time.Time, 0, len(total))
	for d, s := range total {
		if s.known {
			days = append(days, d)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	var out []domain.UsageAggregate
	for _, d := range days {
		out = append(out, agg("total", domain.UsageDimensions{}, d, total[d]))
	}
	keys := make([][2]string, 0, len(perMember))
	for k, s := range perMember {
		if s.known {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		day, _ := time.Parse(time.DateOnly, k[0])
		out = append(out, agg("user", domain.UsageDimensions{User: k[1]}, day.UTC(), perMember[k]))
	}
	return out, truncated, nil
}

func rowDay(r dailyRow) (time.Time, bool) {
	if r.Day != "" {
		d := strings.TrimSpace(r.Day)
		if t, err := time.Parse(time.DateOnly, d[:min(10, len(d))]); err == nil {
			return t.UTC(), true
		}
	}
	if r.Date != nil {
		if ms, err := r.Date.Int64(); err == nil && ms > 0 {
			return time.UnixMilli(ms).UTC().Truncate(24 * time.Hour), true
		}
	}
	return time.Time{}, false
}
