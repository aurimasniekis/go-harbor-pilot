// Package routing turns a request and the active configuration into an
// ordered list of candidates plus a complete explanation of every choice
// and exclusion. Hard restrictions always run before ranking and can never
// be overridden by scores.
package routing

import (
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"sync"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// Health reports runtime eligibility of an account (cooldowns, quota
// exhaustion, auth failures). Unknown is not the same as unavailable.
type Health struct {
	Available bool
	Reason    string     // why unavailable
	Until     *time.Time // cooldown end, if known
	// Remaining is the fraction of the tightest known quota window left
	// (0..1); nil when unknown.
	Remaining *float64
	// ResetAt is when the tightest window resets; nil when unknown.
	ResetAt *time.Time
	Stale   bool
}

// Input is everything routing may use. It contains only trusted context.
type Input struct {
	Snapshot       *cp.Snapshot
	Binding        *cp.Res[cp.BindingSpec]
	Plan           *cp.PlanView
	RequestedModel string
	Required       []string
	SealedFamilies []string
	Inbound        ir.Protocol
	SessionKey     string // trusted/hinted session key for affinity ("" = none)
	RequestSeed    string // deterministic tie-breaking (request id)
	Now            time.Time
	HealthOf       func(accountID string) Health
	// UpstreamFor returns the upstream protocol an executor would use.
	UpstreamFor func(providerType string, inbound ir.Protocol) ir.Protocol
}

// Candidate is an eligible (account, model) target.
type Candidate struct {
	ProviderID string        `json:"provider_id"`
	Provider   string        `json:"provider"`
	AccountID  string        `json:"account_id"`
	Account    string        `json:"account"`
	Model      string        `json:"model"`
	Billing    string        `json:"billing"`
	Upstream   ir.Protocol   `json:"upstream_protocol"`
	Native     bool          `json:"native"`
	Priority   int           `json:"priority"`
	Weight     int           `json:"weight"`
	Score      float64       `json:"score"`
	Reasons    []string      `json:"reasons"`
	ModelSpec  *cp.ModelSpec `json:"-"`
	Health     *Health       `json:"health,omitempty"`
}

// Exclusion explains why a member was not eligible.
type Exclusion struct {
	ProviderID string `json:"provider_id"`
	AccountID  string `json:"account_id"`
	Account    string `json:"account"`
	Model      string `json:"model"`
	Stage      string `json:"stage"` // restriction | capability | health | policy
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

// Decision is the routing outcome and its explanation.
type Decision struct {
	RequestedModel string      `json:"requested_model"`
	AliasID        string      `json:"alias_id"`
	Alias          string      `json:"alias"`
	AliasChain     []string    `json:"alias_chain"`
	Resolution     string      `json:"resolution"`
	PoolID         string      `json:"pool_id"`
	Pool           string      `json:"pool"`
	Strategy       string      `json:"strategy"`
	PlanID         string      `json:"plan_id"`
	PlanVersion    int64       `json:"plan_version"`
	PlanVersionID  string      `json:"plan_version_id"`
	ConfigRevision int64       `json:"config_revision"`
	Required       []string    `json:"required_capabilities"`
	Candidates     []Candidate `json:"candidates"`
	Excluded       []Exclusion `json:"excluded"`
	Notes          []string    `json:"notes"`
	Affinity       string      `json:"affinity,omitempty"`
}

// Error is a routing failure with an explanation.
type Error struct {
	Kind     ir.ErrorKind
	Message  string
	Decision *Decision
}

func (e *Error) Error() string { return e.Message }

// Decide evaluates the plan for a request.
func Decide(in Input) (*Decision, error) {
	s := in.Snapshot
	d := &Decision{RequestedModel: in.RequestedModel, Required: slices.Clone(in.Required), Candidates: []Candidate{}, Excluded: []Exclusion{}, Notes: []string{},
		ConfigRevision: s.ConfigRevision}
	if in.Plan == nil || in.Plan.Published == nil {
		return d, &Error{Kind: ir.ErrPolicy, Message: "the client's routing plan has no published version", Decision: d}
	}
	d.PlanID, d.PlanVersion, d.PlanVersionID = in.Plan.ID, in.Plan.Published.Version, in.Plan.Published.ID
	doc := in.Plan.Published.Document
	if allowed := in.Binding.Spec.AllowedModels; len(allowed) > 0 && !slices.Contains(allowed, in.RequestedModel) {
		return d, &Error{Kind: ir.ErrPermission, Message: fmt.Sprintf("model %q is not allowed for this client", in.RequestedModel), Decision: d}
	}
	alias, how, err := s.ResolveModel(in.Plan, in.RequestedModel)
	if err != nil {
		return d, &Error{Kind: ir.ErrNotFound, Message: err.Error(), Decision: d}
	}
	d.AliasID, d.Alias, d.Resolution = alias.ID, alias.Name, how
	for _, id := range s.AliasChain[alias.ID] {
		d.AliasChain = append(d.AliasChain, s.Aliases[id].Name)
	}
	poolID := s.AliasPool[alias.ID]
	pool := s.Pools[poolID]
	if pool == nil {
		return d, &Error{Kind: ir.ErrInternal, Message: "alias does not resolve to a pool", Decision: d}
	}
	d.PoolID, d.Pool, d.Strategy = pool.ID, pool.Name, pool.Spec.Strategy
	required := slices.Clone(in.Required)
	required = append(required, doc.Restrictions.RequiredCapabilities...)
	for _, id := range s.AliasChain[alias.ID] {
		required = append(required, s.Aliases[id].Spec.RequiredCapabilities...)
	}
	required = cp.SortedUnique(required)
	d.Required = required

	for _, m := range pool.Spec.Members {
		acc := s.Accounts[m.AccountID]
		ex := Exclusion{AccountID: m.AccountID, Model: m.Model}
		if acc == nil {
			ex.Stage, ex.Reason = "restriction", "account_missing"
			d.Excluded = append(d.Excluded, ex)
			continue
		}
		ex.Account = acc.Name
		prov := s.Providers[acc.Spec.ProviderID]
		if prov == nil {
			ex.Stage, ex.Reason = "restriction", "provider_missing"
			d.Excluded = append(d.Excluded, ex)
			continue
		}
		ex.ProviderID = prov.ID
		fail := func(stage, reason, detail string) {
			ex.Stage, ex.Reason, ex.Detail = stage, reason, detail
			d.Excluded = append(d.Excluded, ex)
		}
		if !acc.Spec.Enabled {
			fail("restriction", "account_disabled", "")
			continue
		}
		r := doc.Restrictions
		if len(r.AllowedProviderIDs) > 0 && !slices.Contains(r.AllowedProviderIDs, prov.ID) {
			fail("restriction", "provider_not_allowed", "plan restricts providers")
			continue
		}
		if slices.Contains(r.DeniedProviderIDs, prov.ID) {
			fail("restriction", "provider_denied", "plan denies this provider")
			continue
		}
		if len(r.AllowedBilling) > 0 && !slices.Contains(r.AllowedBilling, acc.Spec.Billing) {
			fail("restriction", "billing_not_allowed", "plan allows "+fmt.Sprint(r.AllowedBilling))
			continue
		}
		if len(r.AllowedClassifications) > 0 && !slices.Contains(r.AllowedClassifications, prov.Spec.Classification) {
			fail("restriction", "classification_not_allowed", prov.Spec.Classification)
			continue
		}
		up := ir.Protocol("")
		if in.UpstreamFor != nil {
			up = in.UpstreamFor(prov.Spec.Type, in.Inbound)
		}
		if up == "" {
			fail("restriction", "no_executor", "no executor available for provider type "+prov.Spec.Type)
			continue
		}
		c := Candidate{ProviderID: prov.ID, Provider: prov.Name, AccountID: acc.ID, Account: acc.Name, Model: m.Model, Billing: acc.Spec.Billing,
			Upstream: up, Native: up == in.Inbound, Priority: m.Priority, Weight: m.Weight, Reasons: []string{}}
		if ms, ok := s.ModelOf(prov.ID, m.Model); ok {
			c.ModelSpec = ms
			var missing []string
			if len(ms.Capabilities) > 0 {
				for _, cap := range required {
					if !slices.Contains(ms.Capabilities, cap) {
						missing = append(missing, cap)
					}
				}
			}
			if len(missing) > 0 {
				fail("capability", "missing_capabilities", fmt.Sprint(missing))
				continue
			}
			if len(ms.Capabilities) == 0 && len(required) > 0 {
				c.Reasons = append(c.Reasons, "capabilities not declared for model; requirements unverified")
			}
		} else if len(required) > 0 {
			c.Reasons = append(c.Reasons, "model not in provider catalog; capabilities unknown")
		}
		if in.HealthOf != nil {
			h := in.HealthOf(acc.ID)
			c.Health = &h
			if !h.Available {
				fail("health", "unavailable", h.Reason)
				continue
			}
			if h.Stale {
				c.Reasons = append(c.Reasons, "quota data is stale")
			}
		}
		for _, fam := range in.SealedFamilies {
			if up.Family() != fam {
				c.Reasons = append(c.Reasons, "opaque "+fam+" state in history will not reach this upstream")
			}
		}
		d.Candidates = append(d.Candidates, c)
	}

	rank(d, pool, in)

	// Paid fallback: a metered candidate may only follow a non-metered
	// first choice when the plan explicitly authorizes paid fallback.
	if len(d.Candidates) > 0 && d.Candidates[0].Billing != cp.BillingMetered && !doc.Fallback.AllowPaidFallback {
		kept := d.Candidates[:1]
		for _, c := range d.Candidates[1:] {
			if c.Billing == cp.BillingMetered {
				d.Excluded = append(d.Excluded, Exclusion{ProviderID: c.ProviderID, AccountID: c.AccountID, Account: c.Account, Model: c.Model,
					Stage: "policy", Reason: "paid_fallback_not_authorized", Detail: "plan does not allow falling back to metered API accounts"})
				continue
			}
			kept = append(kept, c)
		}
		d.Candidates = kept
	}
	if len(d.Candidates) == 0 {
		kind := ir.ErrQuotaExhausted
		msg := "no eligible account is available for model " + in.RequestedModel
		allHealth := len(d.Excluded) > 0
		for _, e := range d.Excluded {
			if e.Stage != "health" {
				allHealth = false
			}
		}
		if !allHealth {
			kind = ir.ErrPolicy
		}
		return d, &Error{Kind: kind, Message: msg, Decision: d}
	}
	return d, nil
}

// rank orders candidates deterministically by pool strategy and affinity.
func rank(d *Decision, pool *cp.Res[cp.PoolSpec], in Input) {
	cs := d.Candidates
	seed := in.RequestSeed
	if in.SessionKey != "" {
		seed = in.SessionKey
	}
	switch pool.Spec.Strategy {
	case "weighted":
		// Weighted order without replacement, seeded per session so a
		// session keeps a stable preference.
		h := fnv.New64a()
		h.Write([]byte(seed))
		x := h.Sum64()
		for i := range cs {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			r := float64(x%1_000_000)/1_000_000 + 1e-9
			cs[i].Score = -logApprox(r) / float64(max(cs[i].Weight, 1))
		}
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].Score < cs[j].Score })
		d.Notes = append(d.Notes, "weighted ordering seeded per session")
	case "round_robin":
		n := rrNext(pool.ID, len(cs))
		if len(cs) > 0 {
			cs = append(cs[n:], cs[:n]...)
		}
		d.Notes = append(d.Notes, "round robin")
	case "reset_soonest", "least_used":
		sort.SliceStable(cs, func(i, j int) bool {
			hi, hj := cs[i].Health, cs[j].Health
			ki, kj := quotaKey(hi, pool.Spec.Strategy), quotaKey(hj, pool.Spec.Strategy)
			if ki != kj {
				return ki < kj
			}
			return cs[i].Priority < cs[j].Priority
		})
		d.Notes = append(d.Notes, pool.Spec.Strategy+" ordering (members without quota data rank after members with data)")
	default:
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].Priority < cs[j].Priority })
	}
	// Session affinity keeps a conversation on the member it last used while
	// that member remains eligible (prompt-cache friendly).
	if pool.Spec.Affinity == "session" && in.SessionKey != "" {
		if prev, ok := affinity.get(pool.ID + "|" + in.SessionKey); ok {
			for i, c := range cs {
				if c.AccountID+"|"+c.Model == prev && i > 0 {
					c.Reasons = append(c.Reasons, "session affinity")
					cs = append([]Candidate{c}, append(cs[:i:i], cs[i+1:]...)...)
					d.Affinity = "kept previous member"
					break
				} else if c.AccountID+"|"+c.Model == prev {
					d.Affinity = "kept previous member"
				}
			}
			if d.Affinity == "" {
				d.Affinity = "previous member no longer eligible"
			}
		}
	}
	// Prefer same-family upstreams when the history carries opaque state,
	// without overriding explicit priorities (stable sort by penalty).
	if len(in.SealedFamilies) > 0 {
		for start := 0; start < len(cs); {
			end := start + 1
			for end < len(cs) && cs[end].Priority == cs[start].Priority {
				end++
			}
			run := cs[start:end]
			sort.SliceStable(run, func(i, j int) bool {
				return familyPenalty(run[i], in.SealedFamilies) < familyPenalty(run[j], in.SealedFamilies)
			})
			start = end
		}
	}
	d.Candidates = cs
}

func familyPenalty(c Candidate, fams []string) int {
	for _, f := range fams {
		if c.Upstream.Family() != f {
			return 1
		}
	}
	return 0
}

func quotaKey(h *Health, strategy string) float64 {
	if h == nil {
		return 1e18
	}
	switch strategy {
	case "reset_soonest":
		if h.ResetAt == nil {
			return 1e18
		}
		return float64(h.ResetAt.Unix())
	default: // least_used: most remaining first
		if h.Remaining == nil {
			return 1e18
		}
		return 1 - *h.Remaining
	}
}

func logApprox(x float64) float64 {
	// Natural log via series on (x-1)/(x+1); adequate for ordering.
	y := (x - 1) / (x + 1)
	y2 := y * y
	sum, term := 0.0, y
	for n := 1; n < 40; n += 2 {
		sum += term / float64(n)
		term *= y2
	}
	return 2 * sum
}

// RecordAffinity remembers the member a session used.
func RecordAffinity(poolID, sessionKey, accountID, model string) {
	if sessionKey == "" {
		return
	}
	affinity.put(poolID+"|"+sessionKey, accountID+"|"+model)
}

var (
	rrMu    sync.Mutex
	rrState = map[string]int{}
)

func rrNext(pool string, n int) int {
	if n == 0 {
		return 0
	}
	rrMu.Lock()
	defer rrMu.Unlock()
	v := rrState[pool] % n
	rrState[pool]++
	return v
}

// affinity is a bounded in-memory map (lost on restart, by design).
var affinity = &lru{max: 50000, ttl: 6 * time.Hour, m: map[string]lruEntry{}}

type lruEntry struct {
	v  string
	at time.Time
}

type lru struct {
	mu  sync.Mutex
	max int
	ttl time.Duration
	m   map[string]lruEntry
}

func (l *lru) get(k string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[k]
	if !ok || time.Since(e.at) > l.ttl {
		return "", false
	}
	return e.v, true
}

func (l *lru) put(k, v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) >= l.max {
		var oldestK string
		var oldest time.Time
		for kk, e := range l.m {
			if oldestK == "" || e.at.Before(oldest) {
				oldestK, oldest = kk, e.at
			}
		}
		delete(l.m, oldestK)
	}
	l.m[k] = lruEntry{v: v, at: time.Now()}
}

// Transition reports whether the fallback policy allows moving from a
// failed candidate to the next one, and which step permits it.
func Transition(f cp.FallbackPolicy, from, to Candidate) (string, bool) {
	var step string
	switch {
	case from.AccountID == to.AccountID && from.Model == to.Model:
		step = cp.StepRetry
	case from.ProviderID != to.ProviderID:
		step = cp.StepAlternateProvider
	case from.Model == to.Model:
		step = cp.StepAlternateAccount
	default:
		step = cp.StepAlternateModel
	}
	for _, s := range f.Steps {
		if s == cp.StepStop {
			return step, false
		}
		if s == step {
			return step, true
		}
	}
	return step, false
}
