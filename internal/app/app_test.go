package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
)

// TestEnv is a running app behind httptest.
type TestEnv struct {
	App *App
	Srv *httptest.Server
}

// NewTestEnv starts an app with a fresh data directory.
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.DataDir = t.TempDir()
	srv := httptest.NewUnstartedServer(nil)
	cfg.Server.PublicURL = "http://" + srv.Listener.Addr().String()
	cfg.Server.Listen = srv.Listener.Addr().String()
	key := crypto.GenerateKEKString()
	a, err := New(context.Background(), Options{Config: cfg, Version: "test", Log: slog.New(slog.DiscardHandler),
		Env: func(k string) string {
			if k == "HP_MASTER_KEY" {
				return key
			}
			return ""
		}})
	if err != nil {
		t.Fatal(err)
	}
	a.readyMu.Lock()
	a.ready = true
	a.readyMu.Unlock()
	srv.Config.Handler = a.Handler()
	srv.Start()
	t.Cleanup(func() { srv.Close(); a.Close() })
	return &TestEnv{App: a, Srv: srv}
}

// Client is a browser-like client with a cookie jar and CSRF handling.
type Client struct {
	t    *testing.T
	env  *TestEnv
	hc   *http.Client
	csrf string
	bear string
}

// Browser returns a cookie-carrying client.
func (e *TestEnv) Browser(t *testing.T) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{t: t, env: e, hc: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Bearer returns a token client.
func (e *TestEnv) Bearer(t *testing.T, tok string) *Client {
	return &Client{t: t, env: e, hc: &http.Client{}, bear: tok}
}

// Do performs a JSON request and decodes the response into out (if non-nil).
func (c *Client) Do(method, path string, body any, out any, hdr ...string) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, c.env.Srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", c.csrf)
		req.Header.Set("Origin", c.env.App.Cfg.PublicOrigin())
	}
	if c.bear != "" {
		req.Header.Set("Authorization", "Bearer "+c.bear)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	if strings.Contains(string(data), "$argon2id$") {
		c.t.Fatalf("%s %s leaked a password hash", method, path)
	}
	return resp.StatusCode
}

// SetupAdmin bootstraps the first admin and returns a signed-in client.
func (e *TestEnv) SetupAdmin(t *testing.T) *Client {
	t.Helper()
	ctx := context.Background()
	tok, _, err := e.App.Auth.IssueOneTime(ctx, auth.PurposeSetup, "", "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	c := e.Browser(t)
	var s struct {
		CSRF string `json:"csrf_token"`
	}
	if code := c.Do("POST", "/api/v1/auth/setup", map[string]string{"token": tok, "email": "admin@example.test",
		"display_name": "Admin", "password": "correct-horse-battery"}, &s); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	c.csrf = s.CSRF
	return c
}

func TestAuthFlowsOverHTTP(t *testing.T) {
	env := NewTestEnv(t)
	anon := env.Browser(t)
	var sess map[string]any
	if anon.Do("GET", "/api/v1/session", nil, &sess) != 200 || sess["setup_required"] != true {
		t.Fatalf("session: %v", sess)
	}
	if code := anon.Do("GET", "/api/v1/users", nil, nil); code != 401 {
		t.Fatalf("anon users: %d", code)
	}
	admin := env.SetupAdmin(t)

	// Create an operator via invite, accept the invite in a new browser.
	var created struct {
		User      struct{ ID string } `json:"user"`
		InviteURL string              `json:"invite_url"`
	}
	if code := admin.Do("POST", "/api/v1/users", map[string]any{"email": "op@example.test", "role": "operator", "invite": true}, &created); code != 201 {
		t.Fatalf("create user: %d", code)
	}
	invite := created.InviteURL[strings.Index(created.InviteURL, "token=")+6:]
	op := env.Browser(t)
	var opSess struct {
		Role string `json:"role"`
		CSRF string `json:"csrf_token"`
	}
	if code := op.Do("POST", "/api/v1/auth/accept", map[string]string{"token": invite, "purpose": "invite", "password": "operator-password"}, &opSess); code != 200 || opSess.Role != "operator" {
		t.Fatalf("accept invite: %d %+v", code, opSess)
	}
	op.csrf = opSess.CSRF
	if code := op.Do("GET", "/api/v1/users", nil, nil); code != 403 {
		t.Fatalf("operator listing users: %d", code)
	}

	// Service principal + API token; token works without CSRF, cookie-less.
	var sp struct{ ID string }
	if code := admin.Do("POST", "/api/v1/service-principals", map[string]any{"name": "terraform", "permissions": []string{"users.manage"}}, &sp,
		"X-HP-Managed-By", "terraform"); code != 201 {
		t.Fatalf("create sp: %d", code)
	}
	var issued struct{ Plaintext string }
	if code := admin.Do("POST", "/api/v1/service-principals/"+sp.ID+"/tokens", map[string]any{"name": "ci"}, &issued); code != 201 || !strings.HasPrefix(issued.Plaintext, "hpa_") {
		t.Fatalf("issue token: %d", code)
	}
	bot := env.Bearer(t, issued.Plaintext)
	if code := bot.Do("GET", "/api/v1/users", nil, nil); code != 200 {
		t.Fatalf("token list users: %d", code)
	}
	if code := bot.Do("GET", "/api/v1/auth/settings", nil, nil); code != 403 {
		t.Fatalf("token without auth.settings: %d", code)
	}
	if code := env.Bearer(t, "hpa_aaaaaaaa_"+strings.Repeat("A", 43)).Do("GET", "/api/v1/users", nil, nil); code != 401 {
		t.Fatalf("bogus token: %d", code)
	}

	// Optimistic concurrency.
	var u struct{ Revision int64 }
	admin.Do("GET", "/api/v1/users/"+created.User.ID, nil, &u)
	if code := admin.Do("PATCH", "/api/v1/users/"+created.User.ID, map[string]any{"display_name": "Op"}, nil, "If-Match", `"r999"`); code != 412 {
		t.Fatalf("stale If-Match: %d", code)
	}
	if code := admin.Do("PATCH", "/api/v1/users/"+created.User.ID, map[string]any{"display_name": "Op"}, nil, "If-Match", fmt.Sprintf(`"r%d"`, u.Revision)); code != 200 {
		t.Fatalf("fresh If-Match: %d", code)
	}

	// Audit trail recorded the actions.
	var audit struct {
		Items []struct{ Action string } `json:"items"`
	}
	admin.Do("GET", "/api/v1/audit?limit=100", nil, &audit)
	seen := map[string]bool{}
	for _, e := range audit.Items {
		seen[e.Action] = true
	}
	for _, want := range []string{"auth.bootstrap", "user.create", "auth.accept_invite", "service_principal.create", "api_token.issue", "user.update"} {
		if !seen[want] {
			t.Errorf("audit missing %s (have %v)", want, seen)
		}
	}

	// Logout revokes the session.
	if code := admin.Do("DELETE", "/api/v1/session", nil, nil); code != 200 {
		t.Fatalf("logout: %d", code)
	}
	if code := admin.Do("GET", "/api/v1/users", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

func TestCrossOriginMutationRejected(t *testing.T) {
	env := NewTestEnv(t)
	admin := env.SetupAdmin(t)
	code := admin.Do("POST", "/api/v1/service-principals", map[string]any{"name": "x", "permissions": []string{}}, nil, "Origin", "https://evil.example")
	if code != 403 {
		t.Fatalf("cross-origin mutation: %d", code)
	}
}

func TestSecurityHeadersAndSPA(t *testing.T) {
	env := NewTestEnv(t)
	resp, err := http.Get(env.Srv.URL + "/some/client/route")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", resp.Header)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("spa fallback content type %q", resp.Header.Get("Content-Type"))
	}
}
