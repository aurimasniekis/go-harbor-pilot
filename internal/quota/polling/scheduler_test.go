package polling

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

type fakeAdapter struct {
	mu      sync.Mutex
	calls   int
	errs    []error // returned in order; nil = success
	block   chan struct{}
	started chan struct{}
}

func (f *fakeAdapter) Info() domain.ProviderInfo              { return domain.ProviderInfo{ID: "fake"} }
func (f *fakeAdapter) Validate(providers.AccountConfig) error { return nil }
func (f *fakeAdapter) Fetch(ctx context.Context, _ providers.FetchRequest) (*providers.FetchResult, error) {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, providers.WrapError(domain.ErrInternal, "cancelled", ctx.Err())
		}
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	v := 50.0
	return &providers.FetchResult{Buckets: []domain.BucketObservation{{
		Spec: domain.BucketSpec{Key: "k", Unit: domain.UnitPercent}, Remaining: &v,
	}}}, nil
}

type recSink struct {
	mu      sync.Mutex
	results []Result
}

func (r *recSink) RecordPoll(_ context.Context, res Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
	return nil
}

func (r *recSink) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.results)
}

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func newTestScheduler(clk clock.Clock, sink Sink, opt Options) *Scheduler {
	opt.Rand = rand.New(rand.NewPCG(1, 2))
	if opt.Jitter == 0 {
		opt.Jitter = 0.0001
	}
	return New(clk, sink, slog.New(slog.NewTextHandler(io.Discard, nil)), opt, nil)
}

func target(id string, a providers.Adapter, gen int64) Target {
	return Target{
		Config:     providers.AccountConfig{Account: domain.Account{ID: domain.AccountID(id), ProviderID: "fake", Enabled: true}},
		Adapter:    a,
		Interval:   time.Minute,
		Generation: gen,
	}
}

// drain runs queued polls synchronously.
func drain(ctx context.Context, s *Scheduler) {
	for {
		select {
		case id := <-s.queue:
			s.poll(ctx, id)
		default:
			return
		}
	}
}

func TestSchedulerSuccessReschedules(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	s := newTestScheduler(clk, sink, Options{})
	s.SetTargets([]Target{target("a", &fakeAdapter{}, 1)}, nil)
	clk.Advance(15 * time.Second)
	s.dispatchDue()
	drain(context.Background(), s)
	if sink.len() != 1 {
		t.Fatalf("want 1 result, got %d", sink.len())
	}
	next, _ := s.NextAttempt("a")
	if d := next.Sub(clk.Now()); d < 59*time.Second || d > 61*time.Second {
		t.Fatalf("next attempt in %v, want ~1m", d)
	}
}

func TestSchedulerSingleFlight(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	ad := &fakeAdapter{block: make(chan struct{}), started: make(chan struct{}, 4)}
	s := newTestScheduler(clk, sink, Options{})
	s.SetTargets([]Target{target("a", ad, 1)}, nil)
	clk.Advance(15 * time.Second)
	s.dispatchDue()
	id := <-s.queue
	done := make(chan struct{})
	go func() { s.poll(context.Background(), id); close(done) }()
	<-ad.started
	if st := s.Refresh("a"); st != RefreshInProgress {
		t.Fatalf("refresh during in-flight poll = %s", st)
	}
	s.dispatchDue()
	if len(s.queue) != 0 {
		t.Fatal("in-flight account was enqueued twice")
	}
	close(ad.block)
	<-done
	if ad.calls != 1 {
		t.Fatalf("adapter called %d times", ad.calls)
	}
}

func TestSchedulerManualRefreshThrottled(t *testing.T) {
	clk := clock.NewFake(t0)
	s := newTestScheduler(clk, &recSink{}, Options{ManualMinInterval: 30 * time.Second})
	s.SetTargets([]Target{target("a", &fakeAdapter{}, 1)}, nil)
	if st := s.Refresh("a"); st != RefreshQueued {
		t.Fatalf("first refresh = %s", st)
	}
	s.dispatchDue()
	drain(context.Background(), s)
	if st := s.Refresh("a"); st != RefreshThrottled {
		t.Fatalf("second refresh = %s", st)
	}
	clk.Advance(31 * time.Second)
	if st := s.Refresh("a"); st != RefreshQueued {
		t.Fatalf("refresh after interval = %s", st)
	}
	if st := s.Refresh("missing"); st != RefreshUnknown {
		t.Fatalf("unknown = %s", st)
	}
}

func TestSchedulerRetryAfterAndBackoff(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMin time.Duration
		wantMax time.Duration
		auth    AuthState
	}{
		{
			name:    "retry-after longer than backoff wins",
			err:     &providers.Error{Kind: domain.ErrRateLimited, RetryAfter: 10 * time.Minute},
			wantMin: 10 * time.Minute, wantMax: 10*time.Minute + time.Second,
		},
		{
			name:    "transient failure uses base backoff",
			err:     &providers.Error{Kind: domain.ErrUnavailable},
			wantMin: 29 * time.Second, wantMax: 31 * time.Second,
		},
		{
			name:    "auth failure uses long cooldown",
			err:     &providers.Error{Kind: domain.ErrAuthentication},
			wantMin: time.Hour, wantMax: time.Hour,
			auth: AuthFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := clock.NewFake(t0)
			sink := &recSink{}
			s := newTestScheduler(clk, sink, Options{BackoffBase: 30 * time.Second})
			s.SetTargets([]Target{target("a", &fakeAdapter{errs: []error{tt.err}}, 1)}, nil)
			clk.Advance(15 * time.Second)
			s.dispatchDue()
			drain(context.Background(), s)
			next, _ := s.NextAttempt("a")
			if d := next.Sub(clk.Now()); d < tt.wantMin || d > tt.wantMax {
				t.Fatalf("next in %v, want [%v,%v]", d, tt.wantMin, tt.wantMax)
			}
			res := sink.results[0]
			if res.Snapshot.Error == nil {
				t.Fatal("failure must be recorded as an error, not data")
			}
			if len(res.Snapshot.Buckets) != 0 {
				t.Fatal("failed fetch must not produce bucket values")
			}
			if tt.auth != "" && res.State.AuthState != tt.auth {
				t.Fatalf("auth state = %s", res.State.AuthState)
			}
		})
	}
}

func TestSchedulerExponentialBackoffGrows(t *testing.T) {
	clk := clock.NewFake(t0)
	errs := []error{
		&providers.Error{Kind: domain.ErrNetwork},
		&providers.Error{Kind: domain.ErrNetwork},
		&providers.Error{Kind: domain.ErrNetwork},
	}
	s := newTestScheduler(clk, &recSink{}, Options{BackoffBase: 10 * time.Second})
	s.SetTargets([]Target{target("a", &fakeAdapter{errs: errs}, 1)}, nil)
	var waits []time.Duration
	clk.Advance(15 * time.Second)
	for range 3 {
		s.dispatchDue()
		drain(context.Background(), s)
		next, _ := s.NextAttempt("a")
		waits = append(waits, next.Sub(clk.Now()))
		clk.Set(next)
	}
	if waits[0] >= waits[1] || waits[1] >= waits[2] {
		t.Fatalf("backoff not increasing: %v", waits)
	}
}

func TestSchedulerDiscardsResultAfterReload(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	ad := &fakeAdapter{block: make(chan struct{}), started: make(chan struct{}, 1)}
	s := newTestScheduler(clk, sink, Options{})
	s.SetTargets([]Target{target("a", ad, 1)}, nil)
	clk.Advance(15 * time.Second)
	s.dispatchDue()
	id := <-s.queue
	done := make(chan struct{})
	go func() { s.poll(context.Background(), id); close(done) }()
	<-ad.started
	// Reload with a new generation while the old poll is in flight.
	s.SetTargets([]Target{target("a", &fakeAdapter{}, 2)}, nil)
	close(ad.block)
	<-done
	if sink.len() != 0 {
		t.Fatal("result from previous configuration generation must be discarded")
	}
}

func TestSchedulerRemovedTargetNotPolled(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	s := newTestScheduler(clk, sink, Options{})
	s.SetTargets([]Target{target("a", &fakeAdapter{}, 1)}, nil)
	s.SetTargets(nil, nil)
	clk.Advance(time.Hour)
	s.dispatchDue()
	drain(context.Background(), s)
	if sink.len() != 0 {
		t.Fatal("removed account was polled")
	}
}

func TestSchedulerCancellationNotRecordedAsFailure(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	ad := &fakeAdapter{block: make(chan struct{}), started: make(chan struct{}, 1)}
	s := newTestScheduler(clk, sink, Options{})
	s.SetTargets([]Target{target("a", ad, 1)}, nil)
	clk.Advance(15 * time.Second)
	s.dispatchDue()
	id := <-s.queue
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.poll(ctx, id); close(done) }()
	<-ad.started
	cancel()
	<-done
	if sink.len() != 0 {
		t.Fatal("shutdown cancellation must not be persisted as a failed poll")
	}
	if s.InFlight("a") {
		t.Fatal("in-flight flag not cleared")
	}
}

func TestSchedulerProviderBudget(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	s := newTestScheduler(clk, sink, Options{ProviderBudgets: map[domain.ProviderID]int{"fake": 2}})
	s.SetTargets([]Target{target("a", &fakeAdapter{}, 1), target("b", &fakeAdapter{}, 1), target("c", &fakeAdapter{}, 1)}, nil)
	clk.Advance(15 * time.Second)
	s.dispatchDue()
	if got := len(s.queue); got != 2 {
		t.Fatalf("queued %d polls, want 2 under a 2/min budget", got)
	}
}

func TestSchedulerPersistedCooldownSurvivesRestart(t *testing.T) {
	clk := clock.NewFake(t0)
	cool := t0.Add(40 * time.Minute)
	s := newTestScheduler(clk, &recSink{}, Options{})
	s.SetTargets([]Target{target("a", &fakeAdapter{}, 1)}, map[domain.AccountID]PollState{
		"a": {AccountID: "a", AuthState: AuthFailed, CooldownUntil: &cool},
	})
	next, _ := s.NextAttempt("a")
	if !next.Equal(cool) {
		t.Fatalf("next = %v, want cooldown end %v", next, cool)
	}
	if st := s.Refresh("a"); st != RefreshCooldown {
		t.Fatalf("manual refresh during auth cooldown = %s", st)
	}
}
