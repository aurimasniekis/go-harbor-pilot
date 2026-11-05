package acceptance_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestSecretsNeverLeak configures a canary secret everywhere a secret can
// be set, drives success and failure paths (including an upstream that
// echoes the rejected key), and checks that no canary appears in logs,
// API responses, metrics, events, the audit log or the raw database files.
func TestSecretsNeverLeak(t *testing.T) {
	logs := &syncBuffer{}
	e := apptest.New(t, apptest.WithLog(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	canaries := map[string]string{
		"provider credential":     "sk-CANARY-provider-91f2",
		"rejected credential":     "sk-CANARY-rejected-5c0d",
		"webhook signing secret":  "whsec-CANARY-signing-77aa",
		"channel secret":          "https://hooks.example.test/CANARY-channel-3b1e",
		"quota monitor key":       "sk-CANARY-monitor-a40c",
		"privacy secret value":    "CANARY-privacy-value-e81f",
		"oidc client secret":      "CANARY-oidc-secret-12bd",
		"user password":           "CANARY-password-correct-horse",
		"invalid gateway token":   "hpk_CANARYbad_0000000000000000000000000000000000000000000",
		"prompt marker (content)": "CANARY-prompt-content-4d2e",
	}

	// An upstream that serves the good key and echoes a rejected one, as some
	// providers do in their 401 messages.
	up := apptest.NewUpstream(t, "anthropic")
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if k := r.Header.Get("x-api-key"); k == canaries["rejected credential"] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key: `+k+`"}}`)
			return
		}
		up.ServeHTTP(w, r)
	}))
	t.Cleanup(echo.Close)

	prov := e.Provider("anthropic", "anthropic", echo.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	bad := e.Account("bad", prov, apptest.AccountOpts{Secret: canaries["rejected credential"]})
	good := e.Account("good", prov, apptest.AccountOpts{Secret: canaries["provider credential"]})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: bad, Model: "m"}, apptest.Member{Account: good, Model: "m", Priority: 1}))
	var pp struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": "mask", "spec": map[string]any{"required": true},
		"secrets": map[string]string{"secret_values": canaries["privacy secret value"]}}, &pp)
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias, PrivacyPolicy: pp.ID})
	tok := e.Token(e.Binding("b", plan, apptest.BindingOpts{}))

	e.Admin.MustDo(201, "POST", "/api/v1/event-destinations", map[string]any{"name": "hook", "spec": map[string]any{"type": "webhook", "enabled": true,
		"webhook_url": "http://127.0.0.1:9/hook", "allow_private_network": true}, "secrets": map[string]string{"signing_secret": canaries["webhook signing secret"]}}, nil)
	e.Admin.MustDo(201, "POST", "/api/v1/notification-channels", map[string]any{"name": "slack", "spec": map[string]any{"type": "slack", "enabled": true},
		"secrets": map[string]string{"secret": canaries["channel secret"]}}, nil)
	e.Admin.MustDo(201, "POST", "/api/v1/quota-monitors", map[string]any{"name": "mon", "spec": map[string]any{"adapter": "deepseek", "enabled": false},
		"secrets": map[string]string{"api_key": canaries["quota monitor key"]}}, nil)
	e.Admin.MustDo(201, "POST", "/api/v1/auth/oidc-providers", map[string]any{"slug": "sso", "display_name": "SSO", "issuer": "https://idp.example.test",
		"client_id": "hp", "client_secret": canaries["oidc client secret"], "provisioning": "preprovisioned", "enabled": false}, nil)
	if code := e.Admin.Do("POST", "/api/v1/session/password", map[string]string{"current_password": "correct-horse-battery",
		"new_password": canaries["user password"]}, nil); code >= 300 {
		t.Fatalf("change password: %d", code)
	}

	// Traffic: a failover past the rejected key, a prompt carrying the masked
	// value and a marker, and a request with a forged token.
	r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "x", "max_tokens": 8, "messages": []any{map[string]any{"role": "user",
		"content": "deploy with " + canaries["privacy secret value"] + " and " + canaries["prompt marker (content)"]}}})
	if b := apptest.ReadAll(r); r.StatusCode != 200 {
		t.Fatalf("gateway %d %s", r.StatusCode, b)
	}
	// Every attempt fails: the client sees the provider's error, scrubbed.
	onlyBad := e.Alias("coding/bad", e.Pool("bad-only", "ordered", apptest.Member{Account: bad, Model: "m"}))
	badTok := e.Token(e.Binding("bad", e.Plan("bad", apptest.PlanOpts{DefaultAlias: onlyBad}), apptest.BindingOpts{}))
	failed := e.Gateway("POST", "/v1/messages", badTok, map[string]any{"model": "x", "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "x"}}})
	clientError := apptest.ReadAll(failed)
	if failed.StatusCode < 400 {
		t.Fatalf("all-failing request: %d %s", failed.StatusCode, clientError)
	}
	if r := e.Gateway("POST", "/v1/messages", canaries["invalid gateway token"], map[string]any{"model": "x", "max_tokens": 8,
		"messages": []any{map[string]any{"role": "user", "content": "x"}}}); r.StatusCode != 401 {
		t.Fatalf("forged token: %d", r.StatusCode)
	}
	if code := e.Admin.Do("POST", "/api/v1/session", map[string]string{"email": "admin@example.test", "password": canaries["user password"] + "-wrong"}, nil); code != 401 {
		t.Fatalf("wrong password sign-in: %d", code)
	}
	e.Flush()
	if up.Count() == 0 {
		t.Fatal("the good account never reached the upstream")
	}

	// Everything an operator or integration can read.
	var surfaces = map[string]string{"logs": logs.String(), "client error response": clientError}
	for _, path := range []string{"/provider-accounts", "/providers", "/privacy-policies", "/event-destinations", "/notification-channels",
		"/quota-monitors", "/auth/oidc-providers", "/users", "/audit?limit=500", "/events?limit=500", "/activity/requests?limit=500",
		"/notifications", "/config/export", "/attention", "/quota/board"} {
		var raw json.RawMessage
		e.Admin.MustDo(200, "GET", "/api/v1"+path, nil, &raw)
		surfaces["GET "+path] = string(raw)
	}
	var reqs struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests", nil, &reqs)
	for _, it := range reqs.Items {
		var raw json.RawMessage
		e.Admin.MustDo(200, "GET", "/api/v1/activity/requests/"+it.ID, nil, &raw)
		surfaces["request "+it.ID] = string(raw)
	}
	if resp, err := http.Get(e.Srv.URL + "/metrics"); err == nil {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		surfaces["metrics"] = string(b)
	}
	e.Stop()
	files, _ := filepath.Glob(filepath.Join(e.DataDir, "*.db*"))
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // test data directory
		if err != nil {
			t.Fatal(err)
		}
		surfaces["file "+filepath.Base(f)] = string(b)
	}
	if len(files) < 2 {
		t.Fatalf("expected database files, found %v", files)
	}

	for what, canary := range canaries {
		for where, text := range surfaces {
			if strings.Contains(text, canary) {
				i := strings.Index(text, canary)
				lo, hi := max(0, i-120), min(len(text), i+len(canary)+40)
				t.Errorf("%s leaked into %s: …%s…", what, where, strings.ToValidUTF8(text[lo:hi], "?"))
			}
		}
	}
}
