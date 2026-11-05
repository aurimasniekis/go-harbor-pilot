package credentials

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultEnvPrefixes are the environment variable prefixes secret
// references may read when nothing else is configured.
var DefaultEnvPrefixes = []string{"HP_SECRET_"}

// ErrEnvNotAllowed means an env: reference names a variable outside the
// allowed prefixes, or a variable that is never readable as a secret.
var ErrEnvNotAllowed = errors.New("environment variable is not allowed as a secret reference")

// ErrProtectedPath means a file: reference points at the master key or the
// data directory.
var ErrProtectedPath = errors.New("secret path points at the master key or the data directory")

// deniedEnv are bootstrap variables no reference may read, whatever the
// configured prefixes: they carry the master key or server configuration.
var deniedEnv = map[string]bool{"HP_MASTER_KEY": true, "HP_MASTER_KEY_FILE": true, "HP_PREVIOUS_MASTER_KEYS": true, "HP_PREVIOUS_MASTER_KEY_FILES": true}

// Policy limits what secret references may read. Anyone who can set a
// reference can make its value leave the process (it is sent upstream), so
// references are confined to variables and files meant to hold secrets.
type Policy struct {
	// EnvPrefixes a referenced variable must start with.
	EnvPrefixes []string
	// Protected paths (files or directories) that file: references may not
	// read: the master key files and the data directory.
	Protected []string
}

var (
	policyMu sync.RWMutex
	policy   = Policy{EnvPrefixes: DefaultEnvPrefixes}
)

// SetPolicy installs the process-wide reference policy (at startup).
func SetPolicy(p Policy) {
	if len(p.EnvPrefixes) == 0 {
		p.EnvPrefixes = DefaultEnvPrefixes
	}
	policyMu.Lock()
	policy = p
	policyMu.Unlock()
}

// CurrentPolicy returns the installed policy.
func CurrentPolicy() Policy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return policy
}

// CheckEnvName reports whether name may be read by an env: reference.
func (p Policy) CheckEnvName(name string) error {
	if name == "" || deniedEnv[name] {
		return ErrEnvNotAllowed
	}
	for _, pre := range p.EnvPrefixes {
		if pre != "" && strings.HasPrefix(name, pre) && len(name) > len(pre) {
			return nil
		}
	}
	return ErrEnvNotAllowed
}

// CheckPath reports whether an absolute path may be read by a file:
// reference (roots are checked separately by the resolver).
func (p Policy) CheckPath(path string) error {
	clean := filepath.Clean(path)
	for _, prot := range p.Protected {
		if prot == "" {
			continue
		}
		pc := filepath.Clean(prot)
		if clean == pc || strings.HasPrefix(clean, pc+string(filepath.Separator)) {
			return ErrProtectedPath
		}
	}
	return nil
}

// CheckRef validates a reference string ("env:NAME" or "file:/path")
// against the policy, for use when a reference is saved. The message
// explains what is allowed; it never contains a secret value.
func CheckRef(ref string) error {
	kind, val, ok := strings.Cut(ref, ":")
	if !ok || val == "" {
		return errors.New("must be env:NAME or file:/path")
	}
	p := CurrentPolicy()
	switch kind {
	case "env":
		if err := p.CheckEnvName(val); err != nil {
			return fmt.Errorf("environment variables used as secrets must start with %s", strings.Join(p.EnvPrefixes, " or "))
		}
		return nil
	case "file":
		if !filepath.IsAbs(val) {
			return errors.New("file references need an absolute path")
		}
		if err := p.CheckPath(val); err != nil {
			return errors.New("file references may not point at the master key or the data directory")
		}
		return nil
	}
	return errors.New("must be env:NAME or file:/path")
}
