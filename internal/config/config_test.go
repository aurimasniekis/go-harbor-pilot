package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hp.yaml")
	if err := os.WriteFile(p, []byte("server:\n  listen: 127.0.0.1:9000\n  data_dir: /from-file\nlog:\n  level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HP_DATA_DIR": "/from-env", "HP_LOG_LEVEL": "warn"}
	c, err := Load(p, func(k string) string { return env[k] }, Flags{DataDir: "/from-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Listen != "127.0.0.1:9000" || c.Server.DataDir != "/from-flag" || c.Log.Level != "warn" {
		t.Fatalf("unexpected precedence result: %+v", c)
	}
	if c.Server.PublicURL != "http://127.0.0.1:9000" {
		t.Fatalf("derived public url %q", c.Server.PublicURL)
	}
}

func TestDerivedPublicURL(t *testing.T) {
	tests := []struct {
		listen, want string
	}{
		{"127.0.0.1:9000", "http://127.0.0.1:9000"},
		{":8080", "http://localhost:8080"},
		{"0.0.0.0:8080", "http://localhost:8080"},
		{"[::]:8080", "http://localhost:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			env := map[string]string{"HP_LISTEN": tt.listen}
			c, err := Load("", func(k string) string { return env[k] }, Flags{})
			if err != nil {
				t.Fatal(err)
			}
			if c.Server.PublicURL != tt.want {
				t.Fatalf("public url %q, want %q", c.Server.PublicURL, tt.want)
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{name: "unknown key", yaml: "server:\n  listn: x\n", want: "listn"},
		{name: "public url path", yaml: "server:\n  public_url: https://h/x\n", want: "must not contain a path"},
		{name: "half tls", yaml: "server:\n  tls:\n    cert_file: a\n", want: "set together"},
		{name: "demo clock without demo", yaml: "demo:\n  clock: 2026-11-05T12:00:00Z\n", want: "requires demo"},
		{name: "bad level", yaml: "log:\n  level: loud\n", want: "log.level"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c.yaml")
			if err := os.WriteFile(p, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(p, func(string) string { return "" }, Flags{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v want %q", err, tt.want)
			}
		})
	}
}

func TestEmptyFileUsesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p, func(string) string { return "" }, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Listen != Defaults().Server.Listen {
		t.Fatalf("defaults not applied: %+v", c.Server)
	}
}
