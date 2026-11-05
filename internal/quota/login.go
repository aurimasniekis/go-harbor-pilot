package quota

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/openai"
)

// oauthCapable adapters support in-app login.
type oauthCapable interface {
	OAuthClient(cfg providers.AccountConfig) (oauthflow.Client, error)
}

// LoginView is the sign-in state of a monitor using in-app login.
type LoginView struct {
	MonitorID    string     `json:"monitor_id"`
	Name         string     `json:"name"`
	Adapter      string     `json:"adapter"`
	Tier         string     `json:"tier"`
	SignedIn     bool       `json:"signed_in"`
	ExpiresAt    *time.Time `json:"expires_at"`
	ObtainedAt   *time.Time `json:"obtained_at"`
	Instructions string     `json:"instructions"`
}

// Login errors.
var (
	ErrNotLoginMonitor = errors.New("monitor is not configured with token_source login")
	ErrBadCallback     = errors.New("callback_url must be an absolute URL on this server's origin")
)

// LoginError is a sanitized failure from the provider's token endpoint.
type LoginError struct{ Message string }

func (e *LoginError) Error() string { return e.Message }

func loginInstructions(adapter string) string {
	switch adapter {
	case "claude_subscription":
		return "Open the link, approve access, then paste the code shown on the provider page (format code#state). Unofficial integration: the provider's terms may restrict third-party use of subscription credentials."
	case "codex_subscription":
		return "Open the link and sign in. The browser then redirects to a localhost URL that fails to load; copy that full URL from the address bar and paste it here. Unofficial integration."
	case "openrouter":
		return "Open the link and authorize. You are redirected back here and the connection completes automatically."
	}
	return "Open the link and paste the resulting code."
}

func (s *Service) loginMonitor(ctx context.Context, id string) (providers.AccountConfig, oauthCapable, error) {
	snap := s.o.CP.Snapshot()
	m := snap.Monitors[id]
	if m == nil {
		return providers.AccountConfig{}, nil, ErrNotLoginMonitor
	}
	if src, _ := m.Spec.Options["token_source"].(string); src != oauthflow.ModeLogin {
		return providers.AccountConfig{}, nil, ErrNotLoginMonitor
	}
	a, ok := s.reg.Get(domain.ProviderID(m.Spec.Adapter))
	if !ok {
		return providers.AccountConfig{}, nil, ErrNotLoginMonitor
	}
	oc, ok := a.(oauthCapable)
	if !ok {
		return providers.AccountConfig{}, nil, ErrNotLoginMonitor
	}
	info := a.Info()
	cfg := providers.AccountConfig{Account: domain.Account{ID: domain.AccountID(m.ID), ProviderID: info.ID, Alias: m.Name, Enabled: true, Tier: info.Tier},
		Options: m.Spec.Options, Credentials: s.resolveCredentials(ctx, m.ID)}
	return cfg, oc, nil
}

// Logins lists monitors that use in-app login.
func (s *Service) Logins() []LoginView {
	snap := s.o.CP.Snapshot()
	out := []LoginView{}
	if snap == nil {
		return out
	}
	ts := &tokenStore{db: s.o.DB, keyring: s.o.Keyring, now: s.o.Clock.Now}
	for _, m := range snap.Monitors {
		if src, _ := m.Spec.Options["token_source"].(string); src != oauthflow.ModeLogin {
			continue
		}
		v := LoginView{MonitorID: m.ID, Name: m.Name, Adapter: m.Spec.Adapter, Instructions: loginInstructions(m.Spec.Adapter)}
		if a, ok := s.reg.Get(domain.ProviderID(m.Spec.Adapter)); ok {
			v.Tier = string(a.Info().Tier)
		}
		if s.o.Keyring != nil {
			if t, err := ts.Get(domain.AccountID(m.ID)); err == nil {
				v.SignedIn = true
				if !t.ExpiresAt.IsZero() {
					e := t.ExpiresAt
					v.ExpiresAt = &e
				}
				o := t.ObtainedAt
				v.ObtainedAt = &o
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StartLogin begins an authorization. origin is this server's public origin;
// key-exchange flows redirect back to callbackURL, which must be on it.
func (s *Service) StartLogin(ctx context.Context, id, callbackURL, origin string) (authorizeURL, state, instructions string, err error) {
	cfg, oc, err := s.loginMonitor(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	client, err := oc.OAuthClient(cfg)
	if err != nil {
		return "", "", "", &LoginError{Message: providers.AsError(err).Message}
	}
	if client.KeyExchange {
		if callbackURL == "" || !strings.HasPrefix(callbackURL, strings.TrimRight(origin, "/")+"/") {
			return "", "", "", ErrBadCallback
		}
		client.RedirectURI = callbackURL
	}
	u, st, err := s.flow.Start(client, domain.AccountID(id))
	if err != nil {
		return "", "", "", &LoginError{Message: "authorization could not be started"}
	}
	return u, st, loginInstructions(string(cfg.Account.ProviderID)), nil
}

// CompleteLogin exchanges the pasted code and stores the token sealed.
func (s *Service) CompleteLogin(ctx context.Context, id, state, code string) (string, error) {
	if _, _, err := s.loginMonitor(ctx, id); err != nil {
		return "", err
	}
	tr, _, err := s.flow.Complete(ctx, domain.AccountID(id), state, code)
	if err != nil {
		msg := "authorization failed"
		var pe *providers.Error
		if errors.As(err, &pe) {
			msg = pe.Message
		}
		return "", &LoginError{Message: msg}
	}
	tok := oauthflow.ToToken(tr, providers.OAuthToken{}, s.o.Clock.Now())
	if tr.IDToken != "" {
		tok.Subject = openai.AccountIDFromIDToken(tr.IDToken)
	}
	if tok.AccessToken.IsZero() {
		return "", &LoginError{Message: "the provider returned no token"}
	}
	ts := &tokenStore{db: s.o.DB, keyring: s.o.Keyring, now: s.o.Clock.Now}
	if err := ts.Put(domain.AccountID(id), tok); err != nil {
		return "", err
	}
	s.SyncNow(ctx)
	return s.Refresh(id), nil
}

// Logout removes a stored token.
func (s *Service) Logout(ctx context.Context, id string) error {
	if _, _, err := s.loginMonitor(ctx, id); err != nil {
		return err
	}
	ts := &tokenStore{db: s.o.DB, keyring: s.o.Keyring, now: s.o.Clock.Now}
	return ts.Delete(domain.AccountID(id))
}
