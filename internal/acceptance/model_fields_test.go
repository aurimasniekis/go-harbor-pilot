package acceptance_test

import (
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// modelFieldsGateway wires a client -> gateway -> fake OpenRouter-shaped
// upstream, so a test can assert what was actually sent on the wire.
func modelFieldsGateway(t *testing.T, policy string) (*apptest.Env, *apptest.Upstream, string) {
	t.Helper()
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "openai")
	up.Default = apptest.Reply{Text: "served", InputTok: 5, OutputTok: 2}

	prov := e.ProviderWithModels("openrouter", "openai_compatible", up.Srv.URL, map[string]any{
		"id": "relace/fp4", "display_name": "Relace FP4", "context_window": 200000, "max_output_tokens": 8192,
		"capabilities":         []string{"streaming", "tools"},
		"request_field_policy": policy,
		"provider_routing": map[string]any{
			"order": []string{"relace", "deepinfra"}, "allow_fallbacks": false, "sort": "throughput",
			"max_price": map[string]any{"prompt": 1.5},
		},
		"request_fields": map[string]any{"transforms": []string{"middle-out"}},
	})
	acc := e.Account("openrouter-key", prov, apptest.AccountOpts{Secret: "sk-or-synthetic-secret"})
	alias := e.Alias("coding/default", e.Pool("openrouter", "ordered", apptest.Member{Account: acc, Model: "relace/fp4"}))
	plan := e.Plan("default", apptest.PlanOpts{Mappings: map[string]string{"coding/*": alias}})
	tok := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))
	return e, up, tok
}

func modelFieldsChat(extra map[string]any) map[string]any {
	body := map[string]any{"model": "coding/default", "max_tokens": 32,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// TestScenarioO_ModelRequestFieldsAreSentUpstream proves the operator's
// per-model pin reaches the upstream although the client never sent it.
func TestScenarioO_ModelRequestFieldsAreSentUpstream(t *testing.T) {
	e, up, tok := modelFieldsGateway(t, "configured")
	resp := e.Gateway("POST", "/v1/chat/completions", tok, modelFieldsChat(nil))
	if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := up.Last().Body

	provider, ok := got["provider"].(map[string]any)
	if !ok {
		t.Fatalf("the configured provider pin did not reach the upstream: %v", got)
	}
	order, _ := provider["order"].([]any)
	if len(order) != 2 || order[0] != "relace" || order[1] != "deepinfra" {
		t.Errorf("provider.order = %v, want the configured order, in order", provider["order"])
	}
	if provider["allow_fallbacks"] != false {
		t.Errorf("allow_fallbacks = %v, want false (a set false must be sent)", provider["allow_fallbacks"])
	}
	if provider["sort"] != "throughput" {
		t.Errorf("provider.sort = %v", provider["sort"])
	}
	if price, _ := provider["max_price"].(map[string]any); price["prompt"] != 1.5 {
		t.Errorf("provider.max_price = %v", provider["max_price"])
	}
	tr, _ := got["transforms"].([]any)
	if len(tr) != 1 || tr[0] != "middle-out" {
		t.Errorf("transforms = %v, want the configured free-form field", got["transforms"])
	}
	// The gateway still owns the structural fields.
	if got["model"] != "relace/fp4" {
		t.Errorf("model = %v, want the routed model", got["model"])
	}
	if got["messages"] == nil {
		t.Error("messages must be the client's own")
	}
}

// TestScenarioO_ClientFieldConflict covers all three conflict policies end to
// end, including that a rejected request never reaches the upstream.
func TestScenarioO_ClientFieldConflict(t *testing.T) {
	tests := []struct {
		policy string
		status int
		want   string // provider.order[0] the upstream received
		errHas string // fragment expected in the error body instead
	}{
		{"configured", 200, "relace", ""},
		{"client", 200, "client-choice", ""},
		{"reject", 400, "", "provider"},
	}
	for _, tc := range tests {
		t.Run(tc.policy, func(t *testing.T) {
			e, up, tok := modelFieldsGateway(t, tc.policy)
			clientProvider := map[string]any{"provider": map[string]any{"order": []string{"client-choice"}}}
			resp := e.Gateway("POST", "/v1/chat/completions", tok, modelFieldsChat(clientProvider))
			body := apptest.ReadAll(resp)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			if tc.errHas != "" {
				if !strings.Contains(body, tc.errHas) {
					t.Fatalf("the error should name the field: %s", body)
				}
				if n := up.Count(); n != 0 {
					t.Errorf("a rejected request must never reach the upstream, %d calls", n)
				}
				e.Flush()
				if n := e.QueryInt(`SELECT count(*) FROM requests WHERE status = 'error' AND error_kind = 'invalid_request'`); n != 1 {
					t.Errorf("the refusal should be recorded as a request error, got %d rows", n)
				}
				return
			}
			order, _ := up.Last().Body["provider"].(map[string]any)["order"].([]any)
			if len(order) == 0 || order[0] != tc.want {
				t.Errorf("provider.order = %v, want %q first", order, tc.want)
			}
		})
	}
}

// plainModelGateway wires a provider whose model configures no request fields.
func plainModelGateway(t *testing.T) (*apptest.Env, *apptest.Upstream, string) {
	t.Helper()
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "openai")
	up.Default = apptest.Reply{Text: "ok", InputTok: 1, OutputTok: 1}
	prov := e.Provider("plain", "openai_compatible", up.Srv.URL, apptest.Model{ID: "plain-model", Capabilities: []string{"streaming"}})
	acc := e.Account("plain-key", prov, apptest.AccountOpts{Secret: "sk-plain"})
	alias := e.Alias("plain/default", e.Pool("plain", "ordered", apptest.Member{Account: acc, Model: "plain-model"}))
	plan := e.Plan("plain", apptest.PlanOpts{Mappings: map[string]string{"plain/*": alias}})
	return e, up, e.Token(e.Binding("plain", plan, apptest.BindingOpts{}))
}

// TestScenarioO_ModelWithoutFieldsIsUnchanged guards against any body change
// for models (nearly all of them) that configure nothing.
func TestScenarioO_ModelWithoutFieldsIsUnchanged(t *testing.T) {
	e, up, tok := plainModelGateway(t)
	resp := e.Gateway("POST", "/v1/chat/completions", tok, modelFieldsChat(map[string]any{"model": "plain/default"}))
	if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := up.Last().Body
	for _, k := range []string{"provider", "transforms"} {
		if _, ok := got[k]; ok {
			t.Errorf("%q must not appear for a model that configures none: %v", k, got)
		}
	}
}

// TestScenarioO_FreeFormFieldFromClientSurvivesWithoutConfig is the other
// half of the guarantee: with no configured field, a client's own extension
// field still reaches the upstream untouched.
func TestScenarioO_FreeFormFieldFromClientSurvivesWithoutConfig(t *testing.T) {
	e, up, tok := plainModelGateway(t)
	resp := e.Gateway("POST", "/v1/chat/completions", tok, modelFieldsChat(map[string]any{
		"model": "plain/default", "provider": map[string]any{"order": []string{"client-choice"}}}))
	if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	order, _ := up.Last().Body["provider"].(map[string]any)["order"].([]any)
	if len(order) != 1 || order[0] != "client-choice" {
		t.Errorf("provider.order = %v, want the client's own value untouched", order)
	}
}
