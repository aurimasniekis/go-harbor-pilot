package quota

import (
	"database/sql"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/polling"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
)

// bucketState is the latest observation of one bucket.
type bucketState struct {
	owner      string // monitor id or provider account id
	monitorID  string
	accountID  string
	source     string // adapter | response_headers
	obs        domain.BucketObservation
	observedAt time.Time
}

type monitorLink struct {
	accountID      string
	exhaustedBelow float64
	staleAfter     time.Duration
	name           string
}

type pollInfo struct {
	state    polling.PollState
	identity *string
}

// board is the in-memory quota state used on the request path.
type board struct {
	mu     sync.RWMutex
	owners map[string]map[string]bucketState // owner -> bucket key -> state
	links  map[string]monitorLink            // monitor id -> link
	polls  map[string]pollInfo
	byAcct map[string]map[string]struct{} // account id -> owners
}

func newBoard() *board {
	return &board{owners: map[string]map[string]bucketState{}, links: map[string]monitorLink{}, polls: map[string]pollInfo{},
		byAcct: map[string]map[string]struct{}{}}
}

func (b *board) reindexLocked() {
	b.byAcct = map[string]map[string]struct{}{}
	add := func(acc, owner string) {
		if acc == "" {
			return
		}
		if b.byAcct[acc] == nil {
			b.byAcct[acc] = map[string]struct{}{}
		}
		b.byAcct[acc][owner] = struct{}{}
	}
	for id, l := range b.links {
		add(l.accountID, id)
	}
	for owner, bs := range b.owners {
		for _, s := range bs {
			if s.source == sourceHeaders || s.source == sourceCLI {
				add(s.accountID, owner)
			}
			break
		}
	}
}

func (b *board) setLinks(l map[string]monitorLink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.links = l
	b.reindexLocked()
}

func (b *board) link(id string) monitorLink {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.links[id]
}

func (b *board) setPoll(id string, st polling.PollState, identity *string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pi := b.polls[id]
	pi.state = st
	if identity != nil {
		pi.identity = identity
	}
	b.polls[id] = pi
}

func (b *board) authState(id string) polling.AuthState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.polls[id].state.AuthState
}

func (b *board) replaceOwner(owner string, states []bucketState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := make(map[string]bucketState, len(states))
	for _, s := range states {
		m[s.obs.Spec.Key] = s
	}
	b.owners[owner] = m
	b.reindexLocked()
}

// mergeOwner updates some buckets of an owner (header observations).
func (b *board) mergeOwner(owner string, states []bucketState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.owners[owner]
	if m == nil {
		m = map[string]bucketState{}
		b.owners[owner] = m
	}
	newOwner := len(m) == 0
	for _, s := range states {
		m[s.obs.Spec.Key] = s
	}
	if newOwner {
		b.reindexLocked()
	}
}

// exhausted returns the exhausted global buckets of an owner.
func (b *board) exhausted(owner string, now time.Time) map[string]bucketState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	l := b.links[owner]
	out := map[string]bucketState{}
	for k, s := range b.owners[owner] {
		if isExhausted(s, l.exhaustedBelow, now) {
			out[k] = s
		}
	}
	return out
}

func (b *board) staleAfter(s bucketState) time.Duration {
	switch s.source {
	case sourceHeaders:
		return headerFreshness
	case sourceCLI:
		return cliFreshness
	}
	if l, ok := b.links[s.owner]; ok && l.staleAfter > 0 {
		return l.staleAfter
	}
	return 15 * time.Minute
}

// health derives routing health for a provider account.
func (b *board) health(accountID string, now time.Time) routing.Health {
	b.mu.RLock()
	defer b.mu.RUnlock()
	h := routing.Health{Available: true}
	owners := b.byAcct[accountID]
	if len(owners) == 0 {
		return h
	}
	seen, fresh := 0, 0
	var tightest *float64
	for owner := range owners {
		l := b.links[owner]
		for _, s := range b.owners[owner] {
			seen++
			if now.Sub(s.observedAt) > b.staleAfter(s) {
				continue
			}
			fresh++
			if (s.source == sourceAdapter || s.source == sourceCLI) && isExhausted(s, l.exhaustedBelow, now) && h.Available {
				h.Available = false
				h.Reason = "quota exhausted: " + label(s)
				h.Until = s.obs.ResetAt
			}
			if s.obs.ResetAt != nil && !s.obs.ResetAt.After(now) {
				continue
			}
			if f := s.obs.RemainingFraction(); f != nil && (tightest == nil || *f < *tightest) {
				v := *f
				tightest = &v
				h.ResetAt = s.obs.ResetAt
			}
		}
	}
	h.Remaining = tightest
	h.Stale = seen > 0 && fresh == 0
	return h
}

func label(s bucketState) string {
	if s.obs.Spec.Label != "" {
		return s.obs.Spec.Label
	}
	return s.obs.Spec.Key
}

// BucketView is the API view of a bucket.
type BucketView struct {
	Key               string     `json:"key"`
	Label             string     `json:"label"`
	Scope             string     `json:"scope"`
	Kind              string     `json:"kind"`
	Unit              string     `json:"unit"`
	Currency          string     `json:"currency,omitempty"`
	Model             string     `json:"model,omitempty"`
	WindowSeconds     int64      `json:"window_seconds,omitempty"`
	ResetSemantics    string     `json:"reset_semantics"`
	Global            bool       `json:"global"`
	Official          bool       `json:"official"`
	Source            string     `json:"source" enum:"adapter,response_headers,cli_event"`
	Limit             *float64   `json:"limit"`
	Used              *float64   `json:"used"`
	Remaining         *float64   `json:"remaining"`
	RemainingFraction *float64   `json:"remaining_fraction"`
	LimitAmount       *string    `json:"limit_amount,omitempty"`
	UsedAmount        *string    `json:"used_amount,omitempty"`
	RemainingAmount   *string    `json:"remaining_amount,omitempty"`
	ResetAt           *time.Time `json:"reset_at"`
	SourceTime        *time.Time `json:"source_time"`
	ObservedAt        time.Time  `json:"observed_at"`
	Quality           string     `json:"quality"`
	Stale             bool       `json:"stale"`
	Exhausted         bool       `json:"exhausted"`
	// ResetPassed is true when the observed reset time is in the past, so
	// the values describe an earlier window.
	ResetPassed bool `json:"reset_passed"`
}

func amountStr(a *money.Amount) *string {
	if a == nil {
		return nil
	}
	s := a.String()
	return &s
}

func (b *board) viewLocked(s bucketState, now time.Time) BucketView {
	o := s.obs
	l := b.links[s.owner]
	v := BucketView{Key: o.Spec.Key, Label: label(s), Scope: string(o.Spec.Scope), Kind: string(o.Spec.Kind), Unit: string(o.Spec.Unit),
		Currency: o.Spec.Currency, Model: o.Spec.Model, WindowSeconds: int64(o.Spec.Window / time.Second), ResetSemantics: string(o.Spec.ResetSemantics),
		Global: o.Spec.Global, Official: o.Spec.Official, Source: s.source, Limit: o.Limit, Used: o.Used, Remaining: o.Remaining,
		RemainingFraction: o.RemainingFraction(), LimitAmount: amountStr(o.LimitAmount), UsedAmount: amountStr(o.UsedAmount),
		RemainingAmount: amountStr(o.RemainingAmount), ResetAt: o.ResetAt, SourceTime: o.SourceTime, ObservedAt: s.observedAt,
		Quality: string(o.Quality), Stale: now.Sub(s.observedAt) > b.staleAfter(s)}
	v.ResetPassed = o.ResetAt != nil && !o.ResetAt.After(now)
	v.Exhausted = (s.source == sourceAdapter || s.source == sourceCLI) && isExhausted(s, l.exhaustedBelow, now)
	return v
}

// buckets returns API views of an owner's buckets ordered by key.
func (b *board) buckets(owner string, now time.Time) []BucketView {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m := b.owners[owner]
	out := make([]BucketView, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, b.viewLocked(m[k], now))
	}
	return out
}

func (b *board) poll(id string) (pollInfo, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	p, ok := b.polls[id]
	return p, ok
}

func nf(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func na(v sql.NullInt64) *money.Amount {
	if !v.Valid {
		return nil
	}
	a := money.Amount(v.Int64)
	return &a
}

// SeatWindow is one subscription window of a provider account, as the
// gateway reports it to clients (D-048).
type SeatWindow struct {
	// Name is "five_hour" or "seven_day".
	Name string
	// Utilization is the used fraction, 0 to 1.
	Utilization float64
	ResetAt     *time.Time
	// Exhausted is true when the window blocks the account.
	Exhausted bool
}

// seatKeys maps the board keys that describe an account's rolling
// subscription windows (CLI events and the subscription monitor) to window
// names.
var seatKeys = map[string]string{"cli:five_hour": "five_hour", "five_hour": "five_hour", "cli:seven_day": "seven_day", "seven_day": "seven_day"}

// seatWindows returns the 5-hour and 7-day windows of an account. Stale
// observations and windows whose reset has passed are left out; when both
// sources hold a window the latest observation wins.
func (b *board) seatWindows(accountID string, now time.Time) []SeatWindow {
	b.mu.RLock()
	defer b.mu.RUnlock()
	best := map[string]bucketState{}
	for owner := range b.byAcct[accountID] {
		for key, s := range b.owners[owner] {
			name, ok := seatKeys[key]
			if !ok || s.obs.Spec.Unit != domain.UnitPercent || s.obs.Used == nil || now.Sub(s.observedAt) > b.staleAfter(s) {
				continue
			}
			if s.obs.ResetAt != nil && !s.obs.ResetAt.After(now) {
				continue
			}
			if cur, ok := best[name]; ok && !s.observedAt.After(cur.observedAt) {
				continue
			}
			best[name] = s
		}
	}
	out := make([]SeatWindow, 0, len(best))
	for _, name := range []string{"five_hour", "seven_day"} {
		s, ok := best[name]
		if !ok {
			continue
		}
		limit := 100.0
		if s.obs.Limit != nil && *s.obs.Limit > 0 {
			limit = *s.obs.Limit
		}
		u := min(max(*s.obs.Used/limit, 0), 1)
		w := SeatWindow{Name: name, Utilization: u, Exhausted: isExhausted(s, b.links[s.owner].exhaustedBelow, now)}
		if s.obs.ResetAt != nil {
			r := *s.obs.ResetAt
			w.ResetAt = &r
		}
		out = append(out, w)
	}
	return out
}
