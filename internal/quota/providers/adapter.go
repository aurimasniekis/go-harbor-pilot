// Package providers defines the capability-driven adapter contract and the
// registry of adapter implementations.
package providers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// AccountConfig is the adapter-relevant part of an account's configuration.
type AccountConfig struct {
	Account domain.Account
	// Options holds adapter-specific, non-secret settings (base URL, org ID,
	// field mapping, ...). Adapters decode them in Validate.
	Options map[string]any
	// Credentials are resolved secrets. Never log them.
	Credentials credentials.Bundle
}

// FetchRequest asks an adapter for the current state of an account.
type FetchRequest struct {
	Config AccountConfig
	Now    time.Time
	// UsageSince is the start of the usage window to (re)fetch for adapters
	// with usage capabilities; nil means adapter default.
	UsageSince *time.Time
}

// FetchResult is what an adapter observed. Every slice may be empty; absence
// is never interpreted as zero.
type FetchResult struct {
	Buckets          []domain.BucketObservation
	Usage            []domain.UsageAggregate
	APIKeys          []domain.APIKeyRecord
	Prices           []domain.PriceRecord
	SourceTime       *time.Time
	MaskedIdentifier *string
	// Notes are sanitized, user-visible caveats about this fetch.
	Notes []string
}

// Adapter is implemented by each provider integration.
type Adapter interface {
	// Info returns static metadata and capabilities.
	Info() domain.ProviderInfo
	// Validate checks options and that required credentials are present. It
	// must not perform network calls.
	Validate(cfg AccountConfig) error
	// Fetch performs one poll. Returned errors should be *Error.
	Fetch(ctx context.Context, req FetchRequest) (*FetchResult, error)
}

// Error is a typed, sanitized adapter error. Message must never contain
// secrets or raw provider bodies.
type Error struct {
	Kind       domain.ErrorKind
	Message    string
	HTTPStatus int
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s: %s (HTTP %d)", e.Kind, e.Message, e.HTTPStatus)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// Unwrap exposes the (sanitized) cause for errors.Is checks such as
// context.Canceled.
func (e *Error) Unwrap() error { return e.cause }

// NewError builds a typed error.
func NewError(kind domain.ErrorKind, msg string) *Error {
	return &Error{Kind: kind, Message: msg}
}

// WrapError builds a typed error keeping cause for errors.Is.
func WrapError(kind domain.ErrorKind, msg string, cause error) *Error {
	return &Error{Kind: kind, Message: msg, cause: cause}
}

// AsError converts any error into a sanitized *Error. Unknown errors become
// internal errors with a generic message, so raw strings never leak.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return WrapError(domain.ErrNetwork, "request timed out", err)
	}
	if errors.Is(err, context.Canceled) {
		return WrapError(domain.ErrInternal, "request cancelled", err)
	}
	return WrapError(domain.ErrInternal, "unexpected adapter error", err)
}

// ToFetchError converts to persisted error metadata.
func (e *Error) ToFetchError() *domain.FetchError {
	if e == nil {
		return nil
	}
	return &domain.FetchError{Kind: e.Kind, Message: e.Message, HTTPStatus: e.HTTPStatus}
}

// Factory creates an adapter. Adapters are stateless per account apart from
// shared HTTP clients, so one instance serves all accounts of a provider.
type Factory func(deps Deps) Adapter

// Deps are shared dependencies handed to adapters.
type Deps struct {
	Now func() time.Time
	// HTTP is the default outbound client (timeouts, bounded bodies).
	HTTP *HTTPClient
	// Tokens is the optional writable OAuth token store.
	Tokens *TokenHolder
	// Files reads credential files under the configured secret roots.
	Files *FileReader
	// Version is the build version (User-Agent).
	Version string
}

// Registry maps provider IDs to adapters.
type Registry struct {
	mu       sync.RWMutex
	adapters map[domain.ProviderID]Adapter
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{adapters: map[domain.ProviderID]Adapter{}}
}

// Register adds an adapter; duplicate IDs panic because they indicate a
// programming error.
func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := a.Info().ID
	if _, ok := r.adapters[id]; ok {
		panic("providers: duplicate adapter " + string(id))
	}
	r.adapters[id] = a
}

// Get returns an adapter.
func (r *Registry) Get(id domain.ProviderID) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[id]
	return a, ok
}

// All returns all adapters sorted by ID.
func (r *Registry) All() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Info().ID < out[j].Info().ID })
	return out
}

// Caps is a helper to build capability maps; unspecified capabilities are
// reported as unsupported.
func Caps(supported map[domain.Capability]domain.CapabilityInfo) map[domain.Capability]domain.CapabilityInfo {
	out := make(map[domain.Capability]domain.CapabilityInfo, len(domain.AllCapabilities))
	for _, c := range domain.AllCapabilities {
		if v, ok := supported[c]; ok {
			out[c] = v
			continue
		}
		out[c] = domain.CapabilityInfo{Status: domain.CapUnsupported}
	}
	return out
}

// Sup is shorthand for a supported capability with an optional note.
func Sup(note string) domain.CapabilityInfo {
	return domain.CapabilityInfo{Status: domain.CapSupported, Note: note}
}

// Part is shorthand for a partially supported capability.
func Part(note string) domain.CapabilityInfo {
	return domain.CapabilityInfo{Status: domain.CapPartial, Note: note}
}

// Unsup is shorthand for an explicitly unsupported capability with a reason.
func Unsup(note string) domain.CapabilityInfo {
	return domain.CapabilityInfo{Status: domain.CapUnsupported, Note: note}
}
