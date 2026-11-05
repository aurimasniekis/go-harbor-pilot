package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupAndUninstallPreserveUserConfig(t *testing.T) {
	existing := map[string]string{
		Claude:   `{"env":{"FOO":"bar"},"theme":"dark"}`,
		OpenCode: `{"provider":{"local":{"name":"Local"}}}`,
		Pi:       `{"providers":{"other":{"baseUrl":"http://x"}}}`,
		Codex:    "model = \"o4\"\n\n[model_providers.other]\nname = \"Other\"\n",
	}
	for _, kind := range Kinds {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			o := Options{Kind: kind, BaseURL: "https://hp.example.test/", TokenFile: filepath.Join(dir, "token"), ConfigDir: dir, Model: "coding/default"}
			path, _ := Path(o)
			if err := os.WriteFile(path, []byte(existing[kind]), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Plan(o)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(c.Diff, "+ ") || !strings.Contains(c.After, "hp.example.test") {
				t.Fatalf("diff %s", c.Diff)
			}
			if strings.Contains(c.After, "hpk_") {
				t.Fatal("token copied into config")
			}
			backup, err := Apply(c)
			if err != nil || backup == "" {
				t.Fatalf("apply %v %q", err, backup)
			}
			if st, _ := GetStatus(o); !st.Managed || st.Modified {
				t.Fatalf("status %+v", st)
			}
			// A second preview is idempotent (no duplicated blocks).
			c2, err := Plan(o)
			if err != nil {
				t.Fatal(err)
			}
			if c2.After != c.After {
				t.Fatalf("setup not idempotent:\n%s\n---\n%s", c.After, c2.After)
			}
			// Conflict: the file changed after the preview.
			_ = os.WriteFile(path, []byte(c.After+"\n"), 0o600)
			if _, err := Apply(c2); err != ErrConflict {
				t.Fatalf("expected conflict, got %v", err)
			}
			_ = os.WriteFile(path, []byte(c.After), 0o600)
			u, err := PlanUninstall(o, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyUninstall(u); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if strings.Contains(string(got), "harbor") || strings.Contains(string(got), "hp.example.test") {
				t.Fatalf("leftovers after uninstall: %s", got)
			}
			for _, keep := range []string{"FOO", "local", "other", "o4"} {
				if strings.Contains(existing[kind], keep) && !strings.Contains(string(got), keep) {
					t.Fatalf("uninstall removed the user's %s: %s", keep, got)
				}
			}
		})
	}
}

func TestRestoreBackupAndFreshFile(t *testing.T) {
	dir := t.TempDir()
	o := Options{Kind: Claude, BaseURL: "http://127.0.0.1:8080", TokenEnv: "HP_TOKEN", ConfigDir: dir}
	c, err := Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Notes) == 0 || !strings.Contains(c.Notes[0], "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("notes %v", c.Notes)
	}
	if b, err := Apply(c); err != nil || b != "" {
		t.Fatalf("%v %q", err, b)
	}
	u, err := PlanUninstall(o, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyUninstall(u); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("restoring a fresh setup must remove the file it created")
	}
}

func TestInvalidExistingJSONIsRefused(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o600)
	if _, err := Plan(Options{Kind: Claude, BaseURL: "http://x", TokenEnv: "T", ConfigDir: dir}); err == nil {
		t.Fatal("invalid JSON must not be overwritten")
	}
}
