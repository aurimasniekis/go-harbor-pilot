package claudecode

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// claudeBinary returns the installed CLI, or skips. These tests run the
// real binary against a synthetic upstream (no credentials, no network).
func claudeBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("HP_TEST_CLAUDE_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("claude")
	if err != nil {
		home, _ := os.UserHomeDir()
		if _, serr := os.Stat(home + "/.local/bin/claude"); serr == nil {
			return home + "/.local/bin/claude"
		}
		t.Skip("claude CLI not installed")
	}
	return p
}

type rig struct {
	b   *Bridge
	up  *synthetic.Upstream
	tgt executor.Target
	rl  []RateLimitInfo
	mu  sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	bin := claudeBinary(t)
	up := synthetic.New("anthropic")
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	r := &rig{up: up}
	r.b = New(Options{Binary: bin, Root: t.TempDir(), ExtraEnv: []string{"ANTHROPIC_BASE_URL=" + srv.URL},
		OnRateLimit: func(_ string, info RateLimitInfo) { r.mu.Lock(); r.rl = append(r.rl, info); r.mu.Unlock() }})
	t.Cleanup(r.b.Close)
	r.tgt = executor.Target{Provider: &cp.Res[cp.ProviderSpec]{ID: "prv_cc", Spec: cp.ProviderSpec{Type: cp.ProviderClaudeCode}},
		Account: &cp.Res[cp.AccountSpec]{ID: "acc_cc", Spec: cp.AccountSpec{Billing: cp.BillingSubscription}}, Model: "claude-test-model"}
	return r
}

var weatherTool = ir.Tool{Name: "get_weather", Description: "Weather for a city", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}

func userText(s string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: s}}}
}

func (r *rig) run(t *testing.T, msgs []ir.Message, tools []ir.Tool) *ir.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req := &ir.Request{Source: ir.AnthropicMessages, Model: "x", System: []ir.Part{{Kind: ir.PartText, Text: "You are a test assistant."}}, Messages: msgs, Tools: tools, Stream: true}
	res, aerr := r.b.Execute(ctx, r.tgt, credentials.NewSecret("sk-ant-oat01-synthetic-test-token"), executor.Request{IR: req, Inbound: ir.AnthropicMessages}, true)
	if aerr != nil {
		t.Fatalf("execute: %v", aerr)
	}
	out, err := ir.Collect(res.Stream)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return out
}

func lastUpstreamMessages(t *testing.T, up *synthetic.Upstream) []any {
	t.Helper()
	if up.Count() == 0 {
		t.Fatal("upstream saw no request")
	}
	msgs, _ := up.Last().Body["messages"].([]any)
	return msgs
}

func TestBridgeToolRoundTripReusesProcess(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{ToolName: "mcp__client__get_weather", ToolArgs: `{"city":"Vilnius"}`, InputTok: 30, OutputTok: 9},
		synthetic.Reply{Text: "It is sunny in Vilnius.", InputTok: 40, OutputTok: 7},
		synthetic.Reply{Text: "You're welcome.", InputTok: 50, OutputTok: 4})

	msgs := []ir.Message{userText("What's the weather in Vilnius?")}
	res := r.run(t, msgs, []ir.Tool{weatherTool})
	if res.StopReason != ir.StopToolUse || len(res.Parts) != 1 || res.Parts[0].ToolCall == nil {
		t.Fatalf("first turn: %+v", res)
	}
	call := res.Parts[0].ToolCall
	if call.Name != "get_weather" || !strings.Contains(call.Arguments, "Vilnius") {
		t.Fatalf("tool call not mapped back to the client name: %+v", call)
	}
	if res.Usage.InputTokens == nil || *res.Usage.InputTokens != 30 || *res.Usage.OutputTokens != 9 {
		t.Fatalf("usage %+v", res.Usage)
	}

	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts},
		ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: call.ID, Content: []ir.Part{{Kind: ir.PartText, Text: "sunny, 21C"}}}}}})
	res = r.run(t, msgs, []ir.Tool{weatherTool})
	if res.StopReason != ir.StopEndTurn || len(res.Parts) != 1 || res.Parts[0].Text != "It is sunny in Vilnius." {
		t.Fatalf("second turn: %+v", res)
	}
	up := lastUpstreamMessages(t, r.up)
	if b, _ := json.Marshal(up); !strings.Contains(string(b), "sunny, 21C") {
		t.Fatalf("tool result did not reach the model: %s", b)
	}
	if n := len(r.b.procs); n != 1 {
		t.Fatalf("expected one reused process, got %d", n)
	}

	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts}, userText("Thanks!"))
	res = r.run(t, msgs, []ir.Tool{weatherTool})
	if res.Parts[0].Text != "You're welcome." || len(r.b.procs) != 1 || r.up.Count() != 3 {
		t.Fatalf("follow-up turn: %+v procs=%d upstream=%d", res, len(r.b.procs), r.up.Count())
	}
}

func TestBridgeResumesHistoryFromTranscript(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "Second answer.", InputTok: 20, OutputTok: 3})
	msgs := []ir.Message{userText("first question"), {Role: ir.RoleAssistant, Parts: []ir.Part{{Kind: ir.PartText, Text: "first answer"}}}, userText("second question")}
	res := r.run(t, msgs, nil)
	if res.Parts[0].Text != "Second answer." {
		t.Fatalf("%+v", res)
	}
	up := lastUpstreamMessages(t, r.up)
	var roles, texts []string
	for _, m := range up {
		mm := m.(map[string]any)
		if mm["role"] == "system" {
			continue
		}
		roles = append(roles, mm["role"].(string))
		b, _ := json.Marshal(mm["content"])
		texts = append(texts, string(b))
	}
	if strings.Join(roles, ",") != "user,assistant,user" || !strings.Contains(texts[0], "first question") || !strings.Contains(texts[1], "first answer") ||
		!strings.Contains(texts[2], "second question") {
		t.Fatalf("history not resumed in order: %v %v", roles, texts)
	}
}

func TestBridgeMapsUpstreamErrors(t *testing.T) {
	r := newRig(t)
	reset := time.Now().Add(2 * time.Hour).Unix()
	r.up.Push(synthetic.Reply{Status: 429, Headers: map[string]string{
		"anthropic-ratelimit-unified-status": "rejected", "anthropic-ratelimit-unified-representative-claim": "five_hour",
		"anthropic-ratelimit-unified-reset": itoa(reset), "anthropic-ratelimit-unified-5h-utilization": "1.0",
		"anthropic-ratelimit-unified-5h-reset": itoa(reset), "anthropic-ratelimit-unified-5h-status": "rejected"}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := &ir.Request{Source: ir.AnthropicMessages, Messages: []ir.Message{userText("hi")}, Stream: true}
	res, aerr := r.b.Execute(ctx, r.tgt, credentials.NewSecret("sk-ant-oat01-synthetic-test-token"), executor.Request{IR: req}, true)
	if aerr != nil {
		t.Fatal(aerr)
	}
	_, err := ir.Collect(res.Stream)
	ie, ok := err.(*ir.Error)
	if !ok || ie.Kind != ir.ErrRateLimited || ie.RetryAfterSeconds < 3500 {
		t.Fatalf("error %#v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rl) == 0 || r.rl[len(r.rl)-1].Status != "rejected" || r.rl[len(r.rl)-1].UnifiedWindows["five_hour"].Utilization != 1 {
		t.Fatalf("rate limit info %+v", r.rl)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestFingerprintIgnoresFormatting(t *testing.T) {
	a := []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`),
		json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"s"},{"type":"text","text":"hello "}]}`)}
	b := []json.RawMessage{json.RawMessage(`{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}`),
		json.RawMessage(`{"content":[{"text":"hello","type":"text"}],"role":"assistant"}`)}
	if fingerprint(a) != fingerprint(b) {
		t.Fatal("equivalent conversations fingerprint differently")
	}
	c := []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`), json.RawMessage(`{"role":"assistant","content":"goodbye"}`)}
	if fingerprint(a) == fingerprint(c) {
		t.Fatal("different conversations share a fingerprint")
	}
}
