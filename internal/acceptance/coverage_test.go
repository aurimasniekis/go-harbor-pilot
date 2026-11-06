package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Namespaced base URLs serve the same gateway as the default ones.
func TestNamespacedBaseURLs(t *testing.T) {
	e := apptest.New(t)
	ant := apptest.NewUpstream(t, "anthropic")
	oai := apptest.NewUpstream(t, "openai")
	pa := e.Provider("anthropic", "anthropic", ant.Srv.URL, apptest.Model{ID: "am", MaxOutput: 1024})
	po := e.Provider("openai", "openai", oai.Srv.URL, apptest.Model{ID: "om", MaxOutput: 1024})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: e.Account("a", pa, apptest.AccountOpts{}), Model: "am"}))
	other := e.Alias("coding/openai", e.Pool("q", "ordered", apptest.Member{Account: e.Account("o", po, apptest.AccountOpts{}), Model: "om"}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias, Mappings: map[string]string{"gpt-*": other}}), apptest.BindingOpts{}))
	msg := []any{map[string]any{"role": "user", "content": "hi"}}
	tests := []struct {
		name, path string
		body       map[string]any
	}{
		{"anthropic namespace", "/anthropic/v1/messages", map[string]any{"model": "claude-x", "max_tokens": 8, "messages": msg}},
		{"openai namespace", "/openai/v1/chat/completions", map[string]any{"model": "gpt-x", "messages": msg}},
		{"openai responses namespace", "/openai/v1/responses", map[string]any{"model": "gpt-x", "input": "hi"}},
		{"gemini namespace", "/gemini/v1beta/models/claude-x:generateContent", map[string]any{"contents": []any{map[string]any{"role": "user",
			"parts": []any{map[string]any{"text": "hi"}}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.Gateway("POST", tt.path, tok, tt.body)
			if b := apptest.ReadAll(r); r.StatusCode != 200 {
				t.Fatalf("%s: %d %s", tt.path, r.StatusCode, b)
			}
		})
	}
}

// Tool observation: offered tools, calls generated, results seen and
// errors, and unique tool results across a session's repeated history.
func TestToolObservationAndUniqueResults(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	up.Default = apptest.Reply{ToolName: "read_file", ToolArgs: `{"path":"main.go"}`, InputTok: 10, OutputTok: 5}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Capabilities: []string{"tools"}})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: e.Account("a", prov, apptest.AccountOpts{}), Model: "m"}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{TrustSessions: true}))
	tools := []any{
		map[string]any{"name": "read_file", "description": "read", "input_schema": map[string]any{"type": "object"}},
		map[string]any{"name": "run_tests", "description": "test", "input_schema": map[string]any{"type": "object"}},
	}
	history := []any{
		map[string]any{"role": "user", "content": "fix the build"},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": map[string]any{"path": "a.go"}},
			map[string]any{"type": "tool_use", "id": "toolu_2", "name": "run_tests", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "package a"},
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_2", "content": "FAIL", "is_error": true}}},
	}
	send := func() {
		r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "x", "max_tokens": 64, "tools": tools, "messages": history},
			"X-Claude-Code-Session-Id", "sess-tools-1")
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
	send()
	send() // the same history again within the session
	e.Flush()

	var list struct {
		Items []struct {
			ID        string `json:"id"`
			ToolCalls int    `json:"tool_calls"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests", nil, &list)
	if len(list.Items) != 2 {
		t.Fatalf("requests %d", len(list.Items))
	}
	var d struct {
		ToolsOffered     int      `json:"tools_offered"`
		ToolResults      int      `json:"tool_results_seen"`
		ToolResultErrors int      `json:"tool_result_errors"`
		ToolNames        []string `json:"tool_names"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests/"+list.Items[1].ID, nil, &d)
	if d.ToolsOffered != 2 || d.ToolResults != 2 || d.ToolResultErrors != 1 || list.Items[1].ToolCalls != 1 {
		t.Fatalf("tool observation %+v, calls %d", d, list.Items[1].ToolCalls)
	}
	first := e.QueryInt(`SELECT unique_tool_results FROM requests WHERE id = ?`, list.Items[1].ID)
	second := e.QueryInt(`SELECT unique_tool_results FROM requests WHERE id = ?`, list.Items[0].ID)
	if first != 2 || second != 0 {
		t.Fatalf("unique tool results first %d second %d, want 2 then 0 (repeated history)", first, second)
	}
}

// Token counts are labelled estimates in each protocol's own shape.
func TestTokenCountsUseEachProtocolsShape(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: e.Account("a", prov, apptest.AccountOpts{}), Model: "m"}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	text := strings.Repeat("abcd", 25) // 100 characters, about 25 tokens
	gem := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}}}}
	tests := []struct {
		name, path string
		body       any
		field      string
	}{
		{"anthropic", "/v1/messages/count_tokens", map[string]any{"model": "x", "messages": []any{map[string]any{"role": "user", "content": text}}}, "input_tokens"},
		{"gemini", "/v1beta/models/x:countTokens", gem, "totalTokens"},
		{"gemini wrapped request", "/v1beta/models/x:countTokens", map[string]any{"generateContentRequest": gem}, "totalTokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.Gateway("POST", tt.path, tok, tt.body)
			body := apptest.ReadAll(r)
			if r.StatusCode != 200 || r.Header.Get("X-HP-Token-Count") != "estimated" {
				t.Fatalf("%d %s (header %q)", r.StatusCode, body, r.Header.Get("X-HP-Token-Count"))
			}
			var out map[string]float64
			if err := json.Unmarshal([]byte(body), &out); err != nil || out[tt.field] < 20 || out[tt.field] > 30 || len(out) != 1 {
				t.Fatalf("want only %s ≈ 25, got %s", tt.field, body)
			}
		})
	}
}
