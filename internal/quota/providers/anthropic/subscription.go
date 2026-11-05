package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// SubscriptionID is the unofficial Claude Pro/Max subscription adapter.
const SubscriptionID domain.ProviderID = "claude_subscription"

// Endpoint and OAuth client details below were taken from the Claude Code
// 2.1.302 client bundle. They are undocumented, may change without notice,
// and Anthropic's terms restrict third-party use of Claude.ai tokens (see
// docs/provider-support.md). All of them are overridable via options.
const (
	DefaultUsageURL      = "https://api.anthropic.com/api/oauth/usage"
	DefaultOAuthBeta     = "oauth-2025-04-20"
	DefaultAuthorizeURL  = "https://claude.com/cai/oauth/authorize"
	DefaultTokenURL      = "https://platform.claude.com/v1/oauth/token"
	DefaultRedirectURI   = "https://platform.claude.com/oauth/code/callback"
	DefaultOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

// SubscriptionOptions configure the adapter.
type SubscriptionOptions struct {
	// TokenSource: file | env | login.
	TokenSource     string   `json:"token_source"`
	CredentialsFile string   `json:"credentials_file"`
	UsageURL        string   `json:"usage_url"`
	TokenURL        string   `json:"token_url"`
	AuthorizeURL    string   `json:"authorize_url"`
	ClientID        string   `json:"client_id"`
	Scopes          []string `json:"scopes"`
}

// Subscription is the adapter.
type Subscription struct {
	deps providers.Deps
	flow *oauthflow.Flow
}

// NewSubscription creates the adapter.
func NewSubscription(deps providers.Deps, flow *oauthflow.Flow) *Subscription {
	return &Subscription{deps: deps, flow: flow}
}

// Info implements providers.Adapter.
func (a *Subscription) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: SubscriptionID, DisplayName: "Claude subscription (unofficial)", Family: "claude", AdapterVersion: "1",
		Tier: domain.TierUnofficial, DefaultPollInterval: 10 * time.Minute,
		CredentialTypes: []string{"mounted Claude Code credentials file (read-only)", "OAuth access token (env)", "in-app login (encrypted token store)"},
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining:  s("utilization 0–100 per window"),
			domain.CapQuotaUsed:       s(""),
			domain.CapQuotaLimit:      p("normalized percentage only"),
			domain.CapFixedReset:      s("resets_at per window"),
			domain.CapModelQuota:      s("weekly per-model windows"),
			domain.CapMonetarySpend:   p("extra-usage utilization only"),
			domain.CapAccountIdentity: u("not requested"),
		}),
		Limitations: []string{
			"Undocumented endpoint used by Claude Code's /usage; may change, rate limit (HTTP 429) or stop working without notice.",
			"Anthropic's Claude Code legal terms prohibit third-party tools from collecting or using Claude.ai credentials; enabling this adapter is the operator's decision.",
			"Mounted credential files are never refreshed; when Claude Code is not running the token expires and polling reports authentication failure.",
			"The documented alternative is the claude_code_statusline push adapter.",
		},
		DocsURLs: []string{"https://code.claude.com/docs/en/legal-and-compliance", "https://code.claude.com/docs/en/statusline"},
	}
}

func parseSubOpts(cfg providers.AccountConfig) (SubscriptionOptions, error) {
	var o SubscriptionOptions
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return o, err
	}
	if o.TokenSource == "" {
		o.TokenSource = oauthflow.ModeFile
	}
	if !oauthflow.ValidMode(o.TokenSource) {
		return o, providers.NewError(domain.ErrConfiguration, "options.token_source must be file, env or login")
	}
	if o.TokenSource == oauthflow.ModeFile && o.CredentialsFile == "" {
		return o, providers.NewError(domain.ErrConfiguration, "options.credentials_file is required for token_source file")
	}
	for _, u := range []string{o.UsageURL, o.TokenURL, o.AuthorizeURL} {
		if u != "" {
			if _, err := providers.ValidateBaseURL(u); err != nil {
				return o, err
			}
		}
	}
	return o, nil
}

// Validate implements providers.Adapter.
func (a *Subscription) Validate(cfg providers.AccountConfig) error {
	o, err := parseSubOpts(cfg)
	if err != nil {
		return err
	}
	if o.TokenSource == oauthflow.ModeEnv {
		if _, ok := cfg.Credentials.Get("access_token"); !ok {
			if _, ok := cfg.Credentials.Get("api_key"); !ok {
				return providers.NewError(domain.ErrConfiguration, "credential access_token is required for token_source env")
			}
		}
	}
	return nil
}

// OAuthClient returns the login client for an account.
func (a *Subscription) OAuthClient(cfg providers.AccountConfig) (oauthflow.Client, error) {
	o, err := parseSubOpts(cfg)
	if err != nil {
		return oauthflow.Client{}, err
	}
	c := oauthflow.Client{
		Name: "claude", AuthorizeURL: DefaultAuthorizeURL, TokenURL: DefaultTokenURL, ClientID: DefaultOAuthClientID,
		RedirectURI: DefaultRedirectURI, Scopes: []string{"user:profile"}, Encoding: oauthflow.EncodingJSON,
		StateInExchange: true, IncludeScopeInRefresh: true, ExtraAuthParams: [][2]string{{"code", "true"}},
	}
	if o.AuthorizeURL != "" {
		c.AuthorizeURL = o.AuthorizeURL
	}
	if o.TokenURL != "" {
		c.TokenURL = o.TokenURL
	}
	if o.ClientID != "" {
		c.ClientID = o.ClientID
	}
	if len(o.Scopes) > 0 {
		c.Scopes = o.Scopes
	}
	return c, nil
}

// claudeCredentialsFile is the shape of ~/.claude/.credentials.json.
type claudeCredentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken  string   `json:"accessToken"`
		RefreshToken string   `json:"refreshToken"`
		ExpiresAt    int64    `json:"expiresAt"` // unix ms
		Scopes       []string `json:"scopes"`
	} `json:"claudeAiOauth"`
}

// ParseCredentialsFile parses a Claude Code credentials file.
func ParseCredentialsFile(b []byte) (providers.OAuthToken, error) {
	var f claudeCredentialsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return providers.OAuthToken{}, err
	}
	t := providers.OAuthToken{AccessToken: credentials.NewSecret(f.ClaudeAiOauth.AccessToken), Scopes: f.ClaudeAiOauth.Scopes}
	if f.ClaudeAiOauth.ExpiresAt > 0 {
		t.ExpiresAt = time.UnixMilli(f.ClaudeAiOauth.ExpiresAt).UTC()
	}
	return t, nil
}

type window struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type usageResponse struct {
	FiveHour          *window `json:"five_hour"`
	SevenDay          *window `json:"seven_day"`
	SevenDayOpus      *window `json:"seven_day_opus"`
	SevenDaySonnet    *window `json:"seven_day_sonnet"`
	SevenDayOAuthApps *window `json:"seven_day_oauth_apps"`
	Limits            []struct {
		Kind     string   `json:"kind"`
		Percent  *float64 `json:"percent"`
		ResetsAt *string  `json:"resets_at"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
	ExtraUsage *struct {
		IsEnabled   bool     `json:"is_enabled"`
		Utilization *float64 `json:"utilization"`
	} `json:"extra_usage"`
}

func obsFromWindow(spec domain.BucketSpec, w *window, at time.Time) domain.BucketObservation {
	o := domain.BucketObservation{Spec: spec, Quality: domain.QualityProviderReported, Source: "claude:oauth/usage", SourceTime: &at}
	if w == nil {
		return o
	}
	if w.Utilization != nil {
		used := clamp(*w.Utilization)
		o.Used, o.Remaining, o.Limit = providers.Float(used), providers.Float(100-used), providers.Float(100)
	}
	if w.ResetsAt != nil {
		if t, err := time.Parse(time.RFC3339, *w.ResetsAt); err == nil {
			t = t.UTC()
			o.ResetAt = &t
		}
	}
	return o
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func pctSpec(key, label string, window time.Duration, scope domain.BucketScope, model string, global bool) domain.BucketSpec {
	return domain.BucketSpec{Key: key, Label: label, Scope: scope, Kind: domain.KindFixedWindow, Window: window,
		Unit: domain.UnitPercent, Model: model, ModelFamily: model, ResetSemantics: domain.ResetProviderTimestamp,
		Global: global, Official: false}
}

// Fetch implements providers.Adapter.
func (a *Subscription) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	o, err := parseSubOpts(req.Config)
	if err != nil {
		return nil, err
	}
	client, _ := a.OAuthClient(req.Config)
	tok, err := a.flow.Resolve(ctx, req.Config, oauthflow.Source{
		Mode: o.TokenSource, File: o.CredentialsFile, ParseFile: ParseCredentialsFile, Client: client,
	}, a.deps, req.Now)
	if err != nil {
		return nil, err
	}
	u := DefaultUsageURL
	if o.UsageURL != "" {
		u = o.UsageURL
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+tok.AccessToken.Reveal())
	h.Set("anthropic-beta", DefaultOAuthBeta)
	var body usageResponse
	if _, err := a.deps.HTTP.DoJSON(ctx, providers.Request{URL: u, Header: h}, &body); err != nil {
		return nil, err
	}
	now := req.Now.UTC()
	week := 7 * 24 * time.Hour
	res := &providers.FetchResult{SourceTime: &now}
	res.Buckets = append(res.Buckets,
		obsFromWindow(pctSpec("five_hour", "5-hour session", 5*time.Hour, domain.ScopeAccount, "", true), body.FiveHour, now),
		obsFromWindow(pctSpec("seven_day", "7-day weekly", week, domain.ScopeAccount, "", true), body.SevenDay, now),
	)
	if body.SevenDayOpus != nil {
		res.Buckets = append(res.Buckets, obsFromWindow(pctSpec("seven_day:model:opus", "7-day Opus", week, domain.ScopeModel, "opus", false), body.SevenDayOpus, now))
	}
	if body.SevenDaySonnet != nil {
		res.Buckets = append(res.Buckets, obsFromWindow(pctSpec("seven_day:model:sonnet", "7-day Sonnet", week, domain.ScopeModel, "sonnet", false), body.SevenDaySonnet, now))
	}
	if body.SevenDayOAuthApps != nil {
		res.Buckets = append(res.Buckets, obsFromWindow(pctSpec("seven_day:oauth_apps", "7-day OAuth apps", week, domain.ScopeAccount, "", false), body.SevenDayOAuthApps, now))
	}
	seen := map[string]bool{}
	for _, l := range body.Limits {
		if l.Kind != "weekly_scoped" || l.Scope == nil || l.Scope.Model == nil || l.Scope.Model.DisplayName == "" {
			continue
		}
		name := strings.ToLower(l.Scope.Model.DisplayName)
		key := "seven_day:model:" + name
		if seen[key] || (name == "opus" && body.SevenDayOpus != nil) || (name == "sonnet" && body.SevenDaySonnet != nil) {
			continue
		}
		seen[key] = true
		res.Buckets = append(res.Buckets, obsFromWindow(pctSpec(key, "7-day "+l.Scope.Model.DisplayName, week, domain.ScopeModel, name, false),
			&window{Utilization: l.Percent, ResetsAt: l.ResetsAt}, now))
	}
	if body.ExtraUsage != nil && body.ExtraUsage.IsEnabled {
		spec := domain.BucketSpec{Key: "extra_usage", Label: "Extra usage (monthly)", Scope: domain.ScopeAccount, Kind: domain.KindBudget,
			Unit: domain.UnitPercent, ResetSemantics: domain.ResetUnknown, Global: false, Official: false}
		res.Buckets = append(res.Buckets, obsFromWindow(spec, &window{Utilization: body.ExtraUsage.Utilization}, now))
	}
	return res, nil
}
