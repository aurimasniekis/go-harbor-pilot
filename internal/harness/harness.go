// Package harness configures coding harnesses (Claude Code, Codex,
// OpenCode, Pi) to use Harbor Pilot, reversibly.
//
// Every change is previewed as a diff first. Applying it backs up the
// original file, writes atomically (temporary file and rename), refuses to
// overwrite a file that changed since the preview, and records what it
// added so uninstall removes exactly those keys. Gateway tokens are kept
// out of harness config files wherever the harness can reference a file or
// an environment variable instead.
package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Kinds of harness.
const (
	Claude   = "claude"
	Codex    = "codex"
	OpenCode = "opencode"
	Pi       = "pi"
)

// Kinds lists supported harnesses.
var Kinds = []string{Claude, Codex, OpenCode, Pi}

// Options describe the gateway connection to configure.
type Options struct {
	Kind string
	// BaseURL is the Harbor Pilot public URL (no trailing slash).
	BaseURL string
	// TokenFile is where the gateway token lives (0600). Harnesses that can
	// read a file reference it; the token is never copied into config.
	TokenFile string
	// TokenEnv is the environment variable holding the token, for
	// harnesses that only support environment references.
	TokenEnv string
	// Model is the model or alias to select (optional).
	Model string
	// ConfigDir overrides the harness's config directory (tests).
	ConfigDir string
	// Home is the user's home directory (default os.UserHomeDir).
	Home string
}

// Change is a planned edit to one file.
type Change struct {
	Path string `json:"path"`
	// BaseHash is the hash of the file the plan was computed from ("" when
	// absent); Apply refuses to write if the file changed since.
	BaseHash string `json:"base_hash"`
	Before   string `json:"-"`
	After    string `json:"-"`
	Diff     string `json:"diff"`
	// Notes are instructions the user must follow (e.g. export a variable).
	Notes []string `json:"notes"`
	// Added lists the keys this change owns, for uninstall.
	Added []string `json:"added"`
}

// ErrConflict means the file changed between preview and apply.
var ErrConflict = errors.New("the file changed since the preview; run the preview again")

func hashOf(b []byte) string {
	if b == nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func read(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // user-selected harness config path
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

func (o *Options) home() string {
	if o.Home != "" {
		return o.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

func (o *Options) validate() error {
	if !strings.HasPrefix(o.BaseURL, "http://") && !strings.HasPrefix(o.BaseURL, "https://") {
		return errors.New("base URL must start with http:// or https://")
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.TokenFile == "" && o.TokenEnv == "" {
		return errors.New("a token file or token environment variable is required")
	}
	return nil
}

// Path returns the config file a harness setup edits.
func Path(o Options) (string, error) {
	switch o.Kind {
	case Claude:
		dir := o.ConfigDir
		if dir == "" {
			if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
				dir = d
			} else {
				dir = filepath.Join(o.home(), ".claude")
			}
		}
		return filepath.Join(dir, "settings.json"), nil
	case Codex:
		dir := o.ConfigDir
		if dir == "" {
			if d := os.Getenv("CODEX_HOME"); d != "" {
				dir = d
			} else {
				dir = filepath.Join(o.home(), ".codex")
			}
		}
		return filepath.Join(dir, "config.toml"), nil
	case OpenCode:
		dir := o.ConfigDir
		if dir == "" {
			dir = filepath.Join(o.home(), ".config", "opencode")
		}
		return filepath.Join(dir, "opencode.json"), nil
	case Pi:
		dir := o.ConfigDir
		if dir == "" {
			dir = filepath.Join(o.home(), ".pi", "agent")
		}
		return filepath.Join(dir, "models.json"), nil
	}
	return "", fmt.Errorf("unknown harness %q (supported: %s)", o.Kind, strings.Join(Kinds, ", "))
}

// Plan computes the change for a setup without writing anything.
func Plan(o Options) (*Change, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	path, err := Path(o)
	if err != nil {
		return nil, err
	}
	before, err := read(path)
	if err != nil {
		return nil, err
	}
	var after []byte
	var added, notes []string
	switch o.Kind {
	case Claude:
		after, added, notes, err = planClaude(before, o)
	case Codex:
		after, added, notes, err = planCodex(before, o)
	case OpenCode:
		after, added, notes, err = planOpenCode(before, o)
	case Pi:
		after, added, notes, err = planPi(before, o)
	}
	if err != nil {
		return nil, err
	}
	return &Change{Path: path, BaseHash: hashOf(before), Before: string(before), After: string(after), Diff: Diff(string(before), string(after)),
		Notes: notes, Added: added}, nil
}

// Apply writes a planned change: backup, conflict check, atomic replace,
// and a record of owned keys next to the file.
func Apply(c *Change) (backup string, err error) {
	cur, err := read(c.Path)
	if err != nil {
		return "", err
	}
	if hashOf(cur) != c.BaseHash {
		return "", ErrConflict
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return "", err
	}
	if cur != nil {
		backup = fmt.Sprintf("%s.hp-backup-%s", c.Path, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.WriteFile(backup, cur, 0o600); err != nil {
			return "", err
		}
	}
	if err := atomicWrite(c.Path, []byte(c.After)); err != nil {
		return backup, err
	}
	rec, _ := json.MarshalIndent(record{Added: c.Added, Backup: backup, Hash: hashOf([]byte(c.After)), At: time.Now().UTC()}, "", "  ")
	return backup, atomicWrite(c.Path+".hp-managed.json", rec)
}

type record struct {
	Added  []string  `json:"added"`
	Backup string    `json:"backup"`
	Hash   string    `json:"hash"`
	At     time.Time `json:"applied_at"`
}

func atomicWrite(path string, b []byte) error {
	// A symlinked config (dotfile managers) is written through to its
	// target, so the link itself is kept.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // already renamed on success
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Status reports whether Harbor Pilot setup is present for a harness.
type Status struct {
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Managed   bool      `json:"managed"`
	Modified  bool      `json:"modified_since_setup"`
	Backup    string    `json:"backup,omitempty"`
	AppliedAt time.Time `json:"applied_at,omitempty"`
}

// GetStatus inspects a harness's config.
func GetStatus(o Options) (Status, error) {
	path, err := Path(o)
	if err != nil {
		return Status{}, err
	}
	st := Status{Kind: o.Kind, Path: path}
	b, err := read(path + ".hp-managed.json")
	if err != nil || b == nil {
		return st, err
	}
	var r record
	if json.Unmarshal(b, &r) != nil {
		return st, nil //nolint:nilerr // an unreadable record means "not managed"
	}
	cur, _ := read(path)
	st.Managed, st.Backup, st.AppliedAt, st.Modified = true, r.Backup, r.At, hashOf(cur) != r.Hash
	return st, nil
}

// PlanUninstall computes removal of the keys setup added. With
// restoreBackup the original file from the backup is restored instead.
func PlanUninstall(o Options, restoreBackup bool) (*Change, error) {
	path, err := Path(o)
	if err != nil {
		return nil, err
	}
	b, err := read(path + ".hp-managed.json")
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, errors.New("no Harbor Pilot setup recorded for this harness")
	}
	var r record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	cur, err := read(path)
	if err != nil {
		return nil, err
	}
	var after []byte
	if restoreBackup {
		if r.Backup == "" {
			after = nil // there was no file before setup
		} else if after, err = read(r.Backup); err != nil || after == nil {
			return nil, errors.New("the backup file is missing")
		}
	} else {
		switch o.Kind {
		case Claude, OpenCode, Pi:
			after, err = removeJSON(cur, r.Added)
		case Codex:
			after = removeTOML(cur, r.Added)
		}
		if err != nil {
			return nil, err
		}
	}
	return &Change{Path: path, BaseHash: hashOf(cur), Before: string(cur), After: string(after), Diff: Diff(string(cur), string(after)), Added: r.Added}, nil
}

// ApplyUninstall writes an uninstall change and removes the record.
func ApplyUninstall(c *Change) error {
	cur, err := read(c.Path)
	if err != nil {
		return err
	}
	if hashOf(cur) != c.BaseHash {
		return ErrConflict
	}
	if c.After == "" && c.Before != "" && strings.TrimSpace(c.After) == "" {
		if err := os.Remove(c.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := atomicWrite(c.Path, []byte(c.After)); err != nil {
		return err
	}
	return os.Remove(c.Path + ".hp-managed.json")
}

// ---------- JSON helpers ----------

// jsonDoc decodes an object preserving nothing but values (key order is
// normalized on write, which the diff shows).
func jsonDoc(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("existing file is not valid JSON (fix or move it first): %w", err)
	}
	return m, nil
}

func encodeJSON(m map[string]any) []byte {
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\n')
}

func object(m map[string]any, key string) map[string]any {
	v, ok := m[key].(map[string]any)
	if !ok {
		v = map[string]any{}
		m[key] = v
	}
	return v
}

// removeJSON deletes dotted key paths (e.g. "env.ANTHROPIC_BASE_URL") and
// prunes objects left empty.
func removeJSON(b []byte, keys []string) ([]byte, error) {
	m, err := jsonDoc(b)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		parts := strings.Split(k, "\x1f")
		var walk func(cur map[string]any, i int) bool
		walk = func(cur map[string]any, i int) bool {
			if i == len(parts)-1 {
				delete(cur, parts[i])
				return len(cur) == 0
			}
			next, ok := cur[parts[i]].(map[string]any)
			if !ok {
				return false
			}
			if walk(next, i+1) {
				delete(cur, parts[i])
			}
			return len(cur) == 0
		}
		walk(m, 0)
	}
	if len(m) == 0 {
		return []byte{}, nil
	}
	return encodeJSON(m), nil
}

func key(parts ...string) string { return strings.Join(parts, "\x1f") }

// ---------- harness plans ----------

func planClaude(before []byte, o Options) ([]byte, []string, []string, error) {
	m, err := jsonDoc(before)
	if err != nil {
		return nil, nil, nil, err
	}
	env := object(m, "env")
	set := map[string]string{"ANTHROPIC_BASE_URL": o.BaseURL}
	var added, notes []string
	if o.Model != "" {
		set["ANTHROPIC_MODEL"] = o.Model
	}
	for k, v := range set {
		if cur, ok := env[k]; ok && cur != v {
			notes = append(notes, fmt.Sprintf("replaces your existing %s", k))
		}
		env[k] = v
		added = append(added, key("env", k))
	}
	if o.TokenFile != "" {
		// apiKeyHelper prints the token; it never lives in settings.json.
		m["apiKeyHelper"] = "cat " + shellQuote(o.TokenFile)
		added = append(added, key("apiKeyHelper"))
	} else {
		notes = append(notes, fmt.Sprintf("export ANTHROPIC_AUTH_TOKEN=\"$%s\" in your shell before starting claude", o.TokenEnv))
	}
	sort.Strings(added)
	return encodeJSON(m), added, notes, nil
}

// shellQuote quotes s for a POSIX shell: anything beyond a conservative
// set of path characters is single-quoted, so metacharacters (; | & < > ( )
// * ? newline ...) in a token path cannot run commands.
func shellQuote(s string) string {
	if s != "" && safeShellWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var safeShellWord = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// KnownKind reports whether kind is a supported harness.
func KnownKind(kind string) bool { return slices.Contains(Kinds, kind) }

func planOpenCode(before []byte, o Options) ([]byte, []string, []string, error) {
	m, err := jsonDoc(before)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, ok := m["$schema"]; !ok {
		m["$schema"] = "https://opencode.ai/config.json"
	}
	providers := object(m, "provider")
	apiKey := "{env:" + o.TokenEnv + "}"
	if o.TokenFile != "" {
		apiKey = "{file:" + o.TokenFile + "}"
	}
	model := o.Model
	if model == "" {
		model = "coding/default"
	}
	providers["harbor-pilot"] = map[string]any{
		"npm": "@ai-sdk/openai-compatible", "name": "Harbor Pilot",
		"options": map[string]any{"baseURL": o.BaseURL + "/v1", "apiKey": apiKey},
		"models":  map[string]any{model: map[string]any{"name": model}},
	}
	return encodeJSON(m), []string{key("provider", "harbor-pilot")}, []string{"select the model as harbor-pilot/" + model}, nil
}

func planPi(before []byte, o Options) ([]byte, []string, []string, error) {
	m, err := jsonDoc(before)
	if err != nil {
		return nil, nil, nil, err
	}
	providers := object(m, "providers")
	env := o.TokenEnv
	var notes []string
	if env == "" {
		env = "HARBOR_PILOT_TOKEN"
		notes = append(notes, fmt.Sprintf("Pi reads keys from the environment: export %s=\"$(cat %s)\"", env, shellQuote(o.TokenFile)))
	}
	model := o.Model
	if model == "" {
		model = "coding/default"
	}
	providers["harbor-pilot"] = map[string]any{"baseUrl": o.BaseURL + "/v1", "api": "openai-completions", "apiKey": env,
		"models": []any{map[string]any{"id": model, "name": "Harbor Pilot " + model}}}
	return encodeJSON(m), []string{key("providers", "harbor-pilot")}, notes, nil
}

// ---------- Codex (TOML) ----------

const tomlBegin = "# >>> harbor-pilot (managed; remove with `harbor-pilot harness uninstall codex`)"
const tomlEnd = "# <<< harbor-pilot"

func planCodex(before []byte, o Options) ([]byte, []string, []string, error) {
	text := removeTOML(before, nil)
	env := o.TokenEnv
	var notes []string
	if env == "" {
		env = "HARBOR_PILOT_TOKEN"
		notes = append(notes, fmt.Sprintf("Codex reads the key from the environment: export %s=\"$(cat %s)\"", env, shellQuote(o.TokenFile)))
	}
	var top strings.Builder
	if !strings.Contains(string(text), "\nmodel_provider") && !strings.HasPrefix(string(text), "model_provider") {
		top.WriteString("model_provider = \"harbor-pilot\"\n")
		if o.Model != "" {
			fmt.Fprintf(&top, "model = %q\n", o.Model)
		}
	} else {
		notes = append(notes, "your config already sets model_provider; select harbor-pilot with `codex -c model_provider=harbor-pilot`")
	}
	block := fmt.Sprintf("%s\n[model_providers.harbor-pilot]\nname = \"Harbor Pilot\"\nbase_url = %q\nwire_api = \"responses\"\nenv_key = %q\n%s\n",
		tomlBegin, o.BaseURL+"/v1", env, tomlEnd)
	// Top-level keys must precede any table, so they go first in their own
	// managed block.
	var out strings.Builder
	if top.Len() > 0 {
		out.WriteString(tomlBegin + "\n" + top.String() + tomlEnd + "\n")
	}
	out.Write(text)
	if len(text) > 0 && !bytes.HasSuffix(text, []byte("\n")) {
		out.WriteString("\n")
	}
	out.WriteString(block)
	return []byte(out.String()), []string{"managed-blocks"}, notes, nil
}

// removeTOML strips managed blocks.
func removeTOML(b []byte, _ []string) []byte {
	var out []string
	skip := false
	for _, line := range strings.SplitAfter(string(b), "\n") {
		t := strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(t, tomlBegin):
			skip = true
			continue
		case t == tomlEnd:
			skip = false
			continue
		}
		if !skip && line != "" {
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, ""))
}

// ---------- diff ----------

// Diff renders a minimal line diff (LCS) in unified style without hunks.
func Diff(a, b string) string {
	al, bl := splitLines(a), splitLines(b)
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			out.WriteString("  " + al[i] + "\n")
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			out.WriteString("+ " + bl[j] + "\n")
			j++
		default:
			out.WriteString("- " + al[i] + "\n")
			i++
		}
	}
	return out.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
