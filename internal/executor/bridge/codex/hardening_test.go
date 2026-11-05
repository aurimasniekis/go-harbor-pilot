package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

var lookupTool = []ir.Tool{{Name: "lookup", Description: "Look something up", Parameters: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)}}

// The system prompt and the MCP token never appear in arguments; they reach
// the CLI through a 0600 profile and the environment, and the profile is
// removed when the run ends.
func TestCodexArgumentsHoldNoSecrets(t *testing.T) {
	r := newRig(t)
	if _, err := r.exec(t, []ir.Message{text("please use tool")}, lookupTool); err != nil {
		t.Fatal(err)
	}
	c := r.calls(t)[0]
	args := strings.Join(toStrings(c["args"]), " ")
	if strings.Contains(args, "Be brief.") || strings.Contains(args, "developer_instructions") || strings.Contains(args, "mcp_servers") {
		t.Fatalf("argv carries the system prompt or MCP config: %s", args)
	}
	prof := c["profile"].(string)
	for _, want := range []string{`developer_instructions = "Be brief."`, `history.persistence = "none"`, `bearer_token_env_var = "HP_MCP_TOKEN"`} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile lacks %q:\n%s", want, prof)
		}
	}
	if c["mcp_token_set"] != true {
		t.Error("the MCP token was not passed in the environment")
	}
	r.b.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		left, _ := filepath.Glob(filepath.Join(r.b.o.Root, "accounts", "*", "codex_home", "hp-*.config.toml"))
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("profiles left after the runs ended: %v", left)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A parked process waiting for tool results is resumed only by the same
// gateway client; another client sending the same transcript gets a fresh
// process.
func TestCodexParkedProcessesAreScoped(t *testing.T) {
	r := newRig(t)
	r.b.o.MaxPerAccount = 2 // client A's parked process keeps its slot
	msgs := []ir.Message{text("please use tool")}
	res, err := r.execScoped(t, "client-a", msgs, lookupTool)
	if err != nil || res.StopReason != ir.StopToolUse {
		t.Fatalf("%+v %v", res, err)
	}
	call := res.Parts[0].ToolCall
	msgs = append(msgs, ir.Message{Role: ir.RoleAssistant, Parts: res.Parts},
		ir.Message{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: call.ID, Content: []ir.Part{{Kind: ir.PartText, Text: "FORGED"}}}}}})
	res, err = r.execScoped(t, "client-b", msgs, lookupTool)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Parts[0].Text, "FORGED") {
		t.Fatalf("client B's tool result reached client A's process: %+v", res)
	}
	if n := len(r.calls(t)); n != 2 {
		t.Fatalf("another client's results continued the parked process (processes started: %d)", n)
	}
}

// The sign-in file is reseeded when the stored credential changes, and kept
// (with the CLI's own refreshes) while it does not.
func TestSeedAuthFollowsCredentialChanges(t *testing.T) {
	home := t.TempDir()
	auth := filepath.Join(home, "auth.json")
	read := func() string { b, _ := os.ReadFile(auth); return string(b) }
	if err := seedAuth(home, credentials.NewSecret(`{"v":1}`)); err != nil || read() != `{"v":1}` {
		t.Fatalf("first seed: %q %v", read(), err)
	}
	_ = os.WriteFile(auth, []byte(`{"v":1,"refreshed":true}`), 0o600) // the CLI refreshed its token
	if err := seedAuth(home, credentials.NewSecret(`{"v":1}`)); err != nil || !strings.Contains(read(), "refreshed") {
		t.Fatalf("unchanged credential overwrote the refreshed file: %q %v", read(), err)
	}
	if err := seedAuth(home, credentials.NewSecret(`{"v":2}`)); err != nil || read() != `{"v":2}` {
		t.Fatalf("rotated credential not applied: %q %v", read(), err)
	}
	if st, _ := os.Stat(auth); st.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode %v", st.Mode().Perm())
	}
}

// Deleted accounts lose their private directories; resumable rollouts and
// prompt history are removed once older than the thread TTL.
func TestCodexPrunesAccountsAndSweepsSessions(t *testing.T) {
	root := t.TempDir()
	b := &Bridge{o: Options{Root: root, ThreadTTL: time.Hour, ToolTimeout: time.Minute}, running: map[string]*run{}}
	for _, d := range []string{"acc_keep/codex_home/sessions/2026/11/05", "acc_gone/codex_home"} {
		if err := os.MkdirAll(filepath.Join(root, "accounts", d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldRollout := filepath.Join(root, "accounts", "acc_keep/codex_home/sessions/2026/11/05/rollout-old.jsonl")
	newRollout := filepath.Join(root, "accounts", "acc_keep/codex_home/sessions/2026/11/05/rollout-new.jsonl")
	history := filepath.Join(root, "accounts", "acc_keep/codex_home/history.jsonl")
	for _, f := range []string{oldRollout, newRollout, history} {
		_ = os.WriteFile(f, []byte("{}"), 0o600)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(oldRollout, past, past)
	_ = os.Chtimes(history, past, past)
	b.PruneAccounts(map[string]bool{"acc_keep": true})
	b.sweepSessions(time.Now())
	for path, want := range map[string]bool{filepath.Join(root, "accounts", "acc_gone"): false, oldRollout: false, history: false, newRollout: true} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", path, err == nil, want)
		}
	}
}
