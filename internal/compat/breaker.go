// Package compat implements compatibility circuit breakers and Protocol
// Watch.
//
// Breakers are scoped narrowly: one per provider and protocol path
// (inbound protocol to upstream protocol). Only compatibility failures
// count (unparseable upstream output, schema rejections); authentication
// errors, rate limits and outages do not. A breaker opens after repeated
// failures within a window, stays open for a cooldown, then lets one real
// request through in normal mode (half-open). Success closes it after
// consecutive successes; failure re-opens it with a longer cooldown.
// Operators can force a breaker open or closed.
package compat

import (
	"context"
	"database/sql"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Breaker states.
const (
	Closed   = "closed"
	Open     = "open"
	HalfOpen = "half_open"
)

// Overrides.
const (
	OverrideAuto        = "auto"
	OverrideForceOpen   = "force_open"
	OverrideForceClosed = "force_closed"
)

// Failure classes.
const (
	ClassCompat     = "compatibility"
	ClassCapability = "capability"
	ClassAuth       = "authentication"
	ClassRateLimit  = "rate_limit"
	ClassOutage     = "outage"
	ClassClient     = "client"
	ClassOther      = "other"
)

var schemaRejection = regexp.MustCompile(`(?i)(unknown (field|parameter|property)|unrecognized (field|request argument|parameter)|extra (inputs|fields) (are )?not permitted|additional properties|unsupported (parameter|value|field)|invalid type|did not match (any|the) schema|unexpected (field|property|key)|cannot be parsed|is not a valid enum value)`)

// Classify separates compatibility failures from other failures.
func Classify(e *ir.Error) string {
	if e == nil {
		return ClassOther
	}
	switch e.Kind {
	case ir.ErrProtocol:
		return ClassCompat
	case ir.ErrAuthentication, ir.ErrPermission:
		return ClassAuth
	case ir.ErrRateLimited, ir.ErrQuotaExhausted, ir.ErrBudget:
		return ClassRateLimit
	case ir.ErrOverloaded, ir.ErrUpstream, ir.ErrTimeout:
		return ClassOutage
	case ir.ErrUnsupported:
		if strings.Contains(e.Message, "cannot provide") {
			return ClassCapability // static mismatch, not drift
		}
		if schemaRejection.MatchString(e.Message) {
			return ClassCompat
		}
		return ClassClient
	case ir.ErrInvalidRequest:
		if schemaRejection.MatchString(e.Message) {
			return ClassCompat
		}
		return ClassClient
	}
	return ClassOther
}

// Options tune breakers.
type Options struct {
	Threshold   int           // compat failures within Window that open (default 3)
	Window      time.Duration // default 5m
	Cooldown    time.Duration // first open period (default 2m)
	MaxCooldown time.Duration // default 1h
	CloseAfter  int           // consecutive half-open successes to close (default 2)
}

func (o *Options) defaults() {
	if o.Threshold <= 0 {
		o.Threshold = 3
	}
	if o.Window <= 0 {
		o.Window = 5 * time.Minute
	}
	if o.Cooldown <= 0 {
		o.Cooldown = 2 * time.Minute
	}
	if o.MaxCooldown <= 0 {
		o.MaxCooldown = time.Hour
	}
	if o.CloseAfter <= 0 {
		o.CloseAfter = 2
	}
}

// Breaker is one breaker's state.
type Breaker struct {
	Key            string     `json:"key"`
	ProviderID     string     `json:"provider_id"`
	Path           string     `json:"path"`
	State          string     `json:"state" enum:"closed,open,half_open"`
	Override       string     `json:"override" enum:"auto,force_open,force_closed"`
	OverrideReason string     `json:"override_reason,omitempty"`
	OpenedAt       *time.Time `json:"opened_at"`
	RetryAt        *time.Time `json:"retry_at"`
	Failures       int        `json:"recent_failures"`
	LastClass      string     `json:"last_class,omitempty"`
	LastReason     string     `json:"last_reason,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`

	recent   []time.Time
	cooldown time.Duration
	probing  bool
	success  int
}

// Effective returns the state after overrides.
func (b *Breaker) Effective() string {
	switch b.Override {
	case OverrideForceOpen:
		return Open
	case OverrideForceClosed:
		return Closed
	}
	return b.State
}

// Key builds a breaker key.
func Key(providerID string, inbound, upstream ir.Protocol) (string, string) {
	path := string(inbound) + ">" + string(upstream)
	return providerID + "|" + path, path
}

// Manager holds breakers in memory and persists transitions.
type Manager struct {
	DB    *store.DB
	Clock clock.Clock
	Opts  Options
	// OnTransition runs after a persisted state change (Protocol Watch
	// records restorations).
	OnTransition func(b Breaker, from, to string)

	mu sync.Mutex
	m  map[string]*Breaker
}

// NewManager loads persisted breakers.
func NewManager(ctx context.Context, db *store.DB, c clock.Clock, o Options) (*Manager, error) {
	o.defaults()
	if c == nil {
		c = clock.Real{}
	}
	m := &Manager{DB: db, Clock: c, Opts: o, m: map[string]*Breaker{}}
	rows, err := store.Query(ctx, db.R, func(r *sql.Rows) (*Breaker, error) {
		b := &Breaker{}
		var opened, retry, cool sql.NullInt64
		var reason, cls, last sql.NullString
		var upd int64
		err := r.Scan(&b.Key, &b.ProviderID, &b.Path, &b.State, &b.Override, &reason, &opened, &retry, &cool, &b.Failures, &cls, &last, &upd)
		b.OverrideReason, b.LastClass, b.LastReason = reason.String, cls.String, last.String
		b.OpenedAt, b.RetryAt = tptr(opened), tptr(retry)
		b.cooldown = time.Duration(cool.Int64) * time.Millisecond
		b.UpdatedAt = time.UnixMilli(upd).UTC()
		return b, err
	}, `SELECT key, provider_id, path, state, override, override_reason, opened_at, retry_at, cooldown_ms, failures, last_class, last_reason, updated_at FROM compat_breakers`)
	if err != nil {
		return nil, err
	}
	for _, b := range rows {
		if b.State == HalfOpen {
			b.State = Open // a probe in flight at shutdown did not finish
		}
		m.m[b.Key] = b
	}
	return m, nil
}

func tptr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

func ms(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func (m *Manager) get(key, providerID, path string) *Breaker {
	b := m.m[key]
	if b == nil {
		b = &Breaker{Key: key, ProviderID: providerID, Path: path, State: Closed, Override: OverrideAuto}
		m.m[key] = b
	}
	return b
}

// Decision is what a request may do on a path.
type Decision struct {
	State string
	// Probe is true when this request is the half-open probe.
	Probe bool
}

// Check returns the breaker decision for a path. In half-open state only
// one request at a time is let through as the probe.
func (m *Manager) Check(providerID string, inbound, upstream ir.Protocol) Decision {
	key, _ := Key(providerID, inbound, upstream)
	m.mu.Lock()
	b := m.m[key]
	if b == nil {
		m.mu.Unlock()
		return Decision{State: Closed}
	}
	now := m.Clock.Now()
	var transition *Breaker
	from := ""
	if b.Override == OverrideAuto && b.State == Open && b.RetryAt != nil && !now.Before(*b.RetryAt) {
		from = b.State
		b.State, b.success = HalfOpen, 0
		cp := *b
		transition = &cp
	}
	d := Decision{State: b.Effective()}
	if d.State == HalfOpen {
		if b.probing {
			d.State = Open
		} else {
			b.probing, d.Probe = true, true
		}
	}
	m.mu.Unlock()
	if transition != nil {
		m.persist(*transition, from, HalfOpen, "")
	}
	return d
}

// Record reports an attempt outcome on a path. err is nil on success.
func (m *Manager) Record(providerID string, inbound, upstream ir.Protocol, probe bool, err *ir.Error) {
	key, path := Key(providerID, inbound, upstream)
	class := ClassOther
	if err != nil {
		class = Classify(err)
		if class != ClassCompat {
			if probe {
				m.mu.Lock()
				if b := m.m[key]; b != nil {
					b.probing = false
				}
				m.mu.Unlock()
			}
			return
		}
	}
	m.mu.Lock()
	if err == nil && m.m[key] == nil {
		m.mu.Unlock()
		return
	}
	b := m.get(key, providerID, path)
	now := m.Clock.Now()
	from := b.State
	to := from
	reason := ""
	if err != nil {
		reason = truncate(err.Message, 200)
		b.LastClass, b.LastReason = class, reason
		cut := now.Add(-m.Opts.Window)
		kept := b.recent[:0]
		for _, t := range b.recent {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		b.recent = append(kept, now)
		b.Failures = len(b.recent)
		switch b.State {
		case HalfOpen:
			to = Open
			b.cooldown = min(max(b.cooldown*2, m.Opts.Cooldown), m.Opts.MaxCooldown)
		case Closed:
			if len(b.recent) >= m.Opts.Threshold {
				to = Open
				b.cooldown = m.Opts.Cooldown
			}
		}
		if to == Open && from != Open {
			t := now
			b.OpenedAt = &t
			r := now.Add(b.cooldown)
			b.RetryAt = &r
		}
		b.probing = false
	} else {
		switch b.State {
		case HalfOpen:
			b.probing = false
			b.success++
			if b.success >= m.Opts.CloseAfter {
				to = Closed
				b.recent, b.Failures, b.OpenedAt, b.RetryAt, b.cooldown = nil, 0, nil, nil, 0
			}
		case Closed:
			b.recent, b.Failures = nil, 0
		}
	}
	b.State = to
	b.UpdatedAt = now
	snap := *b
	m.mu.Unlock()
	if to != from || err != nil {
		m.persist(snap, from, to, reason)
	}
}

// SetOverride forces a breaker open or closed (or back to automatic).
func (m *Manager) SetOverride(ctx context.Context, providerID, path, override, reason string) (Breaker, error) {
	key := providerID + "|" + path
	m.mu.Lock()
	b := m.get(key, providerID, path)
	from := b.Effective()
	b.Override, b.OverrideReason = override, reason
	if override == OverrideAuto && b.State == HalfOpen {
		b.probing = false
	}
	b.UpdatedAt = m.Clock.Now()
	to := b.Effective()
	snap := *b
	m.mu.Unlock()
	m.persist(snap, from, to, "manual override: "+override)
	return snap, nil
}

// List returns every known breaker.
func (m *Manager) List() []Breaker {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Breaker, 0, len(m.m))
	for _, b := range m.m {
		c := *b
		c.State = b.State
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (m *Manager) persist(b Breaker, from, to, reason string) {
	ctx := context.Background()
	err := m.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO compat_breakers(key, provider_id, path, state, override, override_reason, opened_at, retry_at, cooldown_ms,
			failures, last_class, last_reason, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET state = excluded.state, override = excluded.override, override_reason = excluded.override_reason,
			opened_at = excluded.opened_at, retry_at = excluded.retry_at, cooldown_ms = excluded.cooldown_ms, failures = excluded.failures,
			last_class = excluded.last_class, last_reason = excluded.last_reason, updated_at = excluded.updated_at`,
			b.Key, b.ProviderID, b.Path, b.State, b.Override, nullable(b.OverrideReason), ms(b.OpenedAt), ms(b.RetryAt), b.cooldown.Milliseconds(),
			b.Failures, nullable(b.LastClass), nullable(b.LastReason), b.UpdatedAt.UnixMilli()); err != nil {
			return err
		}
		if from == to {
			return nil
		}
		action := map[string]string{Open: "opened", HalfOpen: "half_opened", Closed: "closed"}[to]
		sev := events.SeverityWarning
		if to == Closed {
			sev = events.SeverityInfo
		}
		_, err := events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "compat", Entity: "breaker", Action: action}, Severity: sev,
			OccurredAt: b.UpdatedAt, DedupeKey: "breaker:" + b.Key, Subjects: map[string]string{"provider_id": b.ProviderID},
			Data: map[string]any{"path": b.Path, "from": from, "to": to, "reason": reason, "override": b.Override, "retry_at": b.RetryAt}})
		return err
	})
	if err == nil && from != to && m.OnTransition != nil {
		m.OnTransition(b, from, to)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// BeginProbe moves a breaker to half-open for an explicitly authorized
// canary; it reports false when the breaker is closed or already probing.
func (m *Manager) BeginProbe(providerID, path string) bool {
	key := providerID + "|" + path
	m.mu.Lock()
	b := m.m[key]
	if b == nil || b.State == Closed || b.probing {
		m.mu.Unlock()
		return false
	}
	from := b.State
	b.State, b.probing, b.UpdatedAt = HalfOpen, true, m.Clock.Now()
	snap := *b
	m.mu.Unlock()
	if from != HalfOpen {
		m.persist(snap, from, HalfOpen, "live canary")
	}
	return true
}

// ProbeResult settles an explicit canary: success closes the breaker,
// failure re-opens it with a longer cooldown.
func (m *Manager) ProbeResult(providerID, path string, err *ir.Error) Breaker {
	key := providerID + "|" + path
	m.mu.Lock()
	b := m.get(key, providerID, path)
	now := m.Clock.Now()
	from := b.State
	b.probing = false
	reason := "live canary succeeded"
	if err == nil {
		b.State, b.recent, b.Failures, b.OpenedAt, b.RetryAt, b.cooldown, b.success = Closed, nil, 0, nil, nil, 0, 0
	} else {
		reason = "live canary failed: " + truncate(err.Message, 160)
		b.State = Open
		b.cooldown = min(max(b.cooldown*2, m.Opts.Cooldown), m.Opts.MaxCooldown)
		r := now.Add(b.cooldown)
		b.RetryAt = &r
		b.LastClass, b.LastReason = Classify(err), truncate(err.Message, 200)
	}
	b.UpdatedAt = now
	snap := *b
	m.mu.Unlock()
	m.persist(snap, from, snap.State, reason)
	return snap
}
