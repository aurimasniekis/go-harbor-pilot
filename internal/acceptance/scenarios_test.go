// Package acceptance_test runs the product's critical end-to-end scenarios
// against a real Harbor Pilot instance and synthetic upstream providers.
package acceptance_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

func decodeClientStream(t *testing.T, p ir.Protocol, body string) []ir.Event {
	t.Helper()
	rd := wire.NewSSEReader(strings.NewReader(body), 1<<20)
	dec := protocol.NewDecoder(p)
	var out []ir.Event
	for {
		ev, err := rd.Next()
		if err != nil {
			break
		}
		evs, _, err := dec.Feed(ev)
		if err != nil {
			t.Fatalf("client stream is not valid %s: %v\n%s", p, err, body)
		}
		out = append(out, evs...)
	}
	out = append(out, dec.Close()...)
	if !dec.Finished() {
		t.Fatalf("client stream did not terminate properly:\n%s", body)
	}
	return out
}

type summary struct {
	text, tool, args string
	stop             ir.StopReason
}

func summarize(evs []ir.Event) summary {
	var s summary
	kinds := map[int]ir.PartKind{}
	for _, e := range evs {
		switch e.Kind {
		case ir.EvPartStart:
			kinds[e.Index] = e.Part.Kind
			if e.Part.ToolCall != nil {
				s.tool = e.Part.ToolCall.Name
			}
		case ir.EvDelta:
			switch kinds[e.Index] {
			case ir.PartText:
				s.text += e.Delta
			case ir.PartToolCall:
				s.args += e.Delta
			}
		case ir.EvStop:
			s.stop = e.StopReason
		}
	}
	return s
}

// Scenario A: a standalone harness uses only a gateway token, a base URL and
// a model selection. No custom headers, no daemon.
func TestScenarioA_StandaloneHarness(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	up.Default = apptest.Reply{Text: "Hello from the synthetic upstream", InputTok: 20, OutputTok: 6, CacheRead: 100}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "claude-sonnet-x", Capabilities: []string{"streaming", "tools"}, MaxOutput: 8192,
		Price: &[2]string{"3", "15"}})
	acc := e.Account("anthropic-main", prov, apptest.AccountOpts{Secret: "sk-ant-synthetic-secret-value"})
	alias := e.Alias("coding/default", e.Pool("sonnet", "ordered", apptest.Member{Account: acc, Model: "claude-sonnet-x"}))
	plan := e.Plan("default", apptest.PlanOpts{Mappings: map[string]string{"claude-*": alias}})
	tok := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{TrustSessions: true}))

	// Exactly what Claude Code sends: bearer token (ANTHROPIC_AUTH_TOKEN) to
	// ANTHROPIC_BASE_URL/v1/messages with a client model name.
	resp := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "claude-sonnet-4-5", "max_tokens": 512, "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}},
		"User-Agent", "claude-cli/2.1.318 (external, cli)", "anthropic-version", "2023-06-01", "X-Claude-Code-Session-Id", "4c7c1d2e-session")
	body := apptest.ReadAll(resp)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d %s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	s := summarize(decodeClientStream(t, ir.AnthropicMessages, body))
	if s.text != "Hello from the synthetic upstream" || s.stop != ir.StopEndTurn {
		t.Fatalf("summary %+v", s)
	}
	// The upstream got the provider credential, never the gateway token,
	// and the resolved upstream model.
	got := up.Last()
	if got.Header.Get("x-api-key") != "sk-ant-synthetic-secret-value" || strings.Contains(got.Raw, tok) || got.Header.Get("Authorization") != "" {
		t.Fatal("credential handling upstream is wrong")
	}
	if got.Body["model"] != "claude-sonnet-x" {
		t.Fatalf("upstream model %v", got.Body["model"])
	}
	e.Flush()
	if n := e.QueryInt(`SELECT count(*) FROM requests WHERE status = 'ok' AND harness = 'claude_code' AND session_confidence = 'medium'`); n != 1 {
		t.Fatalf("activity not recorded with harness/session: %d", n)
	}
	if n := e.QueryInt(`SELECT count(*) FROM ledger WHERE kind = 'usage' AND input_tokens = 20 AND cache_read_tokens = 100 AND output_tokens = 6 AND cost_kind = 'estimated_api_charge'`); n != 1 {
		t.Fatalf("ledger usage missing")
	}
	// A second unrelated client sharing nothing but the binding without a
	// session header is not merged into the session.
	resp = e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "claude-sonnet-4-5", "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": "x"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("non-stream: %d %s", resp.StatusCode, apptest.ReadAll(resp))
	}
	var msg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&msg)
	resp.Body.Close()
	if msg["type"] != "message" {
		t.Fatalf("non-stream body %v", msg)
	}
	e.Flush()
	if n := e.QueryInt(`SELECT count(*) FROM requests WHERE session_id IS NULL AND session_confidence = 'unknown'`); n != 1 {
		t.Fatalf("request without session evidence must stay unattributed to a session")
	}
	// Model listing works with the same token.
	lr := e.Gateway("GET", "/v1/models", tok, nil, "anthropic-version", "2023-06-01")
	if lb := apptest.ReadAll(lr); lr.StatusCode != 200 || !strings.Contains(lb, "coding/default") {
		t.Fatalf("models: %d %s", lr.StatusCode, lb)
	}
	// Invalid token is rejected in the client's protocol format.
	bad := e.Gateway("POST", "/v1/messages", "hpk_aaaaaaaa_"+strings.Repeat("A", 43), map[string]any{"model": "x", "max_tokens": 1, "messages": []any{}})
	if bb := apptest.ReadAll(bad); bad.StatusCode != 401 || !strings.Contains(bb, "authentication_error") {
		t.Fatalf("bad token: %d %s", bad.StatusCode, bb)
	}
}

// Scenario B: a supported cross-protocol request preserves tool and
// streaming semantics (Anthropic client -> OpenAI Chat upstream and
// OpenAI Chat client -> Anthropic upstream).
func TestScenarioB_CrossProtocolTools(t *testing.T) {
	e := apptest.New(t)
	oai := apptest.NewUpstream(t, "openai")
	oai.Default = apptest.Reply{ToolName: "bash", ToolArgs: `{"command":"ls -la"}`, InputTok: 30, OutputTok: 9}
	ant := apptest.NewUpstream(t, "anthropic")
	ant.Default = apptest.Reply{ToolName: "lookup", ToolArgs: `{"q":"weather"}`, InputTok: 15, OutputTok: 4}
	pOAI := e.Provider("compat", "openai_compatible", oai.Srv.URL, apptest.Model{ID: "gpt-x", Capabilities: []string{"streaming", "tools"}})
	pAnt := e.Provider("anthropic", "anthropic", ant.Srv.URL, apptest.Model{ID: "claude-x", Capabilities: []string{"streaming", "tools"}, MaxOutput: 4096})
	aOAI := e.Account("compat-1", pOAI, apptest.AccountOpts{})
	aAnt := e.Account("ant-1", pAnt, apptest.AccountOpts{})
	_ = e.Alias("cross/oai", e.Pool("oai", "ordered", apptest.Member{Account: aOAI, Model: "gpt-x"}))
	_ = e.Alias("cross/ant", e.Pool("ant", "ordered", apptest.Member{Account: aAnt, Model: "claude-x"}))
	plan := e.Plan("default", apptest.PlanOpts{})
	tok := e.Token(e.Binding("dev", plan, apptest.BindingOpts{}))

	t.Run("anthropic client to chat upstream", func(t *testing.T) {
		resp := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "cross/oai", "max_tokens": 256, "stream": true,
			"tools":    []any{map[string]any{"name": "bash", "description": "run", "input_schema": map[string]any{"type": "object"}}},
			"messages": []any{map[string]any{"role": "user", "content": "list files"}}})
		body := apptest.ReadAll(resp)
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
		s := summarize(decodeClientStream(t, ir.AnthropicMessages, body))
		if s.tool != "bash" || s.args != `{"command":"ls -la"}` || s.stop != ir.StopToolUse {
			t.Fatalf("tool semantics lost: %+v\n%s", s, body)
		}
		up := oai.Last()
		tools, _ := up.Body["tools"].([]any)
		if len(tools) != 1 || up.Body["stream"] != true {
			t.Fatalf("upstream chat body: %s", up.Raw)
		}
	})

	t.Run("chat client with tool result to anthropic upstream", func(t *testing.T) {
		resp := e.Gateway("POST", "/v1/chat/completions", tok, map[string]any{"model": "cross/ant", "stream": true,
			"stream_options": map[string]any{"include_usage": true},
			"tools":          []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}},
			"messages": []any{
				map[string]any{"role": "user", "content": "weather?"},
				map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"q":"x"}`}}}},
				map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "sunny"},
			}})
		body := apptest.ReadAll(resp)
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
		s := summarize(decodeClientStream(t, ir.OpenAIChat, body))
		if s.tool != "lookup" || s.args != `{"q":"weather"}` || s.stop != ir.StopToolUse {
			t.Fatalf("summary %+v\n%s", s, body)
		}
		if !strings.Contains(body, `"usage"`) {
			t.Fatal("include_usage requested but no usage chunk")
		}
		up := ant.Last()
		msgs := up.Body["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
		if last["type"] != "tool_result" || last["tool_use_id"] != "call_1" {
			t.Fatalf("tool result not translated: %s", up.Raw)
		}
	})

	t.Run("responses client to anthropic upstream", func(t *testing.T) {
		resp := e.Gateway("POST", "/v1/responses", tok, map[string]any{"model": "cross/ant", "stream": true, "input": "weather?",
			"tools": []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}}}})
		body := apptest.ReadAll(resp)
		s := summarize(decodeClientStream(t, ir.OpenAIResponses, body))
		if resp.StatusCode != 200 || s.tool != "lookup" || s.args != `{"q":"weather"}` {
			t.Fatalf("%d %+v\n%s", resp.StatusCode, s, body)
		}
	})

	t.Run("gemini client to chat upstream", func(t *testing.T) {
		resp := e.Gateway("POST", "/v1beta/models/cross%2Foai:streamGenerateContent?alt=sse", tok, map[string]any{
			"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "list"}}}},
			"tools":    []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "bash", "parameters": map[string]any{"type": "object"}}}}}})
		body := apptest.ReadAll(resp)
		if resp.StatusCode != 200 || !strings.Contains(body, `"functionCall"`) || !strings.Contains(body, `"ls -la"`) {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
	})
}

// Scenario E: concurrent requests cannot silently over-allocate local
// reservations (account max_concurrency = 2).
func TestScenarioE_ConcurrentReservations(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	up.Default = apptest.Reply{Text: "slow", Delay: 300 * time.Millisecond, InputTok: 1, OutputTok: 1}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "claude-x", MaxOutput: 1024})
	acc := e.Account("only", prov, apptest.AccountOpts{MaxConcurrency: 2})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "claude-x"}))
	tok := e.Token(e.Binding("ci", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))

	const n = 10
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "anything", "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "x"}}})
			_ = apptest.ReadAll(r)
			codes[i] = r.StatusCode
		}()
	}
	wg.Wait()
	ok, limited := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected status %d in %v", c, codes)
		}
	}
	if max := up.MaxSeen.Load(); max > 2 {
		t.Fatalf("upstream saw %d concurrent requests; limit is 2", max)
	}
	if ok < 2 || ok+limited != n {
		t.Fatalf("ok=%d limited=%d", ok, limited)
	}
	if active := e.QueryInt(`SELECT count(*) FROM reservations WHERE status = 'active'`); active != 0 {
		t.Fatalf("%d reservations left active", active)
	}
	if committed := e.QueryInt(`SELECT count(*) FROM reservations WHERE status = 'committed'`); committed != int64(ok) {
		t.Fatalf("committed %d != ok %d", committed, ok)
	}
}

func TestAttemptCountAndFailoverRecorded(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("a1", prov, apptest.AccountOpts{})
	a2 := e.Account("a2", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	up.Push(apptest.Reply{Status: 429, RetryAfter: 5})
	r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": "x", "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "x"}}})
	if b := apptest.ReadAll(r); r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	e.Flush()
	if n := e.QueryInt(`SELECT attempt_count FROM requests`); n != 2 {
		t.Fatalf("attempt_count %d", n)
	}
	if n := e.QueryInt(`SELECT count(*) FROM attempts WHERE status = 'error' AND error_kind = 'rate_limited' AND executed = 0 AND retry_after_s = 5`); n != 1 {
		t.Fatal("failed attempt not recorded")
	}
	if n := e.QueryInt(`SELECT count(*) FROM attempts WHERE status = 'ok' AND failover_reason = 'alternate_account'`); n != 1 {
		t.Fatal("failover attempt not recorded")
	}
	if n := e.QueryInt(`SELECT count(*) FROM reservations WHERE status = 'released'`); n != 1 {
		t.Fatal("unexecuted attempt's reservation must be released")
	}
}
