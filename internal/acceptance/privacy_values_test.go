package acceptance_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Configured secret values that cannot be resolved fail closed for a
// required policy; an optional policy still masks what it can. Values are
// re-read once the reference resolves.
func TestUnresolvableSecretValuesFailClosed(t *testing.T) {
	const (
		ref    = "HP_SECRET_TEST_MASK_VALUES"
		custom = "corp-internal-value-77"
		key    = "sk-ant-api03-SYNTHETICsyntheticSYNTHETIC0000"
	)
	t.Setenv(ref, "") // unset: the reference cannot be resolved
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: e.Account("a", prov, apptest.AccountOpts{}), Model: "m"}))
	send := func(required bool) (int, string) {
		t.Helper()
		var pp struct {
			ID string `json:"id"`
		}
		name := map[bool]string{true: "required", false: "optional"}[required]
		e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": name, "spec": map[string]any{"required": required,
			"detectors": []string{"configured_secrets", "anthropic_key"}, "secret_values_ref": "env:" + ref}}, &pp)
		tok := e.Token(e.Binding(name, e.Plan(name, apptest.PlanOpts{DefaultAlias: alias, PrivacyPolicy: pp.ID}), apptest.BindingOpts{}))
		r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "x", "max_tokens": 8, "messages": []any{map[string]any{"role": "user",
			"content": "use " + custom + " and " + key}}})
		return r.StatusCode, apptest.ReadAll(r)
	}
	if code, body := send(true); code == 200 {
		t.Fatalf("required policy with unresolvable values dispatched: %s", body)
	}
	if up.Count() != 0 {
		t.Fatal("the upstream received a request")
	}
	if code, body := send(false); code != 200 {
		t.Fatalf("optional policy: %d %s", code, body)
	}
	sent := up.Last().Raw
	if strings.Contains(sent, key) {
		t.Fatal("built-in detectors stopped masking when configured values were unavailable")
	}
	if !strings.Contains(sent, custom) {
		t.Fatal("expected the unresolvable custom value to pass (optional policy)")
	}
}

// A placeholder issued to one credential is never restored for another,
// even when both send the same (client-asserted) session id.
func TestPlaceholdersAreScopedToTheCredential(t *testing.T) {
	const key = "sk-ant-api03-SYNTHETICsyntheticSYNTHETIC0000"
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	up.Default = apptest.Reply{Echo: true, InputTok: 5, OutputTok: 5}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: e.Account("a", prov, apptest.AccountOpts{}), Model: "m"}))
	var pp struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": "restore", "spec": map[string]any{"required": true,
		"restore_in_responses": true}}, &pp)
	binding := e.Binding("shared", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias, PrivacyPolicy: pp.ID}), apptest.BindingOpts{TrustSessions: true})
	tokA, tokB := e.Token(binding), e.Token(binding)
	send := func(tok, content string) string {
		r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "x", "max_tokens": 64, "messages": []any{map[string]any{"role": "user",
			"content": content}}}, "X-Claude-Code-Session-Id", "shared-session")
		body := apptest.ReadAll(r)
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, body)
		}
		return body
	}
	if got := send(tokA, key); !strings.Contains(got, key) {
		t.Fatalf("the owner should get its own value restored: %s", got)
	}
	ph := regexp.MustCompile(`HPSEC_[A-Z2-7]+`).FindString(up.Last().Raw)
	if ph == "" {
		t.Fatal("no placeholder reached the upstream")
	}
	if got := send(tokB, ph); strings.Contains(got, key) {
		t.Fatal("another credential on the same client and session recovered the secret")
	}
}

// The sandbox would let a reader test guesses against stored secret values,
// so testing such a saved policy requires secrets.write.
func TestSandboxGuardsConfiguredValues(t *testing.T) {
	e := apptest.New(t)
	var pp struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": "vals", "spec": map[string]any{"required": true,
		"detectors": []string{"configured_secrets"}}, "secrets": map[string]string{"secret_values": "Winter2026!secret"}}, &pp)
	var sp struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/service-principals", map[string]any{"name": "reader", "permissions": []string{"config.read"}}, &sp)
	var tok struct {
		Plaintext string `json:"plaintext"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/service-principals/"+sp.ID+"/tokens", map[string]any{"name": "t"}, &tok)
	guess := map[string]any{"policy_id": pp.ID, "text": "Summer2026!secret Winter2026!secret"}
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/privacy/sandbox", strings.NewReader(mustJSON(t, guess)))
	req.Header.Set("Authorization", "Bearer "+tok.Plaintext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := apptest.ReadAll(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reader tested guesses against stored values: %d %s", resp.StatusCode, body)
	}
	// Unsaved definitions and admins are unaffected.
	var res struct {
		Findings []any `json:"findings"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/privacy/sandbox", guess, &res)
	if len(res.Findings) != 1 {
		t.Fatalf("admin sandbox findings %d", len(res.Findings))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A configured value too short to mask is rejected when stored, naming the
// line but never the value, instead of being skipped silently.
func TestShortSecretValuesAreRejected(t *testing.T) {
	e := apptest.New(t)
	tests := []struct {
		name, values, want string
		status             int
	}{
		{"one short line", "q7Zx1\nsupersecretvalue123", "line 1 is shorter than 6 characters", 422},
		{"several short lines", "q7Zx1\n\nsupersecretvalue123\nk9Wv2", "lines 1, 4 are shorter", 422},
		{"blank lines are ignored", "supersecretvalue123\n\n", "", 201},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out json.RawMessage
			status := e.Admin.Do("POST", "/api/v1/privacy-policies", map[string]any{"name": fmt.Sprintf("short-%d", i),
				"spec": map[string]any{"required": true, "detectors": []string{"configured_secrets"}}, "secrets": map[string]string{"secret_values": tt.values}}, &out)
			if status != tt.status {
				t.Fatalf("status %d: %s", status, out)
			}
			if !strings.Contains(string(out), tt.want) || strings.Contains(string(out), "q7Zx1") || strings.Contains(string(out), "k9Wv2") {
				t.Fatalf("body %s", out)
			}
		})
	}
}
