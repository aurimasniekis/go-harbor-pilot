package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
)

// Server key purposes.
const (
	KeyTokenPepper   = "token-pepper"   // HMAC key for gateway/API token verifiers
	KeySessionPepper = "session-pepper" // HMAC key for session/one-time token hashes
	KeyPlaceholder   = "placeholder"    // masking placeholder derivation
	KeyWebhookSeed   = "webhook-seed"   // default webhook signing secret derivation
)

// ServerKeys manages random server-side keys stored envelope-encrypted in
// the server_keys table. Keys are generated on first use and cached.
type ServerKeys struct {
	db    *DB
	kr    *crypto.Keyring
	mu    sync.Mutex
	cache map[string][]byte
}

// NewServerKeys creates the key manager.
func NewServerKeys(db *DB, kr *crypto.Keyring) *ServerKeys {
	return &ServerKeys{db: db, kr: kr, cache: map[string][]byte{}}
}

// Get returns the key for purpose, creating it if needed.
func (s *ServerKeys) Get(ctx context.Context, purpose string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.cache[purpose]; ok {
		return k, nil
	}
	aad := "server-key|" + purpose
	var env string
	err := s.db.W.QueryRowContext(ctx, `SELECT envelope FROM server_keys WHERE purpose = ?`, purpose).Scan(&env)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		key := crypto.RandomBytes(crypto.KeySize)
		env, err = s.kr.Seal(key, aad)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.W.ExecContext(ctx, `INSERT INTO server_keys(purpose, envelope, created_at) VALUES (?, ?, ?)`,
			purpose, env, time.Now().UnixMilli()); err != nil {
			return nil, err
		}
		s.cache[purpose] = key
		return key, nil
	case err != nil:
		return nil, err
	}
	key, err := s.kr.Open(env, aad)
	if err != nil {
		return nil, err
	}
	s.cache[purpose] = key
	return key, nil
}

// MustGet is Get for startup paths.
func (s *ServerKeys) MustGet(ctx context.Context, purpose string) []byte {
	k, err := s.Get(ctx, purpose)
	if err != nil {
		panic("server key " + purpose + ": " + err.Error())
	}
	return k
}

// RewrapAll re-wraps every server key under the keyring's current KEK.
func (s *ServerKeys) RewrapAll(ctx context.Context) (int, error) {
	type kv struct{ purpose, env string }
	all, err := Query(ctx, s.db.W, func(r *sql.Rows) (kv, error) {
		var x kv
		err := r.Scan(&x.purpose, &x.env)
		return x, err
	}, `SELECT purpose, envelope FROM server_keys`)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range all {
		re, err := s.kr.Rewrap(x.env, "server-key|"+x.purpose)
		if err != nil {
			return n, err
		}
		if re == x.env {
			continue
		}
		if _, err := s.db.W.ExecContext(ctx, `UPDATE server_keys SET envelope = ?, rotated_at = ? WHERE purpose = ?`,
			re, time.Now().UnixMilli(), x.purpose); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
