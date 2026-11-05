// Package xai implements the xAI Management API billing adapter using only
// documented endpoints (https://docs.x.ai/developers/management-api-guide,
// https://docs.x.ai/developers/rest-api-reference/management/billing):
//
//   - GET  /auth/management-keys/validation (team discovery when team_id is unset)
//   - GET  /v1/billing/teams/{team}/prepaid/balance
//   - GET  /v1/billing/teams/{team}/postpaid/spending-limits
//   - GET  /v1/billing/teams/{team}/postpaid/invoice/preview
//   - POST /v1/billing/teams/{team}/usage (hourly, grouped by description)
//
// Money values are {val: "<cents>"} strings and are parsed exactly. The
// usage analytics "usd" values are floats whose unit the docs do not state;
// they are imported as estimated, currency-unverified cost.
package xai

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID is the adapter ID.
const ProviderID domain.ProviderID = "xai"

// DefaultBaseURL is the documented Management API origin.
const DefaultBaseURL = "https://management-api.x.ai"

// credential is the credential name holding the management key (separate
// from inference API keys).
const credential = "management_key"

// Options configure the adapter.
type Options struct {
	BaseURL string `json:"base_url"`
	// TeamID skips discovery via the key validation endpoint.
	TeamID string `json:"team_id"`
	// UsageLookbackHours of hourly usage to (re)fetch each poll (1..168).
	UsageLookbackHours int `json:"usage_lookback_hours"`
	// Usage enables the usage analytics call (default true).
	Usage *bool `json:"usage"`
}

type parsedOpts struct {
	Options
	base     string
	lookback time.Duration
}

func parse(cfg providers.AccountConfig) (*parsedOpts, error) {
	var o Options
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return nil, err
	}
	p := &parsedOpts{Options: o, base: DefaultBaseURL, lookback: 48 * time.Hour}
	if o.BaseURL != "" {
		b, err := providers.ValidateBaseURL(o.BaseURL)
		if err != nil {
			return nil, err
		}
		p.base = b
	}
	if o.UsageLookbackHours != 0 {
		if o.UsageLookbackHours < 1 || o.UsageLookbackHours > 168 {
			return nil, providers.NewError(domain.ErrConfiguration, "options.usage_lookback_hours must be 1..168")
		}
		p.lookback = time.Duration(o.UsageLookbackHours) * time.Hour
	}
	if strings.ContainsAny(o.TeamID, "/?#") {
		return nil, providers.NewError(domain.ErrConfiguration, "options.team_id contains invalid characters")
	}
	if _, err := providers.RequireSecret(cfg, credential); err != nil {
		return nil, err
	}
	return p, nil
}

// Adapter implements providers.Adapter.
type Adapter struct {
	http *providers.HTTPClient
}

// New creates the adapter.
func New(deps providers.Deps) providers.Adapter { return &Adapter{http: deps.HTTP} }

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "xAI", Family: "xai", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes:     []string{"management_key (Console → Settings → Management Keys; not an inference key)"},
		DefaultPollInterval: 15 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapMonetaryBalance:   p("prepaid credit in USD; sign convention (negative total = available credit) inferred from the docs example"),
			domain.CapBudget:            s("postpaid spending limit vs current-cycle invoice preview (USD)"),
			domain.CapMonetarySpend:     p("hourly usage analytics in \"usd\"; value unit undocumented, imported as estimated and unverified"),
			domain.CapModelUsage:        p("grouped by usage description (product and model, e.g. \"Chat grok-4\")"),
			domain.CapHistoricalBackfil: p("hourly usage re-fetched over a configurable lookback (default 48h)"),
			domain.CapQuotaRemaining:    u("no quota windows; per-key qps/qpm/tpm are configured limits, not remaining"),
			domain.CapTokenCounters:     u("token value names for usage analytics are undocumented"),
			domain.CapAPIKeyUsage:       u("not requested; grouping keys beyond description are undocumented"),
		}),
		Limitations: []string{
			"Prepaid balance sign convention is inferred: purchased credit appears as a negative total.",
			"Usage analytics \"usd\" values have an undocumented unit; shown as estimated, currency unverified.",
			"Billing cycle reset time and timezone are not documented, so the spending budget has no reset time.",
		},
		DocsURLs: []string{
			"https://docs.x.ai/developers/management-api-guide",
			"https://docs.x.ai/developers/rest-api-reference/management/billing",
		},
	}
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	_, err := parse(cfg)
	return err
}

// cents is the documented {val: "<cents>"} money shape.
type cents struct {
	Val string `json:"val"`
}

func (c *cents) amount() (money.Amount, bool) {
	if c == nil || strings.TrimSpace(c.Val) == "" {
		return 0, false
	}
	a, err := money.ParseScaledDecimal(c.Val, 2)
	if err != nil {
		return 0, false
	}
	return a, true
}

func centsString(s string) (money.Amount, bool) {
	return (&cents{Val: s}).amount()
}

func (a *Adapter) do(ctx context.Context, key string, r providers.Request, out any) error {
	r.Header = providers.BearerHeader(key)
	r.NotFoundIsUnsupported = true
	_, err := a.http.DoJSON(ctx, r, out)
	return err
}

// optional reports whether err only disables one part of the fetch (the
// endpoint is not permitted or not available); other errors abort.
func optional(err error) bool {
	var pe *providers.Error
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Kind {
	case domain.ErrPermission, domain.ErrUnsupported, domain.ErrInvalidResponse:
		return true
	}
	return false
}

func (a *Adapter) team(ctx context.Context, key string, p *parsedOpts) (string, error) {
	if p.TeamID != "" {
		return p.TeamID, nil
	}
	var v struct {
		TeamID  string `json:"teamId"` // deprecated
		Scope   string `json:"scope"`
		ScopeID string `json:"scopeId"`
	}
	if err := a.do(ctx, key, providers.Request{URL: p.base + "/auth/management-keys/validation"}, &v); err != nil {
		return "", err
	}
	switch {
	case v.Scope == "SCOPE_TEAM" && v.ScopeID != "":
		return v.ScopeID, nil
	case v.TeamID != "":
		return v.TeamID, nil
	}
	return "", providers.NewError(domain.ErrConfiguration, "management key is not team-scoped; set options.team_id")
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	p, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	key, _ := providers.RequireSecret(req.Config, credential)
	now := req.Now.UTC()
	team, err := a.team(ctx, key, p)
	if err != nil {
		return nil, err
	}
	billing := p.base + "/v1/billing/teams/" + url.PathEscape(team)
	res := &providers.FetchResult{SourceTime: &now}
	masked := providers.MaskIdentifier(team)
	res.MaskedIdentifier = &masked

	var firstOptional error
	note := func(err error, msg string) error {
		if !optional(err) {
			return err
		}
		if firstOptional == nil {
			firstOptional = err
		}
		res.Notes = append(res.Notes, msg)
		return nil
	}

	// Prepaid balance.
	var bal struct {
		Total *cents `json:"total"`
	}
	if err := a.do(ctx, key, providers.Request{URL: billing + "/prepaid/balance"}, &bal); err != nil {
		if err := note(err, "prepaid balance unavailable for this key"); err != nil {
			return nil, err
		}
	} else if total, ok := bal.Total.amount(); ok {
		// Assumption: the docs example shows purchased credit as a negative
		// total ({"val":"-1000"} after a $10 purchase), so available credit
		// is the negated total.
		avail := -total
		f := avail.Float64()
		res.Buckets = append(res.Buckets, domain.BucketObservation{
			Spec: domain.BucketSpec{Key: "balance:prepaid", Label: "Prepaid credit (USD)", Scope: domain.ScopeAccount,
				Kind: domain.KindBalance, Unit: domain.UnitCurrency, Currency: "USD", ResetSemantics: domain.ResetNone,
				Global: false, Official: true},
			Remaining: &f, RemainingAmount: &avail, Quality: domain.QualityProviderReported,
			Source: "xai:prepaid/balance", SourceTime: &now,
		})
	}

	// Postpaid spending limit and current-cycle invoice preview.
	var limits struct {
		SpendingLimits struct {
			EffectiveHardSl *cents `json:"effectiveHardSl"`
			EffectiveSl     *cents `json:"effectiveSl"`
		} `json:"spendingLimits"`
	}
	limitsErr := a.do(ctx, key, providers.Request{URL: billing + "/postpaid/spending-limits"}, &limits)
	if limitsErr != nil {
		if err := note(limitsErr, "postpaid spending limits unavailable for this key"); err != nil {
			return nil, err
		}
	}
	var inv struct {
		CoreInvoice *struct {
			AmountBeforeVat string `json:"amountBeforeVat"`
		} `json:"coreInvoice"`
		EffectiveSpendingLimit string `json:"effectiveSpendingLimit"`
		BillingCycle           *struct {
			Year  int `json:"year"`
			Month int `json:"month"`
		} `json:"billingCycle"`
	}
	if err := a.do(ctx, key, providers.Request{URL: billing + "/postpaid/invoice/preview"}, &inv); err != nil {
		if err := note(err, "postpaid invoice preview unavailable for this key"); err != nil {
			return nil, err
		}
	} else if inv.CoreInvoice != nil {
		limit, okLimit := centsString(inv.EffectiveSpendingLimit)
		if !okLimit && limitsErr == nil {
			limit, okLimit = limits.SpendingLimits.EffectiveSl.amount()
		}
		used, okUsed := centsString(inv.CoreInvoice.AmountBeforeVat)
		if okLimit && limit > 0 && okUsed {
			rem := limit - used
			lf, uf, rf := limit.Float64(), used.Float64(), rem.Float64()
			label := "Postpaid spend vs spending limit (USD)"
			if c := inv.BillingCycle; c != nil && c.Year > 0 && c.Month >= 1 && c.Month <= 12 {
				label += " · cycle " + strconv.Itoa(c.Year) + "-" + twoDigits(c.Month)
			}
			res.Buckets = append(res.Buckets, domain.BucketObservation{
				Spec: domain.BucketSpec{Key: "budget:postpaid", Label: label, Scope: domain.ScopeAccount, Kind: domain.KindBudget,
					Unit: domain.UnitCurrency, Currency: "USD", ResetSemantics: domain.ResetUnknown, Global: true, Official: true},
				Limit: &lf, Used: &uf, Remaining: &rf, LimitAmount: &limit, UsedAmount: &used, RemainingAmount: &rem,
				Quality: domain.QualityProviderReported, Source: "xai:postpaid/invoice/preview", SourceTime: &now,
			})
		} else if !okLimit || limit <= 0 {
			res.Notes = append(res.Notes, "no postpaid spending limit is set")
		}
	}

	// Usage analytics.
	if p.Usage == nil || *p.Usage {
		usage, err := a.usage(ctx, key, billing, now, p.lookback)
		if err != nil {
			if err := note(err, "usage analytics unavailable for this key"); err != nil {
				return nil, err
			}
		} else {
			res.Usage = usage
		}
	}

	if len(res.Buckets) == 0 && len(res.Usage) == 0 && firstOptional != nil {
		// Nothing at all was permitted: surface the cause instead of an
		// empty success.
		return nil, firstOptional
	}
	return res, nil
}

func twoDigits(m int) string {
	if m < 10 {
		return "0" + strconv.Itoa(m)
	}
	return strconv.Itoa(m)
}

type usageRequest struct {
	AnalyticsRequest analyticsRequest `json:"analyticsRequest"`
}

type analyticsRequest struct {
	TimeRange struct {
		StartTime string `json:"startTime"`
		EndTime   string `json:"endTime"`
		Timezone  string `json:"timezone"`
	} `json:"timeRange"`
	TimeUnit string `json:"timeUnit"`
	Values   []struct {
		Name        string `json:"name"`
		Aggregation string `json:"aggregation"`
	} `json:"values"`
	GroupBy []string `json:"groupBy"`
	Filters []any    `json:"filters"`
}

type usageResponse struct {
	TimeSeries []struct {
		Group       []string `json:"group"`
		GroupLabels []string `json:"groupLabels"`
		DataPoints  []struct {
			Timestamp string    `json:"timestamp"`
			Values    []float64 `json:"values"`
		} `json:"dataPoints"`
	} `json:"timeSeries"`
	LimitReached bool `json:"limitReached"`
}

const timeLayout = "2006-01-02 15:04:05"

// floatAmount converts a float to an exact Amount via its shortest decimal
// representation (the source value is already a float).
func floatAmount(v float64) (money.Amount, bool) {
	a, err := money.ParseDecimal(strconv.FormatFloat(v, 'f', -1, 64))
	return a, err == nil
}

func (a *Adapter) usage(ctx context.Context, key, billing string, now time.Time, lookback time.Duration) ([]domain.UsageAggregate, error) {
	start := now.Add(-lookback).Truncate(time.Hour)
	end := now.Truncate(time.Hour).Add(time.Hour) // exclusive
	var body usageRequest
	body.AnalyticsRequest.TimeRange.StartTime = start.Format(timeLayout)
	body.AnalyticsRequest.TimeRange.EndTime = end.Format(timeLayout)
	body.AnalyticsRequest.TimeRange.Timezone = "Etc/GMT"
	body.AnalyticsRequest.TimeUnit = "TIME_UNIT_HOUR"
	body.AnalyticsRequest.Values = []struct {
		Name        string `json:"name"`
		Aggregation string `json:"aggregation"`
	}{{Name: "usd", Aggregation: "AGGREGATION_SUM"}}
	body.AnalyticsRequest.GroupBy = []string{"description"}
	body.AnalyticsRequest.Filters = []any{}

	var resp usageResponse
	if err := a.do(ctx, key, providers.Request{Method: "POST", URL: billing + "/usage", Body: body}, &resp); err != nil {
		return nil, err
	}
	totals := map[time.Time]money.Amount{}
	var out []domain.UsageAggregate
	agg := func(at time.Time, breakdown string, dims domain.UsageDimensions, amt money.Amount) domain.UsageAggregate {
		iend := at.Add(time.Hour)
		return domain.UsageAggregate{
			Origin: domain.OriginProviderReported, Breakdown: breakdown, Dimensions: dims,
			IntervalStart: at, IntervalEnd: iend, Granularity: domain.GranHour,
			EstimatedCost: &money.Money{Amount: amt, Currency: "USD"}, CurrencyVerified: false,
			Partial: now.Before(iend), Quality: domain.QualityEstimated,
		}
	}
	for _, ts := range resp.TimeSeries {
		desc := strings.TrimSpace(strings.Join(firstNonEmpty(ts.GroupLabels, ts.Group), " "))
		if desc == "" {
			desc = "unknown"
		}
		for _, dp := range ts.DataPoints {
			at, err := time.Parse(time.RFC3339, dp.Timestamp)
			if err != nil || len(dp.Values) == 0 {
				continue
			}
			at = at.UTC().Truncate(time.Hour)
			amt, ok := floatAmount(dp.Values[0])
			if !ok {
				continue
			}
			out = append(out, agg(at, "model", domain.UsageDimensions{RequestedModel: desc}, amt))
			if t, err := money.Add(totals[at], amt); err == nil {
				totals[at] = t
			}
		}
	}
	hours := make([]time.Time, 0, len(totals))
	for h := range totals {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	for _, h := range hours {
		out = append(out, agg(h, "total", domain.UsageDimensions{}, totals[h]))
	}
	return out, nil
}

func firstNonEmpty(xs ...[]string) []string {
	for _, x := range xs {
		if len(x) > 0 {
			return x
		}
	}
	return nil
}
