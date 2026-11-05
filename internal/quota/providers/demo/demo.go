// Package demo implements deterministic demo adapters. They never perform
// network calls and require no credentials. Every value is synthetic and
// labelled as demo data.
package demo

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"
	_ "time/tzdata" // Europe/Vilnius must resolve in minimal containers

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Step is the simulation resolution.
const Step = 5 * time.Minute

// History is how far before the start time demo history is backfilled.
const History = 14 * 24 * time.Hour

const warmup = History + 24*time.Hour

var vilnius = mustLoad("Europe/Vilnius")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Simulator owns the deterministic state of all demo accounts. All scenario
// timings are relative to Start, so a demo started at any time shows the same
// situations; with a frozen clock it is byte-for-byte reproducible.
type Simulator struct {
	start time.Time
	simT0 time.Time

	mu    sync.Mutex
	sims  map[domain.AccountID]*accountSim
	scens []scenario
}

type accountSim struct {
	sc     scenario
	states [][]float64 // per step, per bucket: remaining (percent/balance) or used (budgets)
	ring   []float64   // rolling-window consumption per step
}

// NewSimulator creates a simulator anchored at start (floored to Step).
func NewSimulator(start time.Time) *Simulator {
	start = start.UTC().Truncate(Step)
	s := &Simulator{start: start, simT0: start.Add(-warmup), sims: map[domain.AccountID]*accountSim{}, scens: scenarios()}
	for _, sc := range s.scens {
		s.sims[sc.id] = &accountSim{sc: sc}
	}
	return s
}

// Start returns the anchor time.
func (s *Simulator) Start() time.Time { return s.start }

// Accounts returns the demo accounts.
func (s *Simulator) Accounts() []domain.Account {
	out := make([]domain.Account, 0, len(s.scens))
	for _, sc := range s.scens {
		var masked *string
		if sc.masked != "" {
			m := sc.masked
			masked = &m
		}
		out = append(out, domain.Account{
			ID: sc.id, ProviderID: sc.provider, Alias: sc.alias, MaskedIdentifier: masked, Tags: sc.tags,
			Enabled: true, Demo: true, Tier: domain.TierDemo, SharedScopeID: sc.sharedScope,
			Provenance: domain.Provenance{File: "builtin:demo", CredentialType: "none", CredentialRef: "none (demo)"},
		})
	}
	return out
}

// Note returns the scenario description for an account.
func (s *Simulator) Note(id domain.AccountID) string {
	if a, ok := s.sims[id]; ok {
		return a.sc.note
	}
	return ""
}

// splitmix64 is a tiny deterministic hash for noise.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func unit(seed uint64, slot int64) float64 {
	return float64(splitmix64(seed*1_000_003+uint64(slot))>>11) / float64(1<<53)
}

var officeHours = [24]float64{
	0.04, 0.03, 0.02, 0.02, 0.02, 0.03, 0.08, 0.3, 0.7, 1.0, 1.0, 0.95,
	0.55, 0.85, 1.0, 1.0, 0.9, 0.75, 0.45, 0.35, 0.3, 0.2, 0.1, 0.06,
}

func (s *Simulator) activity(sc scenario, t time.Time) float64 {
	lt := t.In(vilnius)
	var a float64
	switch sc.profile {
	case profileCI:
		a = 0.75
		if lt.Hour() < 6 {
			a = 0.55
		}
	default:
		a = officeHours[lt.Hour()]
		if wd := lt.Weekday(); wd == time.Saturday || wd == time.Sunday {
			a *= 0.5
		}
	}
	slot := t.Unix() / int64(15*60)
	a *= 0.6 + 0.8*unit(sc.seed, slot)
	off := t.Sub(s.start)
	if sc.spikeExtra > 0 && off >= sc.spikeFrom && off < sc.spikeTo {
		a += sc.spikeExtra * (0.85 + 0.3*unit(sc.seed+7, slot))
	}
	return a
}

func floorDiv(a, b time.Duration) int64 {
	q := int64(a / b)
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func monthStart(t time.Time) time.Time {
	lt := t.In(vilnius)
	return time.Date(lt.Year(), lt.Month(), 1, 0, 0, 0, 0, vilnius).UTC()
}

func nextMonthStart(t time.Time) time.Time {
	lt := t.In(vilnius)
	return time.Date(lt.Year(), lt.Month()+1, 1, 0, 0, 0, 0, vilnius).UTC()
}

// extend advances an account simulation to include step idx.
func (s *Simulator) extend(a *accountSim, idx int) {
	sc := a.sc
	stepH := Step.Hours()
	rollSteps := int((5 * time.Hour) / Step)
	for len(a.states) <= idx {
		i := len(a.states)
		t := s.simT0.Add(time.Duration(i) * Step)
		var prev []float64
		cur := make([]float64, len(sc.buckets))
		if i == 0 {
			for bi, b := range sc.buckets {
				switch b.kind {
				case bucketFixedPercent, bucketRollingPercent:
					cur[bi] = b.limit
				case bucketBudget:
					cur[bi] = b.weight * sc.base * 0.35 * t.Sub(monthStart(t)).Hours()
				case bucketBalance:
					cur[bi] = sc.initialBalance
				}
			}
			a.states = append(a.states, cur)
			continue
		}
		prev = a.states[i-1]
		copy(cur, prev)
		tPrev := t.Add(-Step)
		// Resets first.
		for bi, b := range sc.buckets {
			switch b.kind {
			case bucketFixedPercent:
				base := s.start.Add(b.anchor)
				if floorDiv(t.Sub(base), b.spec.Period) > floorDiv(tPrev.Sub(base), b.spec.Period) {
					cur[bi] = b.limit
				}
			case bucketBudget:
				if !monthStart(t).Equal(monthStart(tPrev)) {
					cur[bi] = 0
				}
			case bucketBalance:
				for off, amt := range sc.topUps {
					at := s.start.Add(off)
					if !at.After(tPrev) || at.After(t) {
						continue
					}
					cur[bi] += amt
				}
			}
		}
		// Blocked when any global bucket is exhausted.
		blocked := false
		for bi, b := range sc.buckets {
			if !b.spec.Global {
				continue
			}
			switch b.kind {
			case bucketFixedPercent, bucketRollingPercent, bucketBalance:
				if cur[bi] <= 0 {
					blocked = true
				}
			case bucketBudget:
				if cur[bi] >= b.limit {
					blocked = true
				}
			}
		}
		act := 0.0
		if !blocked {
			act = s.activity(sc, t)
		}
		cons := 0.0
		for bi, b := range sc.buckets {
			amount := b.weight * sc.base * act * stepH
			switch b.kind {
			case bucketFixedPercent:
				cur[bi] = math.Max(0, cur[bi]-amount)
			case bucketRollingPercent:
				cons = math.Min(amount, math.Max(0, cur[bi]))
			case bucketBudget:
				cur[bi] = math.Min(b.limit, cur[bi]+amount)
			case bucketBalance:
				cur[bi] = math.Max(0, cur[bi]-amount)
			}
		}
		a.ring = append(a.ring, cons)
		for bi, b := range sc.buckets {
			if b.kind != bucketRollingPercent {
				continue
			}
			var sum float64
			for j := max(0, len(a.ring)-rollSteps); j < len(a.ring); j++ {
				sum += a.ring[j]
			}
			cur[bi] = math.Max(0, b.limit-sum)
		}
		a.states = append(a.states, cur)
	}
}

func (s *Simulator) stateAt(id domain.AccountID, t time.Time) ([]float64, time.Time, bool) {
	a, ok := s.sims[id]
	if !ok || t.Before(s.simT0) {
		return nil, time.Time{}, false
	}
	idx := int(t.Sub(s.simT0) / Step)
	s.extend(a, idx)
	return a.states[idx], s.simT0.Add(time.Duration(idx) * Step), true
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func f(v float64) *float64 { return &v }

func amount(v float64) *money.Amount {
	a, err := money.ParseDecimal(formatFloat(round(v, 2)))
	if err != nil {
		return nil
	}
	return &a
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// observe computes the account's demo observations at t.
func (s *Simulator) observe(id domain.AccountID, t time.Time) (*providers.FetchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.sims[id]
	if !ok {
		return nil, providers.NewError(domain.ErrConfiguration, "unknown demo account")
	}
	sc := a.sc
	off := t.Sub(s.start)
	if sc.authFailFrom != nil && off >= *sc.authFailFrom {
		return nil, &providers.Error{Kind: domain.ErrAuthentication, Message: "demo: credential expired", HTTPStatus: 401}
	}
	if sc.unavailableFrom != nil && off >= *sc.unavailableFrom {
		return nil, &providers.Error{Kind: domain.ErrNetwork, Message: "demo: provider unreachable (timeout)"}
	}
	state, at, ok := s.stateAt(id, t)
	if !ok {
		return nil, providers.NewError(domain.ErrUnavailable, "demo: time outside simulation range")
	}
	res := &providers.FetchResult{SourceTime: &at}
	if sc.masked != "" {
		m := sc.masked
		res.MaskedIdentifier = &m
	}
	for bi, b := range sc.buckets {
		obs := domain.BucketObservation{Spec: b.spec, Quality: domain.QualityDemo, Source: "demo", SourceTime: &at}
		switch b.kind {
		case bucketFixedPercent:
			rem := round(state[bi], 1)
			obs.Limit, obs.Remaining, obs.Used = f(100), f(rem), f(round(100-rem, 1))
			base := s.start.Add(b.anchor)
			next := base.Add(time.Duration(floorDiv(t.Sub(base), b.spec.Period)+1) * b.spec.Period)
			obs.ResetAt = &next
		case bucketRollingPercent:
			rem := round(state[bi], 1)
			obs.Limit, obs.Remaining, obs.Used = f(100), f(rem), f(round(100-rem, 1))
		case bucketBudget:
			used := round(state[bi], 2)
			obs.Limit, obs.Used, obs.Remaining = f(b.limit), f(used), f(round(b.limit-used, 2))
			obs.LimitAmount, obs.UsedAmount, obs.RemainingAmount = amount(b.limit), amount(used), amount(b.limit-used)
			next := nextMonthStart(t)
			obs.ResetAt = &next
		case bucketSharedBudget:
			used := 0.0
			for _, other := range s.scens {
				if other.sharedScope != b.spec.SharedScopeID {
					continue
				}
				st, _, ok := s.stateAt(other.id, t)
				if !ok {
					continue
				}
				for obi, ob := range other.buckets {
					if ob.kind == bucketBudget {
						used += st[obi]
					}
				}
			}
			used = round(math.Min(used, b.limit), 2)
			obs.Limit, obs.Used, obs.Remaining = f(b.limit), f(used), f(round(b.limit-used, 2))
			obs.LimitAmount, obs.UsedAmount, obs.RemainingAmount = amount(b.limit), amount(used), amount(b.limit-used)
			next := nextMonthStart(t)
			obs.ResetAt = &next
		case bucketBalance:
			bal := round(state[bi], 2)
			obs.Remaining = f(bal)
			obs.RemainingAmount = amount(bal)
		}
		res.Buckets = append(res.Buckets, obs)
	}
	return res, nil
}

// Adapter is a demo adapter for one demo provider.
type Adapter struct {
	id  domain.ProviderID
	sim *Simulator
}

// NewAdapters returns one adapter per demo provider sharing a simulator.
func NewAdapters(sim *Simulator) []*Adapter {
	return []*Adapter{
		{id: ProviderClaude, sim: sim},
		{id: ProviderCodex, sim: sim},
		{id: ProviderNexos, sim: sim},
		{id: ProviderDeepSeek, sim: sim},
	}
}

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	base := domain.ProviderInfo{
		ID: a.id, AdapterVersion: "demo-1", Tier: domain.TierDemo, CredentialTypes: []string{"none"},
		DefaultPollInterval: time.Minute,
		Limitations:         []string{"Synthetic deterministic demo data. Never calls a real provider."},
	}
	p, u, s := providers.Part, providers.Unsup, providers.Sup
	switch a.id {
	case ProviderClaude:
		base.DisplayName, base.Family = "Claude (demo)", "claude"
		base.Capabilities = providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapAccountIdentity: p("masked placeholder identity"),
			domain.CapQuotaRemaining:  s(""), domain.CapQuotaUsed: s(""), domain.CapQuotaLimit: s("normalized to 100%"),
			domain.CapFixedReset: s("5-hour and 7-day resets"), domain.CapModelQuota: s("weekly per-model bucket"),
			domain.CapMonetarySpend: u("not part of subscription quota data"),
		})
	case ProviderCodex:
		base.DisplayName, base.Family = "Codex (demo)", "openai"
		base.Capabilities = providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapAccountIdentity: p("masked placeholder identity"),
			domain.CapQuotaRemaining:  s(""), domain.CapQuotaUsed: s(""), domain.CapQuotaLimit: s("normalized to 100%"),
			domain.CapFixedReset: p("only the next reset time is reported"), domain.CapRollingWindow: s("team account"),
			domain.CapModelQuota: u("no model-specific quota is exposed"),
		})
	case ProviderNexos:
		base.DisplayName, base.Family = "nexos.ai (demo)", "nexos"
		base.Capabilities = providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapBudget: s("API key and team budgets"), domain.CapMonetarySpend: s(""),
			domain.CapQuotaRemaining: p("budget-derived only"), domain.CapOrgProjectScope: s("team scope"),
			domain.CapTokenCounters: s(""), domain.CapRequestCounters: s(""), domain.CapModelUsage: s(""),
			domain.CapAPIKeyUsage: s(""), domain.CapAPIKeyInventory: s(""), domain.CapModelPricing: s(""),
			domain.CapMonetaryBalance:  u("credit balance is UI-only for nexos"),
			domain.CapUsageAttribution: p("instrumented events only"),
		})
	case ProviderDeepSeek:
		base.DisplayName, base.Family = "DeepSeek (demo)", "deepseek"
		base.Capabilities = providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapMonetaryBalance: s("prepaid balance"),
			domain.CapQuotaRemaining:  u("no quota windows; balance only"),
		})
	}
	return base
}

// ScenarioID returns the simulated account a configuration reads: the
// "scenario" option when set, otherwise the account id.
func ScenarioID(cfg providers.AccountConfig) domain.AccountID {
	if v, ok := cfg.Options["scenario"].(string); ok && v != "" {
		return domain.AccountID(v)
	}
	return cfg.Account.ID
}

// Observe returns the simulated state of a scenario at t (history backfill).
func (s *Simulator) Observe(id domain.AccountID, t time.Time) (*providers.FetchResult, error) {
	return s.observe(id, t)
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	if !cfg.Account.Demo {
		return providers.NewError(domain.ErrConfiguration, "demo adapters only serve demo accounts")
	}
	return nil
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, providers.WrapError(domain.ErrInternal, "cancelled", err)
	}
	id := ScenarioID(req.Config)
	res, err := a.sim.observe(id, req.Now)
	if err != nil || a.id != ProviderNexos {
		return res, err
	}
	res.Usage = a.sim.UsageAggregates(id, req.Now.Add(-48*time.Hour), req.Now, req.Now)
	res.APIKeys = a.sim.APIKeys(id, req.Now)
	return res, nil
}
