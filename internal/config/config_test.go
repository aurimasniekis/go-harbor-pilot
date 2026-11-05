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
		{name: "secret root contains data dir", yaml: "server:\n  data_dir: /var/lib/hp\nsecurity:\n  secret_roots: [/var/lib]\n", want: "secret_roots"},
		{name: "secret root contains master key", yaml: "security:\n  master_key_file: /etc/hp/master.key\n  secret_roots: [/etc/hp]\n", want: "secret_roots"},
		{name: "secret root is filesystem root", yaml: "security:\n  secret_roots: [/]\n", want: "secret_roots"},
		{name: "demo on a public address", yaml: "server:\n  listen: 0.0.0.0:8080\ndemo:\n  enabled: true\n", want: "loopback"},
		{name: "broad env prefix", yaml: "security:\n  secret_env_prefixes: [HP_]\n", want: "too broad"},
		{name: "empty-ish env prefix", yaml: "security:\n  secret_env_prefixes: [A]\n", want: "too broad"},
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

func TestDemoPublicListenNeedsOverride(t *testing.T) {
	tests := []struct {
		listen string
		allow  bool
		ok     bool
	}{
		{"127.0.0.1:8080", false, true},
		{"localhost:8080", false, true},
		{"[::1]:8080", false, true},
		{":8080", false, false},
		{"0.0.0.0:8080", false, false},
		{"0.0.0.0:8080", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			_, err := Load("", func(string) string { return "" }, Flags{Listen: tt.listen, Demo: true, DemoAllowPublic: tt.allow})
			if (err == nil) != tt.ok {
				t.Fatalf("err=%v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestSecretEnvPrefixesFromEnv(t *testing.T) {
	env := map[string]string{"HP_SECRET_ENV_PREFIXES": "HP_SECRET_, TEAM_SECRET_"}
	c, err := Load("", func(k string) string { return env[k] }, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Security.SecretEnvPrefixes, ",") != "HP_SECRET_,TEAM_SECRET_" {
		t.Fatalf("prefixes %v", c.Security.SecretEnvPrefixes)
	}
	if got := c.ProtectedPaths(); len(got) == 0 || got[0] != filepath.Clean(c.Server.DataDir) {
		t.Fatalf("protected paths %v", got)
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
