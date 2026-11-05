// Package nexos implements the nexos.ai Gateway API adapter using only
// documented endpoints (https://docs.nexos.ai/gateway-api/):
//
//   - GET /v1/usage (hourly totals, top models, optional api_key grouping)
//   - GET /v1/companies/{companyId}/api_keys/with_usage (optional; key status
//     and budget figures)
//   - GET /v1/models/all (optional; per-token prices, imported only when the
//     operator states the pricing currency, which nexos does not document)
//
// nexos exposes no credit-balance, request-log or rate-limit endpoint.
package nexos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID is the adapter ID.
const ProviderID domain.ProviderID = "nexos"

// DefaultBaseURL is the documented API origin.
const DefaultBaseURL = "https://api.nexos.ai"

// Options configure the adapter.
type Options struct {
	BaseURL string `json:"base_url"`
	// LookbackHours of hourly usage to (re)fetch each poll (1..168).
	LookbackHours int `json:"lookback_hours"`
	// APIKeyBreakdown requests group_by=api_key (team or owner keys).
	APIKeyBreakdown *bool `json:"api_key_breakdown"`
	// CompanyID enables the per-key status/budget listing (owner keys).
	CompanyID string `json:"company_id"`
	// KeyUUID identifies this account's own key for a budget bucket.
	KeyUUID string `json:"key_uuid"`
	// PricingCurrency enables importing /v1/models/all prices; nexos does
	// not document the pricing currency, so the operator must state it.
	PricingCurrency string `json:"pricing_currency"`
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
	if o.LookbackHours != 0 {
		if o.LookbackHours < 1 || o.LookbackHours > 168 {
			return nil, providers.NewError(domain.ErrConfiguration, "options.lookback_hours must be 1..168 (hourly data is limited to 7 days)")
		}
		p.lookback = time.Duration(o.LookbackHours) * time.Hour
	}
	if o.PricingCurrency != "" && len(o.PricingCurrency) != 3 {
		return nil, providers.NewError(domain.ErrConfiguration, "options.pricing_currency must be a 3-letter code")
	}
	if o.KeyUUID != "" && o.CompanyID == "" {
		return nil, providers.NewError(domain.ErrConfiguration, "options.key_uuid requires options.company_id")
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

// New creates the adapter.
func New(deps providers.Deps) *Adapter { return &Adapter{http: deps.HTTP} }

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "nexos.ai", Family: "nexos", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes:     []string{"api_key (user-… or team-… key; owner keys see all teams)"},
		DefaultPollInterval: 10 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapTokenCounters:     s("input, output, cache read/write, image tokens per hour"),
			domain.CapRequestCounters:   s("requests per hour"),
			domain.CapMonetarySpend:     p("cost_total documented as USD while credits are priced in EUR; currency flagged unverified"),
			domain.CapModelUsage:        p("top models (≤20) plus an aggregated __other__ row"),
			domain.CapAPIKeyUsage:       p("group_by=api_key with team or owner keys"),
			domain.CapAPIKeyInventory:   p("requires company_id and an owner key"),
			domain.CapBudget:            p("per-key budget_used/budget_limit; units undocumented"),
			domain.CapOrgProjectScope:   s("team scope"),
			domain.CapHistoricalBackfil: s("hourly ≤7 days, daily ≤31-90 days"),
			domain.CapModelPricing:      p("per-token strings; currency undocumented (operator must declare)"),
			domain.CapMonetaryBalance:   u("credit balance is shown only in the nexos UI"),
			domain.CapQuotaRemaining:    u("no quota-window API"),
			domain.CapUsageAttribution:  u("request logs are UI-only; use the ingestion API for instrumented traffic"),
		}),
		Limitations: []string{
			"No credit-balance, request-log or rate-limit header API is documented.",
			"cost_total is labelled USD although API credits are priced 1 € = 1 credit; shown as unverified currency.",
			"Model breakdown covers the top 20 models; the remainder is reported as __other__.",
			"The current hour is partial and is re-fetched on the next poll.",
		},
		DocsURLs: []string{"https://docs.nexos.ai/gateway-api/usage", "https://docs.nexos.ai/gateway-api/company-management"},
	}
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	_, err := parse(cfg)
	return err
}

type metrics struct {
	Requests           json.Number `json:"requests"`
	TokensInput        json.Number `json:"tokens_input"`
	TokensOutput       json.Number `json:"tokens_output"`
	TokensCacheWrite   json.Number `json:"tokens_cache_write"`
	TokensCacheWrite1h json.Number `json:"tokens_cache_write_1h"`
	TokensCacheRead    json.Number `json:"tokens_cache_read"`
	TokensImageInput   json.Number `json:"tokens_image_input"`
	TokensImageOutput  json.Number `json:"tokens_image_output"`
	TokensImageCache   json.Number `json:"tokens_image_cache_read"`
	TokensTotal        json.Number `json:"tokens_total"`
	CostTotal          json.Number `json:"cost_total"`
}

type topModel struct {
	metrics
	Model string `json:"model"`
}

type item struct {
	metrics
	Date      string     `json:"date"`
	TopModels []topModel `json:"top_models"`
}

type group struct {
	Dimensions struct {
		APIKeyID   string `json:"api_key_id"`
		APIKeyName string `json:"api_key_name"`
	} `json:"dimensions"`
	Items []item `json:"items"`
}

type usageReport struct {
	Granularity string  `json:"granularity"`
	Items       []item  `json:"items"`
	Groups      []group `json:"groups"`
}

func n64(n json.Number) *int64 {
	if n == "" {
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil {
			return nil
		}
		v = int64(f)
	}
	return &v
}

func sum(ps ...*int64) int64 {
	var t int64
	for _, p := range ps {
		if p != nil {
			t += *p
		}
	}
	return t
}

// tokens converts metrics, inferring overlap from tokens_total: nexos does
// not document whether cache tokens are part of tokens_input, so the
// relationship is checked against the reported total for every row.
func (m metrics) tokens() domain.TokenCounts {
	in, out := n64(m.TokensInput), n64(m.TokensOutput)
	cw := sum(n64(m.TokensCacheWrite), n64(m.TokensCacheWrite1h))
	cr := n64(m.TokensCacheRead)
	tk := domain.TokenCounts{Input: in, Output: out, CacheRead: cr, CacheWrite: &cw,
		ImageInput: n64(m.TokensImageInput), ImageOutput: n64(m.TokensImageOutput)}
	if m.TokensCacheWrite == "" && m.TokensCacheWrite1h == "" {
		tk.CacheWrite = nil
	}
	if total := n64(m.TokensTotal); total != nil {
		images := sum(n64(m.TokensImageInput), n64(m.TokensImageOutput), n64(m.TokensImageCache))
		exclusive := sum(in, out, cr) + cw + images
		inclusive := sum(in, out) + images
		if *total != exclusive && *total == inclusive {
			tk.CacheReadIncludedInInput, tk.CacheWriteIncludedInInput = true, true
		}
	}
	return tk
}

func (m metrics) cost() *money.Money {
	if m.CostTotal == "" {
		return nil
	}
	a, err := money.ParseDecimal(string(m.CostTotal))
	if err != nil {
		return nil
	}
	return &money.Money{Amount: a, Currency: "USD"}
}

func (m metrics) aggregate(start time.Time, now time.Time, breakdown string, dims domain.UsageDimensions) domain.UsageAggregate {
	end := start.Add(time.Hour)
	return domain.UsageAggregate{
		Origin: domain.OriginProviderReported, Breakdown: breakdown, Dimensions: dims,
		IntervalStart: start, IntervalEnd: end, Granularity: domain.GranHour,
		Requests: n64(m.Requests), Tokens: m.tokens(), ActualCost: m.cost(), CurrencyVerified: false,
		Partial: now.Before(end), Quality: domain.QualityProviderReported,
	}
}

func (a *Adapter) get(ctx context.Context, key, u string, out any) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	_, err := a.http.DoJSON(ctx, providers.Request{URL: u, Header: h, NotFoundIsUnsupported: true}, out)
	return err
}

func usageURL(base string, from, to time.Time, extra url.Values) string {
	q := url.Values{}
	q.Set("from", from.Format("2006-01-02"))
	q.Set("to", to.Format("2006-01-02"))
	q.Set("granularity", "hour")
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	return base + "/v1/usage?" + q.Encode()
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	p, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	key, _ := providers.RequireSecret(req.Config, "api_key")
	now := req.Now.UTC()
	from := now.Add(-p.lookback).Truncate(24 * time.Hour)
	res := &providers.FetchResult{SourceTime: &now}

	var totals usageReport
	if err := a.get(ctx, key, usageURL(p.base, from, now, url.Values{
		"include": {"top_models"}, "top_models_limit": {"20"},
	}), &totals); err != nil {
		return nil, err
	}
	for _, it := range totals.Items {
		start, err := time.Parse(time.RFC3339, it.Date)
		if err != nil {
			continue
		}
		start = start.UTC()
		res.Usage = append(res.Usage, it.aggregate(start, now, "total", domain.UsageDimensions{}))
		for _, tm := range it.TopModels {
			model := tm.Model
			res.Usage = append(res.Usage, tm.aggregate(start, now, "model", domain.UsageDimensions{RequestedModel: model}))
		}
	}

	if p.APIKeyBreakdown == nil || *p.APIKeyBreakdown {
		var grouped usageReport
		err := a.get(ctx, key, usageURL(p.base, from, now, url.Values{
			"group_by": {"api_key"}, "limit": {"100"}, "include_other": {"true"},
		}), &grouped)
		var pe *providers.Error
		switch {
		case err == nil:
			for _, g := range grouped.Groups {
				id := g.Dimensions.APIKeyID
				if id == "" {
					continue
				}
				if id != "__other__" {
					res.APIKeys = append(res.APIKeys, domain.APIKeyRecord{ProviderKeyID: id, Name: g.Dimensions.APIKeyName})
				}
				for _, it := range g.Items {
					start, err := time.Parse(time.RFC3339, it.Date)
					if err != nil {
						continue
					}
					res.Usage = append(res.Usage, it.aggregate(start.UTC(), now, "api_key", domain.UsageDimensions{APIKeyID: id}))
				}
			}
		case asErr(err, &pe) && (pe.Kind == domain.ErrPermission || pe.Kind == domain.ErrUnsupported):
			res.Notes = append(res.Notes, "API-key breakdown not permitted for this key type (requires a team or owner key)")
		default:
			return nil, err
		}
	}

	if p.CompanyID != "" {
		keys, bucket, err := a.companyKeys(ctx, key, p)
		var pe *providers.Error
		switch {
		case err == nil:
			res.APIKeys = mergeKeys(res.APIKeys, keys)
			if bucket != nil {
				res.Buckets = append(res.Buckets, *bucket)
			}
		case asErr(err, &pe) && (pe.Kind == domain.ErrPermission || pe.Kind == domain.ErrUnsupported):
			res.Notes = append(res.Notes, "company API-key listing requires a company owner key")
		default:
			return nil, err
		}
	}

	if p.PricingCurrency != "" {
		prices, err := a.prices(ctx, key, p, now)
		if err == nil {
			res.Prices = prices
		} else {
			res.Notes = append(res.Notes, "model pricing unavailable")
		}
	}
	return res, nil
}

func asErr(err error, target **providers.Error) bool {
	if err == nil {
		return false
	}
	pe := providers.AsError(err)
	*target = pe
	return true
}

type companyKey struct {
	UUID        string       `json:"uuid"`
	Name        string       `json:"name"`
	KeyOwner    string       `json:"key_owner"`
	OwnerUUID   string       `json:"owner_uuid"`
	Type        string       `json:"type"`
	Status      string       `json:"status"`
	LastUsedAt  *time.Time   `json:"last_used_at"`
	BudgetUsed  *json.Number `json:"budget_used"`
	BudgetLimit *json.Number `json:"budget_limit"`
}

func decAmount(n *json.Number) *money.Amount {
	if n == nil || *n == "" {
		return nil
	}
	a, err := money.ParseDecimal(string(*n))
	if err != nil {
		return nil
	}
	return &a
}

func (a *Adapter) companyKeys(ctx context.Context, key string, p *parsedOpts) ([]domain.APIKeyRecord, *domain.BucketObservation, error) {
	var out []domain.APIKeyRecord
	var bucket *domain.BucketObservation
	for offset := 0; offset < 2000; offset += 100 {
		var page struct {
			Items []companyKey `json:"items"`
			Total int64        `json:"total"`
		}
		u := fmt.Sprintf("%s/v1/companies/%s/api_keys/with_usage?limit=100&offset=%d", p.base, url.PathEscape(p.CompanyID), offset)
		if err := a.get(ctx, key, u, &page); err != nil {
			return nil, nil, err
		}
		for _, k := range page.Items {
			st := k.Status
			rec := domain.APIKeyRecord{
				ProviderKeyID: k.UUID, Name: k.Name, Status: &st, Project: k.KeyOwner,
				ProviderLastUsedAt: k.LastUsedAt, BudgetUsed: decAmount(k.BudgetUsed), BudgetLimit: decAmount(k.BudgetLimit),
			}
			out = append(out, rec)
			if k.UUID == p.KeyUUID && rec.BudgetLimit != nil {
				used, lim := rec.BudgetUsed, rec.BudgetLimit
				o := domain.BucketObservation{
					Spec: domain.BucketSpec{
						Key: "budget:key", Label: "API key budget (nexos units)", Scope: domain.ScopeAPIKey,
						Kind: domain.KindBudget, Unit: domain.UnitCredits, ResetSemantics: domain.ResetUnknown,
						Global: true, Official: true,
					},
					Quality: domain.QualityProviderReported, Source: "nexos:api_keys/with_usage",
				}
				o.Limit = providers.Float(lim.Float64())
				if used != nil {
					o.Used = providers.Float(used.Float64())
					o.Remaining = providers.Float(lim.Float64() - used.Float64())
				}
				bucket = &o
			}
		}
		if int64(offset+len(page.Items)) >= page.Total || len(page.Items) == 0 {
			break
		}
	}
	return out, bucket, nil
}

func mergeKeys(a, b []domain.APIKeyRecord) []domain.APIKeyRecord {
	idx := map[string]int{}
	for i, k := range a {
		idx[k.ProviderKeyID] = i
	}
	for _, k := range b {
		if i, ok := idx[k.ProviderKeyID]; ok {
			if k.Name == "" {
				k.Name = a[i].Name
			}
			a[i] = k
			continue
		}
		a = append(a, k)
	}
	return a
}

type globalModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Pricing *struct {
		Input      string `json:"input_cost_per_token"`
		Output     string `json:"output_cost_per_token"`
		CacheWrite string `json:"cache_write_cost_per_token"`
		CacheRead  string `json:"cache_read_cost_per_token"`
	} `json:"pricing"`
}

// perMillion converts a per-token decimal string into a per-million amount.
func perMillion(s string) (money.Amount, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	per, err := money.ParseDecimal(s)
	if err != nil || per < 0 {
		return 0, false
	}
	total := int64(per) * 1_000_000
	if per != 0 && total/1_000_000 != int64(per) {
		return 0, false
	}
	return money.Amount(total), true
}

func (a *Adapter) prices(ctx context.Context, key string, p *parsedOpts, now time.Time) ([]domain.PriceRecord, error) {
	var body struct {
		Data []globalModel `json:"data"`
	}
	if err := a.get(ctx, key, p.base+"/v1/models/all", &body); err != nil {
		return nil, err
	}
	day := now.Truncate(24 * time.Hour)
	var out []domain.PriceRecord
	for _, m := range body.Data {
		if m.Pricing == nil {
			continue
		}
		for cat, v := range map[string]string{"input": m.Pricing.Input, "output": m.Pricing.Output,
			"cache_write": m.Pricing.CacheWrite, "cache_read": m.Pricing.CacheRead} {
			pm, ok := perMillion(v)
			if !ok {
				continue
			}
			for _, name := range uniq(m.ID, m.Name) {
				out = append(out, domain.PriceRecord{ProviderID: ProviderID, Model: name, Category: cat, PerMillion: pm,
					Currency: strings.ToUpper(p.PricingCurrency), EffectiveFrom: day, Source: "nexos:models/all"})
			}
		}
	}
	return out, nil
}

func uniq(xs ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
