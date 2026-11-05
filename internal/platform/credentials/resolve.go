package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxSecretFileSize bounds secret file reads.
const MaxSecretFileSize = 256 << 10

// Ref is the resolver's view of a secret reference.
type Ref struct {
	Type     string
	Name     string
	Path     string
	Key      string
	Optional bool
}

// Describe returns a non-secret description.
func (r Ref) Describe() string {
	switch r.Type {
	case "env":
		return "env:" + r.Name
	case "file":
		if r.Key != "" {
			return "file:" + r.Path + "#" + r.Key
		}
		return "file:" + r.Path
	case "env_file":
		return "env_file:" + r.Path + "#" + r.Key
	}
	return r.Type
}

// ErrNotFound means the referenced secret does not exist.
var ErrNotFound = errors.New("secret not found")

// ErrOutsideRoots means a secret path is not inside an allowed root.
var ErrOutsideRoots = errors.New("secret path is outside the configured secret roots")

// Resolver resolves references. File access is confined to Roots using
// os.Root, so ".." components and symlinks pointing outside a root fail.
type Resolver struct {
	Roots     []string
	LookupEnv func(string) (string, bool)
	// Policy overrides the process-wide reference policy (tests).
	Policy *Policy
}

func (r *Resolver) policy() Policy {
	if r.Policy != nil {
		return *r.Policy
	}
	return CurrentPolicy()
}

// NewResolver builds a resolver using the process environment.
func NewResolver(roots []string) *Resolver {
	return &Resolver{Roots: roots, LookupEnv: os.LookupEnv}
}

// Resolve returns the secret for ref. Errors never contain secret values.
func (r *Resolver) Resolve(ref Ref) (Secret, error) {
	s, err := r.resolve(ref)
	if err != nil {
		if ref.Optional && errors.Is(err, ErrNotFound) {
			return Secret{}, nil
		}
		return Secret{}, fmt.Errorf("%s: %w", ref.Describe(), err)
	}
	if s.IsZero() && !ref.Optional {
		return Secret{}, fmt.Errorf("%s: %w (empty value)", ref.Describe(), ErrNotFound)
	}
	return s, nil
}

func (r *Resolver) resolve(ref Ref) (Secret, error) {
	switch ref.Type {
	case "env":
		if ref.Name == "" {
			return Secret{}, errors.New("env reference requires name")
		}
		if err := r.policy().CheckEnvName(ref.Name); err != nil {
			return Secret{}, err
		}
		v, ok := r.LookupEnv(ref.Name)
		if !ok {
			return Secret{}, ErrNotFound
		}
		return NewSecret(strings.TrimSpace(v)), nil
	case "file":
		b, err := r.ReadFile(ref.Path)
		if err != nil {
			return Secret{}, err
		}
		if ref.Key == "" {
			return NewSecret(strings.TrimRight(string(b), "\r\n")), nil
		}
		v, err := jsonPath(b, ref.Key)
		if err != nil {
			return Secret{}, err
		}
		return NewSecret(v), nil
	case "env_file":
		if ref.Key == "" {
			return Secret{}, errors.New("env_file reference requires key")
		}
		b, err := r.ReadFile(ref.Path)
		if err != nil {
			return Secret{}, err
		}
		vals, err := ParseDotenv(b)
		if err != nil {
			return Secret{}, err
		}
		v, ok := vals[ref.Key]
		if !ok {
			return Secret{}, ErrNotFound
		}
		return NewSecret(v), nil
	default:
		return Secret{}, fmt.Errorf("unsupported secret reference type %q (want env, file or env_file)", ref.Type)
	}
}

// ReadFile reads a file that must live under one of the roots.
func (r *Resolver) ReadFile(path string) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("secret file path must be absolute")
	}
	clean := filepath.Clean(path)
	if err := r.policy().CheckPath(clean); err != nil {
		return nil, err
	}
	for _, root := range r.Roots {
		rootClean := filepath.Clean(root)
		rel, err := filepath.Rel(rootClean, clean)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return readInRoot(rootClean, rel)
	}
	return nil, ErrOutsideRoots
}

func readInRoot(root, rel string) ([]byte, error) {
	rt, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open secret root: %w", sanitizeFSError(err))
	}
	defer rt.Close()
	f, err := rt.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, sanitizeFSError(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, sanitizeFSError(err)
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("secret path is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSecretFileSize+1))
	if err != nil {
		return nil, sanitizeFSError(err)
	}
	if len(b) > MaxSecretFileSize {
		return nil, errors.New("secret file too large")
	}
	return b, nil
}

func sanitizeFSError(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		if strings.Contains(pe.Err.Error(), "escapes") || strings.Contains(err.Error(), "escapes") {
			return ErrOutsideRoots
		}
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	if strings.Contains(err.Error(), "escapes") {
		return ErrOutsideRoots
	}
	return err
}

// jsonPath extracts a string at a dotted path like "a.b.c".
func jsonPath(b []byte, path string) (string, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "", errors.New("secret file is not valid JSON")
	}
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return "", ErrNotFound
		}
		v, ok = m[part]
		if !ok {
			return "", ErrNotFound
		}
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case float64, bool:
		return fmt.Sprint(t), nil
	default:
		return "", errors.New("secret JSON field is not a scalar")
	}
}

// JSONFile reads and parses a JSON file under the roots (used by adapters
// that need several fields of a credential file, e.g. token + expiry).
func (r *Resolver) JSONFile(path string, out any) error {
	b, err := r.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("credential file is not valid JSON")
	}
	return nil
}

var dotenvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseDotenv parses KEY=VALUE lines. It performs no variable expansion and
// no command substitution: "$X" and "$(cmd)" are literal text. Supported:
// comments (#), optional "export " prefix, single quotes (literal), double
// quotes (\n, \", \\ escapes), unquoted values with trailing " # comment".
// Duplicate keys are an error. Errors report line numbers, never values.
func ParseDotenv(b []byte) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("dotenv line %d: expected KEY=VALUE", i+1)
		}
		key := strings.TrimSpace(line[:eq])
		if !dotenvKey.MatchString(key) {
			return nil, fmt.Errorf("dotenv line %d: invalid key", i+1)
		}
		val := strings.TrimSpace(line[eq+1:])
		switch {
		case strings.HasPrefix(val, "'"):
			end := strings.IndexByte(val[1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("dotenv line %d: unterminated single quote", i+1)
			}
			val = val[1 : 1+end]
		case strings.HasPrefix(val, `"`):
			var sb strings.Builder
			closed := false
			for j := 1; j < len(val); j++ {
				c := val[j]
				if c == '\\' && j+1 < len(val) {
					j++
					switch val[j] {
					case 'n':
						sb.WriteByte('\n')
					case '"', '\\':
						sb.WriteByte(val[j])
					default:
						sb.WriteByte('\\')
						sb.WriteByte(val[j])
					}
					continue
				}
				if c == '"' {
					closed = true
					break
				}
				sb.WriteByte(c)
			}
			if !closed {
				return nil, fmt.Errorf("dotenv line %d: unterminated double quote", i+1)
			}
			val = sb.String()
		default:
			if k := strings.Index(val, " #"); k >= 0 {
				val = strings.TrimSpace(val[:k])
			}
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("dotenv line %d: duplicate key %s", i+1, key)
		}
		out[key] = val
	}
	return out, nil
}
