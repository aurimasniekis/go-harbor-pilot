package quota

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

const (
	sourceAdapter = "adapter"
	sourceHeaders = "response_headers"
	sourceCLI     = "cli_event"
	// headerFreshness bounds how long a response-derived rate-limit
	// observation is considered current (these windows are short).
	headerFreshness = 2 * time.Minute
	// cliFreshness bounds CLI-reported subscription windows, which update
	// on every bridged request.
	cliFreshness = 30 * time.Minute
)

type headerObs struct {
	accountID string
	states    []bucketState
}

var droppedHeaderObs atomic.Int64

// DroppedObservations reports header observations dropped because the
// writer queue was full (coverage indicator).
func DroppedObservations() int64 { return droppedHeaderObs.Load() }

// ObserveResponse derives rate-limit observations from upstream response
// headers. It is called on the request path: parsing is cheap, the board
// update is a short critical section and persistence is queued (dropped,
// and counted, when the queue is full).
func (s *Service) ObserveResponse(accountID, providerType, model string, h http.Header) {
	if len(h) == 0 || accountID == "" {
		return
	}
	now := s.o.Clock.Now()
	var obs []domain.BucketObservation
	switch providerType {
	case cp.ProviderAnthropic:
		obs = parseAnthropicHeaders(h, model, now)
	case cp.ProviderOpenAI, cp.ProviderOpenAICompatible:
		obs = parseOpenAIHeaders(h, model, now)
	}
	if len(obs) == 0 {
		return
	}
	states := make([]bucketState, 0, len(obs))
	for _, o := range obs {
		states = append(states, bucketState{owner: accountID, accountID: accountID, source: sourceHeaders, obs: o, observedAt: now})
	}
	s.board.mergeOwner(accountID, states)
	select {
	case s.headerQ <- headerObs{accountID: accountID, states: states}:
	default:
		droppedHeaderObs.Add(1)
	}
}

// headerWriter persists the latest header observations, coalesced.
func (s *Service) headerWriter(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	pending := map[string]bucketState{}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		tx, err := s.o.DB.W.BeginTx(context.WithoutCancel(ctx), nil)
		if err != nil {
			return
		}
		for _, b := range pending {
			if err := upsertBucket(ctx, tx, b, false); err != nil {
				_ = tx.Rollback()
				return
			}
		}
		if tx.Commit() == nil {
			pending = map[string]bucketState{}
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case o := <-s.headerQ:
			for _, b := range o.states {
				pending[b.owner+"|"+b.obs.Spec.Key] = b
			}
		case <-t.C:
			flush()
		}
	}
}

func headerFloat(h http.Header, k string) *float64 {
	v := strings.TrimSpace(h.Get(k))
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return nil
	}
	return &f
}

func rateLimitSpec(key, label, model string, unit domain.Unit) domain.BucketSpec {
	scope := domain.ScopeAccount
	if model != "" {
		scope = domain.ScopeModel
	}
	return domain.BucketSpec{Key: key, Label: label, Scope: scope, Kind: domain.KindRateLimit, Unit: unit, Model: model,
		Window: time.Minute, ResetSemantics: domain.ResetRolling, Official: true}
}

// parseAnthropicHeaders reads the documented anthropic-ratelimit-* headers.
// Reset values are RFC 3339 timestamps.
func parseAnthropicHeaders(h http.Header, model string, now time.Time) []domain.BucketObservation {
	var out []domain.BucketObservation
	for _, m := range []struct {
		name, label string
		unit        domain.Unit
	}{
		{"requests", "Requests per minute", domain.UnitRequests},
		{"tokens", "Tokens per minute", domain.UnitTokens},
		{"input-tokens", "Input tokens per minute", domain.UnitTokens},
		{"output-tokens", "Output tokens per minute", domain.UnitTokens},
	} {
		p := "anthropic-ratelimit-" + m.name + "-"
		lim, rem := headerFloat(h, p+"limit"), headerFloat(h, p+"remaining")
		if lim == nil && rem == nil {
			continue
		}
		o := domain.BucketObservation{Spec: rateLimitSpec("rl:"+strings.ReplaceAll(m.name, "-", "_")+modelSuffix(model), m.label, model, m.unit),
			Limit: lim, Remaining: rem, Quality: domain.QualityProviderReported, Source: sourceHeaders, SourceTime: &now}
		if lim != nil && rem != nil {
			u := *lim - *rem
			o.Used = &u
		}
		if r := strings.TrimSpace(h.Get(p + "reset")); r != "" {
			if t, err := time.Parse(time.RFC3339, r); err == nil {
				o.ResetAt = &t
			}
		}
		out = append(out, o)
	}
	return out
}

// parseOpenAIHeaders reads x-ratelimit-* headers. Reset values are
// durations such as "1s", "6m0s" or "20ms".
func parseOpenAIHeaders(h http.Header, model string, now time.Time) []domain.BucketObservation {
	var out []domain.BucketObservation
	for _, m := range []struct {
		name, label string
		unit        domain.Unit
	}{
		{"requests", "Requests per minute", domain.UnitRequests},
		{"tokens", "Tokens per minute", domain.UnitTokens},
	} {
		lim, rem := headerFloat(h, "x-ratelimit-limit-"+m.name), headerFloat(h, "x-ratelimit-remaining-"+m.name)
		if lim == nil && rem == nil {
			continue
		}
		o := domain.BucketObservation{Spec: rateLimitSpec("rl:"+m.name+modelSuffix(model), m.label, model, m.unit),
			Limit: lim, Remaining: rem, Quality: domain.QualityProviderReported, Source: sourceHeaders, SourceTime: &now}
		if lim != nil && rem != nil {
			u := *lim - *rem
			o.Used = &u
		}
		if r := strings.TrimSpace(h.Get("x-ratelimit-reset-" + m.name)); r != "" {
			if d, err := time.ParseDuration(r); err == nil && d >= 0 {
				t := now.Add(d)
				o.ResetAt = &t
			}
		}
		out = append(out, o)
	}
	return out
}

func modelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return ":model:" + model
}

// CLIWindow is one subscription window reported by a CLI bridge.
type CLIWindow struct {
	Name        string // five_hour | seven_day | seven_day_overage_included | ...
	Utilization float64
	ResetsAt    time.Time
	Status      string // allowed | allowed_warning | rejected
}

var cliWindows = map[string]struct {
	label  string
	window time.Duration
}{
	"five_hour":                  {"5-hour window", 5 * time.Hour},
	"seven_day":                  {"7-day window", 7 * 24 * time.Hour},
	"seven_day_opus":             {"7-day window (Opus)", 7 * 24 * time.Hour},
	"seven_day_sonnet":           {"7-day window (Sonnet)", 7 * 24 * time.Hour},
	"seven_day_overage_included": {"7-day window (overage included)", 7 * 24 * time.Hour},
}

// ObserveCLI records subscription windows reported by a CLI bridge for a
// provider account. A rejected window marks the account exhausted until its
// reset. These observations come from the vendor CLI on each request and
// are trusted like provider-reported quota.
func (s *Service) ObserveCLI(accountID string, windows []CLIWindow) {
	if accountID == "" || len(windows) == 0 {
		return
	}
	now := s.o.Clock.Now()
	states := make([]bucketState, 0, len(windows))
	for _, w := range windows {
		meta, ok := cliWindows[w.Name]
		if !ok {
			meta.label, meta.window = w.Name, 0
		}
		used := w.Utilization * 100
		if used < 0 {
			used = 0
		}
		rem := 100 - used
		if rem < 0 {
			rem = 0
		}
		if w.Status == "rejected" {
			rem = 0
		}
		limit := 100.0
		o := domain.BucketObservation{Spec: domain.BucketSpec{Key: "cli:" + w.Name, Label: meta.label, Scope: domain.ScopeAccount,
			Kind: domain.KindFixedWindow, Unit: domain.UnitPercent, Window: meta.window, ResetSemantics: domain.ResetProviderTimestamp,
			Global: w.Name == "five_hour" || w.Name == "seven_day", Official: false},
			Limit: &limit, Used: &used, Remaining: &rem, Quality: domain.QualityProviderReported, Source: sourceCLI, SourceTime: &now}
		if !w.ResetsAt.IsZero() {
			r := w.ResetsAt
			o.ResetAt = &r
		}
		states = append(states, bucketState{owner: accountID, accountID: accountID, source: sourceCLI, obs: o, observedAt: now})
	}
	s.board.mergeOwner(accountID, states)
	select {
	case s.headerQ <- headerObs{accountID: accountID, states: states}:
	default:
		droppedHeaderObs.Add(1)
	}
}
