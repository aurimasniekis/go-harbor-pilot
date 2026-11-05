package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "secrets")
	outside := filepath.Join(dir, "outside")
	writeFile(t, filepath.Join(root, "key"), "file-secret-value\n")
	writeFile(t, filepath.Join(root, "creds.json"), `{"claudeAiOauth":{"accessToken":"json-token","expiresAt":1700000000000}}`)
	writeFile(t, filepath.Join(root, "accounts.env"), "OPENAI_WORK=env-file-value\nOTHER='x'\n")
	writeFile(t, filepath.Join(outside, "stolen"), "must-not-read")
	if err := os.Symlink(filepath.Join(outside, "stolen"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "master.key"), "kek-material")
	r := &Resolver{Roots: []string{root}, LookupEnv: func(k string) (string, bool) {
		switch k {
		case "HP_SECRET_NEXOS_KEY":
			return "env-value", true
		case "HP_MASTER_KEY", "HOME", "AWS_SECRET_ACCESS_KEY":
			return "process-only-value", true
		}
		return "", false
	}, Policy: &Policy{EnvPrefixes: DefaultEnvPrefixes, Protected: []string{filepath.Join(root, "master.key")}}}
	tests := []struct {
		name    string
		ref     Ref
		want    string
		wantErr error
	}{
		{name: "env", ref: Ref{Type: "env", Name: "HP_SECRET_NEXOS_KEY"}, want: "env-value"},
		{name: "env missing", ref: Ref{Type: "env", Name: "HP_SECRET_MISSING"}, wantErr: ErrNotFound},
		{name: "env optional missing", ref: Ref{Type: "env", Name: "HP_SECRET_MISSING", Optional: true}, want: ""},
		{name: "env master key denied", ref: Ref{Type: "env", Name: "HP_MASTER_KEY"}, wantErr: ErrEnvNotAllowed},
		{name: "env outside prefixes denied", ref: Ref{Type: "env", Name: "HOME"}, wantErr: ErrEnvNotAllowed},
		{name: "cloud credential denied", ref: Ref{Type: "env", Name: "AWS_SECRET_ACCESS_KEY"}, wantErr: ErrEnvNotAllowed},
		{name: "bare prefix denied", ref: Ref{Type: "env", Name: "HP_SECRET_"}, wantErr: ErrEnvNotAllowed},
		{name: "master key file denied", ref: Ref{Type: "file", Path: filepath.Join(root, "master.key")}, wantErr: ErrProtectedPath},
		{name: "file trims newline", ref: Ref{Type: "file", Path: filepath.Join(root, "key")}, want: "file-secret-value"},
		{name: "file json key", ref: Ref{Type: "file", Path: filepath.Join(root, "creds.json"), Key: "claudeAiOauth.accessToken"}, want: "json-token"},
		{name: "env_file", ref: Ref{Type: "env_file", Path: filepath.Join(root, "accounts.env"), Key: "OPENAI_WORK"}, want: "env-file-value"},
		{name: "outside roots", ref: Ref{Type: "file", Path: filepath.Join(outside, "stolen")}, wantErr: ErrOutsideRoots},
		{name: "dotdot traversal", ref: Ref{Type: "file", Path: root + "/../outside/stolen"}, wantErr: ErrOutsideRoots},
		{name: "symlink escape", ref: Ref{Type: "file", Path: filepath.Join(root, "link")}, wantErr: ErrOutsideRoots},
		{name: "directory symlink escape", ref: Ref{Type: "file", Path: filepath.Join(root, "dirlink", "stolen")}, wantErr: ErrOutsideRoots},
		{name: "relative path", ref: Ref{Type: "file", Path: "secrets/key"}, wantErr: errAny},
		{name: "unknown type", ref: Ref{Type: "literal"}, wantErr: errAny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Resolve(tt.ref)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error, got value")
				}
				if tt.wantErr != errAny && !errors.Is(err, tt.wantErr) {
					t.Fatalf("got error %v, want %v", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "must-not-read") || strings.Contains(err.Error(), "process-only-value") || strings.Contains(err.Error(), "kek-material") {
					t.Fatal("error leaked file contents")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Reveal() != tt.want {
				t.Fatalf("got %q want %q", got.Reveal(), tt.want)
			}
		})
	}
}

var errAny = errors.New("any")

func TestParseDotenv(t *testing.T) {
	in := strings.Join([]string{
		"# comment",
		"",
		"PLAIN=value # trailing comment",
		"export EXPORTED=exp",
		"SINGLE='literal $HOME $(rm -rf /)'",
		`DOUBLE="line1\nline2 \"q\""`,
		"DOLLAR=$(whoami)",
		"BACKTICK=`id`",
		"EMPTY=",
	}, "\n")
	got, err := ParseDotenv([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN":    "value",
		"EXPORTED": "exp",
		"SINGLE":   "literal $HOME $(rm -rf /)",
		"DOUBLE":   "line1\nline2 \"q\"",
		"DOLLAR":   "$(whoami)",
		"BACKTICK": "`id`",
		"EMPTY":    "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q want %q", k, got[k], v)
		}
	}
}

func TestParseDotenvErrorsDoNotLeakValues(t *testing.T) {
	tests := []string{
		"KEY='unterminated secret-abc",
		"KEY=\"unterminated secret-abc",
		"not a pair secret-abc",
		"A=1\nA=secret-abc",
		"1BAD=secret-abc",
	}
	for i, in := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, err := ParseDotenv([]byte(in))
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "secret-abc") {
				t.Fatalf("error leaked value: %v", err)
			}
		})
	}
}

func TestSecretNeverSerializes(t *testing.T) {
	s := NewSecret("top-secret-value")
	b := NewBundle(map[string]Secret{"api_key": s})
	out, _ := json.Marshal(map[string]any{"s": s, "b": b})
	for _, rendered := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v", s, s, s), string(out), fmt.Sprint(b)} {
		if strings.Contains(rendered, "top-secret-value") {
			t.Fatalf("secret leaked: %s", rendered)
		}
	}
	if got, _ := b.Get("api_key"); got.Reveal() != "top-secret-value" {
		t.Fatal("bundle lookup failed")
	}
}

func TestCheckRef(t *testing.T) {
	SetPolicy(Policy{EnvPrefixes: []string{"HP_SECRET_", "TEAM_"}, Protected: []string{"/var/lib/harbor-pilot", "/etc/harbor-pilot/master.key"}})
	t.Cleanup(func() { SetPolicy(Policy{}) })
	tests := []struct {
		ref string
		ok  bool
	}{
		{"env:HP_SECRET_OPENAI", true},
		{"env:TEAM_KEY", true},
		{"env:HP_MASTER_KEY", false},
		{"env:HP_MASTER_KEY_FILE", false},
		{"env:HOME", false},
		{"env:", false},
		{"file:/etc/harbor-pilot/secrets/openai", true},
		{"file:/etc/harbor-pilot/master.key", false},
		{"file:/var/lib/harbor-pilot/harbor.db", false},
		{"file:/var/lib/harbor-pilot/../harbor-pilot/harbor.db", false},
		{"file:relative/path", false},
		{"literal:x", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if err := CheckRef(tt.ref); (err == nil) != tt.ok {
				t.Fatalf("CheckRef(%q) = %v, want ok=%v", tt.ref, err, tt.ok)
			}
		})
	}
}
