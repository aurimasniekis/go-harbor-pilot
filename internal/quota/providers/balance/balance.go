// Package balance implements small adapters for providers that document a
// balance or key-limit endpoint: DeepSeek, Moonshot (Kimi) and OpenRouter.
package balance

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Provider IDs.
const (
	DeepSeekID   domain.ProviderID = "deepseek"
	MoonshotID   domain.ProviderID = "moonshot"
	OpenRouterID domain.ProviderID = "openrouter"
)

type baseOpts struct {
	BaseURL string `json:"base_url"`
}

func base(cfg providers.AccountConfig, def string, out any) (string, error) {
	if err := providers.DecodeOptions(cfg.Options, out); err != nil {
		return "", err
	}
	var b string
	switch o := out.(type) {
	case *baseOpts:
		b = o.BaseURL
	case *openRouterOpts:
		b = o.BaseURL
	}
	if b == "" {
		return def, nil
	}
	return providers.ValidateBaseURL(b)
}

func moneyObs(key, label, currency string, remaining string, at time.Time, source string) (domain.BucketObservation, bool) {
	a, err := money.ParseDecimal(remaining)
	if err != nil {
		return domain.BucketObservation{}, false
	}
	f := a.Float64()
	return domain.BucketObservation{
		Spec: domain.BucketSpec{Key: key, Label: label, Scope: domain.ScopeAccount, Kind: domain.KindBalance,
			Unit: domain.UnitCurrency, Currency: strings.ToUpper(currency), ResetSemantics: domain.ResetNone, Global: true, Official: true},
		Remaining: &f, RemainingAmount: &a, Quality: domain.QualityProviderReported, Source: source, SourceTime: &at,
	}, true
}

// ---------------- DeepSeek ----------------

// DeepSeek adapter (GET /user/balance).
type DeepSeek struct{ http *providers.HTTPClient }

// NewDeepSeek creates the adapter.
func NewDeepSeek(d providers.Deps) *DeepSeek { return &DeepSeek{http: d.HTTP} }

// Info implements providers.Adapter.
func (a *DeepSeek) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		ID: DeepSeekID, DisplayName: "DeepSeek", Family: "deepseek", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes: []string{"api_key"}, DefaultPollInterval: 15 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapMonetaryBalance: providers.Sup("total balance per currency (granted + topped up)"),
			domain.CapQuotaRemaining:  providers.Unsup("no quota windows"),
			domain.CapMonetarySpend:   providers.Unsup("no usage or cost history API"),
		}),
		Limitations: []string{"Balance only; usage history is not available through the API."},
		DocsURLs:    []string{"https://api-docs.deepseek.com/api/get-user-balance"},
	}
}

// Validate implements providers.Adapter.
func (a *DeepSeek) Validate(cfg providers.AccountConfig) error {
	if _, err := base(cfg, "https://api.deepseek.com", &baseOpts{}); err != nil {
		return err
	}
	_, err := providers.RequireSecret(cfg, "api_key")
	return err
}

// Fetch implements providers.Adapter.
func (a *DeepSeek) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	b, err := base(req.Config, "https://api.deepseek.com", &baseOpts{})
	if err != nil {
		return nil, err
	}
	key, err := providers.RequireSecret(req.Config, "api_key")
	if err != nil {
		return nil, err
	}
	var body struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if _, err := a.http.DoJSON(ctx, providers.Request{URL: b + "/user/balance", Header: providers.BearerHeader(key)}, &body); err != nil {
		return nil, err
	}
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	for _, bi := range body.BalanceInfos {
		if len(bi.Currency) != 3 {
			continue
		}
		if o, ok := moneyObs("balance:"+strings.ToUpper(bi.Currency), "Balance ("+strings.ToUpper(bi.Currency)+")", bi.Currency, bi.TotalBalance, now, "deepseek:user/balance"); ok {
			res.Buckets = append(res.Buckets, o)
		}
	}
	if !body.IsAvailable {
		res.Notes = append(res.Notes, "provider reports the balance is insufficient for API calls")
	}
	return res, nil
}

// ---------------- Moonshot / Kimi ----------------

// Moonshot adapter (GET /v1/users/me/balance).
type Moonshot struct{ http *providers.HTTPClient }

// NewMoonshot creates the adapter.
func NewMoonshot(d providers.Deps) *Moonshot { return &Moonshot{http: d.HTTP} }

// Info implements providers.Adapter.
func (a *Moonshot) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		ID: MoonshotID, DisplayName: "Moonshot (Kimi)", Family: "moonshot", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes: []string{"api_key"}, DefaultPollInterval: 15 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapMonetaryBalance: providers.Sup("available, voucher and cash balance (USD)"),
			domain.CapQuotaRemaining:  providers.Unsup("no quota windows"),
		}),
		Limitations: []string{"Balance only. Cash balance can be negative."},
		DocsURLs:    []string{"https://platform.kimi.ai/docs/api/balance"},
	}
}

// Validate implements providers.Adapter.
func (a *Moonshot) Validate(cfg providers.AccountConfig) error {
	if _, err := base(cfg, "https://api.moonshot.ai", &baseOpts{}); err != nil {
		return err
	}
	_, err := providers.RequireSecret(cfg, "api_key")
	return err
}

// Fetch implements providers.Adapter.
func (a *Moonshot) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	b, err := base(req.Config, "https://api.moonshot.ai", &baseOpts{})
	if err != nil {
		return nil, err
	}
	key, err := providers.RequireSecret(req.Config, "api_key")
	if err != nil {
		return nil, err
	}
	var body struct {
		Data *struct {
			Available json.Number `json:"available_balance"`
			Voucher   json.Number `json:"voucher_balance"`
			Cash      json.Number `json:"cash_balance"`
		} `json:"data"`
	}
	if _, err := a.http.DoJSON(ctx, providers.Request{URL: b + "/v1/users/me/balance", Header: providers.BearerHeader(key)}, &body); err != nil {
		return nil, err
	}
	if body.Data == nil {
		return nil, providers.NewError(domain.ErrInvalidResponse, "balance response has no data")
	}
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	for _, x := range []struct{ key, label, v string }{
		{"balance:available", "Available balance", string(body.Data.Available)},
		{"balance:voucher", "Voucher balance", string(body.Data.Voucher)},
		{"balance:cash", "Cash balance", string(body.Data.Cash)},
	} {
		if o, ok := moneyObs(x.key, x.label, "USD", x.v, now, "moonshot:users/me/balance"); ok {
			if x.key != "balance:available" {
				o.Spec.Global = false
			}
			res.Buckets = append(res.Buckets, o)
		}
	}
	return res, nil
}

// ---------------- OpenRouter ----------------

type openRouterOpts struct {
	BaseURL     string `json:"base_url"`
	TokenSource string `json:"token_source"` // env (api_key) | login (PKCE-issued key)
}

// OpenRouter adapter (GET /api/v1/key, optional GET /api/v1/credits).
type OpenRouter struct {
	deps providers.Deps
	flow *oauthflow.Flow
}

// NewOpenRouter creates the adapter.
func NewOpenRouter(d providers.Deps, flow *oauthflow.Flow) *OpenRouter {
	return &OpenRouter{deps: d, flow: flow}
}

// Info implements providers.Adapter.
func (a *OpenRouter) Info() domain.ProviderInfo {
	s, p := providers.Sup, providers.Part
	return domain.ProviderInfo{
		ID: OpenRouterID, DisplayName: "OpenRouter", Family: "openrouter", AdapterVersion: "1", Tier: domain.TierOfficial,
		CredentialTypes:     []string{"api_key", "management_key (optional, for account credits)", "in-app PKCE connect"},
		DefaultPollInterval: 10 * time.Minute,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining:  p("key credit limit remaining when a limit is set"),
			domain.CapQuotaLimit:      p("key credit limit"),
			domain.CapMonetarySpend:   s("key usage (credits): daily, weekly, monthly"),
			domain.CapMonetaryBalance: p("account credits with a management key"),
		}),
		Limitations: []string{"Credits are OpenRouter units; rate-limit headers are only sent on HTTP 429."},
		DocsURLs:    []string{"https://openrouter.ai/docs/api-reference/limits", "https://openrouter.ai/docs/guides/overview/auth/oauth"},
	}
}

func (a *OpenRouter) opts(cfg providers.AccountConfig) (openRouterOpts, string, error) {
	var o openRouterOpts
	b, err := base(cfg, "https://openrouter.ai", &o)
	if err != nil {
		return o, "", err
	}
	if o.TokenSource == "" {
		o.TokenSource = oauthflow.ModeEnv
	}
	if o.TokenSource != oauthflow.ModeEnv && o.TokenSource != oauthflow.ModeLogin {
		return o, "", providers.NewError(domain.ErrConfiguration, "options.token_source must be env or login")
	}
	return o, b, nil
}

// Validate implements providers.Adapter.
func (a *OpenRouter) Validate(cfg providers.AccountConfig) error {
	o, _, err := a.opts(cfg)
	if err != nil {
		return err
	}
	if o.TokenSource == oauthflow.ModeEnv {
		_, err = providers.RequireSecret(cfg, "api_key")
	}
	return err
}

// OAuthClient returns the documented PKCE flow that issues a user API key.
func (a *OpenRouter) OAuthClient(cfg providers.AccountConfig) (oauthflow.Client, error) {
	_, b, err := a.opts(cfg)
	if err != nil {
		return oauthflow.Client{}, err
	}
	return oauthflow.Client{Name: "openrouter", AuthorizeURL: b + "/auth", TokenURL: b + "/api/v1/auth/keys",
		KeyExchange: true, CallbackParam: "callback_url"}, nil
}

func numObs(key, label string, limit, remaining *float64, kind domain.BucketKind, global bool, at time.Time) domain.BucketObservation {
	o := domain.BucketObservation{
		Spec: domain.BucketSpec{Key: key, Label: label, Scope: domain.ScopeAPIKey, Kind: kind, Unit: domain.UnitCredits,
			ResetSemantics: domain.ResetUnknown, Global: global, Official: true},
		Limit: limit, Remaining: remaining, Quality: domain.QualityProviderReported, Source: "openrouter", SourceTime: &at,
	}
	if limit != nil && remaining != nil {
		u := *limit - *remaining
		o.Used = &u
	}
	return o
}

// Fetch implements providers.Adapter.
func (a *OpenRouter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	o, b, err := a.opts(req.Config)
	if err != nil {
		return nil, err
	}
	var key string
	if o.TokenSource == oauthflow.ModeLogin {
		tok, err := a.flow.Resolve(ctx, req.Config, oauthflow.Source{Mode: oauthflow.ModeLogin}, a.deps, req.Now)
		if err != nil {
			return nil, err
		}
		key = tok.AccessToken.Reveal()
	} else if key, err = providers.RequireSecret(req.Config, "api_key"); err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			Label          string       `json:"label"`
			Limit          *json.Number `json:"limit"`
			LimitRemaining *json.Number `json:"limit_remaining"`
			LimitReset     *string      `json:"limit_reset"`
			Usage          json.Number  `json:"usage"`
			UsageDaily     json.Number  `json:"usage_daily"`
			UsageWeekly    json.Number  `json:"usage_weekly"`
			UsageMonthly   json.Number  `json:"usage_monthly"`
			IsFreeTier     bool         `json:"is_free_tier"`
		} `json:"data"`
	}
	if _, err := a.deps.HTTP.DoJSON(ctx, providers.Request{URL: b + "/api/v1/key", Header: providers.BearerHeader(key)}, &body); err != nil {
		return nil, err
	}
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	d := body.Data
	if d.Limit != nil {
		lim, _ := providers.NumFloat(*d.Limit)
		var rem *float64
		if d.LimitRemaining != nil {
			rem, _ = providers.NumFloat(*d.LimitRemaining)
		}
		label := "Key credit limit"
		if d.LimitReset != nil && *d.LimitReset != "" {
			label += " (" + *d.LimitReset + ")"
		}
		res.Buckets = append(res.Buckets, numObs("key_limit", label, lim, rem, domain.KindBudget, true, now))
	}
	for _, x := range []struct {
		key, label string
		v          json.Number
		window     time.Duration
	}{{"usage:daily", "Key usage today", d.UsageDaily, 24 * time.Hour}, {"usage:weekly", "Key usage this week", d.UsageWeekly, 7 * 24 * time.Hour},
		{"usage:monthly", "Key usage this month", d.UsageMonthly, 30 * 24 * time.Hour}} {
		if v, ok := providers.NumFloat(x.v); ok {
			ob := numObs(x.key, x.label, nil, nil, domain.KindFixedWindow, false, now)
			ob.Used = v
			ob.Spec.Window = x.window
			res.Buckets = append(res.Buckets, ob)
		}
	}
	if mk, ok := req.Config.Credentials.Get("management_key"); ok {
		var cr struct {
			Data struct {
				TotalCredits json.Number `json:"total_credits"`
				TotalUsage   json.Number `json:"total_usage"`
			} `json:"data"`
		}
		if _, err := a.deps.HTTP.DoJSON(ctx, providers.Request{URL: b + "/api/v1/credits", Header: providers.BearerHeader(mk.Reveal())}, &cr); err == nil {
			tc, ok1 := providers.NumFloat(cr.Data.TotalCredits)
			tu, ok2 := providers.NumFloat(cr.Data.TotalUsage)
			if ok1 && ok2 {
				rem := *tc - *tu
				ob := numObs("credits", "Account credits", tc, &rem, domain.KindBalance, true, now)
				ob.Spec.Scope = domain.ScopeAccount
				ob.Spec.ResetSemantics = domain.ResetNone
				res.Buckets = append(res.Buckets, ob)
			}
		} else {
			res.Notes = append(res.Notes, "account credits unavailable (management key rejected)")
		}
	}
	if d.Label != "" {
		l := providers.MaskIdentifier(d.Label)
		res.MaskedIdentifier = &l
	}
	return res, nil
}
