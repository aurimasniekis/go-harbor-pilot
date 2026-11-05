// Package events defines transport-independent, versioned domain events and
// the transactional outbox they are written to. Relays (Pulse/NATS, signed
// webhooks) and the notification engine consume the outbox asynchronously;
// nothing on the request path ever waits for delivery.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// SchemaVersion of the event envelope.
const SchemaVersion = 1

// Domain prefix used for subjects and types.
const Domain = "harborpilot"

// Severity levels.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Kind identifies an event by area, entity and action.
type Kind struct {
	Area, Entity, Action string
}

var tokenRE = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Subject returns evt.harborpilot.<area>.<entity>.<action>.
func (k Kind) Subject() string {
	return "evt." + Domain + "." + k.Area + "." + k.Entity + "." + k.Action
}

// Type returns harborpilot.<area>.<entity>_<action>.v1.
func (k Kind) Type() string {
	return fmt.Sprintf("%s.%s.%s_%s.v%d", Domain, k.Area, k.Entity, k.Action, SchemaVersion)
}

// Name returns area.entity.action.
func (k Kind) Name() string { return k.Area + "." + k.Entity + "." + k.Action }

func (k Kind) valid() bool {
	return tokenRE.MatchString(k.Area) && tokenRE.MatchString(k.Entity) && tokenRE.MatchString(k.Action)
}

// Event is one domain transition.
type Event struct {
	Kind          Kind
	Severity      string
	CorrelationID string
	// DedupeKey groups repeated occurrences of the same transition (used by
	// notification grouping; delivery idempotency uses the event id).
	DedupeKey string
	// Subjects are the affected identities (account_id, plan_id, …). Values
	// must be identifiers, never content.
	Subjects map[string]string
	// Data is the allowlisted payload. Never prompts, secrets or raw bodies.
	Data any
	// OccurredAt defaults to now.
	OccurredAt time.Time
}

// Envelope is the serialized event body delivered to every transport.
type Envelope struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Name          string            `json:"name"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Severity      string            `json:"severity"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Subjects      map[string]string `json:"subjects,omitempty"`
	Data          json.RawMessage   `json:"data,omitempty"`
}

// MaxPayloadBytes bounds serialized envelopes (well under transport limits).
const MaxPayloadBytes = 256 << 10

// Append writes an event to the outbox using ex (normally the transaction
// that performs the state change). It returns the event id.
func Append(ctx context.Context, ex store.Execer, e Event) (string, error) {
	if !e.Kind.valid() {
		return "", fmt.Errorf("events: invalid kind %+v", e.Kind)
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}
	if e.CorrelationID == "" {
		e.CorrelationID = observability.RequestID(ctx)
	}
	id := ids.NewAt(ids.Event, e.OccurredAt)
	var data json.RawMessage
	if e.Data != nil {
		b, err := json.Marshal(e.Data)
		if err != nil {
			return "", fmt.Errorf("events: marshal data: %w", err)
		}
		data = b
	}
	env := Envelope{
		SchemaVersion: SchemaVersion, ID: id, Type: e.Kind.Type(), Name: e.Kind.Name(),
		OccurredAt: e.OccurredAt.UTC(), Severity: e.Severity, CorrelationID: e.CorrelationID,
		Subjects: e.Subjects, Data: data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	if len(body) > MaxPayloadBytes {
		env.Data = json.RawMessage(`{"truncated":true}`)
		body, _ = json.Marshal(env)
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO event_outbox(id, type, subject, occurred_at, severity, correlation_id, dedupe_key, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, env.Type, e.Kind.Subject(), e.OccurredAt.UnixMilli(), e.Severity, nullable(e.CorrelationID), nullable(e.DedupeKey), string(body))
	return id, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
