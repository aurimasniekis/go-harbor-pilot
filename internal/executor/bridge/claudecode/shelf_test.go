package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// execute runs one request and returns the result's provenance, losses
// and collected response.
func (r *rig) execute(t *testing.T, msgs []ir.Message, tools []ir.Tool) (*ir.Response, *executor.Result) {
	t.Helper()
	return r.executeWith(t, msgs, func(x *ir.Request) { x.Tools = tools })
}

func (r *rig) executeWith(t *testing.T, msgs []ir.Message, edit func(*ir.Request)) (*ir.Response, *executor.Result) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req := &ir.Request{Source: ir.AnthropicMessages, Model: "x", System: []ir.Part{{Kind: ir.PartText, Text: "You are a test assistant."}}, Messages: msgs, Stream: true}
	edit(req)
	res, aerr := r.b.Execute(ctx, r.tgt, credentials.NewSecret("sk-ant-oat01-synthetic-test-token"), executor.Request{IR: req, Inbound: ir.AnthropicMessages}, true)
	if aerr != nil {
		t.Fatalf("execute: %v", aerr)
	}
	out, err := ir.Collect(res.Stream)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return out, res
}

func (r *rig) onlyProc(t *testing.T) *proc {
	t.Helper()
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if len(r.b.procs) != 1 {
		t.Fatalf("%d processes", len(r.b.procs))
	}
	for _, p := range r.b.procs {
		return p
	}
	return nil
}

// endAndShelve ends the process as the reaper would and waits for its
// session to reach the shelf.
func (r *rig) endAndShelve(t *testing.T, p *proc) *shelfEntry {
	t.Helper()
	p.kill("idle timeout")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.b.mu.Lock()
		for _, e := range r.b.shelf {
			r.b.mu.Unlock()
			return e
		}
		r.b.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the session was not shelved")
	return nil
}

func upstreamMessages(c synthetic.Captured) []string {
	ms, _ := c.Body["messages"].([]any)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		// A string content is the same prompt as one text block; the CLI
		// switches forms when it moves its breakpoint.
		if mm, ok := m.(map[string]any); ok {
			if s, ok := mm["content"].(string); ok {
				mm["content"] = []any{map[string]any{"type": "text", "text": s}}
			}
		}
		b, _ := json.Marshal(stripAllCacheControl(m))
		out = append(out, string(b))
	}
	return out
}

func stripAllCacheControl(v any) any {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "cache_control")
		for k, y := range x {
			x[k] = stripAllCacheControl(y)
		}
	case []any:
		for i, y := range x {
			x[i] = stripAllCacheControl(y)
		}
	}
	return v
}

func provenancePath(res *executor.Result) string {
	s, _ := res.Provenance["bridge_session"].(string)
	return s
}

// After an idle timeout the next turn resumes the CLI's own session from
// the shelf: same session id, the earlier request's messages unchanged as
// the prefix, and only the new user turn typed.
func TestBridgeResumesShelvedSessionAfterIdle(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "first answer"}, synthetic.Reply{Text: "second answer"})
	msgs := []ir.Message{userText("first question")}
	res, ex := r.execute(t, msgs, nil)
	if provenancePath(ex) != pathNew {
		t.Fatalf("a new conversation is served by %q", provenancePath(ex))
	}
	p := r.onlyProc(t)
	sid := p.sid
	e := r.endAndShelve(t, p)
	st, err := os.Stat(e.path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("shelf file %v %v", st, err)
	}
	if dst, _ := os.Stat(filepath.Dir(e.path)); dst.Mode().Perm() != 0o700 {
		t.Fatalf("shelf dir mode %v", dst.Mode())
	}
	if !e.expires.After(time.Now().Add(55*time.Minute)) || len(e.pending) != 0 {
		t.Fatalf("entry %+v (client set no breakpoints: 1h)", e)
	}

	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts}, userText("second question"))
	res, ex = r.execute(t, msgs, nil)
	if res.Parts[0].Text != "second answer" || provenancePath(ex) != pathShelved {
		t.Fatalf("served by %q: %+v", provenancePath(ex), res)
	}
	np := r.onlyProc(t)
	if np.sid != sid || !slices.Contains(np.cmd.Args, "--resume") || np.cmd.Args[slices.Index(np.cmd.Args, "--resume")+1] != sid {
		t.Fatalf("not resumed from the shelved session %s: %v", sid, np.cmd.Args)
	}
	reqs := r.up.Snapshot()
	before, after := upstreamMessages(reqs[len(reqs)-2]), upstreamMessages(reqs[len(reqs)-1])
	// The earlier request's messages are the prefix, byte for byte.
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("prefix differs at %d:\n%s\n%s", i, before[i], after[i])
		}
	}
	tail := strings.Join(after[len(before):], "\n")
	if strings.Count(strings.Join(after, "\n"), "first question") != 1 || !strings.Contains(tail, "second question") {
		t.Fatalf("tail %s", tail)
	}
	r.b.mu.Lock()
	n := len(r.b.shelf)
	r.b.mu.Unlock()
	if _, err := os.Stat(e.path); n != 0 || err == nil {
		t.Fatal("a shelved session is used once")
	}
}

// A process killed while waiting for tool results is shelved; the request
// carrying the results resumes it and the results reach the model.
func TestBridgeResumesShelvedToolCall(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{ToolName: "mcp__client__get_weather", ToolArgs: `{"city":"Vilnius"}`}, synthetic.Reply{Text: "It is sunny."})
	msgs := []ir.Message{userText("weather?")}
	res, _ := r.execute(t, msgs, []ir.Tool{weatherTool})
	call := res.Parts[0].ToolCall
	e := r.endAndShelve(t, r.onlyProc(t))
	if !slices.Equal(e.pending, []string{call.ID}) {
		t.Fatalf("pending %v", e.pending)
	}
	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts},
		ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: call.ID, Content: []ir.Part{{Kind: ir.PartText, Text: "sunny, 21C"}}}}}})
	res, ex := r.execute(t, msgs, []ir.Tool{weatherTool})
	if res.Parts[0].Text != "It is sunny." || provenancePath(ex) != pathShelved {
		t.Fatalf("served by %q: %+v", provenancePath(ex), res)
	}
	reqs := r.up.Snapshot()
	before, after := upstreamMessages(reqs[0]), upstreamMessages(reqs[1])
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("prefix differs at %d", i)
		}
	}
	tail := strings.Join(after[len(before):], "\n")
	if !strings.Contains(tail, `"tool_use_id":"`+call.ID+`"`) || !strings.Contains(tail, "sunny, 21C") || strings.Contains(tail, "interrupted") {
		t.Fatalf("tool result not carried: %s", tail)
	}
	if !slices.ContainsFunc(ex.Losses, func(l ir.Loss) bool { return l.Feature == "tool_continuation" }) {
		t.Fatalf("losses %+v", ex.Losses)
	}
}

// A shelved session only continues a conversation with the same cache TTL.
func TestBridgeShelfMatchesCacheTTL(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "a1"}, synthetic.Reply{Text: "a2"})
	q1 := ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: "q1", CacheControl: &ir.CacheControl{Type: "ephemeral"}}}}
	res, _ := r.execute(t, []ir.Message{q1}, nil)
	e := r.endAndShelve(t, r.onlyProc(t))
	if !e.expires.Before(time.Now().Add(6 * time.Minute)) {
		t.Fatalf("a 5m conversation is kept up to 5m: %v", e.expires)
	}
	q2 := ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: "q2", CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"}}}}
	_, ex := r.execute(t, []ir.Message{q1, {Role: ir.RoleAssistant, Parts: res.Parts}, q2}, nil)
	if provenancePath(ex) != pathTranscript {
		t.Fatalf("a 5m session served a 1h conversation (%s)", provenancePath(ex))
	}
}

// shelfRig is a bridge without a CLI, on a fake clock, for the shelf's
// bookkeeping.
func shelfRig(t *testing.T, o Options) (*Bridge, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	o.Root, o.Now = t.TempDir(), func() time.Time { return now }
	b := New(o)
	t.Cleanup(b.Close)
	return b, &now
}

func shelveFile(t *testing.T, b *Bridge, account, key string, size int, ttl time.Duration) *shelfEntry {
	t.Helper()
	src := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(src, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &shelfEntry{key: key, accountID: account, sid: "sid-" + key, expires: b.o.Now().Add(ttl)}
	b.shelve(e, src)
	return e
}

func shelfKeys(b *Bridge) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ks []string
	for k := range b.shelf {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func TestShelfSweepsExpiredAndUnindexed(t *testing.T) {
	b, now := shelfRig(t, Options{})
	short := shelveFile(t, b, "acc", "short", 10, 5*time.Minute)
	long := shelveFile(t, b, "acc", "long", 10, time.Hour)
	if st, err := os.Stat(short.path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st, err)
	}
	stray := filepath.Join(filepath.Dir(long.path), "left-by-a-restart.jsonl")
	_ = os.WriteFile(stray, []byte("x"), 0o600)
	*now = now.Add(6 * time.Minute)
	b.sweepShelf(*now, nil)
	if got := shelfKeys(b); !slices.Equal(got, []string{"long"}) {
		t.Fatalf("after 6m: %v", got)
	}
	for _, f := range []string{short.path, stray} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s not removed", f)
		}
	}
	*now = now.Add(time.Hour)
	b.sweepShelf(*now, nil)
	if _, err := os.Stat(long.path); len(shelfKeys(b)) != 0 || err == nil {
		t.Fatal("expired entry kept")
	}
}

func TestShelfCapsEvictOldest(t *testing.T) {
	t.Run("per account", func(t *testing.T) {
		b, now := shelfRig(t, Options{ShelfMaxPerAccount: 2})
		first := shelveFile(t, b, "acc", "k1", 10, time.Hour)
		*now = now.Add(time.Second)
		shelveFile(t, b, "acc", "k2", 10, time.Hour)
		*now = now.Add(time.Second)
		shelveFile(t, b, "other", "o1", 10, time.Hour)
		*now = now.Add(time.Second)
		shelveFile(t, b, "acc", "k3", 10, time.Hour)
		if got := shelfKeys(b); !slices.Equal(got, []string{"k2", "k3", "o1"}) {
			t.Fatalf("%v", got)
		}
		if _, err := os.Stat(first.path); err == nil {
			t.Fatal("evicted file kept")
		}
	})
	t.Run("total bytes", func(t *testing.T) {
		b, now := shelfRig(t, Options{ShelfMaxBytes: 25})
		shelveFile(t, b, "a", "k1", 10, time.Hour)
		*now = now.Add(time.Second)
		shelveFile(t, b, "b", "k2", 10, time.Hour)
		*now = now.Add(time.Second)
		shelveFile(t, b, "c", "k3", 10, time.Hour)
		if got := shelfKeys(b); !slices.Equal(got, []string{"k2", "k3"}) {
			t.Fatalf("%v", got)
		}
	})
}

func TestShelfClearedOnCloseAndPrune(t *testing.T) {
	b, _ := shelfRig(t, Options{})
	gone := shelveFile(t, b, "gone", "g", 10, time.Hour)
	kept := shelveFile(t, b, "kept", "k", 10, time.Hour)
	b.PruneAccounts(map[string]bool{"kept": true})
	if got := shelfKeys(b); !slices.Equal(got, []string{"k"}) {
		t.Fatalf("after prune: %v", got)
	}
	if _, err := os.Stat(gone.path); err == nil {
		t.Fatal("pruned account's session kept")
	}
	b.Close()
	if _, err := os.Stat(kept.path); len(shelfKeys(b)) != 0 || err == nil {
		t.Fatal("shutdown keeps shelved sessions")
	}
	shelveFile(t, b, "kept", "late", 10, time.Hour)
	if len(shelfKeys(b)) != 0 {
		t.Fatal("shelved after shutdown")
	}
}
