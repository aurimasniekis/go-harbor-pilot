// Package ids generates stable, prefixed, lexicographically sortable
// identifiers (prefix + "_" + 26-char lowercase ULID).
package ids

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Prefixes for every identified entity. Prefixes are part of the public API
// contract and must never change once released.
const (
	Principal        = "pri"
	User             = "usr"
	Session          = "wss" // human web session (display id)
	Workspace        = "wsp"
	Project          = "prj"
	ClientBinding    = "cbn"
	Credential       = "crd"
	Provider         = "prv"
	Account          = "acc"
	Entitlement      = "ent"
	Pool             = "pol"
	Alias            = "als"
	Plan             = "pln"
	PolicyVersion    = "pvr"
	Budget           = "bdg"
	QuotaMonitor     = "qmn"
	PrivacyPolicy    = "pvp"
	TracePolicy      = "trp"
	Destination      = "dst"
	Channel          = "nch"
	NotificationRule = "nrl"
	OIDCProvider     = "oid"
	Request          = "req"
	Attempt          = "att"
	ConvSession      = "ses"
	Reservation      = "rsv"
	Ledger           = "led"
	Event            = "evt"
	Delivery         = "dlv"
	Trace            = "trc"
	TraceKey         = "tky"
	ProtocolChange   = "pch"
	WorkItem         = "wki"
	Audit            = "aud"
	Secret           = "sec"
	Revision         = "rev"
	Breaker          = "brk"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns a new identifier with the given prefix.
func New(prefix string) string {
	return NewAt(prefix, time.Now())
}

// NewAt returns a new identifier whose time component is t.
func NewAt(prefix string, t time.Time) string {
	mu.Lock()
	id := ulid.MustNew(ulid.Timestamp(t), entropy)
	mu.Unlock()
	return prefix + "_" + strings.ToLower(id.String())
}

// Valid reports whether s is a well-formed identifier with the given prefix.
func Valid(prefix, s string) bool {
	p, rest, ok := strings.Cut(s, "_")
	if !ok || p != prefix || len(rest) != 26 {
		return false
	}
	_, err := ulid.ParseStrict(strings.ToUpper(rest))
	return err == nil
}

// Prefix returns the prefix of an identifier.
func Prefix(s string) string {
	p, _, _ := strings.Cut(s, "_")
	return p
}

// Must validates s against prefix and returns an error describing the problem.
func Must(prefix, s string) error {
	if !Valid(prefix, s) {
		return fmt.Errorf("invalid %s identifier", prefix)
	}
	return nil
}
