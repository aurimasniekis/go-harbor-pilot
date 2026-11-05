package apptest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/polling"
)

// Env is a running Harbor Pilot instance for tests.
type Env struct {
	T     testing.TB
	App   *app.App
	Srv   *httptest.Server
	Admin *Client
	ctx   context.Context
	stop  context.CancelFunc
}

// Setup is what options may change before the app starts.
type Setup struct {
	Config *config.Config
	App    *app.Options
}

// Option customizes the environment before the app starts.
type Option func(*Setup)

// WithBridgeEnv appends environment variables to CLI bridge processes.
func WithBridgeEnv(env ...string) Option {
	return func(s *Setup) { s.App.BridgeEnv = append(s.App.BridgeEnv, env...) }
}

// WithConfig changes the bootstrap configuration.
func WithConfig(fn func(*config.Config)) Option { return func(s *Setup) { fn(s.Config) } }

// New starts an app with a fresh data directory and a signed-in admin.
func New(t testing.TB, opts ...Option) *Env {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.DataDir = t.TempDir()
	srv := httptest.NewUnstartedServer(nil)
	cfg.Server.PublicURL = "http://" + srv.Listener.Addr().String()
	cfg.Server.Listen = srv.Listener.Addr().String()
	key := crypto.GenerateKEKString()
	ao := app.Options{Version: "test", Log: slog.New(slog.DiscardHandler),
		QuotaScheduler: polling.Options{Tick: 20 * time.Millisecond, ManualMinInterval: time.Millisecond, BackoffBase: time.Second},
		Env: func(k string) string {
			if k == "HP_MASTER_KEY" {
				return key
			}
			return ""
		}}
	for _, o := range opts {
		o(&Setup{Config: &cfg, App: &ao})
	}
	ao.Config = cfg
	a, err := app.New(context.Background(), ao)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic upstreams run on loopback.
	a.Gateway.HTTP.AllowedPrivate = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	ctx, stop := context.WithCancel(context.Background())
	e := &Env{T: t, App: a, Srv: srv, ctx: ctx, stop: stop}
	a.MarkReady()
	srv.Config.Handler = a.Handler()
	srv.Start()
	a.StartBackground(ctx)
	t.Cleanup(func() {
		stop()
		srv.Close()
		a.WaitBackground()
		a.Close()
	})
	e.Admin = e.newAdmin()
	return e
}

// Client calls the control-plane API with a session (CSRF-aware).
type Client struct {
	t    testing.TB
	e    *Env
	hc   *http.Client
	csrf string
}

func (e *Env) newAdmin() *Client {
	tok, _, err := e.App.Auth.IssueOneTime(context.Background(), auth.PurposeSetup, "", "", "", "test")
	if err != nil {
		e.T.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	c := &Client{t: e.T, e: e, hc: &http.Client{Jar: jar}}
	var s struct {
		CSRF string `json:"csrf_token"`
	}
	if code := c.Do("POST", "/api/v1/auth/setup", map[string]string{"token": tok, "email": "admin@example.test", "display_name": "Admin", "password": "correct-horse-battery"}, &s); code != 200 {
		e.T.Fatalf("setup admin: %d", code)
	}
	c.csrf = s.CSRF
	return c
}

// Do performs a JSON control-plane request.
func (c *Client) Do(method, path string, body, out any, hdr ...string) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.Srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", c.csrf)
		req.Header.Set("Origin", c.e.App.Cfg.PublicOrigin())
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
			c.t.Fatalf("%s %s: decode %s: %v", method, path, data, err)
		}
	}
	if resp.StatusCode >= 400 && out == nil {
		c.t.Logf("%s %s -> %d %s", method, path, resp.StatusCode, data)
	}
	return resp.StatusCode
}

// MustDo fails the test unless the status matches.
func (c *Client) MustDo(want int, method, path string, body, out any, hdr ...string) {
	c.t.Helper()
	var raw json.RawMessage
	code := c.Do(method, path, body, &raw, hdr...)
	if code != want {
		c.t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatal(err)
		}
	}
}

type obj struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// Model describes a provider model for setup helpers.
type Model struct {
	ID           string
	Capabilities []string
	MaxOutput    int
	Price        *[2]string // input, output per Mtok (USD)
}

// Provider creates a provider pointing at a fake upstream.
func (e *Env) Provider(name, typ, baseURL string, models ...Model) string {
	ms := []map[string]any{}
	for _, m := range models {
		caps := m.Capabilities
		if caps == nil {
			caps = []string{}
		}
		mm := map[string]any{"id": m.ID, "display_name": m.ID, "context_window": 200000, "max_output_tokens": m.MaxOutput, "capabilities": caps}
		if m.Price != nil {
			mm["pricing"] = map[string]any{"currency": "USD", "input_per_mtok": m.Price[0], "output_per_mtok": m.Price[1], "cache_read_per_mtok": "",
				"cache_write_per_mtok": "", "price_version": "test-2026"}
		}
		ms = append(ms, mm)
	}
	var o obj
	e.Admin.MustDo(201, "POST", "/api/v1/providers", map[string]any{"name": name, "spec": map[string]any{"type": typ, "display_name": name,
		"base_url": baseURL, "allow_private_network": true, "classification": "", "headers": map[string]string{}, "request_timeout_seconds": 30,
		"stream_idle_timeout_seconds": 10, "models": ms}}, &o)
	return o.ID
}

// AccountOpts configure an account.
type AccountOpts struct {
	Billing        string
	MaxConcurrency int
	Secret         string
}

// Account creates a provider account.
func (e *Env) Account(name, providerID string, o AccountOpts) string {
	if o.Billing == "" {
		o.Billing = "metered_api"
	}
	if o.Secret == "" {
		o.Secret = "sk-test-" + name
	}
	var out obj
	e.Admin.MustDo(201, "POST", "/api/v1/provider-accounts", map[string]any{"name": name, "spec": map[string]any{"provider_id": providerID,
		"display_name": name, "credential_ref": "", "billing": o.Billing, "allowed_models": []string{}, "max_concurrency": o.MaxConcurrency,
		"requests_per_minute": 0, "enabled": true, "labels": map[string]string{}}, "secrets": map[string]string{"credential": o.Secret}}, &out)
	return out.ID
}

// Member is a pool member.
type Member struct {
	Account  string
	Model    string
	Priority int
}

// Pool creates a pool.
func (e *Env) Pool(name string, strategy string, members ...Member) string {
	ms := []map[string]any{}
	for _, m := range members {
		ms = append(ms, map[string]any{"account_id": m.Account, "model": m.Model, "priority": m.Priority, "weight": 1})
	}
	var out obj
	e.Admin.MustDo(201, "POST", "/api/v1/pools", map[string]any{"name": name, "spec": map[string]any{"display_name": name, "strategy": strategy,
		"affinity": "none", "members": ms}}, &out)
	return out.ID
}

// Alias creates a model alias targeting a pool.
func (e *Env) Alias(name, poolID string) string {
	var out obj
	e.Admin.MustDo(201, "POST", "/api/v1/model-aliases", map[string]any{"name": name, "spec": map[string]any{"description": "", "pool_id": poolID,
		"alias_id": "", "required_capabilities": []string{}}}, &out)
	return out.ID
}

// PlanOpts configure a published plan.
type PlanOpts struct {
	Mappings          map[string]string // match -> alias id
	DefaultAlias      string
	Steps             []string
	MaxAttempts       int
	AllowPaidFallback bool
}

// Plan creates and publishes a routing plan.
func (e *Env) Plan(name string, o PlanOpts) string {
	var plan obj
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans", map[string]any{"name": name, "spec": map[string]any{"display_name": name, "description": ""}}, &plan)
	maps := []map[string]any{}
	for m, a := range o.Mappings {
		maps = append(maps, map[string]any{"match": m, "alias_id": a, "note": ""})
	}
	if o.Steps == nil {
		o.Steps = []string{"alternate_account", "alternate_model", "alternate_provider", "stop"}
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	var draft obj
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans/"+plan.ID+"/versions", map[string]any{"note": "test", "document": map[string]any{
		"schema_version": 1, "model_mappings": maps, "default_alias_id": o.DefaultAlias,
		"restrictions": map[string]any{"allowed_provider_ids": []string{}, "denied_provider_ids": []string{}, "allowed_billing": []string{}, "allowed_classifications": []string{}, "required_capabilities": []string{}},
		"fallback":     map[string]any{"steps": o.Steps, "max_attempts": o.MaxAttempts, "allow_paid_fallback": o.AllowPaidFallback, "wait_max_seconds": 0}}}, &draft)
	e.Admin.MustDo(200, "POST", "/api/v1/routing-plans/"+plan.ID+"/versions/"+draft.ID+"/publish", nil, nil)
	return plan.ID
}

// BindingOpts configure a client binding.
type BindingOpts struct {
	ProjectID      string
	MaxConcurrency int
	TrustSessions  bool
	Harness        string
	ExternalRefs   map[string]string
}

// Binding creates a client binding.
func (e *Env) Binding(name, planID string, o BindingOpts) string {
	refs := o.ExternalRefs
	if refs == nil {
		refs = map[string]string{}
	}
	var out obj
	e.Admin.MustDo(201, "POST", "/api/v1/client-bindings", map[string]any{"name": name, "spec": map[string]any{"display_name": name, "owner_kind": "none",
		"owner_id": "", "plan_id": planID, "project_id": o.ProjectID, "allowed_project_ids": []string{}, "expected_harness": o.Harness,
		"allowed_models": []string{}, "limits": map[string]any{"requests_per_minute": 0, "max_concurrency": o.MaxConcurrency, "max_request_bytes": 0},
		"trust": map[string]any{"session_hints": o.TrustSessions, "project_hints": false, "intent_hints": false}, "external_refs": refs, "enabled": true}}, &out)
	return out.ID
}

// Token issues a gateway credential for a binding.
func (e *Env) Token(bindingID string, scopes ...string) string {
	var out struct {
		Token string `json:"token"`
	}
	body := map[string]any{"name": "test"}
	if len(scopes) > 0 {
		body["scopes"] = scopes
	}
	e.Admin.MustDo(201, "POST", "/api/v1/client-bindings/"+bindingID+"/credentials", body, &out)
	return out.Token
}

// Gateway sends a data-plane request with a gateway token.
func (e *Env) Gateway(method, path, token string, body any, hdr ...string) *http.Response {
	e.T.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.Srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	return resp
}

// ReadAll reads and closes a response body.
func ReadAll(r *http.Response) string {
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// Flush writes queued telemetry synchronously.
func (e *Env) Flush() {
	if err := e.App.Tel.Flush(context.Background()); err != nil {
		e.T.Fatal(err)
	}
}

// QueryInt runs a scalar SQL query against harbor.db.
func (e *Env) QueryInt(q string, args ...any) int64 {
	e.T.Helper()
	var n int64
	if err := e.App.DB.R.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.T.Fatalf("%s: %v", q, err)
	}
	return n
}

// QueryString runs a scalar SQL query.
func (e *Env) QueryString(q string, args ...any) string {
	e.T.Helper()
	var s string
	if err := e.App.DB.R.QueryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		e.T.Fatalf("%s: %v", q, err)
	}
	return s
}

// Sprintf is fmt.Sprintf (convenience for tests).
var Sprintf = fmt.Sprintf

// Eventually polls cond until it holds or the timeout expires.
func (e *Env) Eventually(cond func() bool, what string) {
	e.T.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.T.Fatalf("timed out waiting for %s", what)
}
