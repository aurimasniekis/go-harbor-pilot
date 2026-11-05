// Package githubcopilot implements the GitHub Copilot premium request
// adapter using the documented billing REST API
// (GET /organizations/{org}/settings/billing/premium_request/usage and the
// personal /users/{username}/... variant).
package githubcopilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID identifies the adapter.
const ProviderID domain.ProviderID = "github_copilot"

// DefaultBaseURL is the GitHub REST API origin.
const DefaultBaseURL = "https://api.github.com"

// APIVersion is the REST API version the adapter was written against.
const APIVersion = "2026-03-10"

var namePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// Options configure the adapter. Exactly one of Org or User must be set.
type Options struct {
	BaseURL string `json:"base_url"`
	// Org reports usage billed to an organization (caller must be an org
	// admin or billing manager).
	Org string `json:"org"`
	// User reports a personal account's self-purchased Copilot usage.
	User string `json:"user"`
	// LookbackDays of daily usage to (re)fetch each poll (1..31).
	LookbackDays int `json:"lookback_days"`
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
	if (o.Org == "") == (o.User == "") {
		return nil, providers.NewError(domain.ErrConfiguration, "exactly one of options.org or options.user is required")
	}
	for _, n := range []string{o.Org, o.User} {
		if n != "" && !namePattern.MatchString(n) {
			return nil, providers.NewError(domain.ErrConfiguration, "options.org / options.user must be a valid GitHub login")
		}
	}
	if o.LookbackDays != 0 {
		if o.LookbackDays < 1 || o.LookbackDays > 31 {
			return nil, providers.NewError(domain.ErrConfiguration, "options.lookback_days must be 1..31")
		}
		p.lookback = o.LookbackDays
	}
	if _, err := providers.RequireSecret(cfg, "api_key"); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *parsedOpts) endpoint() string {
	if p.Org != "" {
		return p.base + "/organizations/" + url.PathEscape(p.Org) + "/settings/billing/premium_request/usage"
	}
	return p.base + "/users/" + url.PathEscape(p.User) + "/settings/billing/premium_request/usage"
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
		ID: ProviderID, DisplayName: "GitHub Copilot (premium requests)", Family: "copilot", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes:     []string{"api_key (GitHub token of an org admin / billing manager, or the user for personal usage)"},
		DefaultPollInterval: time.Hour,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapRequestCounters:   s("premium requests per day (gross quantity, rounded to whole requests)"),
			domain.CapMonetarySpend:     s("net billed amount per day and month to date (USD)"),
			domain.CapModelUsage:        s("per model"),
			domain.CapOrgProjectScope:   p("organization or personal account; enterprise scope not implemented"),
			domain.CapHistoricalBackfil: p("daily ≤31 days per poll; the API retains 24 months"),
			domain.CapQuotaRemaining:    u("the REST API does not expose a user's remaining monthly premium request allowance"),
			domain.CapQuotaLimit:        u("the included premium request allowance is not exposed"),
			domain.CapTokenCounters:     u("token counts are not exposed"),
			domain.CapMonetaryBalance:   u("no balance API"),
		}),
		Limitations: []string{
			"Per-user remaining premium requests are not available through the REST API.",
			"The personal endpoint only covers self-purchased Copilot, not organization or enterprise seats.",
			"Premium request quantities can be fractional (model multipliers); request counts are rounded.",
			"Day boundaries are assumed to be UTC (not stated in the documentation).",
		},
		DocsURLs: []string{"https://docs.github.com/en/rest/billing/usage"},
	}
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	_, err := parse(cfg)
	return err
}

func headers(token string) http.Header {
	h := providers.BearerHeader(token)
	h.Set("Accept", "application/vnd.github+json")
	h.Set("X-GitHub-Api-Version", APIVersion)
	return h
}

type usageItem struct {
	Product        string      `json:"product"`
	SKU            string      `json:"sku"`
	Model          string      `json:"model"`
	UnitType       string      `json:"unitType"`
	GrossQuantity  json.Number `json:"grossQuantity"`
	GrossAmount    json.Number `json:"grossAmount"`
	DiscountAmount json.Number `json:"discountAmount"`
	NetQuantity    json.Number `json:"netQuantity"`
	NetAmount      json.Number `json:"netAmount"`
}

type usageResponse struct {
	UsageItems []usageItem `json:"usageItems"`
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	p, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	token, _ := providers.RequireSecret(req.Config, "api_key")
	h := headers(token)
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	ident := "org:" + p.Org
	if p.User != "" {
		ident = "user:" + p.User
	}
	res.MaskedIdentifier = &ident

	// Month to date: a spend counter (not a usage aggregate, so it is never
	// double counted with the daily rows).
	month, err := a.get(ctx, h, p.endpoint(), url.Values{"year": {fmt.Sprint(now.Year())}, "month": {fmt.Sprint(int(now.Month()))}})
	if err != nil {
		return nil, err
	}
	if net, ok := sumAmount(month.UsageItems, func(i usageItem) json.Number { return i.NetAmount }); ok {
		f := net.Float64()
		src := now
		res.Buckets = append(res.Buckets, domain.BucketObservation{
			Spec: domain.BucketSpec{Key: "month_spend", Label: "Premium request spend this month (net)", Scope: domain.ScopeOrganization,
				Kind: domain.KindBudget, Unit: domain.UnitCurrency, Currency: "USD", ResetSemantics: domain.ResetUnknown, Official: true},
			Used: &f, UsedAmount: &net, Quality: domain.QualityProviderReported, Source: "GET premium_request/usage", SourceTime: &src,
		})
	}

	today := now.Truncate(24 * time.Hour)
	for i := p.lookback - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i)
		q := url.Values{"year": {fmt.Sprint(day.Year())}, "month": {fmt.Sprint(int(day.Month()))}, "day": {fmt.Sprint(day.Day())}}
		r, err := a.get(ctx, h, p.endpoint(), q)
		if err != nil {
			return nil, err
		}
		res.Usage = append(res.Usage, dayAggregates(r.UsageItems, day, now)...)
	}
	return res, nil
}

func (a *Adapter) get(ctx context.Context, h http.Header, endpoint string, q url.Values) (*usageResponse, error) {
	var r usageResponse
	_, err := a.http.DoJSON(ctx, providers.Request{URL: endpoint + "?" + q.Encode(), Header: h, NotFoundIsUnsupported: true}, &r)
	if err != nil {
		pe := providers.AsError(err)
		switch pe.Kind {
		case domain.ErrPermission:
			pe.Message = "token lacks access to billing usage (organization admin or billing manager required)"
		case domain.ErrUnsupported:
			pe.Message = "billing usage not available for this organization or user (not found or enhanced billing not enabled)"
		}
		return nil, pe
	}
	return &r, nil
}

func sumAmount(items []usageItem, field func(usageItem) json.Number) (money.Amount, bool) {
	var total money.Amount
	ok := false
	for _, it := range items {
		v := field(it)
		if v == "" {
			continue
		}
		a, err := money.ParseDecimal(v.String())
		if err != nil {
			continue
		}
		total, _ = money.Add(total, a)
		ok = true
	}
	return total, ok
}

type daySum struct {
	requests float64
	net      money.Amount
	haveReq  bool
	haveCost bool
}

func (d *daySum) add(it usageItem) {
	if f, ok := providers.NumFloat(it.GrossQuantity); ok {
		d.requests += *f
		d.haveReq = true
	}
	if it.NetAmount != "" {
		if a, err := money.ParseDecimal(it.NetAmount.String()); err == nil {
			d.net, _ = money.Add(d.net, a)
			d.haveCost = true
		}
	}
}

func dayAggregates(items []usageItem, day, now time.Time) []domain.UsageAggregate {
	if len(items) == 0 {
		return nil
	}
	total := &daySum{}
	byModel := map[string]*daySum{}
	for _, it := range items {
		total.add(it)
		m := it.Model
		if byModel[m] == nil {
			byModel[m] = &daySum{}
		}
		byModel[m].add(it)
	}
	agg := func(breakdown, model string, s *daySum) domain.UsageAggregate {
		ag := domain.UsageAggregate{
			Origin: domain.OriginProviderReported, Breakdown: breakdown, Dimensions: domain.UsageDimensions{RequestedModel: model},
			IntervalStart: day, IntervalEnd: day.Add(24 * time.Hour), Granularity: domain.GranDay,
			Quality: domain.QualityProviderReported, CurrencyVerified: true, Partial: now.Before(day.Add(24 * time.Hour)),
		}
		if s.haveReq {
			r := int64(s.requests + 0.5)
			ag.Requests = &r
		}
		if s.haveCost {
			ag.ActualCost = &money.Money{Amount: s.net, Currency: "USD"}
		}
		return ag
	}
	out := []domain.UsageAggregate{agg("total", "", total)}
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)
	for _, m := range models {
		out = append(out, agg("model", m, byModel[m]))
	}
	return out
}
