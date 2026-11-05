// Package polling schedules account polls with bounded concurrency, jitter,
// backoff, authentication cooldowns, per-provider request budgets and
// single-flight execution per account.
package polling

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Target is one schedulable account.
type Target struct {
	Config   providers.AccountConfig
	Adapter  providers.Adapter
	Interval time.Duration
	// Generation is the configuration generation the target was built from.
	Generation int64
}

// Sink receives poll outcomes. Implementations persist and publish them.
type Sink interface {
	RecordPoll(ctx context.Context, res Result) error
}

// Result is a completed poll.
type Result struct {
	Account  domain.Account
	Snapshot domain.Snapshot
	Fetch    *providers.FetchResult
	State    PollState
	Manual   bool
}

// Options configures the scheduler.
type Options struct {
	Workers int
	// QueueSize bounds pending polls.
	QueueSize int
	// Jitter is the ± fraction applied to intervals (0.1 = ±10%).
	Jitter float64
	// BackoffBase/Max bound transient failure backoff.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// AuthCooldown is applied after authentication/permission failures.
	AuthCooldown time.Duration
	// FetchTimeout bounds a single adapter call.
	FetchTimeout time.Duration
	// ProviderBudgets limits requests per minute per provider (0 = unlimited).
	ProviderBudgets map[domain.ProviderID]int
	// ManualMinInterval is the minimum spacing of manual refreshes per account.
	ManualMinInterval time.Duration
	// Tick is how often due targets are checked.
	Tick time.Duration
	// Rand supplies jitter randomness (tests pass a seeded source).
	Rand *rand.Rand
}

func (o *Options) defaults() {
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.QueueSize <= 0 {
		o.QueueSize = 256
	}
	if o.Jitter < 0 || o.Jitter > 0.5 {
		o.Jitter = 0.1
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = 30 * time.Second
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = 30 * time.Minute
	}
	if o.AuthCooldown <= 0 {
		o.AuthCooldown = time.Hour
	}
	if o.FetchTimeout <= 0 {
		o.FetchTimeout = 30 * time.Second
	}
	if o.ManualMinInterval <= 0 {
		o.ManualMinInterval = 15 * time.Second
	}
	if o.Tick <= 0 {
		o.Tick = time.Second
	}
	if o.Rand == nil {
		o.Rand = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
}

type targetState struct {
	target     Target
	next       time.Time
	inflight   bool
	manual     bool
	lastManual time.Time
	state      PollState
}

// Scheduler runs polls.
type Scheduler struct {
	clk  clock.Clock
	sink Sink
	log  *slog.Logger
	opt  Options

	mu      sync.Mutex
	targets map[domain.AccountID]*targetState
	buckets map[domain.ProviderID]*tokenBucket
	queue   chan domain.AccountID
	wake    chan struct{}
	randMu  sync.Mutex
	metrics Metrics
}

// Metrics is an optional observer of scheduler activity.
type Metrics interface {
	PollCompleted(provider domain.ProviderID, kind string, d time.Duration)
	QueueDepth(n int)
}

type nopMetrics struct{}

func (nopMetrics) PollCompleted(domain.ProviderID, string, time.Duration) {}
func (nopMetrics) QueueDepth(int)                                         {}

// New creates a scheduler. Call SetTargets then Run.
func New(clk clock.Clock, sink Sink, log *slog.Logger, opt Options, m Metrics) *Scheduler {
	opt.defaults()
	if m == nil {
		m = nopMetrics{}
	}
	return &Scheduler{
		clk: clk, sink: sink, log: log, opt: opt, metrics: m,
		targets: map[domain.AccountID]*targetState{},
		buckets: map[domain.ProviderID]*tokenBucket{},
		queue:   make(chan domain.AccountID, opt.QueueSize),
		wake:    make(chan struct{}, 1),
	}
}

// SetTargets atomically replaces the scheduled set (on startup and reload).
// Persisted poll state seeds backoff/cooldown so restarts do not hammer a
// failing provider. Targets absent from the new set stop being polled;
// in-flight polls for them are discarded on completion.
func (s *Scheduler) SetTargets(targets []Target, persisted map[domain.AccountID]PollState) {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[domain.AccountID]*targetState, len(targets))
	for _, t := range targets {
		id := t.Config.Account.ID
		if !t.Config.Account.Enabled {
			continue
		}
		if old, ok := s.targets[id]; ok {
			if old.target.Generation != t.Generation && old.state.CooldownUntil != nil &&
				old.state.AuthState != AuthOK {
				// A reload may carry rotated credentials: retry once now
				// instead of waiting out the authentication cooldown.
				old.state.CooldownUntil = nil
				old.next = now
			}
			old.target = t
			next[id] = old
			continue
		}
		ts := &targetState{target: t, next: now.Add(s.initialDelay(t.Interval))}
		if ps, ok := persisted[id]; ok {
			ts.state = ps
			if ps.NextAttemptAt != nil && ps.NextAttemptAt.After(now) {
				ts.next = *ps.NextAttemptAt
			}
			if ps.CooldownUntil != nil && ps.CooldownUntil.After(ts.next) {
				ts.next = *ps.CooldownUntil
			}
		} else {
			ts.state = PollState{AccountID: id, AuthState: AuthUnknown}
		}
		next[id] = ts
	}
	s.targets = next
	s.signal()
}

// ReseedStates replaces in-memory poll state from persisted state for
// targets that are not currently polling (used after demo backfill).
func (s *Scheduler) ReseedStates(persisted map[domain.AccountID]PollState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ts := range s.targets {
		if ps, ok := persisted[id]; ok && !ts.inflight {
			ts.state = ps
		}
	}
}

func (s *Scheduler) initialDelay(interval time.Duration) time.Duration {
	// Spread the first polls across up to 10% of the interval (max 10s).
	spread := min(interval/10, 10*time.Second)
	if spread <= 0 {
		return 0
	}
	s.randMu.Lock()
	defer s.randMu.Unlock()
	return time.Duration(s.opt.Rand.Int64N(int64(spread)))
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// RefreshStatus is the outcome of a manual refresh request.
type RefreshStatus string

// Refresh statuses.
const (
	RefreshQueued     RefreshStatus = "queued"
	RefreshInProgress RefreshStatus = "already_in_progress"
	RefreshThrottled  RefreshStatus = "throttled"
	RefreshUnknown    RefreshStatus = "unknown_account"
	RefreshCooldown   RefreshStatus = "auth_cooldown"
)

// Refresh requests an immediate poll of one account. Duplicate requests are
// deduplicated (single-flight) and spaced by ManualMinInterval.
func (s *Scheduler) Refresh(id domain.AccountID) RefreshStatus {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.targets[id]
	if !ok {
		return RefreshUnknown
	}
	if ts.inflight {
		return RefreshInProgress
	}
	if !ts.lastManual.IsZero() && now.Sub(ts.lastManual) < s.opt.ManualMinInterval {
		return RefreshThrottled
	}
	if ts.state.CooldownUntil != nil && ts.state.CooldownUntil.After(now) && ts.state.AuthState != AuthOK {
		// Allow one manual retry after rotating credentials: only when the
		// configuration generation changed is the cooldown lifted (see reload).
		return RefreshCooldown
	}
	ts.lastManual = now
	ts.manual = true
	ts.next = now
	s.signal()
	return RefreshQueued
}

// RefreshMany refreshes a set of accounts and returns per-account statuses.
func (s *Scheduler) RefreshMany(ids []domain.AccountID) map[domain.AccountID]RefreshStatus {
	out := make(map[domain.AccountID]RefreshStatus, len(ids))
	for _, id := range ids {
		out[id] = s.Refresh(id)
	}
	return out
}

// AccountIDs lists scheduled accounts, optionally filtered by provider.
func (s *Scheduler) AccountIDs(provider domain.ProviderID) []domain.AccountID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.AccountID
	for id, ts := range s.targets {
		if provider == "" || ts.target.Config.Account.ProviderID == provider {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NextAttempt returns the next scheduled attempt for an account.
func (s *Scheduler) NextAttempt(id domain.AccountID) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.targets[id]
	if !ok {
		return time.Time{}, false
	}
	return ts.next, true
}

// InFlight reports whether a poll is running for the account.
func (s *Scheduler) InFlight(id domain.AccountID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.targets[id]
	return ok && ts.inflight
}

// Run dispatches due polls until ctx is cancelled, then waits for in-flight
// polls to observe cancellation.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range s.opt.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(ctx)
		}()
	}
	for {
		s.dispatchDue()
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-s.wake:
		case <-s.clk.After(s.opt.Tick):
		}
	}
}

// dispatchDue enqueues due targets without blocking.
func (s *Scheduler) dispatchDue() {
	now := s.clk.Now()
	s.mu.Lock()
	var due []*targetState
	for _, ts := range s.targets {
		if !ts.inflight && !ts.next.After(now) {
			due = append(due, ts)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].next.Before(due[j].next) })
	for _, ts := range due {
		id := ts.target.Config.Account.ID
		if wait := s.takeToken(ts.target.Config.Account.ProviderID, now); wait > 0 {
			ts.next = now.Add(wait)
			continue
		}
		select {
		case s.queue <- id:
			ts.inflight = true
		default:
			// Queue full; retry on the next tick.
		}
	}
	depth := len(s.queue)
	s.mu.Unlock()
	s.metrics.QueueDepth(depth)
}

func (s *Scheduler) takeToken(p domain.ProviderID, now time.Time) time.Duration {
	limit := s.opt.ProviderBudgets[p]
	if limit <= 0 {
		return 0
	}
	tb, ok := s.buckets[p]
	if !ok {
		tb = newTokenBucket(limit, now)
		s.buckets[p] = tb
	}
	return tb.take(now)
}

func (s *Scheduler) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			s.poll(ctx, id)
		}
	}
}

func (s *Scheduler) poll(ctx context.Context, id domain.AccountID) {
	s.mu.Lock()
	ts, ok := s.targets[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	target := ts.target
	manual := ts.manual
	ts.manual = false
	prev := ts.state
	s.mu.Unlock()

	start := s.clk.Now()
	fctx, cancel := context.WithTimeout(ctx, s.opt.FetchTimeout)
	res, err := target.Adapter.Fetch(fctx, providers.FetchRequest{Config: target.Config, Now: start})
	cancel()
	end := s.clk.Now()
	latency := end.Sub(start)
	if ctx.Err() != nil {
		// Shutting down: do not record cancellation as a provider failure.
		s.mu.Lock()
		if cur, ok := s.targets[id]; ok {
			cur.inflight = false
		}
		s.mu.Unlock()
		return
	}

	snap := domain.Snapshot{
		AccountID: id, FetchedAt: start, Latency: latency, ConfigGeneration: target.Generation,
	}
	state := prev
	state.AccountID = id
	state.LastAttemptAt = &start
	lat := latency.Milliseconds()
	state.LastLatencyMS = &lat
	var next time.Time
	kind := "success"
	if err != nil {
		pe := providers.AsError(err)
		snap.Error = pe.ToFetchError()
		state.LastError = snap.Error
		state.ConsecutiveFailures++
		kind = string(pe.Kind)
		switch pe.Kind {
		case domain.ErrAuthentication, domain.ErrPermission:
			if pe.Kind == domain.ErrAuthentication {
				state.AuthState = AuthFailed
			} else {
				state.AuthState = AuthDenied
			}
			cool := end.Add(s.opt.AuthCooldown)
			state.CooldownUntil = &cool
			next = cool
		case domain.ErrConfiguration, domain.ErrUnsupported:
			next = end.Add(max(s.opt.AuthCooldown, target.Interval))
		default:
			back := s.backoff(state.ConsecutiveFailures)
			if pe.RetryAfter > back {
				back = pe.RetryAfter
			}
			next = end.Add(back)
		}
	} else {
		snap.SourceTime = res.SourceTime
		snap.Buckets = res.Buckets
		snap.Identity = res.MaskedIdentifier
		state.LastSuccessAt = &start
		state.ConsecutiveFailures = 0
		state.LastError = nil
		state.AuthState = AuthOK
		state.CooldownUntil = nil
		next = end.Add(s.jittered(target.Interval))
	}
	state.NextAttemptAt = &next
	s.metrics.PollCompleted(target.Config.Account.ProviderID, kind, latency)

	// Generation guard: discard results for targets removed or reconfigured
	// while this poll was in flight.
	s.mu.Lock()
	cur, ok := s.targets[id]
	stale := !ok || cur.target.Generation != target.Generation
	if ok {
		cur.inflight = false
		if !stale {
			cur.state = state
			cur.next = next
		}
	}
	s.mu.Unlock()
	if stale {
		s.log.Info("poll.result.discarded", "account_id", id, "reason", "configuration changed during poll")
		s.signal()
		return
	}
	rec := Result{Account: target.Config.Account, Snapshot: snap, Fetch: res, State: state, Manual: manual}
	if err := s.sink.RecordPoll(ctx, rec); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("poll.persist.failed", "account_id", id, "error", err.Error())
	}
	if snap.Error != nil {
		s.log.Warn("poll.failed", "account_id", id, "provider_id", target.Config.Account.ProviderID,
			"error_kind", snap.Error.Kind, "consecutive_failures", state.ConsecutiveFailures, "next_attempt_at", next)
	} else {
		s.log.Debug("poll.succeeded", "account_id", id, "latency_ms", lat, "buckets", len(snap.Buckets))
	}
}

func (s *Scheduler) backoff(failures int) time.Duration {
	exp := math.Pow(2, float64(max(0, failures-1)))
	d := time.Duration(float64(s.opt.BackoffBase) * exp)
	if d > s.opt.BackoffMax || d <= 0 {
		d = s.opt.BackoffMax
	}
	return s.jittered(d)
}

func (s *Scheduler) jittered(d time.Duration) time.Duration {
	if s.opt.Jitter == 0 || d <= 0 {
		return d
	}
	s.randMu.Lock()
	r := s.opt.Rand.Float64()*2 - 1
	s.randMu.Unlock()
	return time.Duration(float64(d) * (1 + r*s.opt.Jitter))
}

// tokenBucket is a per-provider requests-per-minute limiter.
type tokenBucket struct {
	capacity float64
	tokens   float64
	perSec   float64
	last     time.Time
}

func newTokenBucket(perMinute int, now time.Time) *tokenBucket {
	return &tokenBucket{capacity: float64(perMinute), tokens: float64(perMinute), perSec: float64(perMinute) / 60, last: now}
}

// take consumes a token or returns how long to wait for one.
func (b *tokenBucket) take(now time.Time) time.Duration {
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(b.capacity, b.tokens+el*b.perSec)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) / b.perSec * float64(time.Second))
}
