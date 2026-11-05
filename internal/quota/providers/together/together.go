// Package together implements the Together AI billing usage adapter using
// the documented (beta) endpoint GET /v1/billing/usage
// (https://docs.together.ai/reference/billing-usage). Organizations without
// access receive HTTP 404, which is reported as an unsupported state rather
// than a failure.
//
// Line items carry exact decimal-string costs in USD; quantities are in each
// product's native unit and are therefore not mapped to token counters.
package together

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
const ProviderID domain.ProviderID = "together"

// DefaultBaseURL is the documented API origin.
const DefaultBaseURL = "https://api.together.ai"

// otherKey marks line items without an API key (non-inference products) so
// the api_key breakdown still partitions the total.
const otherKey = "__other__"

// unsupportedMsg explains the 404 state.
const unsupportedMsg = "billing usage API is in beta and not enabled for this organization"

// Options configure the adapter.
type Options struct {
	BaseURL string `json:"base_url"`
	// OrganizationID is optional during the beta and required at GA.
	OrganizationID string `json:"organization_id"`
	// MaxPages bounds pagination per billing month (1..50, default 10).
	MaxPages int `json:"max_pages"`
}

type parsedOpts struct {
	Options
	base string
}

func parse(cfg providers.AccountConfig) (*parsedOpts, error) {
	var o Options
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return nil, err
	}
	p := &parsedOpts{Options: o, base: DefaultBaseURL}
	if o.BaseURL != "" {
		b, err := providers.ValidateBaseURL(o.BaseURL)
		if err != nil {
			return nil, err
		}
		p.base = b
	}
	if o.MaxPages == 0 {
		p.MaxPages = 10
	} else if o.MaxPages < 1 || o.MaxPages > 50 {
		return nil, providers.NewError(domain.ErrConfiguration, "options.max_pages must be 1..50")
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
func New(deps providers.Deps) providers.Adapter { return &Adapter{http: deps.HTTP} }

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "Together AI", Family: "together", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes:     []string{"api_key"},
		DefaultPollInterval: 30 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapMonetarySpend:     s("hourly cost line items (exact USD decimal strings)"),
			domain.CapModelUsage:        p("model from pricing dimensions when present, otherwise the product name"),
			domain.CapAPIKeyUsage:       p("api_key_id on inference line items only; other products grouped as __other__"),
			domain.CapOrgProjectScope:   p("organization scope; project_id attribute on inference line items"),
			domain.CapHistoricalBackfil: p("current billing month, plus the previous month during its first two days"),
			domain.CapTokenCounters:     u("quantities are in native product units and are not mapped to tokens"),
			domain.CapMonetaryBalance:   u("no balance endpoint"),
			domain.CapQuotaRemaining:    u("no quota-window API"),
		}),
		Limitations: []string{
			"The billing usage API is in beta; organizations without access get HTTP 404 (shown as unsupported).",
			"Quantities are product-native units, so token and request counters are not derived.",
		},
		DocsURLs: []string{"https://docs.together.ai/reference/billing-usage"},
	}
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	_, err := parse(cfg)
	return err
}

type lineItem struct {
	ProductName       string            `json:"product_name"`
	Quantity          string            `json:"quantity"`
	UnitPrice         string            `json:"unit_price"`
	Cost              string            `json:"cost"`
	PricingDimensions map[string]string `json:"pricing_dimensions"`
	Attributes        map[string]string `json:"attributes"`
}

type window struct {
	Date      string     `json:"date"`
	StartTime time.Time  `json:"start_time"`
	EndTime   time.Time  `json:"end_time"`
	LineItems []lineItem `json:"line_items"`
}

type usagePage struct {
	Currency   string   `json:"currency"`
	Data       []window `json:"data"`
	NextCursor *string  `json:"next_cursor"`
}

// model picks the most specific model name for a line item.
func (li lineItem) model() string {
	for _, k := range []string{"model", "model_name"} {
		if v := strings.TrimSpace(li.PricingDimensions[k]); v != "" {
			return v
		}
		if v := strings.TrimSpace(li.Attributes[k]); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(li.ProductName); v != "" {
		return v
	}
	return "unknown"
}

type bucketKey struct {
	start, end time.Time
	breakdown  string
	dim        string
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	p, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	key, _ := providers.RequireSecret(req.Config, "api_key")
	now := req.Now.UTC()
	months := []time.Time{time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)}
	if now.Day() <= 2 {
		months = append([]time.Time{months[0].AddDate(0, -1, 0)}, months...)
	}

	res := &providers.FetchResult{SourceTime: &now}
	sums := map[bucketKey]money.Amount{}
	currency := map[bucketKey]string{}
	keys := map[string]string{} // api_key_id → project_id
	for _, m := range months {
		truncated, err := a.month(ctx, key, p, m, func(w window, cur string) {
			for _, li := range w.LineItems {
				amt, err := money.ParseDecimal(li.Cost)
				if err != nil {
					continue
				}
				kid := strings.TrimSpace(li.Attributes["api_key_id"])
				if kid == "" {
					kid = otherKey
				} else if _, ok := keys[kid]; !ok || keys[kid] == "" {
					keys[kid] = li.Attributes["project_id"]
				}
				for _, bk := range []bucketKey{
					{w.StartTime, w.EndTime, "total", ""},
					{w.StartTime, w.EndTime, "model", li.model()},
					{w.StartTime, w.EndTime, "api_key", kid},
				} {
					if s, err := money.Add(sums[bk], amt); err == nil {
						sums[bk] = s
						currency[bk] = cur
					}
				}
			}
		})
		if err != nil {
			var pe *providers.Error
			if errors.As(err, &pe) && pe.Kind == domain.ErrUnsupported {
				return nil, &providers.Error{Kind: domain.ErrUnsupported, Message: unsupportedMsg, HTTPStatus: pe.HTTPStatus}
			}
			return nil, err
		}
		if truncated {
			res.Notes = append(res.Notes, "billing usage for "+m.Format("2006-01")+" exceeded the page limit; older windows were not fetched")
		}
	}

	bks := make([]bucketKey, 0, len(sums))
	for bk := range sums {
		bks = append(bks, bk)
	}
	sort.Slice(bks, func(i, j int) bool {
		if !bks[i].start.Equal(bks[j].start) {
			return bks[i].start.Before(bks[j].start)
		}
		if bks[i].breakdown != bks[j].breakdown {
			return bks[i].breakdown < bks[j].breakdown
		}
		return bks[i].dim < bks[j].dim
	})
	for _, bk := range bks {
		cur := currency[bk]
		var dims domain.UsageDimensions
		switch bk.breakdown {
		case "model":
			dims.RequestedModel = bk.dim
		case "api_key":
			dims.APIKeyID = bk.dim
		}
		gran := domain.GranHour
		if bk.end.Sub(bk.start) >= 24*time.Hour {
			gran = domain.GranDay
		}
		res.Usage = append(res.Usage, domain.UsageAggregate{
			Origin: domain.OriginProviderReported, Breakdown: bk.breakdown, Dimensions: dims,
			IntervalStart: bk.start, IntervalEnd: bk.end, Granularity: gran,
			ActualCost: &money.Money{Amount: sums[bk], Currency: cur}, CurrencyVerified: cur == "USD",
			Partial: now.Before(bk.end), Quality: domain.QualityProviderReported,
		})
	}

	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		res.APIKeys = append(res.APIKeys, domain.APIKeyRecord{ProviderKeyID: id, Project: keys[id]})
	}
	return res, nil
}

// month fetches all pages of one billing month (bounded); it reports whether
// pagination was cut off.
func (a *Adapter) month(ctx context.Context, key string, p *parsedOpts, m time.Time, visit func(window, string)) (bool, error) {
	cursor := ""
	for page := 0; page < p.MaxPages; page++ {
		q := url.Values{}
		q.Set("month", m.Format("2006-01"))
		q.Set("granularity", "hour")
		q.Set("limit", strconv.Itoa(100))
		if p.OrganizationID != "" {
			q.Set("organization_id", p.OrganizationID)
		}
		if cursor != "" {
			q.Set("after", cursor)
		}
		var body usagePage
		if _, err := a.http.DoJSON(ctx, providers.Request{URL: p.base + "/v1/billing/usage?" + q.Encode(),
			Header: providers.BearerHeader(key), NotFoundIsUnsupported: true}, &body); err != nil {
			return false, err
		}
		cur := strings.ToUpper(strings.TrimSpace(body.Currency))
		if cur == "" {
			cur = "USD" // documented default currency of the endpoint
		}
		for _, w := range body.Data {
			if w.StartTime.IsZero() || !w.EndTime.After(w.StartTime) {
				continue
			}
			w.StartTime, w.EndTime = w.StartTime.UTC(), w.EndTime.UTC()
			visit(w, cur)
		}
		if body.NextCursor == nil || *body.NextCursor == "" || *body.NextCursor == cursor {
			return false, nil
		}
		cursor = *body.NextCursor
	}
	return true, nil
}
