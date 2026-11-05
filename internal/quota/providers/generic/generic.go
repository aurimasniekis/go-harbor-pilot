// Package generic implements an operator-configured HTTP JSON adapter.
//
// The operator supplies a URL and declarative field paths; no scripting or
// expression evaluation is supported. Outbound requests are SSRF-guarded:
// HTTPS only by default, resolved addresses are checked at dial time (private,
// loopback, link-local and cloud-metadata destinations are blocked unless
// explicitly allowed), redirects are refused and response size and time are
// bounded.
package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID is the adapter ID.
const ProviderID domain.ProviderID = "generic_http"

// BucketMapping maps response fields to one bucket.
type BucketMapping struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Kind          string `json:"kind"`
	Unit          string `json:"unit"`
	Currency      string `json:"currency"`
	Window        string `json:"window"`
	Scope         string `json:"scope"`
	Model         string `json:"model"`
	Global        *bool  `json:"global"`
	LimitPath     string `json:"limit_path"`
	UsedPath      string `json:"used_path"`
	RemainingPath string `json:"remaining_path"`
	ResetAtPath   string `json:"reset_at_path"`
	// ResetSemantics: provider_timestamp | rolling | none | unknown.
	ResetSemantics string `json:"reset_semantics"`
}

// Options configure the adapter.
type Options struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	// AuthType is bearer | header | none.
	AuthType   string `json:"auth_type"`
	AuthHeader string `json:"auth_header"`
	// AllowPrivateNetwork permits private/loopback destinations and plain
	// HTTP to them. Metadata endpoints remain blocked.
	AllowPrivateNetwork bool            `json:"allow_private_network"`
	AllowedCIDRs        []string        `json:"allowed_cidrs"`
	Timeout             string          `json:"timeout"`
	SourceTimePath      string          `json:"source_time_path"`
	Buckets             []BucketMapping `json:"buckets"`
}

type parsed struct {
	opts    Options
	u       *url.URL
	timeout time.Duration
	specs   []domain.BucketSpec
	policy  providers.SSRFPolicy
}

// Adapter is the generic adapter.
type Adapter struct {
	mu      sync.Mutex
	clients map[string]*providers.HTTPClient
	// transportFor builds the guarded transport (overridable in tests).
	transportFor func(p providers.SSRFPolicy) http.RoundTripper
}

// New creates the adapter.
func New() *Adapter {
	return &Adapter{clients: map[string]*providers.HTTPClient{}, transportFor: func(p providers.SSRFPolicy) http.RoundTripper {
		return providers.GuardedTransport(p)
	}}
}

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	p := providers.Part
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "Generic HTTP", Family: "generic", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes: []string{"api_key (bearer or custom header)", "none"},
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining: p("as mapped by the operator"), domain.CapQuotaUsed: p("as mapped"),
			domain.CapQuotaLimit: p("as mapped"), domain.CapFixedReset: p("as mapped"),
			domain.CapMonetaryBalance: p("as mapped"),
		}),
		Limitations: []string{
			"Operator-defined mapping; Harbor Pilot cannot verify the semantics of the remote endpoint.",
			"HTTPS only unless allow_private_network is set; redirects are refused; responses are limited to 1 MiB.",
		},
	}
}

func parse(cfg providers.AccountConfig) (*parsed, error) {
	var o Options
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return nil, err
	}
	u, err := url.Parse(o.URL)
	if err != nil || u.Host == "" {
		return nil, providers.NewError(domain.ErrConfiguration, "options.url must be an absolute URL")
	}
	if u.User != nil {
		return nil, providers.NewError(domain.ErrConfiguration, "options.url must not embed credentials")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !o.AllowPrivateNetwork {
			return nil, providers.NewError(domain.ErrConfiguration, "options.url must use https (http requires allow_private_network)")
		}
	default:
		return nil, providers.NewError(domain.ErrConfiguration, "options.url must use https")
	}
	switch o.Method {
	case "", "GET":
		o.Method = http.MethodGet
	case "POST":
	default:
		return nil, providers.NewError(domain.ErrConfiguration, "options.method must be GET or POST")
	}
	switch o.AuthType {
	case "", "bearer":
		o.AuthType = "bearer"
		if _, err := providers.RequireSecret(cfg, "api_key"); err != nil {
			return nil, err
		}
	case "header":
		if o.AuthHeader == "" || strings.ContainsAny(o.AuthHeader, " :\r\n") {
			return nil, providers.NewError(domain.ErrConfiguration, "options.auth_header must be a valid header name")
		}
		if _, err := providers.RequireSecret(cfg, "api_key"); err != nil {
			return nil, err
		}
	case "none":
	default:
		return nil, providers.NewError(domain.ErrConfiguration, "options.auth_type must be bearer, header or none")
	}
	p := &parsed{opts: o, u: u, timeout: 10 * time.Second}
	if o.Timeout != "" {
		d, err := time.ParseDuration(o.Timeout)
		if err != nil || d <= 0 || d > 30*time.Second {
			return nil, providers.NewError(domain.ErrConfiguration, "options.timeout must be between 0 and 30s")
		}
		p.timeout = d
	}
	p.policy = providers.SSRFPolicy{AllowPrivate: o.AllowPrivateNetwork}
	for _, c := range o.AllowedCIDRs {
		pre, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, providers.NewError(domain.ErrConfiguration, "options.allowed_cidrs contains an invalid CIDR")
		}
		p.policy.AllowedCIDRs = append(p.policy.AllowedCIDRs, pre)
	}
	if len(o.Buckets) == 0 || len(o.Buckets) > 32 {
		return nil, providers.NewError(domain.ErrConfiguration, "options.buckets must define 1..32 buckets")
	}
	seen := map[string]bool{}
	for i, b := range o.Buckets {
		if b.Key == "" || seen[b.Key] {
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].key must be unique and non-empty", i))
		}
		seen[b.Key] = true
		if b.LimitPath == "" && b.UsedPath == "" && b.RemainingPath == "" {
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d] needs at least one value path", i))
		}
		spec := domain.BucketSpec{Key: b.Key, Label: b.Label, Model: b.Model, Currency: strings.ToUpper(b.Currency), Official: true}
		if spec.Label == "" {
			spec.Label = b.Key
		}
		switch domain.BucketKind(b.Kind) {
		case domain.KindFixedWindow, domain.KindRollingWindow, domain.KindBalance, domain.KindBudget, domain.KindRateLimit:
			spec.Kind = domain.BucketKind(b.Kind)
		case "":
			spec.Kind = domain.KindUnknown
		default:
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].kind is invalid", i))
		}
		switch domain.Unit(b.Unit) {
		case domain.UnitPercent, domain.UnitTokens, domain.UnitRequests, domain.UnitCurrency, domain.UnitCredits, domain.UnitOther:
			spec.Unit = domain.Unit(b.Unit)
		default:
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].unit is invalid", i))
		}
		if spec.Unit == domain.UnitCurrency && len(spec.Currency) != 3 {
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].currency is required for currency units", i))
		}
		if b.Window != "" {
			d, err := time.ParseDuration(b.Window)
			if err != nil || d <= 0 {
				return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].window is invalid", i))
			}
			spec.Window = d
		}
		switch b.Scope {
		case "", "account":
			spec.Scope = domain.ScopeAccount
		case "organization", "project", "model", "model_family", "api_key":
			spec.Scope = domain.BucketScope(b.Scope)
		default:
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].scope is invalid", i))
		}
		spec.Global = b.Global == nil || *b.Global
		switch b.ResetSemantics {
		case "", "unknown":
			spec.ResetSemantics = domain.ResetUnknown
			if b.ResetAtPath != "" {
				spec.ResetSemantics = domain.ResetProviderTimestamp
			}
		case "provider_timestamp", "rolling", "none":
			spec.ResetSemantics = domain.ResetSemantics(b.ResetSemantics)
		default:
			return nil, providers.NewError(domain.ErrConfiguration, fmt.Sprintf("options.buckets[%d].reset_semantics is invalid", i))
		}
		p.specs = append(p.specs, spec)
	}
	return p, nil
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	_, err := parse(cfg)
	return err
}

func (a *Adapter) client(p *parsed) *providers.HTTPClient {
	key := fmt.Sprintf("%v|%v|%s", p.policy.AllowPrivate, p.policy.AllowedCIDRs, p.timeout)
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.clients[key]
	if !ok {
		c = providers.NewHTTPClient(providers.HTTPOptions{
			Timeout: p.timeout, MaxBody: 1 << 20, Transport: a.transportFor(p.policy), MaxRedirects: 0,
			UserAgent: "harbor-pilot-generic",
		})
		a.clients[key] = c
	}
	return c
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	p, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	switch p.opts.AuthType {
	case "bearer":
		k, _ := providers.RequireSecret(req.Config, "api_key")
		h.Set("Authorization", "Bearer "+k)
	case "header":
		k, _ := providers.RequireSecret(req.Config, "api_key")
		h.Set(p.opts.AuthHeader, k)
	}
	resp, err := a.client(p).Do(ctx, providers.Request{Method: p.opts.Method, URL: p.u.String(), Header: h})
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(resp.Body)))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, providers.NewError(domain.ErrInvalidResponse, "response is not valid JSON")
	}
	res := &providers.FetchResult{}
	if p.opts.SourceTimePath != "" {
		if t, ok := timeAt(doc, p.opts.SourceTimePath); ok {
			res.SourceTime = &t
		}
	}
	for i, m := range p.opts.Buckets {
		spec := p.specs[i]
		o := domain.BucketObservation{Spec: spec, Quality: domain.QualityProviderReported, Source: "generic_http", SourceTime: res.SourceTime}
		o.Limit, _ = numAt(doc, m.LimitPath)
		o.Used, _ = numAt(doc, m.UsedPath)
		o.Remaining, _ = numAt(doc, m.RemainingPath)
		if o.Remaining == nil && o.Limit != nil && o.Used != nil {
			v := *o.Limit - *o.Used
			o.Remaining = &v
		}
		if m.ResetAtPath != "" {
			if t, ok := timeAt(doc, m.ResetAtPath); ok {
				o.ResetAt = &t
			}
		}
		if spec.Unit == domain.UnitCurrency {
			o.LimitAmount, o.UsedAmount, o.RemainingAmount = decAt(doc, m.LimitPath), decAt(doc, m.UsedPath), decAt(doc, m.RemainingPath)
		}
		if o.Limit == nil && o.Used == nil && o.Remaining == nil {
			res.Notes = append(res.Notes, fmt.Sprintf("bucket %s: mapped fields absent in response (reported as unknown)", m.Key))
		}
		res.Buckets = append(res.Buckets, o)
	}
	return res, nil
}

// lookup walks a dotted path; numeric segments index arrays.
func lookup(doc any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			n, ok := v[seg]
			if !ok {
				return nil, false
			}
			cur = n
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

func numAt(doc any, path string) (*float64, bool) {
	v, ok := lookup(doc, path)
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case json.Number:
		return providers.NumFloat(t)
	case string:
		return providers.NumFloat(json.Number(strings.TrimSpace(t)))
	}
	return nil, false
}

func decAt(doc any, path string) *money.Amount {
	v, ok := lookup(doc, path)
	if !ok {
		return nil
	}
	var s string
	switch t := v.(type) {
	case json.Number:
		s = string(t)
	case string:
		s = t
	default:
		return nil
	}
	a, err := money.ParseDecimal(s)
	if err != nil {
		return nil
	}
	return &a
}

func timeAt(doc any, path string) (time.Time, bool) {
	v, ok := lookup(doc, path)
	if !ok {
		return time.Time{}, false
	}
	switch t := v.(type) {
	case string:
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts.UTC(), true
		}
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			return epoch(n), true
		}
	case json.Number:
		if n, err := t.Float64(); err == nil {
			return epoch(n), true
		}
	}
	return time.Time{}, false
}

// epoch interprets seconds or (if large) milliseconds.
func epoch(n float64) time.Time {
	if n > 1e12 {
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}
