// Package executor runs upstream attempts: it renders the canonical request
// for a provider, sends it with the account's credentials and turns the
// upstream response into canonical events.
package executor

import (
	"context"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

// Target is one routing candidate to execute.
type Target struct {
	Provider *controlplane.Res[controlplane.ProviderSpec]
	Account  *controlplane.Res[controlplane.AccountSpec]
	Model    string
	// ModelSpec is the declared model (nil when the catalog is empty).
	ModelSpec *controlplane.ModelSpec
}

// Request is an attempt request.
type Request struct {
	IR      *ir.Request
	Inbound ir.Protocol
	// AllowLosses permits sending when the translation reports losses of
	// severity reject (an explicit, policy-approved downgrade).
	AllowLosses bool
	// RawBody is the client's original body, used for native passthrough.
	RawBody []byte
	// Passthrough forwards RawBody unchanged to a same-protocol upstream.
	Passthrough bool
	// RequestID is sent upstream for correlation where supported.
	RequestID string
	// Scope identifies the gateway client (binding, or parent credential for
	// child tokens). Stateful executors such as CLI bridges reuse a live
	// process or thread only within the same scope. Empty = unscoped.
	Scope string
	// ClientHeader holds the client's request headers; only allowlisted
	// behaviour flags (e.g. anthropic-beta) are forwarded, and only to a
	// same-protocol upstream.
	ClientHeader http.Header
}

// Mode describes how an attempt was executed.
type Mode string

// Execution modes.
const (
	ModeNative      Mode = "native"      // same protocol, parsed and rebuilt
	ModeTranslated  Mode = "translated"  // different protocol
	ModePassthrough Mode = "passthrough" // raw bytes forwarded
)

// Result is a started attempt.
type Result struct {
	Upstream ir.Protocol
	Mode     Mode
	Losses   []ir.Loss
	// Provenance is recorded with the attempt's ledger entry (for example
	// which CLI session served a bridge turn).
	Provenance map[string]any
	// Status is the upstream HTTP status (0 if no response).
	Status int
	Header http.Header
	// Response is set for non-streaming attempts.
	Response *ir.Response
	// Stream yields events for streaming attempts. The executor closes it;
	// the final error (if any) is available from Err after it is closed.
	Stream <-chan ir.Event
	Err    func() error
	// Unknown receives unrecognized upstream events/fields (Protocol Watch).
	Unknown func() []string
	// RawStream, for passthrough, yields raw upstream bytes.
	RawStream <-chan []byte
	// FirstByteAt is when the upstream sent the first response byte.
	FirstByteAt time.Time
	// Cancel aborts the upstream request.
	Cancel context.CancelFunc
}

// AttemptError describes a failed attempt and whether trying another
// candidate is safe.
type AttemptError struct {
	Err *ir.Error
	// Executed is true when the upstream may have processed the request
	// (ambiguous); such attempts are never replayed automatically.
	Executed bool
	// AccountScoped reports the failure concerns this account (auth, quota,
	// rate limit), so another account may succeed.
	AccountScoped bool
	Status        int
	Header        http.Header
	// Losses are the translation losses of the request as built for this
	// attempt, when it got that far; a failed request records them too.
	Losses []ir.Loss
}

func (e *AttemptError) Error() string { return e.Err.Error() }

// Executor runs attempts against one kind of provider.
type Executor interface {
	Execute(ctx context.Context, t Target, cred credentials.Secret, req Request, stream bool) (*Result, *AttemptError)
}
