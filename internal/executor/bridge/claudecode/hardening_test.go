package claudecode

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/bridge"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// The CLI runs with its built-in tools off, and neither the MCP session
// token nor the system prompt appears in its arguments (readable by any
// local user through ps).
func TestBridgeArgumentsHoldNoSecrets(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{ToolName: "mcp__client__get_weather", ToolArgs: `{"city":"Riga"}`, InputTok: 3, OutputTok: 1})
	res := r.run(t, []ir.Message{userText("weather?")}, []ir.Tool{weatherTool})
	if res.StopReason != ir.StopToolUse {
		t.Fatalf("%+v", res)
	}
	r.b.mu.Lock()
	var p *proc
	for _, x := range r.b.procs {
		p = x
	}
	r.b.mu.Unlock()
	if p == nil {
		t.Fatal("no parked process")
	}
	args := strings.Join(p.cmd.Args, "\x00")
	for _, want := range []string{"--tools\x00\x00", "--strict-mcp-config", "--allowedTools\x00mcp__client", "--setting-sources\x00\x00", "--system-prompt-file"} {
		if !strings.Contains(args, want) {
			t.Errorf("argv lacks %q: %q", want, p.cmd.Args)
		}
	}
	for _, secret := range []string{p.sess.Token(), "You are a test assistant."} {
		if strings.Contains(args, secret) {
			t.Errorf("argv contains %q", secret)
		}
	}
	for _, f := range []string{"mcp.json", "system.txt"} {
		st, err := os.Stat(filepath.Join(p.runDir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
}

// Conversation files are removed when the process ends, including the
// CLI's per-process files (config/sessions/<pid>.*, which hold a peer
// token, the session id and the working directory).
func TestBridgeRemovesTranscriptsAndRunFiles(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "ok", InputTok: 2, OutputTok: 1})
	msgs := []ir.Message{userText("q1"), {Role: ir.RoleAssistant, Parts: []ir.Part{{Kind: ir.PartText, Text: "a1"}}}, userText("q2")}
	r.run(t, msgs, nil)
	r.b.Close()
	var left []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		left = left[:0]
		_ = filepath.WalkDir(r.b.o.Root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".jsonl") || filepath.Base(path) == "system.txt" || filepath.Base(path) == "mcp.json" ||
				strings.Contains(path, string(filepath.Separator)+"sessions"+string(filepath.Separator))) {
				left = append(left, path)
			}
			return nil
		})
		if len(left) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("conversation files left after the process ended: %v", left)
}

// Files a crash left behind are swept once they are older than the idle
// period; files of live processes stay.
func TestSweepRemovesOrphanedConversationFiles(t *testing.T) {
	root := t.TempDir()
	b := &Bridge{o: Options{Root: root, IdleTimeout: time.Minute, ToolTimeout: time.Minute}, procs: map[string]*proc{}}
	old := filepath.Join(root, "accounts", "acc", "config", "projects", "-w-p1")
	fresh := filepath.Join(root, "accounts", "acc", "config", "projects", "-w-p2")
	cache := filepath.Join(root, "accounts", "acc", "home", ".cache", "claude-cli-nodejs", "-w-p1")
	for _, d := range []string{old, fresh, cache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(d, "s.jsonl"), []byte("{}"), 0o600)
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, past, past)
	_ = os.Chtimes(cache, past, past)
	b.sweepConversationFiles(time.Now())
	for d, want := range map[string]bool{old: false, cache: false, fresh: true} {
		if _, err := os.Stat(d); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", d, err == nil, want)
		}
	}
}

// Concurrent spawns cannot exceed the per-account limit: a slot is held
// from reservation until the spawn finishes.
func TestReserveSlotHoldsCapacity(t *testing.T) {
	b := &Bridge{o: Options{MaxProcesses: 4, MaxPerAccount: 1}, procs: map[string]*proc{}, reserved: map[string]int{}, mcp: bridge.NewMCPServer()}
	release, err := b.reserveSlot("acc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.reserveSlot("acc"); !errors.Is(err, errBusy) {
		t.Fatalf("second concurrent spawn allowed: %v", err)
	}
	if rel, err := b.reserveSlot("other"); err != nil {
		t.Fatalf("another account blocked: %v", err)
	} else {
		rel()
	}
	release()
	if rel, err := b.reserveSlot("acc"); err != nil {
		t.Fatalf("slot not released: %v", err)
	} else {
		rel()
	}
}
