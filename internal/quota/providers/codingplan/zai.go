package codingplan

import (
	"context"
	"fmt"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Z.ai hosts. The China mainland service (BigModel) uses the same path.
const (
	ZaiGlobalBase = "https://api.z.ai"
	ZaiChinaBase  = "https://open.bigmodel.cn"
	zaiQuotaPath  = "/api/monitor/usage/quota/limit"
)

// Zai is the Z.ai GLM Coding Plan adapter (unofficial).
type Zai struct{ http *providers.HTTPClient }

// NewZai creates the adapter.
func NewZai(d providers.Deps) *Zai { return &Zai{http: d.HTTP} }

// Info implements providers.Adapter.
func (a *Zai) Info() domain.ProviderInfo {
	s, p := providers.Sup, providers.Part
	return domain.ProviderInfo{
		ID: ZaiID, DisplayName: "Z.ai GLM Coding Plan (unofficial)", Family: "zai", AdapterVersion: "1",
		Tier: domain.TierUnofficial, DefaultPollInterval: 10 * time.Minute,
		CredentialTypes: []string{"api_key (Z.ai / BigModel API key)"},
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining:  s("token-window usage percentage. " + communityNote),
			domain.CapQuotaUsed:       s(communityNote),
			domain.CapQuotaLimit:      p("token totals when reported; windows are normalized to percent"),
			domain.CapFixedReset:      p("nextResetTime when present"),
			domain.CapRequestCounters: p("MCP / tool-call quota (TIME_LIMIT) counts"),
		}),
		Limitations: []string{
			communityNote,
			"Two TOKENS_LIMIT windows (5-hour and weekly) are told apart only by their unit/number fields.",
			"Unit codes (1=day, 3=hour, 5=minute, 6=week) come from a single open-source client.",
		},
		DocsURLs: []string{"https://docs.z.ai/devpack/overview", "https://github.com/onllm-dev/onwatch", "https://github.com/steipete/CodexBar"},
	}
}

// Validate implements providers.Adapter.
func (a *Zai) Validate(cfg providers.AccountConfig) error {
	if _, err := parseOptions(cfg, ZaiGlobalBase, ZaiChinaBase); err != nil {
		return err
	}
	_, err := providers.RequireSecret(cfg, "api_key")
	return err
}

type zaiLimit struct {
	Type          string  `json:"type"`
	Unit          flexNum `json:"unit"`
	Number        flexNum `json:"number"`
	Usage         flexNum `json:"usage"`        // total limit
	CurrentValue  flexNum `json:"currentValue"` // amount used
	Remaining     flexNum `json:"remaining"`
	Percentage    flexNum `json:"percentage"`
	NextResetTime flexNum `json:"nextResetTime"`
}

type zaiResponse struct {
	Code    flexNum `json:"code"`
	Success *bool   `json:"success"`
	Data    *struct {
		Limits   []zaiLimit `json:"limits"`
		Level    string     `json:"level"`
		PlanName string     `json:"planName"`
	} `json:"data"`
}

// zaiWindow converts the unit code and number to a duration.
func zaiWindow(l zaiLimit) time.Duration {
	if !l.Unit.known() || !l.Number.known() || l.Number.val() <= 0 {
		return 0
	}
	unit := map[int]time.Duration{1: 24 * time.Hour, 3: time.Hour, 5: time.Minute, 6: 7 * 24 * time.Hour}[int(l.Unit.val())]
	return time.Duration(l.Number.val()) * unit
}

// Fetch implements providers.Adapter.
func (a *Zai) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	base, err := parseOptions(req.Config, ZaiGlobalBase, ZaiChinaBase)
	if err != nil {
		return nil, err
	}
	key, err := providers.RequireSecret(req.Config, "api_key")
	if err != nil {
		return nil, err
	}
	// onWatch sends the raw key without a scheme; CodexBar sends
	// "Bearer <key>" and notes BigModel accepts both. Bearer is used here.
	var body zaiResponse
	if _, err := a.http.DoJSON(ctx, providers.Request{URL: base + zaiQuotaPath, Header: providers.BearerHeader(key)}, &body); err != nil {
		return nil, err
	}
	// Z.ai can answer HTTP 200 with an error code in the body.
	if code := int(body.Code.val()); body.Code.known() && code != 0 && code != 200 {
		switch code {
		case 401, 403:
			return nil, &providers.Error{Kind: domain.ErrAuthentication, Message: "provider rejected the credentials", HTTPStatus: code}
		case 429:
			return nil, providers.NewError(domain.ErrRateLimited, "provider rate limit reached")
		}
		return nil, providers.NewError(domain.ErrInvalidResponse, fmt.Sprintf("provider returned error code %d", code))
	}
	if body.Success != nil && !*body.Success {
		return nil, providers.NewError(domain.ErrInvalidResponse, "provider reported an unsuccessful response")
	}
	if body.Data == nil {
		return nil, providers.NewError(domain.ErrInvalidResponse, "response has no quota data")
	}
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	seen := map[string]int{}
	for _, l := range body.Data.Limits {
		win := zaiWindow(l)
		var ob domain.BucketObservation
		switch l.Type {
		case "TOKENS_LIMIT":
			k, label := windowKey(win, "tokens")
			ob.Spec = domain.BucketSpec{Key: k, Label: label, Scope: domain.ScopeAccount, Kind: domain.KindFixedWindow, Window: win,
				Unit: domain.UnitPercent, Global: true}
			// Precise percentage from counts when both are known; otherwise the
			// provider's integer percentage.
			switch {
			case l.Usage.known() && l.Usage.val() > 0 && l.CurrentValue.known():
				u := clampPct(l.CurrentValue.val() / l.Usage.val() * 100)
				ob.Used, ob.Remaining, ob.Limit = providers.Float(u), providers.Float(100-u), providers.Float(100)
			case l.Percentage.known():
				u := clampPct(l.Percentage.val())
				ob.Used, ob.Remaining, ob.Limit = providers.Float(u), providers.Float(100-u), providers.Float(100)
			}
		case "TIME_LIMIT":
			k, _ := windowKey(win, "mcp_tools")
			if k == "five_hour" || k == "seven_day" {
				k = "mcp_tools:" + k
			}
			ob.Spec = domain.BucketSpec{Key: k, Label: "MCP / tool calls", Scope: domain.ScopeAccount, Kind: domain.KindFixedWindow, Window: win,
				Unit: domain.UnitRequests, Global: false}
			if l.Usage.known() {
				ob.Limit = providers.Float(l.Usage.val())
			}
			if l.CurrentValue.known() {
				ob.Used = providers.Float(l.CurrentValue.val())
			}
			if l.Remaining.known() {
				ob.Remaining = providers.Float(l.Remaining.val())
			}
		default:
			// CREDIT_LIMIT and unknown types: semantics unverified, skipped.
			continue
		}
		if seen[ob.Spec.Key]++; seen[ob.Spec.Key] > 1 {
			ob.Spec.Key = fmt.Sprintf("%s#%d", ob.Spec.Key, seen[ob.Spec.Key])
		}
		ob.Spec.ResetSemantics = domain.ResetUnknown
		if t := l.NextResetTime.epoch(); t != nil {
			ob.ResetAt = t
			ob.Spec.ResetSemantics = domain.ResetProviderTimestamp
		}
		ob.Quality, ob.Source, ob.SourceTime = domain.QualityProviderReported, "zai:monitor/usage/quota/limit", &now
		res.Buckets = append(res.Buckets, ob)
	}
	plan := body.Data.PlanName
	if plan == "" {
		plan = body.Data.Level
	}
	if plan != "" {
		p := "plan: " + plan
		res.MaskedIdentifier = &p
	}
	if len(res.Buckets) == 0 {
		res.Notes = append(res.Notes, "no recognised quota windows in the response")
	}
	return res, nil
}
