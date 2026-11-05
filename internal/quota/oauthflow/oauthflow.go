// Package oauthflow implements provider-agnostic OAuth 2.0 authorization-code
// flows with PKCE (S256) for in-app logins, plus token refresh.
//
// Harbor Pilot usually runs on a server, so flows that redirect to a fixed
// local or provider-hosted callback are completed by pasting the code (or
// the full redirected URL) into the UI. Pending flows expire after 10
// minutes and are single-use.
package oauthflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Encoding of the token endpoint request body.
type Encoding string

// Encodings.
const (
	EncodingJSON Encoding = "json"
	EncodingForm Encoding = "form"
)

// Client describes one provider's OAuth client.
type Client struct {
	Name         string
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	RedirectURI  string
	Scopes       []string
	// ExtraAuthParams are appended to the authorize URL.
	ExtraAuthParams [][2]string
	Encoding        Encoding
	// RefreshEncoding overrides Encoding for refresh requests.
	RefreshEncoding Encoding
	// StateInExchange includes `state` in the token exchange body.
	StateInExchange bool
	// IncludeScopeInRefresh sends the scope with refresh requests.
	IncludeScopeInRefresh bool
	// KeyExchange is used for providers (OpenRouter) whose flow returns an
	// API key instead of OAuth tokens: POST {code, code_verifier,
	// code_challenge_method} as JSON and read `key`.
	KeyExchange bool
	// CallbackParam names the redirect_uri parameter (default redirect_uri).
	CallbackParam string
}

// TokenResponse is the provider's token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	Key          string `json:"key"`
}

type pending struct {
	client   Client
	verifier string
	account  domain.AccountID
	expires  time.Time
}

// Flow tracks pending authorizations.
type Flow struct {
	http *providers.HTTPClient
	now  func() time.Time
	mu   sync.Mutex
	pend map[string]pending
}

// New creates a flow manager.
func New(http *providers.HTTPClient, now func() time.Time) *Flow {
	return &Flow{http: http, now: now, pend: map[string]pending{}}
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Challenge computes the S256 PKCE challenge.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Start begins an authorization and returns the URL to open and the state.
func (f *Flow) Start(c Client, account domain.AccountID) (string, string, error) {
	u, err := url.Parse(c.AuthorizeURL)
	if err != nil || u.Scheme != "https" {
		return "", "", errors.New("authorize URL must be https")
	}
	verifier := randomURLSafe(48)
	state := randomURLSafe(24)
	q := u.Query()
	if !c.KeyExchange {
		q.Set("response_type", "code")
		q.Set("client_id", c.ClientID)
	}
	cb := c.CallbackParam
	if cb == "" {
		cb = "redirect_uri"
	}
	if c.RedirectURI != "" {
		ru := c.RedirectURI
		if c.KeyExchange {
			// The key-exchange flow has no state parameter; carry it in the
			// callback URL so the completion can be matched.
			if cu, err := url.Parse(ru); err == nil {
				cq := cu.Query()
				cq.Set("state", state)
				cu.RawQuery = cq.Encode()
				ru = cu.String()
			}
		}
		q.Set(cb, ru)
	}
	if len(c.Scopes) > 0 {
		q.Set("scope", strings.Join(c.Scopes, " "))
	}
	q.Set("code_challenge", Challenge(verifier))
	q.Set("code_challenge_method", "S256")
	if !c.KeyExchange {
		q.Set("state", state)
	}
	for _, kv := range c.ExtraAuthParams {
		q.Set(kv[0], kv[1])
	}
	u.RawQuery = q.Encode()
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for k, p := range f.pend {
		if now.After(p.expires) {
			delete(f.pend, k)
		}
	}
	if len(f.pend) > 64 {
		return "", "", errors.New("too many pending logins; try again later")
	}
	f.pend[state] = pending{client: c, verifier: verifier, account: account, expires: now.Add(10 * time.Minute)}
	return u.String(), state, nil
}

// ErrUnknownState means the flow expired, was already used, or never existed.
var ErrUnknownState = errors.New("login session expired or unknown; start again")

// ParseCode extracts the authorization code (and state, when present) from a
// pasted value: a full redirect URL, "code#state" or a bare code.
func ParseCode(input string) (code, state string) {
	input = strings.TrimSpace(input)
	if u, err := url.Parse(input); err == nil && u.Scheme != "" && u.RawQuery != "" {
		q := u.Query()
		return q.Get("code"), q.Get("state")
	}
	if c, s, ok := strings.Cut(input, "#"); ok {
		return c, s
	}
	return input, ""
}

// Complete exchanges a pasted code for tokens. The flow is single-use.
func (f *Flow) Complete(ctx context.Context, account domain.AccountID, state, pasted string) (TokenResponse, Client, error) {
	code, pastedState := ParseCode(pasted)
	if pastedState != "" && state != "" && pastedState != state {
		return TokenResponse{}, Client{}, errors.New("pasted state does not match this login session")
	}
	if state == "" {
		state = pastedState
	}
	f.mu.Lock()
	p, ok := f.pend[state]
	if ok {
		delete(f.pend, state)
	}
	f.mu.Unlock()
	if !ok || f.now().After(p.expires) || p.account != account {
		return TokenResponse{}, Client{}, ErrUnknownState
	}
	if code == "" || len(code) > 2048 {
		return TokenResponse{}, Client{}, errors.New("authorization code missing")
	}
	c := p.client
	var tr TokenResponse
	if c.KeyExchange {
		_, err := f.http.DoJSON(ctx, providers.Request{Method: "POST", URL: c.TokenURL, Body: map[string]string{
			"code": code, "code_verifier": p.verifier, "code_challenge_method": "S256",
		}}, &tr)
		return tr, c, sanitize(err)
	}
	req := providers.Request{Method: "POST", URL: c.TokenURL}
	fields := map[string]string{
		"grant_type": "authorization_code", "code": code, "redirect_uri": c.RedirectURI,
		"client_id": c.ClientID, "code_verifier": p.verifier,
	}
	if c.StateInExchange {
		fields["state"] = state
	}
	setBody(&req, c.Encoding, fields)
	_, err := f.http.DoJSON(ctx, req, &tr)
	return tr, c, sanitize(err)
}

// Refresh exchanges a refresh token. Providers rotate refresh tokens; callers
// must persist the returned refresh token before using the access token.
func (f *Flow) Refresh(ctx context.Context, c Client, refreshToken string) (TokenResponse, error) {
	fields := map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken, "client_id": c.ClientID}
	if c.IncludeScopeInRefresh && len(c.Scopes) > 0 {
		fields["scope"] = strings.Join(c.Scopes, " ")
	}
	req := providers.Request{Method: "POST", URL: c.TokenURL}
	enc := c.Encoding
	if c.RefreshEncoding != "" {
		enc = c.RefreshEncoding
	}
	setBody(&req, enc, fields)
	var tr TokenResponse
	_, err := f.http.DoJSON(ctx, req, &tr)
	return tr, sanitize(err)
}

func setBody(req *providers.Request, enc Encoding, fields map[string]string) {
	if enc == EncodingForm {
		v := url.Values{}
		for k, x := range fields {
			v.Set(k, x)
		}
		req.Form = v
		return
	}
	req.Body = fields
}

// sanitize maps token-endpoint failures to typed errors without bodies.
func sanitize(err error) error {
	if err == nil {
		return nil
	}
	pe := providers.AsError(err)
	if pe.HTTPStatus == 400 || pe.HTTPStatus == 401 {
		return &providers.Error{Kind: domain.ErrAuthentication, Message: "the provider rejected the authorization code or refresh token", HTTPStatus: pe.HTTPStatus}
	}
	return pe
}

// ToToken converts a response into a stored token.
func ToToken(tr TokenResponse, prev providers.OAuthToken, now time.Time) providers.OAuthToken {
	t := prev
	t.ObtainedAt = now
	if tr.AccessToken != "" {
		t.AccessToken = credentialSecret(tr.AccessToken)
	}
	if tr.Key != "" {
		t.AccessToken = credentialSecret(tr.Key)
		t.ExpiresAt = time.Time{}
	}
	if tr.RefreshToken != "" {
		t.RefreshToken = credentialSecret(tr.RefreshToken)
	}
	if tr.ExpiresIn > 0 {
		t.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	if tr.Scope != "" {
		t.Scopes = strings.Fields(tr.Scope)
	}
	return t
}
