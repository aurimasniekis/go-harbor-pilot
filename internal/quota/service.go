// Package quota monitors provider quotas and usage: it polls quota
// monitors through provider adapters, derives observations from gateway
// responses, keeps the latest state in memory for routing health, persists
// history for forecasts and emits transition events.
//
// Missing values stay unknown: a nil remaining value is never treated as
// zero, and stale observations are reported as stale rather than current.
package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/polling"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/balance"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/clientquota"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/codingplan"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/cursor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/demo"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/generic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/githubcopilot"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/nexos"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/openai"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/together"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers/xai"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Options configure the service.
type Options struct {
	DB      *store.DB
	CP      *cp.Service
	Keyring *crypto.Keyring
	Clock   clock.Clock
	Log     *slog.Logger
	Version string
	// Demo registers the synthetic demo adapters.
	Demo bool
	// DemoStart anchors the demo simulator.
	DemoStart time.Time
	// Transport overrides the adapters' outbound transport (tests).
	Transport http.RoundTripper
	// SecretRoots confines file-based credentials adapters read.
	SecretRoots []string
	// Scheduler tunes polling (tests).
	Scheduler polling.Options
}

// Service is the quota module.
type Service struct {
	o     Options
	reg   *providers.Registry
	sched *polling.Scheduler
	flow  *oauthflow.Flow
	sim   *demo.Simulator
	board *board

	headerQ chan headerObs

	mu      sync.Mutex
	syncRev int64
}

// New creates the service and registers adapters.
func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	ua := "harbor-pilot/" + o.Version
	httpc := providers.NewHTTPClient(providers.HTTPOptions{UserAgent: ua, Transport: o.Transport})
	tokens := &providers.TokenHolder{}
	files := &providers.FileReader{}
	files.SetRoots(o.SecretRoots)
	s := &Service{o: o, reg: providers.NewRegistry(), board: newBoard(), headerQ: make(chan headerObs, 1024)}
	if o.Keyring != nil {
		tokens.Set(&tokenStore{db: o.DB, keyring: o.Keyring, now: o.Clock.Now})
	}
	deps := providers.Deps{Now: o.Clock.Now, HTTP: httpc, Tokens: tokens, Files: files, Version: o.Version}
	s.flow = oauthflow.New(httpc, o.Clock.Now)
	for _, a := range []providers.Adapter{
		generic.New(), clientquota.New(), nexos.New(deps), anthropic.NewAdmin(deps), anthropic.NewSubscription(deps, s.flow),
		openai.NewAdmin(deps), openai.NewCodex(deps, s.flow), balance.NewDeepSeek(deps), balance.NewMoonshot(deps),
		balance.NewOpenRouter(deps, s.flow), xai.New(deps), together.New(deps), cursor.New(deps), githubcopilot.New(deps),
		gemini.New(deps), codingplan.NewZai(deps), codingplan.NewMiniMax(deps),
	} {
		s.reg.Register(a)
	}
	if o.Demo {
		start := o.DemoStart
		if start.IsZero() {
			start = o.Clock.Now()
		}
		s.sim = demo.NewSimulator(start)
		for _, a := range demo.NewAdapters(s.sim) {
			s.reg.Register(a)
		}
	}
	so := o.Scheduler
	s.sched = polling.New(o.Clock, sink{s}, o.Log, so, nil)
	cp.ValidateQuotaAdapter = s.validateMonitor
	return s
}

// Registry exposes the adapters (API).
func (s *Service) Registry() *providers.Registry { return s.reg }

// Simulator returns the demo simulator (nil outside demo mode).
func (s *Service) Simulator() *demo.Simulator { return s.sim }

func (s *Service) validateMonitor(v *cp.Validator, m *cp.QuotaMonitorSpec) {
	a, ok := s.reg.Get(domain.ProviderID(m.Adapter))
	if !ok {
		v.Fail("spec.adapter", "unknown quota adapter "+m.Adapter)
		return
	}
	info := a.Info()
	if info.Tier == domain.TierUnofficial && !m.AcknowledgeTermsRisk {
		v.Fail("spec.acknowledge_terms_risk", "this adapter reads undocumented provider endpoints; set acknowledge_terms_risk to confirm you accept that it may break or conflict with provider terms")
	}
	if info.Tier == domain.TierDemo && !s.o.Demo {
		v.Fail("spec.adapter", "demo adapters are only available in demo mode")
		return
	}
	// Check adapter options without network access. Credentials are
	// write-only and may be set after the monitor, so placeholders stand in.
	placeholder := map[string]credentials.Secret{}
	for _, f := range cp.QuotaMonitorDef.SecretFields() {
		placeholder[f.Name] = credentials.NewSecret("placeholder")
	}
	cfg := providers.AccountConfig{Account: domain.Account{ID: "validation", ProviderID: info.ID, Enabled: true, Tier: info.Tier, Demo: info.Tier == domain.TierDemo},
		Options: m.Options, Credentials: credentials.NewBundle(placeholder)}
	if err := a.Validate(cfg); err != nil {
		if pe := providers.AsError(err); pe.Kind == domain.ErrConfiguration {
			v.Fail("spec.options", pe.Message)
		}
	}
}

// Run polls monitors and writes header observations until ctx ends.
func (s *Service) Run(ctx context.Context) {
	if err := s.load(ctx); err != nil {
		s.o.Log.Error("quota.load.failed", "error", err.Error())
	}
	s.syncTargets(ctx)
	if s.o.Demo {
		// The demo may run on a frozen clock, under which scheduled polls
		// never come due; poll every demo monitor once at startup.
		if snap := s.o.CP.Snapshot(); snap != nil {
			for id := range snap.Monitors {
				s.Refresh(id)
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.sched.Run(ctx) }()
	go func() { defer wg.Done(); s.headerWriter(ctx) }()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-t.C:
			s.syncTargets(ctx)
		case <-prune.C:
			s.prune(ctx)
		}
	}
}

// SyncNow rebuilds poll targets from the current configuration (tests and
// API refresh).
func (s *Service) SyncNow(ctx context.Context) { s.syncTargets(ctx) }

func (s *Service) syncTargets(ctx context.Context) {
	snap := s.o.CP.Snapshot()
	if snap == nil {
		return
	}
	s.mu.Lock()
	if snap.ConfigRevision == s.syncRev {
		s.mu.Unlock()
		return
	}
	s.syncRev = snap.ConfigRevision
	s.mu.Unlock()
	targets := make([]polling.Target, 0, len(snap.Monitors))
	links := map[string]monitorLink{}
	for _, m := range snap.Monitors {
		a, ok := s.reg.Get(domain.ProviderID(m.Spec.Adapter))
		if !ok {
			continue
		}
		info := a.Info()
		iv := info.DefaultPollInterval
		if m.Spec.PollIntervalSecs > 0 {
			iv = time.Duration(m.Spec.PollIntervalSecs) * time.Second
		}
		if iv <= 0 {
			iv = 5 * time.Minute
		}
		links[m.ID] = monitorLink{accountID: m.Spec.AccountID, exhaustedBelow: m.Spec.ExhaustedBelow, staleAfter: m.Spec.StaleAfter(iv), name: m.Name}
		if !m.Spec.Enabled || info.PushOnly {
			continue
		}
		if info.Tier == domain.TierUnofficial && !m.Spec.AcknowledgeTermsRisk {
			continue
		}
		cfg := providers.AccountConfig{
			Account: domain.Account{ID: domain.AccountID(m.ID), ProviderID: info.ID, Alias: m.Name, Enabled: true, Tier: info.Tier,
				SharedScopeID: m.Spec.SharedScopeID, Demo: info.Tier == domain.TierDemo},
			Options:     m.Spec.Options,
			Credentials: s.resolveCredentials(ctx, m.ID),
		}
		targets = append(targets, polling.Target{Config: cfg, Adapter: a, Interval: iv, Generation: m.Revision})
	}
	s.board.setLinks(links)
	persisted, err := s.pollStates(ctx)
	if err != nil {
		s.o.Log.Warn("quota.poll_state.load_failed", "error", err.Error())
	}
	s.sched.SetTargets(targets, persisted)
}

func (s *Service) resolveCredentials(ctx context.Context, id string) credentials.Bundle {
	items := map[string]credentials.Secret{}
	for _, f := range cp.QuotaMonitorDef.SecretFields() {
		sec, err := s.o.CP.ResolveSecret(ctx, cp.QuotaMonitorDef, id, f.Name)
		if err == nil && !sec.IsZero() {
			items[f.Name] = sec
		}
	}
	return credentials.NewBundle(items)
}

// Refresh requests an immediate poll of a monitor.
func (s *Service) Refresh(id string) string {
	return string(s.sched.Refresh(domain.AccountID(id)))
}

// Health reports routing health for a provider account from quota state.
// It never blocks on I/O.
func (s *Service) Health(accountID string) routing.Health {
	return s.board.health(accountID, s.o.Clock.Now())
}

// sink persists poll results.
type sink struct{ s *Service }

func (k sink) RecordPoll(ctx context.Context, res polling.Result) error {
	s := k.s
	now := s.o.Clock.Now()
	id := string(res.Account.ID)
	link := s.board.link(id)
	before := s.board.exhausted(id, now)
	prevAuth := s.board.authState(id)
	var obs []bucketState
	if res.Snapshot.Error == nil {
		for _, b := range res.Snapshot.Buckets {
			obs = append(obs, bucketState{owner: id, monitorID: id, accountID: link.accountID, source: "adapter", obs: b, observedAt: res.Snapshot.FetchedAt})
		}
	}
	err := s.o.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := savePollState(ctx, tx, res.State, res.Snapshot.Identity, res.Fetch); err != nil {
			return err
		}
		for _, b := range obs {
			if err := upsertBucket(ctx, tx, b, true); err != nil {
				return err
			}
		}
		if res.Fetch != nil && res.Snapshot.Error == nil {
			for _, u := range res.Fetch.Usage {
				if err := upsertUsage(ctx, tx, id, u, now); err != nil {
					return err
				}
			}
		}
		// Events are transitions, not every poll.
		after := exhaustedAfter(obs, link, now)
		subjects := map[string]string{"monitor_id": id}
		if link.accountID != "" {
			subjects["account_id"] = link.accountID
		}
		if res.Snapshot.Error == nil {
			for key, b := range after {
				if _, was := before[key]; !was {
					if _, err := events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "quota", Entity: "window", Action: "exhausted"},
						Severity: events.SeverityWarning, OccurredAt: now, DedupeKey: "quota:" + id + ":" + key, Subjects: subjects,
						Data: map[string]any{"monitor": link.name, "bucket": key, "label": b.obs.Spec.Label, "reset_at": b.obs.ResetAt, "source": "adapter"}}); err != nil {
						return err
					}
				}
			}
			for key, b := range before {
				if _, still := after[key]; !still {
					if _, err := events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "quota", Entity: "window", Action: "recovered"},
						Severity: events.SeverityInfo, OccurredAt: now, DedupeKey: "quota:" + id + ":" + key, Subjects: subjects,
						Data: map[string]any{"monitor": link.name, "bucket": key, "label": b.obs.Spec.Label}}); err != nil {
						return err
					}
				}
			}
		}
		if res.State.AuthState != prevAuth && (res.State.AuthState == polling.AuthFailed || res.State.AuthState == polling.AuthDenied) {
			data := map[string]any{"monitor": link.name, "auth_state": string(res.State.AuthState)}
			if res.Snapshot.Error != nil {
				data["error_kind"] = string(res.Snapshot.Error.Kind)
			}
			if _, err := events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "quota", Entity: "monitor", Action: "auth_failed"},
				Severity: events.SeverityCritical, OccurredAt: now, DedupeKey: "quota-auth:" + id, Subjects: subjects, Data: data}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.board.setPoll(id, res.State, res.Snapshot.Identity)
	if res.Snapshot.Error == nil {
		s.board.replaceOwner(id, obs)
	}
	return nil
}

// exhaustedAfter returns the global buckets that are exhausted in obs.
func exhaustedAfter(obs []bucketState, link monitorLink, now time.Time) map[string]bucketState {
	out := map[string]bucketState{}
	for _, b := range obs {
		if isExhausted(b, link.exhaustedBelow, now) {
			out[b.obs.Spec.Key] = b
		}
	}
	return out
}

func isExhausted(b bucketState, below float64, now time.Time) bool {
	if !b.obs.Spec.Global && b.obs.Spec.Kind != domain.KindBalance && b.obs.Spec.Kind != domain.KindBudget {
		return false
	}
	if b.obs.ResetAt != nil && !b.obs.ResetAt.After(now) {
		return false // the window has reset since the observation
	}
	f := b.obs.RemainingFraction()
	if f == nil {
		return false
	}
	if below <= 0 {
		return *f <= 0
	}
	return *f <= below
}

// ---------- persistence helpers ----------

func ms(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func fl(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func savePollState(ctx context.Context, tx *sql.Tx, st polling.PollState, identity *string, fetch *providers.FetchResult) error {
	var kind, msg, status any
	if st.LastError != nil {
		kind, msg = string(st.LastError.Kind), st.LastError.Message
		if st.LastError.HTTPStatus != 0 {
			status = st.LastError.HTTPStatus
		}
	}
	notes := "[]"
	if fetch != nil && len(fetch.Notes) > 0 {
		b, _ := json.Marshal(fetch.Notes)
		notes = string(b)
	}
	var lat any
	if st.LastLatencyMS != nil {
		lat = *st.LastLatencyMS
	}
	var ident any
	if identity != nil {
		ident = *identity
	}
	auth := string(st.AuthState)
	if auth == "" {
		auth = string(polling.AuthUnknown)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO quota_poll_state(monitor_id, last_attempt_at, last_success_at, consecutive_failures, next_attempt_at,
		error_kind, error_message, error_http_status, auth_state, cooldown_until, last_latency_ms, masked_identity, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(monitor_id) DO UPDATE SET last_attempt_at = excluded.last_attempt_at, last_success_at = excluded.last_success_at,
		consecutive_failures = excluded.consecutive_failures, next_attempt_at = excluded.next_attempt_at, error_kind = excluded.error_kind,
		error_message = excluded.error_message, error_http_status = excluded.error_http_status, auth_state = excluded.auth_state,
		cooldown_until = excluded.cooldown_until, last_latency_ms = excluded.last_latency_ms,
		masked_identity = COALESCE(excluded.masked_identity, quota_poll_state.masked_identity), notes = excluded.notes`,
		string(st.AccountID), ms(st.LastAttemptAt), ms(st.LastSuccessAt), st.ConsecutiveFailures, ms(st.NextAttemptAt),
		kind, msg, status, auth, ms(st.CooldownUntil), lat, ident, notes)
	return err
}

type specJSON struct {
	domain.BucketSpec
	WindowSecs int64 `json:"window_secs,omitempty"`
	PeriodSecs int64 `json:"period_secs,omitempty"`
}

func encodeSpec(s domain.BucketSpec) string {
	b, _ := json.Marshal(specJSON{BucketSpec: s, WindowSecs: int64(s.Window / time.Second), PeriodSecs: int64(s.Period / time.Second)})
	return string(b)
}

func decodeSpec(raw string) domain.BucketSpec {
	var j specJSON
	_ = json.Unmarshal([]byte(raw), &j)
	s := j.BucketSpec
	s.Window = time.Duration(j.WindowSecs) * time.Second
	s.Period = time.Duration(j.PeriodSecs) * time.Second
	return s
}

func upsertBucket(ctx context.Context, ex store.Execer, b bucketState, history bool) error {
	o := b.obs
	var limE, usedE, remE any
	if o.LimitAmount != nil {
		limE = int64(*o.LimitAmount)
	}
	if o.UsedAmount != nil {
		usedE = int64(*o.UsedAmount)
	}
	if o.RemainingAmount != nil {
		remE = int64(*o.RemainingAmount)
	}
	quality := string(o.Quality)
	if quality == "" {
		quality = string(domain.QualityProviderReported)
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO quota_buckets(owner_key, bucket_key, monitor_id, account_id, source, spec, limit_value, used_value,
		remaining_value, remaining_fraction, limit_e10, used_e10, remaining_e10, reset_at, source_time, observed_at, quality)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key, bucket_key) DO UPDATE SET monitor_id = excluded.monitor_id, account_id = excluded.account_id, source = excluded.source,
		spec = excluded.spec, limit_value = excluded.limit_value, used_value = excluded.used_value, remaining_value = excluded.remaining_value,
		remaining_fraction = excluded.remaining_fraction, limit_e10 = excluded.limit_e10, used_e10 = excluded.used_e10,
		remaining_e10 = excluded.remaining_e10, reset_at = excluded.reset_at, source_time = excluded.source_time,
		observed_at = excluded.observed_at, quality = excluded.quality WHERE excluded.observed_at >= quota_buckets.observed_at`,
		b.owner, o.Spec.Key, nullStr(b.monitorID), nullStr(b.accountID), b.source, encodeSpec(o.Spec), fl(o.Limit), fl(o.Used),
		fl(o.Remaining), fl(o.RemainingFraction()), limE, usedE, remE, ms(o.ResetAt), ms(o.SourceTime), b.observedAt.UnixMilli(), quality)
	if err != nil || !history {
		return err
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO quota_observations(owner_key, bucket_key, observed_at, limit_value, used_value, remaining_value, reset_at, quality)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, b.owner, o.Spec.Key, b.observedAt.UnixMilli(), fl(o.Limit), fl(o.Used), fl(o.Remaining), ms(o.ResetAt), quality)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func upsertUsage(ctx context.Context, ex store.Execer, monitorID string, u domain.UsageAggregate, now time.Time) error {
	dims, _ := json.Marshal(u.Dimensions)
	i64 := func(p *int64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	var actual, est any
	currency := ""
	if u.ActualCost != nil {
		actual, currency = int64(u.ActualCost.Amount), u.ActualCost.Currency
	}
	if u.EstimatedCost != nil {
		est = int64(u.EstimatedCost.Amount)
		if currency == "" {
			currency = u.EstimatedCost.Currency
		}
	}
	partial := 0
	if u.Partial {
		partial = 1
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO external_usage(monitor_id, breakdown, dimensions, interval_start, interval_end, requests,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, actual_cost_e10, estimated_cost_e10, currency, partial, quality, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(monitor_id, breakdown, dimensions, interval_start, interval_end) DO UPDATE SET requests = excluded.requests,
		input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens, cache_read_tokens = excluded.cache_read_tokens,
		cache_write_tokens = excluded.cache_write_tokens, actual_cost_e10 = excluded.actual_cost_e10, estimated_cost_e10 = excluded.estimated_cost_e10,
		currency = excluded.currency, partial = excluded.partial, quality = excluded.quality, observed_at = excluded.observed_at`,
		monitorID, u.Breakdown, string(dims), u.IntervalStart.UnixMilli(), u.IntervalEnd.UnixMilli(), i64(u.Requests),
		i64(u.Tokens.Input), i64(u.Tokens.Output), i64(u.Tokens.CacheRead), i64(u.Tokens.CacheWrite), actual, est, nullStr(currency),
		partial, string(u.Quality), now.UnixMilli())
	return err
}

func (s *Service) pollStates(ctx context.Context) (map[domain.AccountID]polling.PollState, error) {
	rows, err := store.Query(ctx, s.o.DB.R, scanPollState, `SELECT monitor_id, last_attempt_at, last_success_at, consecutive_failures, next_attempt_at,
		error_kind, error_message, error_http_status, auth_state, cooldown_until, last_latency_ms, masked_identity FROM quota_poll_state`)
	if err != nil {
		return nil, err
	}
	out := map[domain.AccountID]polling.PollState{}
	for _, r := range rows {
		out[r.state.AccountID] = r.state
	}
	return out, nil
}

type pollRow struct {
	state    polling.PollState
	identity *string
}

func tptr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

func scanPollState(r *sql.Rows) (pollRow, error) {
	var x pollRow
	var id, auth string
	var la, ls, na, cu, lat, hs sql.NullInt64
	var ek, em, ident sql.NullString
	if err := r.Scan(&id, &la, &ls, &x.state.ConsecutiveFailures, &na, &ek, &em, &hs, &auth, &cu, &lat, &ident); err != nil {
		return x, err
	}
	x.state.AccountID = domain.AccountID(id)
	x.state.LastAttemptAt, x.state.LastSuccessAt, x.state.NextAttemptAt, x.state.CooldownUntil = tptr(la), tptr(ls), tptr(na), tptr(cu)
	x.state.AuthState = polling.AuthState(auth)
	if ek.Valid {
		x.state.LastError = &domain.FetchError{Kind: domain.ErrorKind(ek.String), Message: em.String, HTTPStatus: int(hs.Int64)}
	}
	if lat.Valid {
		v := lat.Int64
		x.state.LastLatencyMS = &v
	}
	if ident.Valid {
		v := ident.String
		x.identity = &v
	}
	return x, nil
}

// load restores the in-memory board from the database at startup.
func (s *Service) load(ctx context.Context) error {
	rows, err := store.Query(ctx, s.o.DB.R, func(r *sql.Rows) (bucketState, error) {
		var b bucketState
		var mon, acc sql.NullString
		var spec, quality string
		var lim, used, rem sql.NullFloat64
		var limE, usedE, remE, reset, srcT sql.NullInt64
		var obsAt int64
		err := r.Scan(&b.owner, &mon, &acc, &b.source, &spec, &lim, &used, &rem, &limE, &usedE, &remE, &reset, &srcT, &obsAt, &quality)
		b.monitorID, b.accountID = mon.String, acc.String
		b.obs.Spec = decodeSpec(spec)
		b.obs.Limit, b.obs.Used, b.obs.Remaining = nf(lim), nf(used), nf(rem)
		b.obs.LimitAmount, b.obs.UsedAmount, b.obs.RemainingAmount = na(limE), na(usedE), na(remE)
		b.obs.ResetAt, b.obs.SourceTime = tptr(reset), tptr(srcT)
		b.obs.Quality = domain.Quality(quality)
		b.observedAt = time.UnixMilli(obsAt).UTC()
		return b, err
	}, `SELECT owner_key, monitor_id, account_id, source, spec, limit_value, used_value, remaining_value, limit_e10, used_e10, remaining_e10,
		reset_at, source_time, observed_at, quality FROM quota_buckets`)
	if err != nil {
		return err
	}
	byOwner := map[string][]bucketState{}
	for _, b := range rows {
		byOwner[b.owner] = append(byOwner[b.owner], b)
	}
	for owner, bs := range byOwner {
		s.board.replaceOwner(owner, bs)
	}
	states, err := store.Query(ctx, s.o.DB.R, scanPollState, `SELECT monitor_id, last_attempt_at, last_success_at, consecutive_failures, next_attempt_at,
		error_kind, error_message, error_http_status, auth_state, cooldown_until, last_latency_ms, masked_identity FROM quota_poll_state`)
	if err != nil {
		return err
	}
	for _, st := range states {
		s.board.setPoll(string(st.state.AccountID), st.state, st.identity)
	}
	return nil
}

func (s *Service) prune(ctx context.Context) {
	cut := s.o.Clock.Now().Add(-90 * 24 * time.Hour).UnixMilli()
	for {
		res, err := s.o.DB.W.ExecContext(ctx, `DELETE FROM quota_observations WHERE id IN (SELECT id FROM quota_observations WHERE observed_at < ? LIMIT 5000)`, cut)
		if err != nil {
			return
		}
		if n, _ := res.RowsAffected(); n < 5000 {
			return
		}
	}
}

// ---------- OAuth token store ----------

type tokenStore struct {
	db      *store.DB
	keyring *crypto.Keyring
	now     func() time.Time
}

type tokenJSON struct {
	Access     string    `json:"a"`
	Refresh    string    `json:"r,omitempty"`
	ExpiresAt  time.Time `json:"e"`
	Scopes     []string  `json:"s,omitempty"`
	Subject    string    `json:"u,omitempty"`
	ObtainedAt time.Time `json:"o"`
}

func tokenAAD(id domain.AccountID) string { return "quota-oauth-token:" + string(id) }

func (t *tokenStore) Get(id domain.AccountID) (providers.OAuthToken, error) {
	var env string
	err := t.db.R.QueryRow(`SELECT envelope FROM quota_oauth_tokens WHERE monitor_id = ?`, string(id)).Scan(&env)
	if errors.Is(err, sql.ErrNoRows) {
		return providers.OAuthToken{}, providers.ErrNoToken
	}
	if err != nil {
		return providers.OAuthToken{}, err
	}
	b, err := t.keyring.Open(env, tokenAAD(id))
	if err != nil {
		return providers.OAuthToken{}, errors.New("stored token cannot be decrypted with the configured master key")
	}
	var j tokenJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return providers.OAuthToken{}, errors.New("stored token is unreadable")
	}
	return providers.OAuthToken{AccessToken: credentials.NewSecret(j.Access), RefreshToken: credentials.NewSecret(j.Refresh), ExpiresAt: j.ExpiresAt,
		Scopes: j.Scopes, Subject: j.Subject, ObtainedAt: j.ObtainedAt}, nil
}

func (t *tokenStore) Put(id domain.AccountID, tok providers.OAuthToken) error {
	b, err := json.Marshal(tokenJSON{Access: tok.AccessToken.Reveal(), Refresh: tok.RefreshToken.Reveal(), ExpiresAt: tok.ExpiresAt,
		Scopes: tok.Scopes, Subject: tok.Subject, ObtainedAt: tok.ObtainedAt})
	if err != nil {
		return err
	}
	env, err := t.keyring.Seal(b, tokenAAD(id))
	if err != nil {
		return err
	}
	_, err = t.db.W.Exec(`INSERT INTO quota_oauth_tokens(monitor_id, envelope, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(monitor_id) DO UPDATE SET envelope = excluded.envelope, updated_at = excluded.updated_at`, string(id), env, t.now().UnixMilli())
	return err
}

func (t *tokenStore) Delete(id domain.AccountID) error {
	_, err := t.db.W.Exec(`DELETE FROM quota_oauth_tokens WHERE monitor_id = ?`, string(id))
	return err
}

// sortedKeys returns map keys in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BackfillDemo writes simulated observation history for demo monitors so
// charts and forecasts have data on a fresh demo instance.
func (s *Service) BackfillDemo(ctx context.Context, history time.Duration) error {
	if s.sim == nil {
		return nil
	}
	var n int
	if err := s.o.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM quota_observations`).Scan(&n); err != nil || n > 0 {
		return err
	}
	snap := s.o.CP.Snapshot()
	now := s.o.Clock.Now()
	return s.o.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, m := range snap.Monitors {
			a, ok := s.reg.Get(domain.ProviderID(m.Spec.Adapter))
			if !ok || a.Info().Tier != domain.TierDemo {
				continue
			}
			scen := demo.ScenarioID(providers.AccountConfig{Account: domain.Account{ID: domain.AccountID(m.ID)}, Options: m.Spec.Options})
			for t := now.Add(-history); t.Before(now); t = t.Add(15 * time.Minute) {
				res, err := s.sim.Observe(scen, t)
				if err != nil {
					continue
				}
				for _, b := range res.Buckets {
					if err := upsertBucket(ctx, tx, bucketState{owner: m.ID, monitorID: m.ID, accountID: m.Spec.AccountID, source: sourceAdapter,
						obs: b, observedAt: t}, true); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// ImportObservation records a historical adapter observation for a monitor
// (used by importers). It is appended to history, and becomes the bucket's
// current state unless a newer observation already is. An observation
// already in history (same bucket and time) is skipped; the result reports
// whether it was recorded.
func ImportObservation(ctx context.Context, ex store.Execer, monitorID string, obs domain.BucketObservation, observedAt time.Time) (bool, error) {
	var one int
	err := ex.QueryRowContext(ctx, `SELECT 1 FROM quota_observations WHERE owner_key = ? AND bucket_key = ? AND observed_at = ?`,
		monitorID, obs.Spec.Key, observedAt.UnixMilli()).Scan(&one)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, upsertBucket(ctx, ex, bucketState{owner: monitorID, monitorID: monitorID, source: "adapter", obs: obs, observedAt: observedAt}, true)
}
