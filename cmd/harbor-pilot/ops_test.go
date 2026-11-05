package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSecretSources(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "tok")
	_ = os.WriteFile(f, []byte("from-file\n"), 0o600)
	t.Setenv("HP_TEST_SECRET", " from-env ")
	tests := []struct {
		name  string
		file  string
		stdin bool
		env   string
		want  string
	}{
		{"file", f, false, "HP_TEST_SECRET", "from-file"},
		{"stdin", "", true, "HP_TEST_SECRET", "from-stdin"},
		{"env", "", false, "HP_TEST_SECRET", "from-env"},
		{"none", "", false, "", ""},
	}
	for _, tt := range tests {
		got, err := readSecret(tt.file, tt.stdin, tt.env, strings.NewReader("from-stdin\nignored\n"))
		if err != nil || got != tt.want {
			t.Errorf("%s: %q %v", tt.name, got, err)
		}
	}
}

func TestHarnessCommandPreviewWritesNothing(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"harness", "setup", "opencode", "--url", "http://127.0.0.1:8080", "--token-env", "HP_TOKEN", "--config-dir", dir}, &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing written") || !strings.Contains(out.String(), "{env:HP_TOKEN}") {
		t.Fatalf("%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("setup without --yes wrote a file")
	}
	out.Reset()
	if err := run([]string{"harness", "setup", "opencode", "--url", "http://127.0.0.1:8080", "--token-env", "HP_TOKEN", "--config-dir", dir, "--yes"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode.json")); err != nil {
		t.Fatal("setup --yes did not write")
	}
}

// The stored token file is owner-only even when a wider file already
// exists, symlinks are refused, and unknown harness kinds are rejected
// before they are used in a path.
func TestHarnessTokenFileHandling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens", "claude.token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTokenFile(path, "hpk_new"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v", st.Mode().Perm())
	}
	link := filepath.Join(dir, "tokens", "codex.token")
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), link); err != nil {
		t.Fatal(err)
	}
	if err := writeTokenFile(link, "hpk_new"); err == nil {
		t.Fatal("wrote the token through a symlink")
	}
	var out bytes.Buffer
	err := run([]string{"harness", "setup", "../../../tmp/evil", "--url", "https://pilot.example.test", "--token-stdin", "--yes"},
		&out, &out)
	if err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Fatalf("path-like harness kind accepted: %v", err)
	}
}
