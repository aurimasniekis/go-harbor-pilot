package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/bridge"
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

// A synthetic session file in the shape harness-wire-facts §1.5 records: a
// user turn and an assistant turn ending in a pending tool call.
func syntheticSession(t *testing.T, version string) string {
	t.Helper()
	lines := []string{
		`{"type":"queue-operation","operation":"enqueue","sessionId":"s1"}`,
		`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"weather?"}]},"uuid":"u1","userType":"external","entrypoint":"sdk-cli","cwd":"/w/p1","sessionId":"s1","version":"` + version + `"}`,
		`{"parentUuid":"u1","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__client__get_weather","input":{"city":"Vilnius"}}]},"uuid":"a1","userType":"external","entrypoint":"sdk-cli","cwd":"/w/p1","sessionId":"s1","version":"` + version + `"}`,
		`{"type":"tools-used","tools":["mcp__client__get_weather"],"sessionId":"s1"}`,
	}
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const toolResultContent = `[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"sunny, 21C"}]}]`

func TestAppendToolResults(t *testing.T) {
	path := syntheticSession(t, "2.1.321")
	if !sessionHolds(path, []string{"toolu_1"}) || sessionHolds(path, []string{"toolu_1", "toolu_2"}) || sessionHolds(path+".missing", nil) {
		t.Fatal("sessionHolds")
	}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	if err := appendToolResults(path, json.RawMessage(toolResultContent), now); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lines", len(lines))
	}
	var e struct {
		ParentUUID string `json:"parentUuid"`
		Type       string `json:"type"`
		UUID       string `json:"uuid"`
		Source     string `json:"sourceToolAssistantUUID"`
		SessionID  string `json:"sessionId"`
		Version    string `json:"version"`
		Cwd        string `json:"cwd"`
		Timestamp  string `json:"timestamp"`
		Message    struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"message"`
		Result []map[string]any `json:"toolUseResult"`
	}
	if err := json.Unmarshal([]byte(lines[4]), &e); err != nil {
		t.Fatal(err)
	}
	// Chained to the last entry with a uuid (the assistant's tool call), not
	// to the bookkeeping line after it.
	if e.ParentUUID != "a1" || e.Source != "a1" || e.Type != "user" || e.UUID == "" || e.SessionID != "s1" || e.Version != "2.1.321" ||
		e.Cwd != "/w/p1" || e.Timestamp != "2026-11-09T12:00:00.000Z" || e.Message.Role != "user" ||
		len(e.Message.Content) != 1 || e.Message.Content[0]["tool_use_id"] != "toolu_1" || len(e.Result) != 1 {
		t.Fatalf("entry %+v", e)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

// The appended entry shape was verified for specific CLI versions only; a
// session written by any other version is left untouched.
func TestAppendToolResultsRefusesUnverifiedCLI(t *testing.T) {
	for _, v := range []string{"9.9.9", ""} {
		path := syntheticSession(t, v)
		before, _ := os.ReadFile(path)
		err := appendToolResults(path, json.RawMessage(toolResultContent), time.Now())
		var uv *unverifiedCLIError
		if !errors.As(err, &uv) || uv.version != v {
			t.Fatalf("version %q: %v", v, err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatal("session changed")
		}
	}
	// Logged once per version.
	var logs strings.Builder
	b := &Bridge{o: Options{Log: slog.New(slog.NewTextHandler(&logs, nil))}}
	b.warnUnverified("9.9.9")
	b.warnUnverified("9.9.9")
	b.warnUnverified("9.9.8")
	if n := strings.Count(logs.String(), "bridge.shelf.unverified_cli_version"); n != 2 {
		t.Fatalf("%d warnings: %s", n, logs.String())
	}
}

// A shelved entry put back after a failed spawn (no free slot) does not get
// the tool results a second time.
func TestAddResultsOnce(t *testing.T) {
	b, _ := shelfRig(t, Options{})
	e := &shelfEntry{path: syntheticSession(t, "2.1.321"), pending: []string{"toolu_1"}}
	for range 2 {
		if err := b.addResults(e, json.RawMessage(toolResultContent)); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(e.path)
	if n := strings.Count(string(raw), `"sourceToolAssistantUUID"`); n != 1 {
		t.Fatalf("results appended %d times", n)
	}
}

// A conversation is shelved only while its cache can still be warm: the
// entry expires the TTL after the conversation was last used.
func TestShelfCandidateExpiresFromLastUse(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	b := &Bridge{o: Options{}}
	mk := func(ttl string, idle time.Duration, st procState) *proc {
		return &proc{b: b, accountID: "acc", sid: "s", cacheTTL: ttl, fp: "fp", state: st, lastUsed: now.Add(-idle)}
	}
	for _, tc := range []struct {
		name   string
		p      *proc
		reason string
		want   time.Duration // expiry after now; 0: not shelved
	}{
		{"5m conversation idle 2m", mk("5m", 2*time.Minute, stateIdle), "idle timeout", 3 * time.Minute},
		{"5m conversation idle 10m", mk("5m", 10*time.Minute, stateIdle), "idle timeout", 0},
		{"1h conversation idle 10m", mk("1h", 10*time.Minute, stateIdle), "idle timeout", 50 * time.Minute},
		{"1h conversation waiting 70m for tools", mk("1h", 70*time.Minute, stateAwaitingTools), "idle timeout", 0},
		{"busy", mk("1h", 0, stateBusy), "idle timeout", 0},
		{"client went away", mk("1h", 0, stateIdle), "client went away", 0},
	} {
		e := tc.p.shelfCandidate(tc.reason, now)
		switch {
		case tc.want == 0 && e != nil:
			t.Errorf("%s: shelved until %v", tc.name, e.expires)
		case tc.want != 0 && (e == nil || !e.expires.Equal(now.Add(tc.want))):
			t.Errorf("%s: %+v, want expiry now+%v", tc.name, e, tc.want)
		}
	}
}

// The CLI's per-process files (config/sessions/<pid>.*) are swept once no
// live process owns them.
func TestSweepRemovesLeftoverCLIProcessFiles(t *testing.T) {
	b, now := shelfRig(t, Options{})
	dir := filepath.Join(b.o.Root, "accounts", "acc", "config", "sessions")
	_ = os.MkdirAll(dir, 0o700)
	old, fresh := []string{"4242.json", "4242.abcd.key"}, "4343.json"
	for _, f := range append(old, fresh) {
		_ = os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600)
	}
	past := now.Add(-2 * time.Minute)
	for _, f := range old {
		_ = os.Chtimes(filepath.Join(dir, f), past, past)
	}
	_ = os.Chtimes(filepath.Join(dir, fresh), *now, *now)
	b.sweepConversationFiles(*now)
	for _, f := range old {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s kept", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, fresh)); err != nil {
		t.Error("a just-written file was removed")
	}
}

// fakeProc is a parked process without the CLI: a sleeping child in its
// own process group, with a session file in a session directory last
// touched long ago (the CLI appends in place, so the directory keeps its
// spawn time).
func fakeProc(t *testing.T, b *Bridge, age time.Duration) *proc {
	t.Helper()
	root := b.o.Root
	work := filepath.Join(root, "w", "p1")
	sessDir := filepath.Join(root, "accounts", "acc", "config", "projects", unsafePath.ReplaceAllString(work, "-"))
	runDir := filepath.Join(root, "r", "run1")
	for _, d := range []string{work, sessDir, runDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sid := newUUID()
	if err := os.WriteFile(filepath.Join(sessDir, sid+".jsonl"), []byte(`{"uuid":"u1","type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := b.o.Now().Add(-age)
	for _, d := range []string{work, sessDir, runDir} {
		_ = os.Chtimes(d, old, old)
	}
	cmd := exec.Command("sleep", "60")
	bridge.Isolate(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sess, err := b.mcp.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &proc{b: b, id: "p1", accountID: "acc", sid: sid, cacheTTL: "1h", fp: "fp", state: stateIdle, lastUsed: b.o.Now(),
		cmd: cmd, stdin: stdin, sess: sess, workDir: work, runDir: runDir, sessDir: sessDir, lines: make(chan line, 1)}
	b.mu.Lock()
	b.procs[p.id] = p
	b.mu.Unlock()
	return p
}

// The reaper ends a process and sweeps right after: the sweep must not
// delete the session of a process whose cleanup (and shelving) is still
// running, even when its directories are older than the sweep's grace.
func TestReaperSweepKeepsSessionBeingShelved(t *testing.T) {
	for range 3 {
		b, now := shelfRig(t, Options{})
		p := fakeProc(t, b, 2*time.Hour)
		p.kill("idle timeout")
		b.sweepConversationFiles(*now)
		deadline := time.Now().Add(5 * time.Second)
		for len(shelfKeys(b)) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if len(shelfKeys(b)) != 1 {
			t.Fatal("the sweep removed the session before it was shelved")
		}
		b.Close()
	}
}

// Close waits for processes still in their cleanup and leaves no process
// group running.
func TestCloseWaitsForCleanup(t *testing.T) {
	b, _ := shelfRig(t, Options{})
	p := fakeProc(t, b, 0)
	p.mu.Lock()
	p.state, p.pending = stateAwaitingTools, []string{"toolu_never_recorded"}
	p.mu.Unlock()
	p.kill("idle timeout") // waits up to 2 s for the call to be recorded
	b.Close()
	b.mu.Lock()
	n := len(b.reaping)
	b.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d processes still in cleanup after Close", n)
	}
	if err := p.cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("the process outlived Close")
	}
}

// With the CLI: a long-running conversation (session directory older than
// the sweep's grace) ended by the reaper, with the sweep right after it, is
// still resumed from the shelf.
func TestBridgeShelvesOldSessionDespiteSweep(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "first answer"}, synthetic.Reply{Text: "second answer"})
	msgs := []ir.Message{userText("first question")}
	res, _ := r.execute(t, msgs, nil)
	p := r.onlyProc(t)
	old := time.Now().Add(-2 * time.Hour)
	for _, d := range []string{p.sessDir, p.workDir, p.runDir} {
		_ = os.Chtimes(d, old, old)
	}
	p.kill("idle timeout")
	r.b.sweepConversationFiles(time.Now())
	deadline := time.Now().Add(10 * time.Second)
	for len(shelfKeys(r.b)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts}, userText("second question"))
	res, ex := r.execute(t, msgs, nil)
	if res.Parts[0].Text != "second answer" || provenancePath(ex) != pathShelved {
		t.Fatalf("served by %q", provenancePath(ex))
	}
}
