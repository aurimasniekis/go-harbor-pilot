package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

// TestMain doubles as a fake `codex` binary emitting the documented
// `codex exec --json` event stream (HP_FAKE_CODEX=1).
func TestMain(m *testing.M) {
	if os.Getenv("HP_FAKE_CODEX") == "1" {
		fakeCodex()
		return
	}
	os.Exit(m.Run())
}

func out(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }

func fakeCodex() {
	args := os.Args[1:]
	prompt, _ := io.ReadAll(os.Stdin)
	thread := fmt.Sprintf("thr_%d", time.Now().UnixNano())
	mcpURL := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "resume" && i+1 < len(args) {
			thread = args[i+1]
		}
		if args[i] == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "mcp_servers.client.url=") {
			_ = json.Unmarshal([]byte(strings.TrimPrefix(args[i+1], "mcp_servers.client.url=")), &mcpURL)
		}
	}
	if f, err := os.OpenFile(os.Getenv("FAKE_CODEX_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		b, _ := json.Marshal(map[string]any{"args": args, "prompt": string(prompt), "codex_home": os.Getenv("CODEX_HOME")})
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	out(map[string]any{"type": "thread.started", "thread_id": thread})
	out(map[string]any{"type": "turn.started"})
	p := string(prompt)
	switch {
	case strings.Contains(p, "AUTHFAIL"):
		out(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "unexpected status 401 Unauthorized"}})
		os.Exit(1)
	case strings.Contains(p, "use tool") && mcpURL != "":
		rpc := func(method string, params any, id int) map[string]any {
			body := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
			if id > 0 {
				body["id"] = id
			}
			b, _ := json.Marshal(body)
			resp, err := http.Post(mcpURL, "application/json", bytes.NewReader(b))
			if err != nil {
				return nil
			}
			defer resp.Body.Close()
			var r map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&r)
			return r
		}
		rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"}, 1)
		rpc("notifications/initialized", nil, 0)
		list := rpc("tools/list", map[string]any{}, 2)
		tool := list["result"].(map[string]any)["tools"].([]any)[0].(map[string]any)["name"].(string)
		out(map[string]any{"type": "item.started", "item": map[string]any{"id": "item_7", "type": "mcp_tool_call", "server": "client", "tool": tool,
			"arguments": map[string]any{"q": "x"}, "status": "in_progress"}})
		res := rpc("tools/call", map[string]any{"name": tool, "arguments": map[string]any{"q": "x"}}, 3)
		text := ""
		if r, ok := res["result"].(map[string]any); ok {
			for _, c := range r["content"].([]any) {
				text += c.(map[string]any)["text"].(string)
			}
		}
		out(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_7", "type": "mcp_tool_call", "server": "client", "tool": tool, "status": "completed"}})
		out(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_8", "type": "agent_message", "text": "tool said: " + text}})
		out(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 200, "cached_input_tokens": 0, "output_tokens": 15}})
	default:
		first := strings.SplitN(strings.TrimSpace(p), "\n", 2)[0]
		out(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_1", "type": "reasoning", "text": "Considering the request."}})
		out(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_2", "type": "agent_message", "text": "echo: " + first}})
		out(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 10}})
	}
}

type rig struct {
	b   *Bridge
	log string
	tgt executor.Target
}

func newRig(t *testing.T) *rig {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{log: t.TempDir() + "/calls.jsonl"}
	r.b = New(Options{Binary: self, Root: t.TempDir(), ExtraEnv: []string{"HP_FAKE_CODEX=1", "FAKE_CODEX_LOG=" + r.log}})
	t.Cleanup(r.b.Close)
	r.tgt = executor.Target{Provider: &cp.Res[cp.ProviderSpec]{ID: "prv_cx", Spec: cp.ProviderSpec{Type: cp.ProviderCodex}},
		Account: &cp.Res[cp.AccountSpec]{ID: "acc_cx"}, Model: "gpt-test"}
	return r
}

func (r *rig) calls(t *testing.T) []map[string]any {
	t.Helper()
	f, err := os.Open(r.log)
	if err != nil {
		return nil
	}
	defer f.Close()
	var outv []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		_ = json.Unmarshal(sc.Bytes(), &m)
		outv = append(outv, m)
	}
	return outv
}

func text(s string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: s}}}
}

func (r *rig) exec(t *testing.T, msgs []ir.Message, tools []ir.Tool) (*ir.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := &ir.Request{Messages: msgs, Tools: tools, Stream: true, System: []ir.Part{{Kind: ir.PartText, Text: "Be brief."}}}
	res, aerr := r.b.Execute(ctx, r.tgt, credentials.NewSecret(`{"tokens":{"access_token":"synthetic"}}`), executor.Request{IR: req}, true)
	if aerr != nil {
		return nil, aerr.Err
	}
	return ir.Collect(res.Stream)
}

func TestCodexTurnsAndResume(t *testing.T) {
	r := newRig(t)
	res, err := r.exec(t, []ir.Message{text("hello")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 2 || res.Parts[1].Text != "echo: hello" || res.Parts[0].Reasoning == nil || !res.Parts[0].Reasoning.Summary {
		t.Fatalf("%+v", res)
	}
	if *res.Usage.InputTokens != 100 || *res.Usage.CacheReadTokens != 20 || !res.Usage.InputIncludesCache {
		t.Fatalf("usage %+v", res.Usage)
	}
	msgs := []ir.Message{text("hello"), {Role: ir.RoleAssistant, Parts: res.Parts}, text("again")}
	if res, err = r.exec(t, msgs, nil); err != nil || res.Parts[1].Text != "echo: again" {
		t.Fatalf("follow-up: %v %+v", err, res)
	}
	calls := r.calls(t)
	if len(calls) != 2 {
		t.Fatalf("calls %d", len(calls))
	}
	args := fmt.Sprint(calls[1]["args"])
	if !strings.Contains(args, "resume thr_") || calls[1]["prompt"] != "again" {
		t.Fatalf("follow-up did not resume the thread: %v %q", args, calls[1]["prompt"])
	}
	if b, err := os.ReadFile(calls[0]["codex_home"].(string) + "/auth.json"); err != nil || !strings.Contains(string(b), "synthetic") {
		t.Fatal("account sign-in file not seeded")
	}

	// A conversation the bridge has not served is replayed as text.
	r2 := newRig(t)
	if _, err := r2.exec(t, msgs, nil); err != nil {
		t.Fatal(err)
	}
	c2 := r2.calls(t)
	if p := c2[0]["prompt"].(string); !strings.Contains(p, "Conversation so far") || strings.Contains(fmt.Sprint(c2[0]["args"]), "resume") {
		t.Fatalf("first contact prompt %q", p)
	}
}

func TestCodexToolRoundTrip(t *testing.T) {
	r := newRig(t)
	tools := []ir.Tool{{Name: "lookup", Description: "Look something up", Parameters: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)}}
	msgs := []ir.Message{text("please use tool")}
	res, err := r.exec(t, msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != ir.StopToolUse || res.Parts[0].ToolCall == nil || res.Parts[0].ToolCall.Name != "lookup" {
		t.Fatalf("%+v", res)
	}
	call := res.Parts[0].ToolCall
	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts},
		ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: call.ID, Content: []ir.Part{{Kind: ir.PartText, Text: "RESULT-42"}}}}}})
	res, err = r.exec(t, msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parts[0].Text != "tool said: RESULT-42" || res.StopReason != ir.StopEndTurn {
		t.Fatalf("%+v", res)
	}
	if n := len(r.calls(t)); n != 1 {
		t.Fatalf("tool result should continue the same process, spawned %d", n)
	}
}

func TestCodexErrors(t *testing.T) {
	r := newRig(t)
	_, err := r.exec(t, []ir.Message{text("AUTHFAIL")}, nil)
	ie, ok := err.(*ir.Error)
	if !ok || ie.Kind != ir.ErrAuthentication {
		t.Fatalf("%#v", err)
	}
}
