package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// A client conversation with 1h cache breakpoints on message blocks and
// inside a tool result, the shape a subscription client sends (synthetic,
// written from the public Messages API documentation).
const cachedConversation = `{"model":"x","max_tokens":1024,"stream":true,
 "system":[{"type":"text","text":"You are a test assistant.","cache_control":{"type":"ephemeral","ttl":"1h"}}],
 "tools":[{"name":"get_weather","description":"Weather for a city","input_schema":{"type":"object","properties":{"city":{"type":"string"}}},"cache_control":{"type":"ephemeral","ttl":"1h"}}],
 "messages":[
  {"role":"user","content":[{"type":"text","text":"What's the weather in Vilnius?","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
  {"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"Vilnius"}}]},
  {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":[{"type":"text","text":"sunny, 21C","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]},
  {"role":"assistant","content":[{"type":"text","text":"It is sunny in Vilnius."}]},
  {"role":"user","content":[{"type":"text","text":"And tomorrow?","cache_control":{"type":"ephemeral","ttl":"1h"}}]}
 ]}`

func parseCached(t *testing.T, body string) *ir.Request {
	t.Helper()
	r, err := anthropic.ParseRequest([]byte(body), anthropic.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The conversation the bridge hands the CLI (transcript and stdin turn)
// carries no client cache markers: the CLI adds its own 5m ones ahead of
// the conversation, and a client 1h marker after them is rejected.
func TestShapeRequestStripsCacheControl(t *testing.T) {
	r := parseCached(t, cachedConversation)
	c, losses, err := shapeRequest(r, "claude-test-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	history, last, err := lastUser(c.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(c.renameTools(last)); strings.Contains(s, "cache_control") || !strings.Contains(s, "And tomorrow?") {
		t.Fatalf("stdin turn: %s", s)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := writeTranscript(path, "sid", "/w", "claude-test-model", c, history); err != nil {
		t.Fatal(err)
	}
	tr, _ := os.ReadFile(path)
	if strings.Contains(string(tr), "cache_control") || !strings.Contains(string(tr), "sunny, 21C") {
		t.Fatalf("transcript: %s", tr)
	}
	i := slices.IndexFunc(losses, func(l ir.Loss) bool { return l.Feature == "cache_control" })
	if i < 0 || losses[i].Severity != ir.LossApproximate {
		t.Fatalf("no cache_control loss: %+v", losses)
	}
	for _, l := range losses {
		if l.Feature == anthropic.LossCacheTTLOrder || l.Feature == anthropic.LossCacheLimit {
			t.Errorf("builder loss leaked through the bridge: %+v", l)
		}
	}

	// Stripping changes neither the conversation fingerprint nor the
	// parked-process keys.
	plain := parseCached(t, strings.ReplaceAll(strings.ReplaceAll(cachedConversation, `,"cache_control":{"type":"ephemeral","ttl":"1h"}`, ""), "\n", ""))
	pc, plosses, err := shapeRequest(plain, "claude-test-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint(c.Messages) != fingerprint(pc.Messages) || hashOf(c.System) != hashOf(pc.System) || toolsKey(c.Tools) != toolsKey(pc.Tools) {
		t.Fatal("cache markers changed the conversation identity")
	}
	if slices.ContainsFunc(plosses, func(l ir.Loss) bool { return l.Feature == "cache_control" }) {
		t.Fatalf("loss recorded without markers: %+v", plosses)
	}
	for i := range c.Messages {
		if !json.Valid(c.Messages[i]) {
			t.Fatalf("message %d: %s", i, c.Messages[i])
		}
	}
}

// End to end with the CLI: the synthetic upstream enforces the TTL order
// and breakpoint limit, so a client marker reaching the CLI shows as a 400.
func TestBridgeDropsClientCacheControl(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "Cloudy tomorrow.", InputTok: 20, OutputTok: 3})
	req := parseCached(t, cachedConversation)
	res := r.run(t, req.Messages, req.Tools)
	if len(res.Parts) != 1 || res.Parts[0].Text != "Cloudy tomorrow." {
		t.Fatalf("%+v", res)
	}
	// The CLI places its own markers (on its system prompt, and depending
	// on the version on the last turn); the history blocks before the last
	// turn are the client's alone and must arrive unmarked.
	up := lastUpstreamMessages(t, r.up)
	var found []string
	for _, m := range up {
		mm, _ := m.(map[string]any)
		if mm["role"] == "system" {
			continue
		}
		blocks, _ := mm["content"].([]any)
		for _, b := range blocks {
			bm, _ := b.(map[string]any)
			nested, _ := bm["content"].([]any)
			for _, x := range append([]any{bm}, nested...) {
				xm, _ := x.(map[string]any)
				if txt, _ := xm["text"].(string); txt == "What's the weather in Vilnius?" || txt == "sunny, 21C" {
					found = append(found, txt)
					if _, ok := xm["cache_control"]; ok {
						t.Errorf("client marker on %q reached the upstream", txt)
					}
				}
			}
		}
	}
	if len(found) != 2 {
		t.Fatalf("history not resumed: %v", found)
	}
}

func TestClientCacheTTL(t *testing.T) {
	cc := func(ttl string) *ir.CacheControl { return &ir.CacheControl{Type: "ephemeral", TTL: ttl} }
	text := func(c *ir.CacheControl) ir.Part { return ir.Part{Kind: ir.PartText, Text: "x", CacheControl: c} }
	for _, tc := range []struct {
		name string
		r    ir.Request
		want string
	}{
		{"no marks", ir.Request{Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{text(nil)}}}}, "1h"},
		{"5m only", ir.Request{System: []ir.Part{text(cc(""))}, Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{text(cc("5m"))}}}}, "5m"},
		{"any 1h", ir.Request{System: []ir.Part{text(cc(""))}, Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{text(cc("1h"))}}}}, "1h"},
		{"1h in a tool result", ir.Request{Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{text(cc("")),
			{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: "t", Content: []ir.Part{text(cc("1h"))}}}}}}}, "1h"},
		{"5m on a tool", ir.Request{Tools: []ir.Tool{{Name: "t", CacheControl: cc("")}}}, "5m"},
		{"top-level automatic 5m", ir.Request{Ext: map[string]json.RawMessage{anthropic.P + ":cache_control": json.RawMessage(`{"type":"ephemeral"}`)}}, "5m"},
	} {
		if got := clientCacheTTL(&tc.r); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestCacheEnvOverridesOperatorTTL(t *testing.T) {
	got := cacheEnv([]string{"A=1", "CLAUDE_CODE_PROMPT_CACHE_TTL=5m", "FORCE_PROMPT_CACHING_5M=1"}, "1h")
	if want := []string{"A=1", "CLAUDE_CODE_PROMPT_CACHE_TTL=1h"}; !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}

// The CLI is told the client's TTL, its own breakpoints follow it, and a
// parked process is only reused for a conversation with the same TTL.
func TestBridgeCacheTTLFollowsClient(t *testing.T) {
	r := newRig(t)
	markers := func() []string {
		var out []string
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, y := range x {
					if k == "cache_control" {
						b, _ := json.Marshal(y)
						out = append(out, string(b))
					} else {
						walk(y)
					}
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			}
		}
		walk(r.up.Last().Body)
		return out
	}
	envOf := func() string {
		r.b.mu.Lock()
		defer r.b.mu.Unlock()
		var v []string
		for _, p := range r.b.procs {
			for _, kv := range p.cmd.Env {
				if strings.HasPrefix(kv, "CLAUDE_CODE_PROMPT_CACHE_TTL=") || kv == "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1" {
					v = append(v, kv)
				}
			}
		}
		slices.Sort(v)
		return strings.Join(v, ",")
	}
	msg := func(text, ttl string) ir.Message {
		p := ir.Part{Kind: ir.PartText, Text: text}
		if ttl != "-" {
			p.CacheControl = &ir.CacheControl{Type: "ephemeral", TTL: ttl}
		}
		return ir.Message{Role: ir.RoleUser, Parts: []ir.Part{p}}
	}
	for _, tc := range []struct {
		name, ttl, wantEnv, wantMark string
	}{
		{"client 1h", "1h", "1h", `{"ttl":"1h","type":"ephemeral"}`},
		{"client 5m", "", "5m", `{"type":"ephemeral"}`},
		{"no client marks", "-", "1h", `{"ttl":"1h","type":"ephemeral"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.b.Close()
			r.b = New(r.b.o)
			r.up.Push(synthetic.Reply{Text: "ok", InputTok: 2, OutputTok: 1})
			r.run(t, []ir.Message{msg("question "+tc.name, tc.ttl)}, nil)
			if got := envOf(); got != "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1,CLAUDE_CODE_PROMPT_CACHE_TTL="+tc.wantEnv {
				t.Fatalf("env %q", got)
			}
			ms := markers()
			if len(ms) == 0 {
				t.Fatal("the CLI placed no breakpoints")
			}
			for _, m := range ms {
				if m != tc.wantMark {
					t.Fatalf("breakpoints %v, want all %s", ms, tc.wantMark)
				}
			}
		})
	}

	// A parked 5m process does not continue the same conversation at 1h.
	r.b.Close()
	r.b = New(r.b.o)
	r.up.Push(synthetic.Reply{Text: "a1", InputTok: 2, OutputTok: 1}, synthetic.Reply{Text: "a2", InputTok: 2, OutputTok: 1})
	first := []ir.Message{msg("q1", "")}
	res := r.run(t, first, nil)
	next := append(first, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts}, msg("q2", "1h"))
	r.run(t, next, nil)
	r.b.mu.Lock()
	n := len(r.b.procs)
	r.b.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d processes: a 5m process was reused for a 1h conversation", n)
	}
}
