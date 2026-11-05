package privacy

import (
	"context"
	"errors"
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

	now   func() time.Time
	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	rev int64
	p   *Policy
	at  time.Time
}

// refreshEvery bounds how long resolved secret values are reused: values
// behind env: or file: references are re-read at least this often.
const refreshEvery = time.Minute

// ErrSecretValuesUnavailable means the policy has configured secret values
// that could not be resolved (missing reference, unreadable file, wrong
// master key). Policy then returns the policy without those values; the
// caller must not treat that as full masking.
var ErrSecretValuesUnavailable = errors.New("configured secret values could not be resolved")

// NewService creates the service.
func NewService(c *cp.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{CP: c, Vault: NewVault(now), now: now, cache: map[string]cached{}}
}

// Policy returns the compiled policy (nil, nil when id is empty). Only
// complete compilations are cached; resolution failures are retried on the
// next call.
func (s *Service) Policy(ctx context.Context, snap *cp.Snapshot, id string) (*Policy, error) {
	if id == "" || snap == nil {
		return nil, nil
	}
	res := snap.Privacy[id]
	if res == nil {
		return nil, ErrUnknownPolicy
	}
	now := s.now()
	s.mu.Lock()
	c, ok := s.cache[id]
	s.mu.Unlock()
	if ok && c.rev == res.Revision && now.Sub(c.at) < refreshEvery {
		return c.p, nil
	}
	var values []string
	var verr error
	if usesConfiguredSecrets(res.Spec) {
		sec, err := s.CP.ResolveSecret(ctx, cp.PrivacyPolicyDef, id, "secret_values")
		switch {
		case err == nil:
			if !sec.IsZero() {
				values = strings.Split(sec.Reveal(), "\n")
			}
		case res.Spec.SecretValuesRef != "" || s.storedValues(ctx, id):
			verr = ErrSecretValuesUnavailable
		}
	}
	p, err := Compile(id, res.Spec, values)
	if err != nil {
		return nil, err
	}
	if verr != nil {
		return p, verr
	}
	s.mu.Lock()
	s.cache[id] = cached{rev: res.Revision, p: p, at: now}
	s.mu.Unlock()
	return p, nil
}

func usesConfiguredSecrets(s cp.PrivacyPolicySpec) bool {
	for _, d := range s.Detectors {
		if d == "configured_secrets" {
			return true
		}
	}
	return false
}

// storedValues reports whether write-only secret values are stored for the
// policy (as opposed to never configured).
func (s *Service) storedValues(ctx context.Context, id string) bool {
	o, err := s.CP.Get(ctx, cp.PrivacyPolicyDef, id)
	if err != nil {
		return true // cannot tell: treat as configured and fail closed
	}
	return o.Secrets["secret_values"].Set
}

// ErrUnknownPolicy means a plan references a missing policy.
var ErrUnknownPolicy = errUnknown{}

type errUnknown struct{}

func (errUnknown) Error() string { return "privacy policy not found" }

// Scope builds the placeholder scope for a request. Placeholders are
// isolated per client binding and per credential (child tokens share their
// parent's scope), and per conversation session within that. The session
// comes from a client hint, so it narrows the scope but never lets one
// credential join another's.
func Scope(bindingID, sessionID, credentialID string) string {
	s := bindingID + "|crd|" + credentialID
	if sessionID != "" {
		s += "|ses|" + sessionID
	}
	return s
}
