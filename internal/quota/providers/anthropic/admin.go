// Package anthropic implements Anthropic adapters: the official Admin API
// (organization usage and cost) and the unofficial Claude subscription
// usage endpoint.
package anthropic

import (
	"context"
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
const AdminID domain.ProviderID = "anthropic_admin"

// DefaultBaseURL is the Anthropic API origin.
const DefaultBaseURL = "https://api.anthropic.com"

const apiVersion = "2023-06-01"

// AdminOptions configure the adapter.
type AdminOptions struct {
	BaseURL string `json:"base_url"`
	// LookbackHours of hourly usage (1..168).
	LookbackHours int `json:"lookback_hours"`
	// LookbackDays of daily usage and cost (1..31).
	LookbackDays int `json:"lookback_days"`
	// ListAPIKeys fetches API key metadata (default true).
	ListAPIKeys *bool `json:"list_api_keys"`
}

// Admin is the adapter.
type Admin struct{ http *providers.HTTPClient }

// NewAdmin creates the adapter.
func NewAdmin(deps providers.Deps) *Admin { return &Admin{http: deps.HTTP} }

// Info implements providers.Adapter.
func (a *Admin) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: AdminID, DisplayName: "Anthropic API (Admin)", Family: "claude", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes: []string{"Admin API key (sk-ant-admin…)"}, DefaultPollInterval: 15 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapTokenCounters:     s("uncached input, cache read, cache creation (5m/1h), output"),
			domain.CapMonetarySpend:     p("cost report is daily and excludes Priority Tier"),
			domain.CapModelUsage:        s(""),
			domain.CapAPIKeyUsage:       s("group_by api_key_id"),
			domain.CapAPIKeyInventory:   s("name, status, partial key hint"),
			domain.CapOrgProjectScope:   s("workspaces"),
			domain.CapHistoricalBackfil: s("hourly ≤7 days per request, daily ≤31"),
			domain.CapRequestCounters:   u("usage report has no request counts"),
			domain.CapQuotaRemaining:    u("no remaining-quota endpoint; rate-limit API returns configured limits only"),
		}),
		Limitations: []string{
			"Usage data typically appears within 5 minutes; the current hour is partial.",
			"Cost is reported per day only; hourly costs are estimates when pricing is configured.",
			"Not available for individual accounts; requires an organization Admin API key.",
		},
		DocsURLs: []string{"https://platform.claude.com/docs/en/manage-claude/usage-cost-api"},
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
	if o.LookbackHours < 1 || o.LookbackHours > 168 || o.LookbackDays < 1 || o.LookbackDays > 31 {
		return o, "", providers.NewError(domain.ErrConfiguration, "lookback_hours must be 1..168 and lookback_days 1..31")
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

func (a *Admin) headers(key string) http.Header {
	h := http.Header{}
	h.Set("x-api-key", key)
	h.Set("anthropic-version", apiVersion)
	return h
}

type usageResult struct {
	UncachedInput int64 `json:"uncached_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation *struct {
		E5m int64 `json:"ephemeral_5m_input_tokens"`
		E1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Output      int64   `json:"output_tokens"`
	APIKeyID    *string `json:"api_key_id"`
	WorkspaceID *string `json:"workspace_id"`
	Model       *string `json:"model"`
	ServiceTier *string `json:"service_tier"`
}

type bucket[T any] struct {
	StartingAt string `json:"starting_at"`
	EndingAt   string `json:"ending_at"`
	Results    []T    `json:"results"`
}

type page[T any] struct {
	Data     []bucket[T] `json:"data"`
	HasMore  bool        `json:"has_more"`
	NextPage *string     `json:"next_page"`
}

func fetchPages[T any](ctx context.Context, c *providers.HTTPClient, h http.Header, base string, q url.Values) ([]bucket[T], error) {
	var out []bucket[T]
	for i := 0; i < 50; i++ {
		var p page[T]
		if _, err := c.DoJSON(ctx, providers.Request{URL: base + "?" + q.Encode(), Header: h}, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || p.NextPage == nil || *p.NextPage == "" {
			return out, nil
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

type sums struct {
	input, cacheRead, cacheWrite, output int64
	cost                                 *money.Amount
}

func (s *sums) add(r usageResult) {
	s.input += r.UncachedInput
	s.cacheRead += r.CacheRead
	if r.CacheCreation != nil {
		s.cacheWrite += r.CacheCreation.E5m + r.CacheCreation.E1h
	}
	s.output += r.Output
}

func (s sums) aggregate(breakdown string, d domain.UsageDimensions, start, end time.Time, gran domain.Granularity, now time.Time) domain.UsageAggregate {
	ag := domain.UsageAggregate{
		Origin: domain.OriginProviderReported, Breakdown: breakdown, Dimensions: d, IntervalStart: start, IntervalEnd: end,
		Granularity: gran, Quality: domain.QualityProviderReported, CurrencyVerified: true, Partial: now.Before(end.Add(5 * time.Minute)),
		// Anthropic reports uncached input separately from cache tokens.
		Tokens: domain.TokenCounts{Input: providers.Int64(s.input), Output: providers.Int64(s.output),
			CacheRead: providers.Int64(s.cacheRead), CacheWrite: providers.Int64(s.cacheWrite)},
	}
	if s.cost != nil {
		ag.ActualCost = &money.Money{Amount: *s.cost, Currency: "USD"}
	}
	return ag
}

// usageAggregates converts buckets grouped by model and api_key_id into four
// consistent breakdowns: total, model, api_key and api_key_model. All four
// derive from the same rows, so they partition identical usage.
func usageAggregates(buckets []bucket[usageResult], gran domain.Granularity, now time.Time, costs map[string]map[string]money.Amount) []domain.UsageAggregate {
	var out []domain.UsageAggregate
	for _, b := range buckets {
		start, err1 := time.Parse(time.RFC3339, b.StartingAt)
		end, err2 := time.Parse(time.RFC3339, b.EndingAt)
		if err1 != nil || err2 != nil {
			continue
		}
		start, end = start.UTC(), end.UTC()
		total := sums{}
		byModel := map[string]*sums{}
		byKey := map[string]*sums{}
		byKeyModel := map[[2]string]*sums{}
		for _, r := range b.Results {
			m, k := str(r.Model), str(r.APIKeyID)
			total.add(r)
			get(byModel, m).add(r)
			get(byKey, k).add(r)
			if byKeyModel[[2]string{k, m}] == nil {
				byKeyModel[[2]string{k, m}] = &sums{}
			}
			byKeyModel[[2]string{k, m}].add(r)
		}
		day := start.Format("2006-01-02")
		if gran == domain.GranDay {
			if c, ok := costs[day]; ok {
				var t money.Amount
				for _, v := range c {
					t, _ = money.Add(t, v)
				}
				total.cost = &t
				for m, s := range byModel {
					if v, ok := c[m]; ok {
						vv := v
						s.cost = &vv
					}
				}
			}
		}
		out = append(out, total.aggregate("total", domain.UsageDimensions{}, start, end, gran, now))
		for _, m := range sortedKeys(byModel) {
			out = append(out, byModel[m].aggregate("model", domain.UsageDimensions{RequestedModel: m}, start, end, gran, now))
		}
		for _, k := range sortedKeys(byKey) {
			out = append(out, byKey[k].aggregate("api_key", domain.UsageDimensions{APIKeyID: k}, start, end, gran, now))
		}
		for km, s := range byKeyModel {
			out = append(out, s.aggregate("api_key_model", domain.UsageDimensions{APIKeyID: km[0], RequestedModel: km[1]}, start, end, gran, now))
		}
	}
	return out
}

func get(m map[string]*sums, k string) *sums {
	if m[k] == nil {
		m[k] = &sums{}
	}
	return m[k]
}

func sortedKeys(m map[string]*sums) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type costResult struct {
	Currency string  `json:"currency"`
	Amount   string  `json:"amount"`
	Model    *string `json:"model"`
}

// Fetch implements providers.Adapter.
func (a *Admin) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	o, base, err := parseAdmin(req.Config)
	if err != nil {
		return nil, err
	}
	key, _ := providers.RequireSecret(req.Config, "api_key")
	h := a.headers(key)
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}

	// Daily cost (exact decimal strings in cents), grouped by description to
	// obtain per-model amounts.
	cq := url.Values{}
	cq.Set("starting_at", now.Add(-time.Duration(o.LookbackDays)*24*time.Hour).Truncate(24*time.Hour).Format(time.RFC3339))
	cq.Set("bucket_width", "1d")
	cq.Set("limit", fmt.Sprint(o.LookbackDays+1))
	cq.Add("group_by[]", "description")
	costBuckets, err := fetchPages[costResult](ctx, a.http, h, base+"/v1/organizations/cost_report", cq)
	if err != nil {
		return nil, err
	}
	costs := map[string]map[string]money.Amount{}
	for _, b := range costBuckets {
		start, err := time.Parse(time.RFC3339, b.StartingAt)
		if err != nil {
			continue
		}
		day := start.UTC().Format("2006-01-02")
		if costs[day] == nil {
			costs[day] = map[string]money.Amount{}
		}
		for _, r := range b.Results {
			if !strings.EqualFold(r.Currency, "USD") {
				continue
			}
			v, err := money.ParseScaledDecimal(r.Amount, 2) // documented as cents
			if err != nil {
				continue
			}
			m := str(r.Model)
			costs[day][m], _ = money.Add(costs[day][m], v)
		}
	}

	for _, gran := range []struct {
		width string
		g     domain.Granularity
		from  time.Time
		limit int
	}{
		{"1h", domain.GranHour, now.Add(-time.Duration(o.LookbackHours) * time.Hour).Truncate(time.Hour), o.LookbackHours + 1},
		{"1d", domain.GranDay, now.Add(-time.Duration(o.LookbackDays) * 24 * time.Hour).Truncate(24 * time.Hour), o.LookbackDays + 1},
	} {
		q := url.Values{}
		q.Set("starting_at", gran.from.Format(time.RFC3339))
		q.Set("bucket_width", gran.width)
		q.Set("limit", fmt.Sprint(gran.limit))
		q.Add("group_by[]", "model")
		q.Add("group_by[]", "api_key_id")
		buckets, err := fetchPages[usageResult](ctx, a.http, h, base+"/v1/organizations/usage_report/messages", q)
		if err != nil {
			return nil, err
		}
		res.Usage = append(res.Usage, usageAggregates(buckets, gran.g, now, costs)...)
	}

	if o.ListAPIKeys == nil || *o.ListAPIKeys {
		keys, err := a.listKeys(ctx, h, base)
		if err == nil {
			res.APIKeys = keys
		} else {
			res.Notes = append(res.Notes, "API key listing unavailable")
		}
	}
	return res, nil
}

type apiKey struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	PartialKeyHint string  `json:"partial_key_hint"`
	WorkspaceID    *string `json:"workspace_id"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
}

func (a *Admin) listKeys(ctx context.Context, h http.Header, base string) ([]domain.APIKeyRecord, error) {
	var out []domain.APIKeyRecord
	after := ""
	for i := 0; i < 20; i++ {
		q := url.Values{}
		q.Set("limit", "100")
		if after != "" {
			q.Set("after_id", after)
		}
		var p struct {
			Data    []apiKey `json:"data"`
			HasMore bool     `json:"has_more"`
			LastID  *string  `json:"last_id"`
		}
		if _, err := a.http.DoJSON(ctx, providers.Request{URL: base + "/v1/organizations/api_keys?" + q.Encode(), Header: h}, &p); err != nil {
			return nil, err
		}
		for _, k := range p.Data {
			st := k.Status
			rec := domain.APIKeyRecord{ProviderKeyID: k.ID, Name: k.Name, Hint: k.PartialKeyHint, Status: &st, Workspace: str(k.WorkspaceID)}
			if t, err := time.Parse(time.RFC3339, k.CreatedAt); err == nil {
				t = t.UTC()
				rec.CreatedAt = &t
			}
			if k.ExpiresAt != nil {
				if t, err := time.Parse(time.RFC3339, *k.ExpiresAt); err == nil {
					t = t.UTC()
					rec.ExpiresAt = &t
				}
			}
			out = append(out, rec)
		}
		if !p.HasMore || p.LastID == nil {
			break
		}
		after = *p.LastID
	}
	return out, nil
}
