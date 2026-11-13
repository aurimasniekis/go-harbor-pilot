package acceptance_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Scenario P: per-model reasoning translation (D-047). A Claude Code client
// (adaptive thinking with output_config.effort, harness-wire-facts §1.7)
// reaches upstreams whose models configure, or do not configure, a reasoning
// option.

// claudeCodeBody is the request shape Claude Code sends, without streaming.
func claudeCodeBody(model string) map[string]any {
	return map[string]any{
		"model": model, "max_tokens": 32000,
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "List the files."}}}},
		"tools": []any{map[string]any{"name": "list_files", "description": "List files.",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}},
		"thinking":           map[string]any{"type": "adaptive", "display": "updates"},
		"output_config":      map[string]any{"effort": "high"},
		"context_management": map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}},
	}
}

// reasoningGateway wires one provider of type typ whose model carries the
// given reasoning option (nil = none) behind alias "team/default". The
// model declares no reasoning capability, so routing a Claude Code request
// to it also proves adaptive thinking alone is not a routing requirement.
func reasoningGateway(t *testing.T, typ string, reasoning map[string]any) (*apptest.Env, *apptest.Upstream, string, string) {
	t.Helper()
	e := apptest.New(t)
	kind := "openai"
	if typ == "anthropic" {
		kind = "anthropic"
	}
	up := apptest.NewUpstream(t, kind)
	up.Default = apptest.Reply{Text: "served", InputTok: 5, OutputTok: 2}
	model := map[string]any{"id": "vendor/model-a", "max_output_tokens": 64000, "capabilities": []string{"streaming", "tools"}}
	if reasoning != nil {
		model["reasoning"] = reasoning
	}
	prov := e.ProviderWithModels("up", typ, up.Srv.URL, model)
	acc := e.Account("up-key", prov, apptest.AccountOpts{Secret: "sk-synthetic-reasoning"})
	alias := e.Alias("team/default", e.Pool("up", "ordered", apptest.Member{Account: acc, Model: "vendor/model-a"}))
	plan := e.Plan("default", apptest.PlanOpts{Mappings: map[string]string{"team/*": alias}})
	return e, up, e.Token(e.Binding("laptop", plan, apptest.BindingOpts{})), prov
}

func lossesOfLast(t *testing.T, e *apptest.Env) string {
	t.Helper()
	e.Flush()
	return e.QueryString(`SELECT losses FROM requests ORDER BY rowid DESC LIMIT 1`)
}

// P1: adaptive thinking and output_config.effort reach an OpenRouter-style
// upstream as its reasoning object, and only context_management is lost.
func TestScenarioP1_ClaudeCodeAdaptiveReachesRouterReasoning(t *testing.T) {
	e, up, tok, _ := reasoningGateway(t, "openai_compatible", map[string]any{"mode": "client", "style": "openrouter_reasoning"})
	resp := e.Gateway("POST", "/v1/messages", tok, claudeCodeBody("team/default"))
	if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := up.Last().Body
	r, _ := got["reasoning"].(map[string]any)
	if len(r) != 1 || r["effort"] != "high" {
		t.Errorf("reasoning = %v, want {effort: high}", got["reasoning"])
	}
	for _, k := range []string{"reasoning_effort", "thinking", "output_config", "context_management"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %v, want absent", k, v)
		}
	}
	losses := lossesOfLast(t, e)
	if !strings.Contains(losses, "field:anthropic_messages:context_management") || !strings.Contains(losses, "server-side context editing") {
		t.Errorf("context_management should still be dropped, with its reason: %s", losses)
	}
	for _, f := range []string{"field:anthropic_messages:thinking", "field:anthropic_messages:output_config"} {
		if strings.Contains(losses, f) {
			t.Errorf("%s is translated now, not dropped: %s", f, losses)
		}
	}
}

// P2: without the option nothing changes: no reasoning parameter, and the
// loss says how to translate it.
func TestScenarioP2_WithoutOptionUnchanged(t *testing.T) {
	e, up, tok, _ := reasoningGateway(t, "openai_compatible", nil)
	resp := e.Gateway("POST", "/v1/messages", tok, claudeCodeBody("team/default"))
	if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := up.Last().Body
	for _, k := range []string{"reasoning", "reasoning_effort", "thinking"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %v, want absent", k, v)
		}
	}
	if losses := lossesOfLast(t, e); !strings.Contains(losses, "field:anthropic_messages:thinking") || !strings.Contains(losses, "reasoning option") {
		t.Errorf("an untranslated thinking field should be a drop loss naming the option: %s", losses)
	}
}

// P3: on a native Anthropic upstream a model in client mode sends exactly
// the bytes a model without the option sends.
func TestScenarioP3_NativeAnthropicByteIdentical(t *testing.T) {
	var raws []string
	for _, opt := range []map[string]any{nil, {"mode": "client", "max_budget_tokens": 16000}} {
		e, up, tok, _ := reasoningGateway(t, "anthropic", opt)
		resp := e.Gateway("POST", "/v1/messages", tok, claudeCodeBody("team/default"))
		if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		raws = append(raws, up.Last().Raw)
	}
	if raws[0] != raws[1] {
		t.Errorf("native body changed by the option\nwithout %s\nwith    %s", raws[0], raws[1])
	}
	if !strings.Contains(raws[0], `"thinking":{"display":"updates","type":"adaptive"}`) {
		t.Errorf("adaptive thinking must reach the native upstream unchanged: %s", raws[0])
	}
}

// P4: fixed and off ignore the client.
func TestScenarioP4_FixedAndOff(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  map[string]any
		want string
	}{
		{"fixed", map[string]any{"mode": "fixed", "style": "openrouter_reasoning", "effort": "low"}, `map[effort:low]`},
		{"fixed budget", map[string]any{"mode": "fixed", "style": "openrouter_reasoning", "budget_tokens": 4096}, `map[max_tokens:4096]`},
		{"off", map[string]any{"mode": "off", "style": "openrouter_reasoning"}, `map[effort:none]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, up, tok, _ := reasoningGateway(t, "openai_compatible", tc.opt)
			resp := e.Gateway("POST", "/v1/messages", tok, claudeCodeBody("team/default"))
			if body := apptest.ReadAll(resp); resp.StatusCode != 200 {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if got := fmtAny(up.Last().Body["reasoning"]); got != tc.want {
				t.Errorf("reasoning = %s, want %s", got, tc.want)
			}
			if losses := lossesOfLast(t, e); !strings.Contains(losses, "reasoning_policy") {
				t.Errorf("overriding the client should be recorded: %s", losses)
			}
		})
	}
}

// P5: the DeepSeek style toggles thinking and clamps the effort.
func TestScenarioP5_DeepSeekToggle(t *testing.T) {
	e, up, tok, _ := reasoningGateway(t, "openai_compatible", map[string]any{"mode": "client", "style": "deepseek_thinking"})
	body := claudeCodeBody("team/default")
	body["output_config"] = map[string]any{"effort": "xhigh"}
	resp := e.Gateway("POST", "/v1/messages", tok, body)
	if b := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := up.Last().Body
	if fmtAny(got["thinking"]) != `map[type:enabled]` || got["reasoning_effort"] != "high" {
		t.Errorf("thinking = %v, reasoning_effort = %v; want enabled and high (xhigh clamped)", got["thinking"], got["reasoning_effort"])
	}
	if losses := lossesOfLast(t, e); !strings.Contains(losses, "reasoning_effort_clamped") {
		t.Errorf("the clamp should be recorded: %s", losses)
	}
}

// P6: earlier reasoning is sent back in the configured history field.
func TestScenarioP6_HistoryField(t *testing.T) {
	chat := map[string]any{"model": "team/default", "max_tokens": 64, "messages": []any{
		map[string]any{"role": "user", "content": "Add 17 and 25."},
		map[string]any{"role": "assistant", "reasoning_content": "17 plus 25 is 42.", "content": "42"},
		map[string]any{"role": "user", "content": "Now double it."},
	}}
	for _, tc := range []struct {
		name string
		opt  map[string]any
		want any
	}{
		{"field", map[string]any{"mode": "client", "style": "deepseek_thinking", "history": "field"}, "17 plus 25 is 42."},
		{"drop by default", map[string]any{"mode": "client", "style": "deepseek_thinking"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, up, tok, _ := reasoningGateway(t, "openai_compatible", tc.opt)
			resp := e.Gateway("POST", "/v1/chat/completions", tok, chat)
			if b := apptest.ReadAll(resp); resp.StatusCode != 200 {
				t.Fatalf("status %d: %s", resp.StatusCode, b)
			}
			msgs, _ := up.Last().Body["messages"].([]any)
			var asst map[string]any
			for _, m := range msgs {
				if mm, _ := m.(map[string]any); mm["role"] == "assistant" {
					asst = mm
				}
			}
			if asst["reasoning_content"] != tc.want {
				t.Errorf("assistant reasoning_content = %v, want %v", asst["reasoning_content"], tc.want)
			}
		})
	}
}

// P7: routing is unchanged. Adaptive thinking alone (P1) does not require
// the reasoning capability, but an explicit level still does.
func TestScenarioP7_RoutingRequirementsUnchanged(t *testing.T) {
	e, up, tok, _ := reasoningGateway(t, "openai_compatible", map[string]any{"mode": "client", "style": "openrouter_reasoning"})
	resp := e.Gateway("POST", "/v1/messages", tok, claudeCodeBody("team/default"))
	if b := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("adaptive thinking must route to a model without the reasoning capability: %d %s", resp.StatusCode, b)
	}
	resp = e.Gateway("POST", "/v1/chat/completions", tok, map[string]any{"model": "team/default", "max_tokens": 8, "reasoning_effort": "low",
		"messages": []any{map[string]any{"role": "user", "content": "x"}}})
	if b := apptest.ReadAll(resp); resp.StatusCode == 200 {
		t.Fatalf("an explicit reasoning level still needs a model with the reasoning capability: %s", b)
	}
	if up.Count() != 1 {
		t.Errorf("upstream calls = %d, want 1", up.Count())
	}
}

// P8: a provider PUT that leaves the option out clears it.
func TestScenarioP8_SpecPutClearsTheOption(t *testing.T) {
	e, _, _, prov := reasoningGateway(t, "openai_compatible", map[string]any{"mode": "fixed", "style": "openrouter_reasoning", "effort": "low"})
	var p struct {
		Spec map[string]any `json:"spec"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/providers/"+prov, nil, &p)
	m := p.Spec["models"].([]any)[0].(map[string]any)
	if r, _ := m["reasoning"].(map[string]any); r["mode"] != "fixed" {
		t.Fatalf("saved option = %v", m["reasoning"])
	}
	delete(m, "reasoning")
	e.Admin.MustDo(200, "PUT", "/api/v1/providers/"+prov, map[string]any{"name": "up", "spec": p.Spec}, nil)
	e.Admin.MustDo(200, "GET", "/api/v1/providers/"+prov, nil, &p)
	if m := p.Spec["models"].([]any)[0].(map[string]any); m["reasoning"] != nil {
		t.Errorf("a PUT without the option should clear it, got %v", m["reasoning"])
	}
}

// A saved option that changes nothing is stored as no option, and an
// invalid one is refused with its location.
func TestScenarioP_OptionValidation(t *testing.T) {
	e := apptest.New(t)
	prov := e.ProviderWithModels("auto", "openai_compatible", "https://router.example.com/v1", map[string]any{"id": "m", "reasoning": map[string]any{"mode": "auto"}})
	var p struct {
		Spec map[string]any `json:"spec"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/providers/"+prov, nil, &p)
	if m := p.Spec["models"].([]any)[0].(map[string]any); m["reasoning"] != nil {
		t.Errorf("an auto-only option should be stored as none, got %v", m["reasoning"])
	}
	var errBody map[string]any
	e.Admin.MustDo(422, "POST", "/api/v1/providers", map[string]any{"name": "bad", "spec": map[string]any{
		"type": "anthropic", "base_url": "https://api.anthropic.com",
		"models": []any{map[string]any{"id": "m", "reasoning": map[string]any{"mode": "client", "style": "openrouter_reasoning"}}},
	}}, &errBody)
	if !strings.Contains(fmtAny(errBody), "reasoning.style") {
		t.Errorf("error should locate the style: %v", errBody)
	}
}

func fmtAny(v any) string { return fmt.Sprint(v) }
