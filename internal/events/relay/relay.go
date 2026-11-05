// Package relay delivers outbox events to external destinations (Pulse
// over NATS JetStream, signed webhooks) with at-least-once semantics.
//
// Each destination is a consumer with its own cursor over the outbox. New
// events that pass the destination's filter become delivery rows, which are
// sent in event order: a round stops at the first failure so ordering is
// kept. Failures back off exponentially with jitter (respecting
// Retry-After) and dead-letter after a maximum number of attempts or age.
// Dead deliveries can be replayed. The relay runs on its own goroutine and
// never touches the request path.
package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/outbound"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Event is one outbox row.
type Event struct {
	Seq        int64
	ID         string
	Type       string
	Subject    string
	Name       string
	Severity   string
	OccurredAt time.Time
	DedupeKey  string
	Subjects   map[string]string
	Payload    json.RawMessage
}

// Publisher publishes to Pulse.
type Publisher interface {
	Publish(ctx context.Context, subject, typ string, payload []byte, id string, at time.Time) (duplicate bool, err error)
	Close()
}

// PulseConnector opens a Pulse publisher.
type PulseConnector func(url string, creds, ca []byte, source string) (Publisher, error)

// Relay delivers events to destinations.
type Relay struct {
	DB       *store.DB
	Clock    clock.Clock
	Log      *slog.Logger
	Snapshot func() *cp.Snapshot
	// Secret resolves a destination's write-only secrets.
	Secret func(ctx context.Context, destID, field string) (credentials.Secret, error)
	// ReadFile reads file: references (creds, CA).
	ReadFile func(path string) ([]byte, error)
	HTTP     *outbound.Client
	Pulse    PulseConnector
	// Wake triggers an immediate round (optional).
	Wake chan struct{}

	mu   sync.Mutex
	pubs map[string]pubEntry
}

type pubEntry struct {
	rev int64
	p   Publisher
}

// New creates a relay.
func New(db *store.DB, c clock.Clock, log *slog.Logger, snap func() *cp.Snapshot) *Relay {
	if c == nil {
		c = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Relay{DB: db, Clock: c, Log: log, Snapshot: snap, HTTP: outbound.New(), Wake: make(chan struct{}, 1), pubs: map[string]pubEntry{}}
}

// Close releases publishers.
func (r *Relay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.pubs {
		e.p.Close()
		delete(r.pubs, k)
	}
}

// Run delivers until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	defer r.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.Wake:
		}
		r.Round(ctx)
	}
}

// Round runs one fan-out and delivery pass (tests call it directly).
func (r *Relay) Round(ctx context.Context) {
	snap := r.Snapshot()
	if snap == nil {
		return
	}
	ids := make([]string, 0, len(snap.Destinations))
	for id := range snap.Destinations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := snap.Destinations[id]
		if !d.Spec.Enabled {
			continue
		}
		if err := r.fanOut(ctx, consumer(id), d.Spec.Filter); err != nil {
			r.Log.Warn("relay.fanout_failed", "destination_id", id, "error", err.Error())
			continue
		}
		r.deliver(ctx, d)
	}
}

func consumer(destID string) string { return "dst:" + destID }

// LoadEvents reads outbox events after a sequence number.
func LoadEvents(ctx context.Context, q store.Execer, after int64, limit int) ([]Event, error) {
	return store.Query(ctx, q, scanEvent, `SELECT seq, id, type, subject, occurred_at, severity, COALESCE(dedupe_key,''), payload
		FROM event_outbox WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
}

func scanEvent(row *sql.Rows) (Event, error) {
	var e Event
	var at int64
	var payload string
	err := row.Scan(&e.Seq, &e.ID, &e.Type, &e.Subject, &at, &e.Severity, &e.DedupeKey, &payload)
	e.OccurredAt = time.UnixMilli(at).UTC()
	e.Payload = json.RawMessage(payload)
	var env struct {
		Name     string            `json:"name"`
		Subjects map[string]string `json:"subjects"`
	}
	_ = json.Unmarshal(e.Payload, &env)
	e.Name, e.Subjects = env.Name, env.Subjects
	return e, err
}

// Cursor returns a consumer's position, initializing new consumers at the
// current end of the outbox (history is not replayed implicitly).
func Cursor(ctx context.Context, tx *sql.Tx, name string, now time.Time) (int64, error) {
	var seq int64
	err := tx.QueryRowContext(ctx, `SELECT last_seq FROM event_cursors WHERE consumer = ?`, name).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM event_outbox`).Scan(&seq); err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO event_cursors(consumer, last_seq, updated_at) VALUES (?, ?, ?)`, name, seq, now.UnixMilli())
		return seq, err
	}
	return seq, err
}

func (r *Relay) fanOut(ctx context.Context, name string, f cp.EventFilter) error {
	now := r.Clock.Now()
	return r.DB.Tx(ctx, func(tx *sql.Tx) error {
		cur, err := Cursor(ctx, tx, name, now)
		if err != nil {
			return err
		}
		evs, err := LoadEvents(ctx, tx, cur, 500)
		if err != nil || len(evs) == 0 {
			return err
		}
		for _, e := range evs {
			if !f.Matches(e.Name, e.Severity, e.Subjects) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO event_deliveries(id, consumer, event_seq, status, attempts, next_attempt_at, created_at)
				VALUES (?, ?, ?, 'pending', 0, ?, ?) ON CONFLICT(consumer, event_seq) DO NOTHING`, ids.NewAt(ids.Delivery, now), name, e.Seq, now.UnixMilli(), now.UnixMilli()); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE event_cursors SET last_seq = ?, updated_at = ? WHERE consumer = ?`, evs[len(evs)-1].Seq, now.UnixMilli(), name)
		return err
	})
}

type due struct {
	id       string
	attempts int
	next     int64
	ev       Event
}

// Backoff returns 2^(n-1) seconds with ±50% jitter, capped at five minutes.
func Backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	base := time.Duration(1<<min(n-1, 9)) * time.Second
	base = min(base, 5*time.Minute)
	j := 0.5 + rand.Float64() //nolint:gosec // jitter, not security
	return time.Duration(float64(base) * j)
}

func (r *Relay) deliver(ctx context.Context, d *cp.Res[cp.DestinationSpec]) {
	now := r.Clock.Now()
	name := consumer(d.ID)
	rows, err := store.Query(ctx, r.DB.R, func(row *sql.Rows) (due, error) {
		var x due
		var at int64
		var payload string
		err := row.Scan(&x.id, &x.attempts, &x.next, &x.ev.Seq, &x.ev.ID, &x.ev.Type, &x.ev.Subject, &at, &x.ev.Severity, &payload)
		x.ev.OccurredAt, x.ev.Payload = time.UnixMilli(at).UTC(), json.RawMessage(payload)
		return x, err
	}, `SELECT d.id, d.attempts, d.next_attempt_at, e.seq, e.id, e.type, e.subject, e.occurred_at, e.severity, e.payload
		FROM event_deliveries d JOIN event_outbox e ON e.seq = d.event_seq
		WHERE d.consumer = ? AND (d.status IN ('pending','deferred') OR (d.status = 'sending' AND d.lease_until < ?)) ORDER BY e.seq LIMIT 50`, name, now.UnixMilli())
	if err != nil {
		return
	}
	for _, x := range rows {
		if x.next > now.UnixMilli() {
			return // ordered: wait for the head of the queue
		}
		if x.attempts >= d.Spec.MaxAttempts || now.Sub(x.ev.OccurredAt) > time.Duration(d.Spec.MaxAgeHours)*time.Hour {
			r.mark(ctx, x.id, "dead", x.attempts, 0, "dead-lettered: attempts or age exceeded", now)
			continue
		}
		claim, err := r.DB.W.ExecContext(ctx, `UPDATE event_deliveries SET status = 'sending', lease_until = ? WHERE id = ?
			AND (status IN ('pending','deferred') OR (status = 'sending' AND lease_until < ?))`, now.Add(2*time.Minute).UnixMilli(), x.id, now.UnixMilli())
		if err != nil {
			return
		}
		if n, _ := claim.RowsAffected(); n == 0 {
			return // another round holds it
		}
		res := r.send(ctx, d, x.ev)
		if res.OK {
			r.mark(ctx, x.id, "delivered", x.attempts+1, res.Status, "", now)
			continue
		}
		if !res.Retryable {
			r.mark(ctx, x.id, "dead", x.attempts+1, res.Status, res.Err, now)
			continue
		}
		wait := Backoff(x.attempts + 1)
		if res.RetryAfter > wait {
			wait = min(res.RetryAfter, time.Hour)
		}
		r.defer_(ctx, x.id, x.attempts+1, res.Status, res.Err, now.Add(wait))
		return // keep order: stop the round at the first failure
	}
}

func (r *Relay) mark(ctx context.Context, id, status string, attempts, code int, errText string, now time.Time) {
	var delivered any
	if status == "delivered" {
		delivered = now.UnixMilli()
	}
	_, _ = r.DB.W.ExecContext(ctx, `UPDATE event_deliveries SET status = ?, attempts = ?, last_status = ?, last_error = ?, delivered_at = ? WHERE id = ?`,
		status, attempts, nullInt(code), nullStr(errText), delivered, id)
}

func (r *Relay) defer_(ctx context.Context, id string, attempts, code int, errText string, next time.Time) {
	_, _ = r.DB.W.ExecContext(ctx, `UPDATE event_deliveries SET status = 'deferred', attempts = ?, last_status = ?, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		attempts, nullInt(code), nullStr(errText), next.UnixMilli(), id)
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *Relay) send(ctx context.Context, d *cp.Res[cp.DestinationSpec], e Event) outbound.Result {
	switch d.Spec.Type {
	case "webhook":
		sec, _ := r.Secret(ctx, d.ID, "signing_secret")
		prev, _ := r.Secret(ctx, d.ID, "previous_signing_secret")
		h := http.Header{}
		for k, v := range d.Spec.Headers {
			h.Set(k, v)
		}
		h.Set("X-Harbor-Event-Id", e.ID)
		h.Set("X-Harbor-Event-Type", e.Type)
		h.Set("X-Harbor-Signature", outbound.Sign(e.Payload, r.Clock.Now(), sec.Reveal(), prev.Reveal()))
		return r.HTTP.Post(ctx, d.Spec.WebhookURL, e.Payload, h, d.Spec.AllowPrivateNetwork, time.Duration(d.Spec.TimeoutSeconds)*time.Second)
	case "pulse":
		p, err := r.publisher(ctx, d)
		if err != nil {
			return outbound.Result{Retryable: true, Err: "could not connect to Pulse: " + sanitize(err)}
		}
		pctx, cancel := context.WithTimeout(ctx, time.Duration(d.Spec.TimeoutSeconds)*time.Second)
		defer cancel()
		if _, err := p.Publish(pctx, e.Subject, e.Type, e.Payload, e.ID, e.OccurredAt); err != nil {
			msg := sanitize(err)
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") {
				msg += " (a publish outside the client's grants also times out: check publish:evt.harborpilot.>)"
			}
			return outbound.Result{Retryable: true, Err: msg}
		}
		return outbound.Result{OK: true}
	}
	return outbound.Result{Err: "unknown destination type"}
}

func sanitize(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (r *Relay) publisher(ctx context.Context, d *cp.Res[cp.DestinationSpec]) (Publisher, error) {
	r.mu.Lock()
	if e, ok := r.pubs[d.ID]; ok && e.rev == d.Revision {
		r.mu.Unlock()
		return e.p, nil
	}
	if e, ok := r.pubs[d.ID]; ok {
		e.p.Close()
		delete(r.pubs, d.ID)
	}
	r.mu.Unlock()
	var creds, ca []byte
	if s, err := r.Secret(ctx, d.ID, "creds"); err == nil && !s.IsZero() {
		creds = []byte(s.Reveal())
	} else if strings.HasPrefix(d.Spec.CredsRef, "file:") && r.ReadFile != nil {
		if creds, err = r.ReadFile(strings.TrimPrefix(d.Spec.CredsRef, "file:")); err != nil {
			return nil, errors.New("creds file is not readable")
		}
	}
	if strings.HasPrefix(d.Spec.CARef, "file:") && r.ReadFile != nil {
		var err error
		if ca, err = r.ReadFile(strings.TrimPrefix(d.Spec.CARef, "file:")); err != nil {
			return nil, errors.New("CA file is not readable")
		}
	}
	p, err := r.Pulse(d.Spec.PulseURL, creds, ca, d.Spec.PulseSource)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.pubs[d.ID] = pubEntry{rev: d.Revision, p: p}
	r.mu.Unlock()
	return p, nil
}

// Delivery is one delivery row.
type Delivery struct {
	ID          string     `json:"id"`
	EventSeq    int64      `json:"event_seq"`
	Consumer    string     `json:"consumer"`
	EventID     string     `json:"event_id"`
	EventType   string     `json:"event_type"`
	OccurredAt  time.Time  `json:"occurred_at"`
	Status      string     `json:"status" enum:"pending,deferred,delivered,dead"`
	Attempts    int        `json:"attempts"`
	NextAttempt time.Time  `json:"next_attempt_at"`
	LastStatus  *int64     `json:"last_status"`
	LastError   string     `json:"last_error,omitempty" required:"false"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

// Deliveries lists delivery history for a destination, newest event first.
// beforeSeq (0 = none) continues a previous page; limit is not clamped
// here (callers fetch one extra row to detect a next page).
func (r *Relay) Deliveries(ctx context.Context, destID, status string, beforeSeq int64, limit int) ([]Delivery, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT d.id, e.seq, d.consumer, e.id, e.type, e.occurred_at, d.status, d.attempts, d.next_attempt_at, d.last_status, COALESCE(d.last_error,''), d.delivered_at
		FROM event_deliveries d JOIN event_outbox e ON e.seq = d.event_seq WHERE d.consumer = ?`
	args := []any{consumer(destID)}
	if status != "" {
		q += ` AND d.status = ?`
		args = append(args, status)
	}
	if beforeSeq > 0 {
		q += ` AND e.seq < ?`
		args = append(args, beforeSeq)
	}
	q += ` ORDER BY e.seq DESC LIMIT ?`
	args = append(args, limit)
	return store.Query(ctx, r.DB.R, func(row *sql.Rows) (Delivery, error) {
		var x Delivery
		var at, next int64
		var ls, del sql.NullInt64
		err := row.Scan(&x.ID, &x.EventSeq, &x.Consumer, &x.EventID, &x.EventType, &at, &x.Status, &x.Attempts, &next, &ls, &x.LastError, &del)
		x.OccurredAt, x.NextAttempt = time.UnixMilli(at).UTC(), time.UnixMilli(next).UTC()
		if ls.Valid {
			x.LastStatus = &ls.Int64
		}
		if del.Valid {
			t := time.UnixMilli(del.Int64).UTC()
			x.DeliveredAt = &t
		}
		return x, err
	}, q, args...)
}

// Replay re-queues dead deliveries (all, or one) of a destination. It
// returns how many were re-queued.
func (r *Relay) Replay(ctx context.Context, destID, deliveryID string) (int64, error) {
	q := `UPDATE event_deliveries SET status = 'pending', attempts = 0, next_attempt_at = ?, last_error = NULL WHERE consumer = ? AND status = 'dead'`
	args := []any{r.Clock.Now().UnixMilli(), consumer(destID)}
	if deliveryID != "" {
		q += ` AND id = ?`
		args = append(args, deliveryID)
	}
	res, err := r.DB.W.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	select {
	case r.Wake <- struct{}{}:
	default:
	}
	return n, nil
}

// ReplaySince queues events since a time for a destination (at-least-once:
// receivers deduplicate by event id).
func (r *Relay) ReplaySince(ctx context.Context, destID string, since time.Time) (int64, error) {
	snap := r.Snapshot()
	d := snap.Destinations[destID]
	if d == nil {
		return 0, errors.New("unknown destination")
	}
	now := r.Clock.Now()
	var n int64
	err := r.DB.Tx(ctx, func(tx *sql.Tx) error {
		evs, err := store.Query(ctx, tx, scanEvent, `SELECT seq, id, type, subject, occurred_at, severity, COALESCE(dedupe_key,''), payload
			FROM event_outbox WHERE occurred_at >= ? ORDER BY seq LIMIT 5000`, since.UnixMilli())
		if err != nil {
			return err
		}
		for _, e := range evs {
			if !d.Spec.Filter.Matches(e.Name, e.Severity, e.Subjects) {
				continue
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO event_deliveries(id, consumer, event_seq, status, attempts, next_attempt_at, created_at)
				VALUES (?, ?, ?, 'pending', 0, ?, ?) ON CONFLICT(consumer, event_seq) DO UPDATE SET status = 'pending', attempts = 0, next_attempt_at = excluded.next_attempt_at`,
				ids.NewAt(ids.Delivery, now), consumer(destID), e.Seq, now.UnixMilli(), now.UnixMilli())
			if err != nil {
				return err
			}
			c, _ := res.RowsAffected()
			n += c
		}
		return nil
	})
	return n, err
}
