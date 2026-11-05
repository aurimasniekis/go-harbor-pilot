package bridge

import (
	"os"
	"path/filepath"
)

// PrivateDir creates a 0700 directory for one CLI run's private files
// (configuration with session tokens, the system prompt), so they never
// appear in process arguments.
func PrivateDir(root string) (string, error) {
	base := filepath.Join(root, "r")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "run")
}

// WritePrivate writes a 0600 file that must not already exist.
func WritePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path inside a private run directory
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
