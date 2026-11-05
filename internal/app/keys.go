package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
)

// ErrNoMasterKey explains how to configure the KEK.
var ErrNoMasterKey = errors.New("no master key configured: generate one with `harbor-pilot keys generate > /secure/path/master.key` " +
	"(mode 0600, outside the data directory) and set security.master_key_file or HP_MASTER_KEY_FILE")

// LoadKeyring loads the KEK (and previous KEKs) from configuration. In demo
// mode a throwaway key is created inside the data directory.
func LoadKeyring(c config.Config, env func(string) string) (*crypto.Keyring, error) {
	var cur crypto.KEK
	var err error
	switch {
	case c.Security.MasterKeyFile != "":
		cur, err = crypto.LoadKEKFile(c.Security.MasterKeyFile)
	case env("HP_MASTER_KEY") != "":
		cur, err = crypto.ParseKEK(env("HP_MASTER_KEY"))
	case c.Demo.Enabled:
		cur, err = demoKey(c.Server.DataDir)
	default:
		return nil, ErrNoMasterKey
	}
	if err != nil {
		return nil, err
	}
	var prev []crypto.KEK
	for _, p := range c.Security.PreviousMasterKeyFiles {
		k, err := crypto.LoadKEKFile(p)
		if err != nil {
			return nil, fmt.Errorf("previous master key: %w", err)
		}
		prev = append(prev, k)
	}
	return crypto.NewKeyring(cur, prev...)
}

func demoKey(dir string) (crypto.KEK, error) {
	p := filepath.Join(dir, "demo-master.key")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return crypto.KEK{}, err
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(p, []byte(crypto.GenerateKEKString()), 0o600); err != nil {
			return crypto.KEK{}, err
		}
	}
	return crypto.LoadKEKFile(p)
}

// LockDataDir takes the exclusive data-directory lock (offline commands).
func LockDataDir(dir string) (func(), error) { return lockDataDir(dir) }
