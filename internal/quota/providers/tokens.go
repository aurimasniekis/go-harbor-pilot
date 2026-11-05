package providers

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// OAuthToken is an OAuth token set held for an account. It is never
// serialized in clear text.
type OAuthToken struct {
	AccessToken  credentials.Secret
	RefreshToken credentials.Secret
	ExpiresAt    time.Time
	Scopes       []string
	// Subject is a non-secret provider account reference (e.g. the ChatGPT
	// account ID) needed as a request header by some providers.
	Subject    string
	ObtainedAt time.Time
}

// Expired reports whether the access token expires within skew.
func (t OAuthToken) Expired(now time.Time, skew time.Duration) bool {
	return !t.ExpiresAt.IsZero() && now.Add(skew).After(t.ExpiresAt)
}

// ErrNoToken means no token is stored for the account.
var ErrNoToken = errors.New("no stored OAuth token")

// ErrTokenStoreDisabled means writable token persistence is not enabled.
var ErrTokenStoreDisabled = errors.New("token store is disabled (set oauth.token_store.enabled and oauth.token_store.key)")

// TokenStore persists OAuth tokens for accounts using in-app login.
type TokenStore interface {
	Get(id domain.AccountID) (OAuthToken, error)
	Put(id domain.AccountID, t OAuthToken) error
	Delete(id domain.AccountID) error
}

// TokenHolder is a late-bound TokenStore (adapters are registered before
// configuration selects whether a store exists).
type TokenHolder struct {
	p atomic.Pointer[TokenStore]
	// mu serializes refreshes per process so concurrent polls never use a
	// rotated refresh token twice.
	mu sync.Mutex
}

// Set installs the store (nil disables it).
func (h *TokenHolder) Set(s TokenStore) {
	if s == nil {
		h.p.Store(nil)
		return
	}
	h.p.Store(&s)
}

// Store returns the store or ErrTokenStoreDisabled.
func (h *TokenHolder) Store() (TokenStore, error) {
	p := h.p.Load()
	if p == nil {
		return nil, ErrTokenStoreDisabled
	}
	return *p, nil
}

// Lock serializes token refreshes.
func (h *TokenHolder) Lock() func() {
	h.mu.Lock()
	return h.mu.Unlock
}

// FileReader reads files confined to the current secret roots (re-read each
// poll so externally refreshed credential files are picked up).
type FileReader struct {
	roots atomic.Pointer[[]string]
}

// SetRoots replaces the allowed roots.
func (f *FileReader) SetRoots(roots []string) {
	cp := append([]string(nil), roots...)
	f.roots.Store(&cp)
}

// Read reads a file under the roots.
func (f *FileReader) Read(path string) ([]byte, error) {
	r := f.roots.Load()
	if r == nil {
		return nil, credentials.ErrOutsideRoots
	}
	res := credentials.Resolver{Roots: *r}
	return res.ReadFile(path)
}
