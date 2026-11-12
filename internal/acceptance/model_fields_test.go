package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// modelFieldsGateway wires a client -> gateway -> synthetic router upstream
// (openai_compatible), so a test can assert what was actually sent on the
// wire.
func modelFieldsGateway(t *testing.T, policy string) (*apptest.Env, *apptest.Upstream, string) {
	t.Helper()
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "openai")
	up.Default = apptest.Reply{Text: "served", InputTok: 5, OutputTok: 2}

	prov := e.ProviderWithModels("router", "openai_compatible", up.Srv.URL, map[string]any{
		"id": "vendor/model-a", "display_name": "Model A", "context_window": 200000, "max_output_tokens": 8192,
		"capabilities":         []string{"streaming", "tools"},
		"request_field_policy": policy,
		"provider_routing": map[string]any{
			"order": []string{"provider-a", "provider-b"}, "allow_fallbacks": false, "sort": "throughput",
			"max_price": map[string]any{"prompt": 1.5},
		},
		"request_fields": map[string]any{"transforms": []string{"middle-out"}, "temperature": 0.3},
	})
	acc := e.Account("router-key", prov, apptest.AccountOpts{Secret: "sk-router-synthetic-secret"})
	alias := e.Alias("team/default", e.Pool("router", "ordered", apptest.Member{Account: acc, Model: "vendor/model-a"}))
	plan := e.Plan("default", apptest.PlanOpts{Mappings: map[string]string{"team/*": alias}})
	tok := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))
	return e, up, tok
}

func modelFieldsChat(extra map[string]any) map[string]any {
	body := map[string]any{"model": "team/default", "max_tokens": 32,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// TestScenarioO_ModelRequestFieldsAreSentUpstream proves the operator's
// per-model pin and allowlisted fields reach the upstream although the client
// never sent them.
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
	if len(order) != 2 || order[0] != "provider-a" || order[1] != "provider-b" {
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
	if got["temperature"] != 0.3 {
		t.Errorf("temperature = %v, want the configured 0.3", got["temperature"])
	}
	// The gateway still owns the structural fields and the output cap.
	if got["model"] != "vendor/model-a" {
		t.Errorf("model = %v, want the routed model", got["model"])
	}
	if got["messages"] == nil {
		t.Error("messages must be the client's own")
	}
	if got["max_tokens"] != float64(32) {
		t.Errorf("max_tokens = %v, want the client's 32", got["max_tokens"])
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
		loss   string // translation loss recorded on the attempt
	}{
		{"configured", 200, "provider-a", "", "request_field:provider"},
		{"client", 200, "client-choice", "", "request_field:provider"},
		{"reject", 400, "", "provider", ""},
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
			e.Flush()
			if n := e.QueryInt(`SELECT count(*) FROM requests WHERE losses LIKE '%` + tc.loss + `%'`); n != 1 {
				t.Errorf("the conflict should be recorded as loss %s", tc.loss)
			}
		})
	}
}

// claudeCodeRequest is the shape of a Claude Code turn on the Messages API:
// cached system blocks, the CLI's metadata, tools and a stream. The model is
// a gateway alias, as Claude Code sends when pointed at the gateway.
func claudeCodeRequest(model string) map[string]any {
	return map[string]any{
		"model": model, "max_tokens": 1024, "stream": true,
		"system":   []any{map[string]any{"type": "text", "text": "You are a coding assistant.", "cache_control": map[string]any{"type": "ephemeral"}}},
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "list the files"}}}},
		"tools": []any{map[string]any{"name": "Bash", "description": "Run a command",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}}}},
		"metadata": map[string]any{"user_id": "user_synthetic_session"},
	}
}

var claudeCodeHeaders = []string{"User-Agent", "claude-cli/2.1.318 (external, cli)", "anthropic-version", "2023-06-01",
	"anthropic-beta", "interleaved-thinking-2025-05-14"}

// TestScenarioO_ClaudeCodePinsTheRouterProvider is the owner's use case: a
// client that cannot send router options (Claude Code on the Messages API)
// reaches an openai_compatible router with the model's pinned provider,
// while the same request for an unpinned model is byte-identical to what it
// was without the feature.
func TestScenarioO_ClaudeCodePinsTheRouterProvider(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "openai")
	up.Default = apptest.Reply{Text: "done", InputTok: 9, OutputTok: 3}
	model := func(pin bool) map[string]any {
		m := map[string]any{"id": "vendor/model-a", "display_name": "Model A", "capabilities": []string{"streaming", "tools"}}
		if pin {
			m["provider_routing"] = map[string]any{"only": []string{"provider-a"}, "allow_fallbacks": false}
		}
		return m
	}
	pinned := e.Account("pinned-key", e.ProviderWithModels("pinned", "openai_compatible", up.Srv.URL, model(true)), apptest.AccountOpts{Secret: "sk-pinned"})
	plain := e.Account("plain-key", e.ProviderWithModels("plain", "openai_compatible", up.Srv.URL, model(false)), apptest.AccountOpts{Secret: "sk-plain"})
	pinnedAlias := e.Alias("pinned/default", e.Pool("pinned", "ordered", apptest.Member{Account: pinned, Model: "vendor/model-a"}))
	plainAlias := e.Alias("plain/default", e.Pool("plain", "ordered", apptest.Member{Account: plain, Model: "vendor/model-a"}))
	plan := e.Plan("default", apptest.PlanOpts{Mappings: map[string]string{"pinned/*": pinnedAlias, "plain/*": plainAlias}})
	tok := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))

	send := func(alias string) string {
		t.Helper()
		resp := e.Gateway("POST", "/v1/messages", tok, claudeCodeRequest(alias), claudeCodeHeaders...)
		body := apptest.ReadAll(resp)
		if resp.StatusCode != 200 || !strings.Contains(body, "message_stop") {
			t.Fatalf("%s: status %d: %s", alias, resp.StatusCode, body)
		}
		got := up.Last()
		if !strings.HasSuffix(strings.SplitN(got.Path, "?", 2)[0], "/chat/completions") {
			t.Fatalf("%s reached %s, want the Chat endpoint", alias, got.Path)
		}
		return got.Raw
	}
	pinnedRaw, plainRaw := send("pinned/default"), send("plain/default")

	var body map[string]any
	if err := json.Unmarshal([]byte(pinnedRaw), &body); err != nil {
		t.Fatal(err)
	}
	provider, ok := body["provider"].(map[string]any)
	if !ok {
		t.Fatalf("the pinned provider did not reach the router: %s", pinnedRaw)
	}
	if only, _ := provider["only"].([]any); len(only) != 1 || only[0] != "provider-a" || provider["allow_fallbacks"] != false {
		t.Errorf("provider = %v", provider)
	}
	if body["stream"] != true || body["tools"] == nil {
		t.Errorf("the translated request lost its own fields: %s", pinnedRaw)
	}

	// The unpinned request is exactly the pinned one without "provider".
	if strings.Contains(plainRaw, `"provider"`) {
		t.Fatalf("an unpinned model must not carry a provider block: %s", plainRaw)
	}
	o, err := wire.ParseObject([]byte(pinnedRaw))
	if err != nil {
		t.Fatal(err)
	}
	delete(o, "provider")
	stripped, _ := wire.Marshal(o)
	if string(stripped) != plainRaw {
		t.Errorf("the unpinned body differs beyond the pin:\npinned-minus-pin %s\nplain            %s", stripped, plainRaw)
	}
}

// TestScenarioO_RejectDoesNotDependOnCandidateOrder covers M2: the reject
// policy is decided from the client's request before dispatch, over every
// candidate, so swapping the candidates' order gives the same answer.
func TestScenarioO_RejectDoesNotDependOnCandidateOrder(t *testing.T) {
	for _, strictFirst := range []bool{true, false} {
		name := "lenient first"
		if strictFirst {
			name = "strict first"
		}
		t.Run(name, func(t *testing.T) {
			e := apptest.New(t)
			up := apptest.NewUpstream(t, "openai")
			model := func(id, policy string) map[string]any {
				return map[string]any{"id": id, "display_name": id, "capabilities": []string{"streaming"},
					"request_field_policy": policy, "request_fields": map[string]any{"temperature": 0.1}}
			}
			strict := e.Account("strict-key", e.ProviderWithModels("strict", "openai_compatible", up.Srv.URL, model("vendor/strict", "reject")),
				apptest.AccountOpts{Secret: "sk-strict"})
			lenient := e.Account("lenient-key", e.ProviderWithModels("lenient", "openai_compatible", up.Srv.URL, model("vendor/lenient", "configured")),
				apptest.AccountOpts{Secret: "sk-lenient"})
			members := []apptest.Member{{Account: lenient, Model: "vendor/lenient"}, {Account: strict, Model: "vendor/strict"}}
			if strictFirst {
				members[0], members[1] = members[1], members[0]
			}
			alias := e.Alias("team/default", e.Pool("both", "ordered", members...))
			plan := e.Plan("default", apptest.PlanOpts{Mappings: map[string]string{"team/*": alias}})
			tok := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))

			resp := e.Gateway("POST", "/v1/chat/completions", tok, modelFieldsChat(map[string]any{"temperature": 0.9}))
			if body := apptest.ReadAll(resp); resp.StatusCode != 400 || !strings.Contains(body, "temperature") {
				t.Fatalf("status %d: %s, want 400 naming temperature", resp.StatusCode, body)
			}
			if n := up.Count(); n != 0 {
				t.Errorf("a refused request must not reach any upstream, %d calls", n)
			}
			// Without the field, both orders serve the request.
			resp = e.Gateway("POST", "/v1/chat/completions", tok, modelFieldsChat(nil))
			if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if up.Last().Body["temperature"] != 0.1 {
				t.Errorf("temperature = %v, want the configured 0.1", up.Last().Body["temperature"])
			}
		})
	}
}

// TestScenarioO_ForbiddenFieldNeverReachesARequest: a field refused at save
// time is never stored, so no request can carry it.
func TestScenarioO_ForbiddenFieldNeverReachesARequest(t *testing.T) {
	e := apptest.New(t)
	for _, f := range []string{"max_tokens", "maxTokens", "models", "cache_control", "tools", "store"} {
		code := e.Admin.Do("POST", "/api/v1/providers", map[string]any{"name": "bad-" + strings.ToLower(f), "spec": map[string]any{
			"type": "openai_compatible", "display_name": "bad", "base_url": "https://router.example.com/v1",
			"models": []any{map[string]any{"id": "m", "request_fields": map[string]any{f: 1}}}}}, nil)
		if code != 422 {
			t.Errorf("%s: status %d, want 422", f, code)
		}
	}
}

// TestScenarioO_SpecPutWithoutFieldsClearsThem records what a client that
// does not know the fields (an older Terraform provider) does on update: the
// spec is replaced, so the fields are cleared. Such a client must send them
// back unchanged.
func TestScenarioO_SpecPutWithoutFieldsClearsThem(t *testing.T) {
	e := apptest.New(t)
	prov := e.ProviderWithModels("router", "openai_compatible", "https://router.example.com/v1", map[string]any{
		"id": "m", "request_fields": map[string]any{"temperature": 0.3},
		"provider_routing": map[string]any{"only": []string{"provider-a"}}, "request_field_policy": "client"})
	var p struct {
		Spec map[string]any `json:"spec"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/providers/"+prov, nil, &p)
	models := p.Spec["models"].([]any)
	m := models[0].(map[string]any)
	delete(m, "request_fields")
	delete(m, "provider_routing")
	delete(m, "request_field_policy")
	e.Admin.MustDo(200, "PUT", "/api/v1/providers/"+prov, map[string]any{"name": "router", "spec": p.Spec}, nil)
	e.Admin.MustDo(200, "GET", "/api/v1/providers/"+prov, nil, &p)
	m = p.Spec["models"].([]any)[0].(map[string]any)
	if rf, _ := m["request_fields"].(map[string]any); len(rf) != 0 || m["provider_routing"] != nil || m["request_field_policy"] != "configured" {
		t.Errorf("a PUT without the fields should clear them, got %v", m)
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
