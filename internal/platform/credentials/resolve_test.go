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
	r := &Resolver{Roots: []string{root}, LookupEnv: func(k string) (string, bool) {
		if k == "NEXOS_KEY" {
			return "env-value", true
		}
		return "", false
	}}
	tests := []struct {
		name    string
		ref     Ref
		want    string
		wantErr error
	}{
		{name: "env", ref: Ref{Type: "env", Name: "NEXOS_KEY"}, want: "env-value"},
		{name: "env missing", ref: Ref{Type: "env", Name: "MISSING"}, wantErr: ErrNotFound},
		{name: "env optional missing", ref: Ref{Type: "env", Name: "MISSING", Optional: true}, want: ""},
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
				if strings.Contains(err.Error(), "must-not-read") {
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
