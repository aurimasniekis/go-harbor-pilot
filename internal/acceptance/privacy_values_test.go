package acceptance_test

import (
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
