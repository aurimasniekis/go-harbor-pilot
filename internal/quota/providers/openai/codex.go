package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// CodexID is the unofficial ChatGPT/Codex plan usage adapter.
const CodexID domain.ProviderID = "codex_subscription"

// Endpoint and OAuth details from the open-source Codex CLI (openai/codex,
// codex-rs: backend-client and login crates). Not a documented public API.
const (
	DefaultCodexUsageURL     = "https://chatgpt.com/backend-api/wham/usage"
	DefaultCodexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	DefaultCodexTokenURL     = "https://auth.openai.com/oauth/token"
	DefaultCodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultCodexRedirectURI  = "http://localhost:1455/auth/callback"
)

// CodexOptions configure the adapter.
type CodexOptions struct {
	TokenSource     string `json:"token_source"`
	CredentialsFile string `json:"credentials_file"`
	UsageURL        string `json:"usage_url"`
	TokenURL        string `json:"token_url"`
	AuthorizeURL    string `json:"authorize_url"`
	ClientID        string `json:"client_id"`
	// AccountID is the ChatGPT account ID (required for token_source env).
	AccountID string `json:"account_id"`
}

// Codex is the adapter.
type Codex struct {
	deps providers.Deps
	flow *oauthflow.Flow
}

// NewCodex creates the adapter.
func NewCodex(deps providers.Deps, flow *oauthflow.Flow) *Codex {
	return &Codex{deps: deps, flow: flow}
}

// Info implements providers.Adapter.
func (a *Codex) Info() domain.ProviderInfo {
	s, p := providers.Sup, providers.Part
	return domain.ProviderInfo{
		ID: CodexID, DisplayName: "ChatGPT / Codex plan (unofficial)", Family: "openai", AdapterVersion: "1",
		Tier: domain.TierUnofficial, DefaultPollInterval: 10 * time.Minute,
		CredentialTypes: []string{"mounted ~/.codex/auth.json (read-only)", "access token + account ID (env)", "in-app login (encrypted token store)"},
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining:  s("used_percent of primary and secondary windows"),
			domain.CapQuotaUsed:       s(""),
			domain.CapQuotaLimit:      p("normalized percentage only"),
			domain.CapFixedReset:      s("reset_at per window"),
			domain.CapModelQuota:      p("additional per-feature limits when present"),
			domain.CapMonetaryBalance: p("credit balance string when reported (units undocumented)"),
		}),
		Limitations: []string{
			"Uses the ChatGPT backend endpoint the open-source Codex CLI calls; not a documented public API and may change.",
			"Documented alternative: Codex app-server account/rateLimits/read (experimental) or the Codex usage dashboard.",
			"Mounted auth.json is never refreshed or modified; when Codex is not used the token expires.",
		},
		DocsURLs: []string{"https://learn.chatgpt.com/codex/pricing", "https://learn.chatgpt.com/codex/app-server"},
	}
}

func parseCodex(cfg providers.AccountConfig) (CodexOptions, error) {
	var o CodexOptions
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
	if o.TokenSource == oauthflow.ModeEnv && o.AccountID == "" {
		return o, providers.NewError(domain.ErrConfiguration, "options.account_id is required for token_source env")
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
func (a *Codex) Validate(cfg providers.AccountConfig) error {
	_, err := parseCodex(cfg)
	return err
}

// OAuthClient returns the login client.
func (a *Codex) OAuthClient(cfg providers.AccountConfig) (oauthflow.Client, error) {
	o, err := parseCodex(cfg)
	if err != nil {
		return oauthflow.Client{}, err
	}
	c := oauthflow.Client{
		Name: "codex", AuthorizeURL: DefaultCodexAuthorizeURL, TokenURL: DefaultCodexTokenURL, ClientID: DefaultCodexClientID,
		RedirectURI: DefaultCodexRedirectURI, Scopes: []string{"openid", "profile", "email", "offline_access"},
		Encoding: oauthflow.EncodingForm, RefreshEncoding: oauthflow.EncodingJSON,
		ExtraAuthParams: [][2]string{{"id_token_add_organizations", "true"}, {"codex_cli_simplified_flow", "true"}},
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
	return c, nil
}

// jwtClaims decodes (without verifying) a JWT payload; used only to read
// expiry and the account ID of the operator's own token.
func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func jwtExpiry(tok string) time.Time {
	if exp, ok := jwtClaims(tok)["exp"].(float64); ok {
		return time.Unix(int64(exp), 0).UTC()
	}
	return time.Time{}
}

// AccountIDFromIDToken reads chatgpt_account_id from an ID token.
func AccountIDFromIDToken(idToken string) string {
	auth, _ := jwtClaims(idToken)["https://api.openai.com/auth"].(map[string]any)
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}

// ParseAuthJSON parses ~/.codex/auth.json.
func ParseAuthJSON(b []byte) (providers.OAuthToken, error) {
	var f struct {
		Tokens *struct {
			IDToken     string `json:"id_token"`
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return providers.OAuthToken{}, err
	}
	if f.Tokens == nil {
		return providers.OAuthToken{}, fmt.Errorf("auth.json has no ChatGPT tokens (API-key mode is not supported)")
	}
	sub := f.Tokens.AccountID
	if sub == "" {
		sub = AccountIDFromIDToken(f.Tokens.IDToken)
	}
	return providers.OAuthToken{AccessToken: credentials.NewSecret(f.Tokens.AccessToken), ExpiresAt: jwtExpiry(f.Tokens.AccessToken), Subject: sub}, nil
}

type windowSnapshot struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAfterSeconds  int64    `json:"reset_after_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

type rateLimit struct {
	Allowed         bool            `json:"allowed"`
	LimitReached    bool            `json:"limit_reached"`
	PrimaryWindow   *windowSnapshot `json:"primary_window"`
	SecondaryWindow *windowSnapshot `json:"secondary_window"`
}

type codexUsage struct {
	PlanType  string     `json:"plan_type"`
	RateLimit *rateLimit `json:"rate_limit"`
	Credits   *struct {
		HasCredits bool    `json:"has_credits"`
		Unlimited  bool    `json:"unlimited"`
		Balance    *string `json:"balance"`
	} `json:"credits"`
	AdditionalRateLimits []struct {
		LimitName      string     `json:"limit_name"`
		MeteredFeature string     `json:"metered_feature"`
		RateLimit      *rateLimit `json:"rate_limit"`
	} `json:"additional_rate_limits"`
}

func windowObs(prefix, label string, w *windowSnapshot, global bool, model string, at time.Time) (domain.BucketObservation, bool) {
	if w == nil {
		return domain.BucketObservation{}, false
	}
	win := time.Duration(w.LimitWindowSeconds) * time.Second
	key := prefix
	switch {
	case prefix == "primary" && win == 5*time.Hour:
		key, label = "five_hour", "5-hour"
	case prefix == "secondary" && win == 7*24*time.Hour:
		key, label = "seven_day", "Weekly"
	}
	scope := domain.ScopeAccount
	if model != "" {
		scope = domain.ScopeModel
	}
	o := domain.BucketObservation{
		Spec: domain.BucketSpec{Key: key, Label: label, Scope: scope, Kind: domain.KindFixedWindow, Window: win,
			Unit: domain.UnitPercent, Model: model, ResetSemantics: domain.ResetProviderTimestamp, Global: global},
		Quality: domain.QualityProviderReported, Source: "chatgpt:wham/usage", SourceTime: &at,
	}
	if w.UsedPercent != nil {
		u := *w.UsedPercent
		if u < 0 {
			u = 0
		}
		if u > 100 {
			u = 100
		}
		o.Used, o.Remaining, o.Limit = providers.Float(u), providers.Float(100-u), providers.Float(100)
	}
	if w.ResetAt > 0 {
		t := time.Unix(w.ResetAt, 0).UTC()
		o.ResetAt = &t
	}
	return o, true
}

// Fetch implements providers.Adapter.
func (a *Codex) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	o, err := parseCodex(req.Config)
	if err != nil {
		return nil, err
	}
	client, _ := a.OAuthClient(req.Config)
	tok, err := a.flow.Resolve(ctx, req.Config, oauthflow.Source{
		Mode: o.TokenSource, File: o.CredentialsFile, ParseFile: ParseAuthJSON, Client: client,
		SubjectFromToken: func(tr oauthflow.TokenResponse) string { return AccountIDFromIDToken(tr.IDToken) },
	}, a.deps, req.Now)
	if err != nil {
		return nil, err
	}
	acct := tok.Subject
	if o.AccountID != "" {
		acct = o.AccountID
	}
	if acct == "" {
		return nil, providers.NewError(domain.ErrConfiguration, "ChatGPT account ID unknown; set options.account_id")
	}
	u := DefaultCodexUsageURL
	if o.UsageURL != "" {
		u = o.UsageURL
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+tok.AccessToken.Reveal())
	h.Set("ChatGPT-Account-Id", acct)
	var body codexUsage
	if _, err := a.deps.HTTP.DoJSON(ctx, providers.Request{URL: u, Header: h}, &body); err != nil {
		return nil, err
	}
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	if body.RateLimit != nil {
		if ob, ok := windowObs("primary", "Primary window", body.RateLimit.PrimaryWindow, true, "", now); ok {
			res.Buckets = append(res.Buckets, ob)
		}
		if ob, ok := windowObs("secondary", "Secondary window", body.RateLimit.SecondaryWindow, true, "", now); ok {
			res.Buckets = append(res.Buckets, ob)
		}
	}
	for _, add := range body.AdditionalRateLimits {
		if add.RateLimit == nil || add.MeteredFeature == "" {
			continue
		}
		for _, w := range []struct {
			suffix string
			snap   *windowSnapshot
		}{{"primary", add.RateLimit.PrimaryWindow}, {"secondary", add.RateLimit.SecondaryWindow}} {
			key := "additional:" + add.MeteredFeature + ":" + w.suffix
			if ob, ok := windowObs(key, add.LimitName+" ("+w.suffix+")", w.snap, false, add.MeteredFeature, now); ok {
				res.Buckets = append(res.Buckets, ob)
			}
		}
	}
	if body.Credits != nil && body.Credits.Balance != nil && !body.Credits.Unlimited {
		if v, ok := providers.NumFloat(json.Number(*body.Credits.Balance)); ok {
			res.Buckets = append(res.Buckets, domain.BucketObservation{
				Spec: domain.BucketSpec{Key: "credits", Label: "Credits balance", Scope: domain.ScopeAccount, Kind: domain.KindBalance,
					Unit: domain.UnitCredits, ResetSemantics: domain.ResetUnknown, Global: false},
				Remaining: v, Quality: domain.QualityProviderReported, Source: "chatgpt:wham/usage", SourceTime: &now,
			})
		}
	}
	if body.PlanType != "" {
		plan := "plan: " + body.PlanType
		res.MaskedIdentifier = &plan
	}
	return res, nil
}
