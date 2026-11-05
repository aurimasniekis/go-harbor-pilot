package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The apiKeyHelper command quotes the token path so shell metacharacters
// in it cannot run commands.
func TestShellQuoteBlocksInjection(t *testing.T) {
	dir := t.TempDir()
	tests := []string{"tok;touch PWNED", "tok|touch PWNED", "tok&&touch PWNED", "tok$(touch PWNED)", "tok\ntouch PWNED", "tok'quote", "plain.token"}
	for _, name := range tests {
		t.Run(strings.ReplaceAll(name, "\n", "\\n"), func(t *testing.T) {
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("hpk_secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", "cat "+shellQuote(path))
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil || strings.TrimSpace(string(out)) != "hpk_secret" {
				t.Fatalf("helper output %q err %v", out, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
				t.Fatal("a command in the token path ran")
			}
		})
	}
}

// Writing through a symlinked config keeps the link and updates its target.
func TestAtomicWriteKeepsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(link, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: %v %v", st, err)
	}
	if b, _ := os.ReadFile(target); string(b) != `{"a":1}` {
		t.Fatalf("target not updated: %s", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o640 {
		t.Fatalf("target mode changed to %v", st.Mode().Perm())
	}
}
