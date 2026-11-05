// Package openai implements OpenAI adapters: the official organization
// Usage/Costs Admin API and the unofficial ChatGPT/Codex plan usage endpoint.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// AdminID is the official Admin API adapter.
const AdminID domain.ProviderID = "openai_admin"

// DefaultBaseURL is the OpenAI API origin.
const DefaultBaseURL = "https://api.openai.com"

// AdminOptions configure the adapter.
type AdminOptions struct {
	BaseURL       string `json:"base_url"`
	LookbackHours int    `json:"lookback_hours"`
	LookbackDays  int    `json:"lookback_days"`
	ListAPIKeys   *bool  `json:"list_api_keys"`
	MaxProjects   int    `json:"max_projects"`
}

// Admin is the adapter.
type Admin struct{ http *providers.HTTPClient }

// NewAdmin creates the adapter.
func NewAdmin(deps providers.Deps) *Admin { return &Admin{http: deps.HTTP} }

// Info implements providers.Adapter.
func (a *Admin) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: AdminID, DisplayName: "OpenAI API (Admin)", Family: "openai", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes: []string{"Admin API key"}, DefaultPollInterval: 15 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapTokenCounters:     s("input (incl. cached), cached input, output"),
			domain.CapRequestCounters:   s("num_model_requests"),
			domain.CapMonetarySpend:     p("daily costs only"),
			domain.CapModelUsage:        s(""),
			domain.CapAPIKeyUsage:       s("group_by api_key_id"),
			domain.CapAPIKeyInventory:   s("project API keys with redacted value and last use"),
			domain.CapOrgProjectScope:   s("projects"),
			domain.CapHistoricalBackfil: s("hourly ≤7 days per request, daily ≤31"),
			domain.CapQuotaRemaining:    u("no balance or remaining-rate-limit endpoint"),
			domain.CapMonetaryBalance:   u("no credit balance API"),
		}),
		Limitations: []string{
			"Costs are daily only; hourly costs are estimates when pricing is configured.",
			"Reporting freshness is not documented; recent buckets may change and are re-fetched.",
		},
		DocsURLs: []string{"https://developers.openai.com/api/reference/go/resources/admin"},
	}
}

func parseAdmin(cfg providers.AccountConfig) (AdminOptions, string, error) {
	var o AdminOptions
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return o, "", err
	}
	base := DefaultBaseURL
	if o.BaseURL != "" {
		b, err := providers.ValidateBaseURL(o.BaseURL)
		if err != nil {
			return o, "", err
		}
		base = b
	}
	if o.LookbackHours == 0 {
		o.LookbackHours = 48
	}
	if o.LookbackDays == 0 {
		o.LookbackDays = 7
	}
	if o.MaxProjects == 0 {
		o.MaxProjects = 50
	}
	if o.LookbackHours < 1 || o.LookbackHours > 168 || o.LookbackDays < 1 || o.LookbackDays > 31 || o.MaxProjects < 1 || o.MaxProjects > 500 {
		return o, "", providers.NewError(domain.ErrConfiguration, "lookback_hours 1..168, lookback_days 1..31, max_projects 1..500")
	}
	if _, err := providers.RequireSecret(cfg, "api_key"); err != nil {
		return o, "", err
	}
	return o, base, nil
}

// Validate implements providers.Adapter.
func (a *Admin) Validate(cfg providers.AccountConfig) error {
	_, _, err := parseAdmin(cfg)
	return err
}

type completionsResult struct {
	InputTokens       int64   `json:"input_tokens"`
	InputCachedTokens int64   `json:"input_cached_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	NumModelRequests  int64   `json:"num_model_requests"`
	ProjectID         *string `json:"project_id"`
	APIKeyID          *string `json:"api_key_id"`
	Model             *string `json:"model"`
}

type costResult struct {
	Amount struct {
		Value    json.Number `json:"value"`
		Currency string      `json:"currency"`
	} `json:"amount"`
}

type bucket[T any] struct {
	StartTime int64 `json:"start_time"`
	EndTime   int64 `json:"end_time"`
	Results   []T   `json:"results"`
}

type page[T any] struct {
	Data     []bucket[T] `json:"data"`
	HasMore  bool        `json:"has_more"`
	NextPage *string     `json:"next_page"`
}

func fetchPages[T any](ctx context.Context, c *providers.HTTPClient, h http.Header, u string, q url.Values) ([]bucket[T], error) {
	var out []bucket[T]
	for i := 0; i < 50; i++ {
		var p page[T]
		if _, err := c.DoJSON(ctx, providers.Request{URL: u + "?" + q.Encode(), Header: h}, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || p.NextPage == nil || *p.NextPage == "" {
			break
		}
		q.Set("page", *p.NextPage)
	}
	return out, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type sums struct{ in, cached, out, req int64 }

func (s *sums) add(r completionsResult) {
	s.in += r.InputTokens
	s.cached += r.InputCachedTokens
	s.out += r.OutputTokens
	s.req += r.NumModelRequests
}

func (s sums) agg(breakdown string, d domain.UsageDimensions, start, end time.Time, g domain.Granularity, now time.Time, cost *money.Money) domain.UsageAggregate {
	return domain.UsageAggregate{
		Origin: domain.OriginProviderReported, Breakdown: breakdown, Dimensions: d, IntervalStart: start, IntervalEnd: end,
		Granularity: g, Requests: providers.Int64(s.req), Quality: domain.QualityProviderReported, CurrencyVerified: true,
		// OpenAI input_tokens include input_cached_tokens.
		Tokens: domain.TokenCounts{Input: providers.Int64(s.in), CacheRead: providers.Int64(s.cached), Output: providers.Int64(s.out),
			CacheReadIncludedInInput: true},
		ActualCost: cost, Partial: now.Before(end.Add(10 * time.Minute)),
	}
}

func keys(m map[string]*sums) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toAggs(bs []bucket[completionsResult], g domain.Granularity, now time.Time, costs map[int64]*money.Money) []domain.UsageAggregate {
	var out []domain.UsageAggregate
	for _, b := range bs {
		start, end := time.Unix(b.StartTime, 0).UTC(), time.Unix(b.EndTime, 0).UTC()
		total := sums{}
		byModel, byKey := map[string]*sums{}, map[string]*sums{}
		for _, r := range b.Results {
			total.add(r)
			m, k := str(r.Model), str(r.APIKeyID)
			if byModel[m] == nil {
				byModel[m] = &sums{}
			}
			if byKey[k] == nil {
				byKey[k] = &sums{}
			}
			byModel[m].add(r)
			byKey[k].add(r)
		}
		var cost *money.Money
		if g == domain.GranDay {
			cost = costs[b.StartTime]
		}
		out = append(out, total.agg("total", domain.UsageDimensions{}, start, end, g, now, cost))
		for _, m := range keys(byModel) {
			out = append(out, byModel[m].agg("model", domain.UsageDimensions{RequestedModel: m}, start, end, g, now, nil))
		}
		for _, k := range keys(byKey) {
			out = append(out, byKey[k].agg("api_key", domain.UsageDimensions{APIKeyID: k}, start, end, g, now, nil))
		}
	}
	return out
}

// Fetch implements providers.Adapter.
func (a *Admin) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	o, base, err := parseAdmin(req.Config)
	if err != nil {
		return nil, err
	}
	key, _ := providers.RequireSecret(req.Config, "api_key")
	h := providers.BearerHeader(key)
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}

	dayFrom := now.Add(-time.Duration(o.LookbackDays) * 24 * time.Hour).Truncate(24 * time.Hour)
	cq := url.Values{}
	cq.Set("start_time", fmt.Sprint(dayFrom.Unix()))
	cq.Set("bucket_width", "1d")
	cq.Set("limit", fmt.Sprint(o.LookbackDays+1))
	costBuckets, err := fetchPages[costResult](ctx, a.http, h, base+"/v1/organization/costs", cq)
	if err != nil {
		return nil, err
	}
	costs := map[int64]*money.Money{}
	for _, b := range costBuckets {
		var total money.Amount
		cur := ""
		ok := true
		for _, r := range b.Results {
			v, err := money.ParseDecimal(string(r.Amount.Value))
			c := strings.ToUpper(r.Amount.Currency)
			if err != nil || (cur != "" && c != cur) {
				ok = false
				break
			}
			cur = c
			total, _ = money.Add(total, v)
		}
		if ok && cur != "" {
			costs[b.StartTime] = &money.Money{Amount: total, Currency: cur}
		}
	}
	for _, gr := range []struct {
		width string
		g     domain.Granularity
		from  time.Time
		limit int
	}{
		{"1h", domain.GranHour, now.Add(-time.Duration(o.LookbackHours) * time.Hour).Truncate(time.Hour), o.LookbackHours + 1},
		{"1d", domain.GranDay, dayFrom, o.LookbackDays + 1},
	} {
		q := url.Values{}
		q.Set("start_time", fmt.Sprint(gr.from.Unix()))
		q.Set("bucket_width", gr.width)
		q.Set("limit", fmt.Sprint(gr.limit))
		q.Add("group_by", "model")
		q.Add("group_by", "api_key_id")
		bs, err := fetchPages[completionsResult](ctx, a.http, h, base+"/v1/organization/usage/completions", q)
		if err != nil {
			return nil, err
		}
		res.Usage = append(res.Usage, toAggs(bs, gr.g, now, costs)...)
	}
	if o.ListAPIKeys == nil || *o.ListAPIKeys {
		ks, err := a.listKeys(ctx, h, base, o.MaxProjects)
		if err != nil {
			res.Notes = append(res.Notes, "project API key listing unavailable")
		} else {
			res.APIKeys = ks
		}
	}
	return res, nil
}

type project struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type projectKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RedactedValue string `json:"redacted_value"`
	CreatedAt     int64  `json:"created_at"`
	LastUsedAt    *int64 `json:"last_used_at"`
}

type list[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

func (a *Admin) listKeys(ctx context.Context, h http.Header, base string, maxProjects int) ([]domain.APIKeyRecord, error) {
	var projects []project
	after := ""
	for len(projects) < maxProjects {
		u := base + "/v1/organization/projects?limit=100"
		if after != "" {
			u += "&after=" + url.QueryEscape(after)
		}
		var l list[project]
		if _, err := a.http.DoJSON(ctx, providers.Request{URL: u, Header: h}, &l); err != nil {
			return nil, err
		}
		projects = append(projects, l.Data...)
		if !l.HasMore || l.LastID == "" {
			break
		}
		after = l.LastID
	}
	if len(projects) > maxProjects {
		projects = projects[:maxProjects]
	}
	var out []domain.APIKeyRecord
	for _, p := range projects {
		var l list[projectKey]
		u := base + "/v1/organization/projects/" + url.PathEscape(p.ID) + "/api_keys?limit=100"
		if _, err := a.http.DoJSON(ctx, providers.Request{URL: u, Header: h}, &l); err != nil {
			return nil, err
		}
		for _, k := range l.Data {
			rec := domain.APIKeyRecord{ProviderKeyID: k.ID, Name: k.Name, Hint: k.RedactedValue, Project: p.Name}
			if k.CreatedAt > 0 {
				t := time.Unix(k.CreatedAt, 0).UTC()
				rec.CreatedAt = &t
			}
			if k.LastUsedAt != nil && *k.LastUsedAt > 0 {
				t := time.Unix(*k.LastUsedAt, 0).UTC()
				rec.ProviderLastUsedAt = &t
			}
			out = append(out, rec)
		}
	}
	return out, nil
}
