package claudecode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// executeMax runs one turn with a max_tokens limit; stream picks the bridge's
// streaming or collected mode.
func (r *rig) executeMax(t *testing.T, stream bool, edit func(*ir.Request)) (events []ir.Event, out *ir.Response, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := &ir.Request{Source: ir.AnthropicMessages, Model: "x", Messages: []ir.Message{userText("title this chat")}, Stream: stream, MaxOutputTokens: ptr(64)}
	if edit != nil {
		edit(req)
	}
	res, aerr := r.b.Execute(ctx, r.tgt, credentials.NewSecret("sk-ant-oat01-synthetic-test-token"), executor.Request{IR: req, Inbound: ir.AnthropicMessages}, stream)
	if aerr != nil {
		return nil, nil, aerr
	}
	if !stream {
		return nil, res.Response, nil
	}
	for e := range res.Stream {
		events = append(events, e)
		if e.Kind == ir.EvError {
			return events, nil, e.Err
		}
	}
	out, err = ir.Collect(replay(events))
	return events, out, err
}

func replay(evs []ir.Event) <-chan ir.Event {
	ch := make(chan ir.Event, len(evs))
	for _, e := range evs {
		ch <- e
	}
	close(ch)
	return ch
}

// Output that reaches max_tokens ends the turn with stop_reason max_tokens
// and what was generated, not with the CLI's API error; the CLI's own
// continuation calls are not made (so there is nothing to fail over on).
func TestBridgeMaxTokensEndsWithPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ir.Request)
	}{
		{"plain", nil},
		{"effort", func(x *ir.Request) { x.Reasoning = &ir.ReasoningConfig{OutputEffort: "high"} }},
	} {
		for _, stream := range []bool{true, false} {
			name := tc.name + "/collected"
			if stream {
				name = tc.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				r := newRig(t)
				for i := 0; i < 5; i++ {
					r.up.Push(synthetic.Reply{Text: "a long title that was cut ", StopReason: "max_tokens", InputTok: 12, OutputTok: 64})
				}
				events, out, err := r.executeMax(t, stream, tc.edit)
				if err != nil {
					t.Fatalf("want a truncated reply, got error: %v", err)
				}
				if out == nil {
					t.Fatal("no response")
				}
				if out.StopReason != ir.StopMaxTokens {
					t.Fatalf("stop %q", out.StopReason)
				}
				if len(out.Parts) != 1 || out.Parts[0].Text != "a long title that was cut " {
					t.Fatalf("parts %+v", out.Parts)
				}
				if out.Usage.InputTokens == nil || *out.Usage.InputTokens != 12 || out.Usage.OutputTokens == nil || *out.Usage.OutputTokens != 64 {
					t.Fatalf("usage %+v", out.Usage)
				}
				if n := r.up.Count(); n != 1 {
					t.Fatalf("%d upstream calls; the CLI's continuation must not run", n)
				}
				if stream {
					if last := events[len(events)-1]; last.Kind != ir.EvStop || last.StopReason != ir.StopMaxTokens {
						t.Fatalf("stream ends with %+v", last)
					}
				}
				if mt, _ := r.up.Last().Body["max_tokens"].(float64); mt != 64 {
					t.Fatalf("upstream max_tokens %v", r.up.Last().Body["max_tokens"])
				}
			})
		}
	}
}

// A normal request with a large budget is untouched.
func TestBridgeLargeMaxTokensUnchanged(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "done", InputTok: 5, OutputTok: 2})
	_, out, err := r.executeMax(t, true, func(x *ir.Request) { x.MaxOutputTokens = ptr(8000) })
	if err != nil || out.StopReason != ir.StopEndTurn || out.Parts[0].Text != "done" {
		t.Fatalf("%+v %v", out, err)
	}
}

// The CLI's report text alone (no upstream max_tokens stop seen) is also a
// truncation, never an error.
func TestTranslatorOutputCapReport(t *testing.T) {
	msg := "API Error: Claude's response exceeded the 64 output token maximum. To configure this behavior, set the CLAUDE_CODE_MAX_OUTPUT_TOKENS environment variable."
	for name, raw := range map[string]string{
		"assistant": mustJSON(map[string]any{"type": "assistant", "error": "max_output_tokens", "is_api_error_message": true, "api_error": "max_output_tokens", "api_error_params": map[string]any{"cut": "output_limit"},
			"message": map[string]any{"role": "assistant", "model": "<synthetic>", "stop_reason": "stop_sequence", "content": []any{map[string]any{"type": "text", "text": msg}}}}),
		"result": mustJSON(map[string]any{"type": "result", "is_error": true, "result": msg}),
	} {
		t.Run(name, func(t *testing.T) {
			tr := newTranslator(&conv{}, "m")
			l := line{raw: []byte(raw)}
			_ = json.Unmarshal(l.raw, &l.head)
			evs, done, err := tr.feed(l, 0, time.Now())
			if err != nil || done != doneMaxTokens || evs[len(evs)-1].StopReason != ir.StopMaxTokens {
				t.Fatalf("done=%v err=%v evs=%+v", done, err, evs)
			}
		})
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// The process is stopped before the final events are delivered: when the
// client sees the stop, the CLI can no longer start its own continuation call
// upstream.
func TestBridgeMaxTokensKillsBeforeFinalEvents(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "cut ", StopReason: "max_tokens", InputTok: 12, OutputTok: 64})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := &ir.Request{Source: ir.AnthropicMessages, Model: "x", Messages: []ir.Message{userText("hi")}, Stream: true, MaxOutputTokens: ptr(64)}
	res, aerr := r.b.Execute(ctx, r.tgt, credentials.NewSecret("sk-ant-oat01-synthetic-test-token"), executor.Request{IR: req, Inbound: ir.AnthropicMessages}, true)
	if aerr != nil {
		t.Fatal(aerr)
	}
	for e := range res.Stream {
		if e.Kind != ir.EvStop {
			continue
		}
		r.b.mu.Lock()
		live := len(r.b.procs)
		r.b.mu.Unlock()
		if live != 0 {
			t.Fatalf("%d process(es) still live when the stop reached the client", live)
		}
	}
}

// The continuation after a truncated main turn is rebuilt from the transcript:
// the CLI has not yet written the truncated reply when the turn ends, and
// appends its own recovery prompt within about 100 ms (harness-wire-facts
// 1.8), so the session is not shelved. The client still gets a correct
// continuation.
func TestBridgeMaxTokensContinuationUsesTranscript(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "a long answer that was cut ", StopReason: "max_tokens", InputTok: 12, OutputTok: 64}, synthetic.Reply{Text: "and the rest.", InputTok: 20, OutputTok: 4})
	first := []ir.Message{userText("write a long answer")}
	limit := func(x *ir.Request) { x.MaxOutputTokens = ptr(64) }
	res, _ := r.executeWith(t, first, limit)
	if res.StopReason != ir.StopMaxTokens {
		t.Fatalf("stop %q", res.StopReason)
	}
	r.b.mu.Lock()
	shelved := len(r.b.shelf)
	r.b.mu.Unlock()
	if shelved != 0 {
		t.Fatal("a session holding the CLI's recovery prompt was shelved")
	}
	second := append(first, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts}, userText("continue"))
	res2, ex2 := r.executeWith(t, second, limit)
	if provenancePath(ex2) != pathTranscript || res2.Parts[0].Text != "and the rest." {
		t.Fatalf("continuation served by %q: %+v", provenancePath(ex2), res2)
	}
	if n := r.up.Count(); n != 2 {
		t.Fatalf("%d upstream calls", n)
	}
}
