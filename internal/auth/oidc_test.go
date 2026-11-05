package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// testOP is a minimal OpenID Provider: discovery, JWKS, token endpoint with
// PKCE verification, ES256-signed ID tokens. It follows the claim shape used
// by Porch (groups under the "porch" claim).
type testOP struct {
	srv    *httptest.Server
	key    *ecdsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]codeGrant
	sub    string
	email  string
	groups []string
	// tamper hooks
	wrongNonce  bool
	wrongAud    bool
	issOverride string
}

type codeGrant struct{ nonce, challenge string }

func newTestOP(t *testing.T) *testOP {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	op := &testOP{key: k, codes: map[string]codeGrant{}, sub: "u_123", email: "dev@example.test", groups: []string{"hp-operators"}}
	mux := http.NewServeMux()
	op.srv = httptest.NewServer(mux)
	t.Cleanup(op.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": op.srv.URL, "authorization_endpoint": op.srv.URL + "/oauth2/authorize",
			"token_endpoint": op.srv.URL + "/oauth2/token", "jwks_uri": op.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"ES256"}, "code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &k.PublicKey, KeyID: "k1", Algorithm: "ES256", Use: "sig"}}})
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		op.mu.Lock()
		g, ok := op.codes[r.Form.Get("code")]
		delete(op.codes, r.Form.Get("code"))
		op.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		nonce := g.nonce
		if op.wrongNonce {
			nonce = "other"
		}
		aud := "hp-client"
		if op.wrongAud {
			aud = "someone-else"
		}
		iss := op.srv.URL
		if op.issOverride != "" {
			iss = op.issOverride
		}
		claims := map[string]any{"iss": iss, "sub": op.sub, "aud": aud, "exp": time.Now().Add(5 * time.Minute).Unix(),
			"iat": time.Now().Unix(), "nonce": nonce, "email": op.email, "email_verified": true, "name": "Dev",
			"porch": map[string]any{"v": 1, "groups": op.groups}}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: k}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		payload, _ := json.Marshal(claims)
		jws, _ := signer.Sign(payload)
		raw, _ := jws.CompactSerialize()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw, "expires_in": 300})
	})
	return op
}

// authorize simulates the browser hitting the authorization endpoint and
// returns the code + state the OP would redirect back with.
func (op *testOP) authorize(t *testing.T, authURL string) (code, state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
		t.Fatalf("missing PKCE/nonce in %s", authURL)
	}
	code = RandomString(16)
	op.mu.Lock()
	op.codes[code] = codeGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	op.mu.Unlock()
	return code, q.Get("state")
}

func setupProvider(t *testing.T, f *fixture, op *testOP, in OIDCInput) *OIDCProvider {
	t.Helper()
	in.Slug, in.DisplayName, in.Issuer, in.ClientID = "porch", "Porch", op.srv.URL, "hp-client"
	secret := "client-secret"
	in.ClientSecret = &secret
	in.AllowPrivateNetwork, in.Enabled = true, true
	in.GroupsClaim = "porch.groups"
	var p *OIDCProvider
	if err := f.db.Tx(f.ctx, func(tx *sql.Tx) error {
		var err error
		p, err = f.svc.SaveOIDCProvider(f.ctx, tx, "", in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.svc.clock = realClockForOIDC{f.clock}
	return p
}

// realClockForOIDC keeps the fake clock for session math while ID token
// expiry uses wall time (the test OP signs with time.Now()).
type realClockForOIDC struct{ fake interface{ Now() time.Time } }

func (r realClockForOIDC) Now() time.Time                         { return time.Now().UTC() }
func (r realClockForOIDC) After(d time.Duration) <-chan time.Time { return time.After(d) }

func TestOIDCJustInTimeLoginWithGroups(t *testing.T) {
	f := newFixture(t)
	op := newTestOP(t)
	setupProvider(t, f, op, OIDCInput{Provisioning: "jit", RoleMappings: []RoleMapping{{Group: "hp-operators", Role: RoleOperator}, {Group: "hp-admins", Role: RoleAdmin}}})

	start, err := f.svc.StartOIDC(f.ctx, "porch", OIDCPurposeLogin, "", "/activity")
	if err != nil {
		t.Fatal(err)
	}
	code, state := op.authorize(t, start.AuthURL)
	res, err := f.svc.CompleteOIDC(f.ctx, state, code, op.srv.URL, start.BrowserCookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.User.Role != RoleOperator || res.User.RoleSource == "local" || res.ReturnTo != "/activity" || res.Session == "" {
		t.Fatalf("unexpected result %+v", res.User)
	}
	// State is single use.
	if _, err := f.svc.CompleteOIDC(f.ctx, state, code, "", start.BrowserCookie, nil); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("state replay: %v", err)
	}
	// Group change on next login updates the role; losing all groups denies.
	op.groups = []string{"hp-admins"}
	start, _ = f.svc.StartOIDC(f.ctx, "porch", OIDCPurposeLogin, "", "/")
	code, state = op.authorize(t, start.AuthURL)
	res, err = f.svc.CompleteOIDC(f.ctx, state, code, "", start.BrowserCookie, nil)
	if err != nil || res.User.Role != RoleAdmin {
		t.Fatalf("role upgrade: %+v %v", res, err)
	}
	op.groups = nil
	start, _ = f.svc.StartOIDC(f.ctx, "porch", OIDCPurposeLogin, "", "/")
	code, state = op.authorize(t, start.AuthURL)
	if _, err := f.svc.CompleteOIDC(f.ctx, state, code, "", start.BrowserCookie, nil); !errors.Is(err, ErrOIDCNoRole) {
		t.Fatalf("no groups should deny: %v", err)
	}
}

func TestOIDCRejections(t *testing.T) {
	tests := []struct {
		name    string
		tamper  func(op *testOP)
		browser func(real string) string
		iss     func(real string) string
		wantSub string
	}{
		{name: "wrong browser", browser: func(string) string { return "other-browser" }, wantSub: "not started from this browser"},
		{name: "nonce mismatch", tamper: func(op *testOP) { op.wrongNonce = true }, wantSub: "nonce"},
		{name: "audience mismatch", tamper: func(op *testOP) { op.wrongAud = true }, wantSub: "ID token rejected"},
		{name: "issuer mismatch in token", tamper: func(op *testOP) { op.issOverride = "https://evil.example" }, wantSub: "ID token rejected"},
		{name: "RFC 9207 iss mismatch", iss: func(string) string { return "https://evil.example" }, wantSub: "issuer mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			op := newTestOP(t)
			setupProvider(t, f, op, OIDCInput{Provisioning: "jit", AllowedDomains: []string{"example.test"}, DefaultRole: RoleViewer})
			if tt.tamper != nil {
				tt.tamper(op)
			}
			start, err := f.svc.StartOIDC(f.ctx, "porch", OIDCPurposeLogin, "", "/")
			if err != nil {
				t.Fatal(err)
			}
			code, state := op.authorize(t, start.AuthURL)
			browser, iss := start.BrowserCookie, ""
			if tt.browser != nil {
				browser = tt.browser(browser)
			}
			if tt.iss != nil {
				iss = tt.iss(op.srv.URL)
			}
			_, err = f.svc.CompleteOIDC(f.ctx, state, code, iss, browser, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err=%v want %q", err, tt.wantSub)
			}
		})
	}
}

func TestOIDCPreprovisionedLinksByVerifiedEmailOnly(t *testing.T) {
	f := newFixture(t)
	op := newTestOP(t)
	setupProvider(t, f, op, OIDCInput{Provisioning: "preprovisioned"})
	start, _ := f.svc.StartOIDC(f.ctx, "porch", OIDCPurposeLogin, "", "/")
	code, state := op.authorize(t, start.AuthURL)
	if _, err := f.svc.CompleteOIDC(f.ctx, state, code, "", start.BrowserCookie, nil); !errors.Is(err, ErrOIDCNoAccount) {
		t.Fatalf("unknown user must be refused: %v", err)
	}
	u := f.createUser(t, "dev@example.test", RoleViewer, "")
	start, _ = f.svc.StartOIDC(f.ctx, "porch", OIDCPurposeLogin, "", "/")
	code, state = op.authorize(t, start.AuthURL)
	res, err := f.svc.CompleteOIDC(f.ctx, state, code, "", start.BrowserCookie, nil)
	if err != nil || res.User.ID != u.ID || len(res.User.Identities) != 1 {
		t.Fatalf("link: %+v %v", res, err)
	}
	// Local role is kept because no mappings are configured.
	if res.User.Role != RoleViewer {
		t.Fatalf("role changed unexpectedly: %s", res.User.Role)
	}
}

func TestClaimStrings(t *testing.T) {
	claims := map[string]any{"porch": map[string]any{"groups": []any{"a", "b", 3}}, "g": "solo"}
	if got := ClaimStrings(claims, "porch.groups"); len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
	if got := ClaimStrings(claims, "g"); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if got := ClaimStrings(claims, "missing.path"); got != nil {
		t.Fatalf("got %v", got)
	}
	if SafeReturnTo("//evil.example") != "/" || SafeReturnTo("https://evil") != "/" || SafeReturnTo("/ok?x=1") != "/ok?x=1" {
		t.Fatal("SafeReturnTo")
	}
}
