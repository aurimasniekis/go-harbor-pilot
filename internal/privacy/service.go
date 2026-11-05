package privacy

import (
	"context"
	"strings"
	"sync"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// Service compiles privacy policies from the configuration snapshot and
// owns the placeholder vault.
type Service struct {
	CP    *cp.Service
	Vault *Vault

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	rev int64
	p   *Policy
	err error
}

// NewService creates the service.
func NewService(c *cp.Service, now func() time.Time) *Service {
	return &Service{CP: c, Vault: NewVault(now), cache: map[string]cached{}}
}

// Policy returns the compiled policy (nil, nil when id is empty).
func (s *Service) Policy(ctx context.Context, snap *cp.Snapshot, id string) (*Policy, error) {
	if id == "" || snap == nil {
		return nil, nil
	}
	res := snap.Privacy[id]
	if res == nil {
		return nil, ErrUnknownPolicy
	}
	s.mu.Lock()
	c, ok := s.cache[id]
	s.mu.Unlock()
	if ok && c.rev == res.Revision {
		return c.p, c.err
	}
	var values []string
	if sec, err := s.CP.ResolveSecret(ctx, cp.PrivacyPolicyDef, id, "secret_values"); err == nil && !sec.IsZero() {
		values = strings.Split(sec.Reveal(), "\n")
	}
	p, err := Compile(id, res.Spec, values)
	s.mu.Lock()
	s.cache[id] = cached{rev: res.Revision, p: p, err: err}
	s.mu.Unlock()
	return p, err
}

// ErrUnknownPolicy means a plan references a missing policy.
var ErrUnknownPolicy = errUnknown{}

type errUnknown struct{}

func (errUnknown) Error() string { return "privacy policy not found" }

// Scope builds the placeholder scope for a request: placeholders are
// isolated per client binding and per conversation session (or per
// credential when no session is known).
func Scope(bindingID, sessionID, credentialID string) string {
	if sessionID != "" {
		return bindingID + "|ses|" + sessionID
	}
	return bindingID + "|crd|" + credentialID
}
