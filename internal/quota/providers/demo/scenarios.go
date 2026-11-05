package demo

import (
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// Demo provider IDs. They are distinct from real provider IDs so demo data
// can never be confused with, or aggregated into, production data.
const (
	ProviderClaude   domain.ProviderID = "demo_claude"
	ProviderCodex    domain.ProviderID = "demo_codex"
	ProviderNexos    domain.ProviderID = "demo_nexos"
	ProviderDeepSeek domain.ProviderID = "demo_deepseek"
)

// activityProfile shapes the account's activity over the day.
type activityProfile int

const (
	profileOffice activityProfile = iota // weekday office hours in Europe/Vilnius
	profileCI                            // near-constant automated workload
)

type bucketKind int

const (
	bucketFixedPercent bucketKind = iota
	bucketRollingPercent
	bucketBudget
	bucketSharedBudget
	bucketBalance
)

type bucketModel struct {
	spec domain.BucketSpec
	kind bucketKind
	// weight converts account activity into consumption of this bucket per
	// hour (percentage points for percent buckets, currency for budgets).
	weight float64
	// anchor is an offset from the simulation "start" at which one reset
	// occurs; resets repeat every spec.Period (fixed percent buckets).
	anchor time.Duration
	limit  float64
	// monthly budgets reset at the start of each calendar month.
	monthly bool
}

type scenario struct {
	id          domain.AccountID
	provider    domain.ProviderID
	alias       string
	masked      string
	tags        []string
	sharedScope string
	seed        uint64
	// base is the activity scale (pp/h of the primary 5h bucket at peak).
	base    float64
	profile activityProfile
	// spike adds activity during [spikeFrom, spikeTo) relative to start.
	spikeFrom, spikeTo time.Duration
	spikeExtra         float64
	// authFailFrom / unavailableFrom make fetches fail from that offset.
	authFailFrom    *time.Duration
	unavailableFrom *time.Duration
	buckets         []bucketModel
	// topUps add balance at offsets from start (balance buckets).
	topUps         map[time.Duration]float64
	initialBalance float64
	note           string
}

func dur(d time.Duration) *time.Duration { return &d }

const (
	h = time.Hour
	d = 24 * time.Hour
)

func fiveHour(anchor time.Duration, periodic bool) bucketModel {
	sem := domain.ResetProviderTimestamp
	if periodic {
		sem = domain.ResetFixedPeriodic
	}
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "five_hour", Label: "5-hour session", Scope: domain.ScopeAccount, Kind: domain.KindFixedWindow,
			Window: 5 * h, Unit: domain.UnitPercent, ResetSemantics: sem, Period: 5 * h, Global: true, Official: true,
		},
		kind: bucketFixedPercent, weight: 1, anchor: anchor, limit: 100,
	}
}

func sevenDay(weight float64, anchor time.Duration, periodic bool) bucketModel {
	sem := domain.ResetProviderTimestamp
	if periodic {
		sem = domain.ResetFixedPeriodic
	}
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "seven_day", Label: "7-day weekly", Scope: domain.ScopeAccount, Kind: domain.KindFixedWindow,
			Window: 7 * d, Unit: domain.UnitPercent, ResetSemantics: sem, Period: 7 * d, Global: true, Official: true,
		},
		kind: bucketFixedPercent, weight: weight, anchor: anchor, limit: 100,
	}
}

func sevenDayModel(model string, weight float64, anchor time.Duration) bucketModel {
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "seven_day:model:" + model, Label: "7-day " + model, Scope: domain.ScopeModel,
			Kind: domain.KindFixedWindow, Window: 7 * d, Unit: domain.UnitPercent, Model: model, ModelFamily: model,
			ResetSemantics: domain.ResetFixedPeriodic, Period: 7 * d, Global: false, Official: true,
		},
		kind: bucketFixedPercent, weight: weight, anchor: anchor, limit: 100,
	}
}

func rollingFiveHour() bucketModel {
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "five_hour", Label: "5-hour rolling", Scope: domain.ScopeAccount, Kind: domain.KindRollingWindow,
			Window: 5 * h, Unit: domain.UnitPercent, ResetSemantics: domain.ResetRolling, Global: true, Official: true,
		},
		kind: bucketRollingPercent, weight: 1, limit: 100,
	}
}

func keyBudget(limit, weight float64) bucketModel {
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "budget:key", Label: "API key monthly budget", Scope: domain.ScopeAPIKey, Kind: domain.KindBudget,
			Unit: domain.UnitCurrency, Currency: "EUR", ResetSemantics: domain.ResetProviderTimestamp,
			Global: true, Official: true,
		},
		kind: bucketBudget, weight: weight, limit: limit, monthly: true,
	}
}

func teamBudget(scope string, limit float64) bucketModel {
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "budget:team", Label: "Team monthly budget (shared)", Scope: domain.ScopeOrganization,
			Kind: domain.KindBudget, Unit: domain.UnitCurrency, Currency: "EUR", SharedScopeID: scope,
			ResetSemantics: domain.ResetProviderTimestamp, Global: true, Official: true,
		},
		kind: bucketSharedBudget, limit: limit, monthly: true,
	}
}

func balance() bucketModel {
	return bucketModel{
		spec: domain.BucketSpec{
			Key: "balance", Label: "Prepaid balance", Scope: domain.ScopeAccount, Kind: domain.KindBalance,
			Unit: domain.UnitCurrency, Currency: "CNY", ResetSemantics: domain.ResetNone, Global: true, Official: true,
		},
		kind: bucketBalance, weight: 1,
	}
}

const nexosTeamScope = "demo-nexos-team-platform"

// scenarios returns the deterministic demo account set (spec §17).
func scenarios() []scenario {
	return []scenario{
		// --- Claude-style (6) ---
		{
			id: "claude-main", provider: ProviderClaude, alias: "main", masked: "m***@example.test",
			tags: []string{"personal"}, seed: 11, base: 10, profile: profileOffice,
			buckets: []bucketModel{fiveHour(-2*h, true), sevenDay(0.10, -3*d-4*h, true), sevenDayModel("opus", 0.15, -3*d-4*h)},
			note:    "Healthy account.",
		},
		{
			id: "claude-work-2", provider: ProviderClaude, alias: "work-2", masked: "w***2@example.test",
			tags: []string{"work"}, seed: 22, base: 6, profile: profileOffice,
			spikeFrom: -100 * time.Minute, spikeTo: 140 * time.Minute, spikeExtra: 3.3,
			buckets: []bucketModel{fiveHour(-80*time.Minute, true), sevenDay(0.11, -2*d-6*h, true), sevenDayModel("opus", 0.12, -2*d-6*h)},
			note:    "Burn spike: latest workload far above baseline; predicted to exhaust the 5-hour window before it resets.",
		},
		{
			id: "claude-work-3", provider: ProviderClaude, alias: "work-3", masked: "w***3@example.test",
			tags: []string{"work"}, seed: 33, base: 9, profile: profileOffice,
			buckets: []bucketModel{fiveHour(-1*h, true), sevenDay(0.35, -5*d-6*h, true), sevenDayModel("opus", 0.0, -5*d-6*h)},
			note:    "7-day quota exhausted while the 5-hour and model quotas are full; fixed weekly reset restores it.",
		},
		{
			id: "claude-work-4", provider: ProviderClaude, alias: "work-4", masked: "w***4@example.test",
			tags: []string{"work"}, seed: 44, base: 7, profile: profileOffice, authFailFrom: dur(-5 * h),
			buckets: []bucketModel{fiveHour(-3*h, true), sevenDay(0.1, -1*d, true)},
			note:    "Authentication failing (credential expired).",
		},
		{
			id: "claude-work-5", provider: ProviderClaude, alias: "work-5", masked: "w***5@example.test",
			tags: []string{"work"}, seed: 55, base: 5, profile: profileOffice, unavailableFrom: dur(-3 * h),
			buckets: []bucketModel{fiveHour(-4*h, true), sevenDay(0.1, -4*d, true)},
			note:    "Stale: provider unreachable for the last 3 hours.",
		},
		{
			id: "claude-work-6", provider: ProviderClaude, alias: "work-6", masked: "w***6@example.test",
			tags: []string{"work", "opus-heavy"}, seed: 66, base: 8, profile: profileOffice,
			buckets: []bucketModel{fiveHour(-30*time.Minute, true), sevenDay(0.09, -6*d, true), sevenDayModel("opus", 0.42, -6*d)},
			note:    "Model-specific weekly quota nearly exhausted while the global quota is healthy.",
		},
		// --- Codex-style (3) ---
		{
			id: "codex-personal", provider: ProviderCodex, alias: "personal", masked: "p***@example.test",
			tags: []string{"personal"}, seed: 77, base: 7, profile: profileOffice,
			buckets: []bucketModel{fiveHour(-90*time.Minute, false), sevenDay(0.12, -4*d-2*h, false)},
			note:    "Healthy; provider reports only the next reset time.",
		},
		{
			id: "codex-team", provider: ProviderCodex, alias: "team", masked: "t***@example.test",
			tags: []string{"team"}, seed: 88, base: 9, profile: profileOffice,
			buckets: []bucketModel{rollingFiveHour(), sevenDay(0.1, -2*d, false)},
			note:    "Rolling 5-hour window: only net depletion is observable, so forecast confidence is limited.",
		},
		{
			id: "codex-ci", provider: ProviderCodex, alias: "ci-runner", masked: "c***@example.test",
			tags: []string{"ci"}, seed: 99, base: 32, profile: profileCI,
			buckets: []bucketModel{fiveHour(-4*h, false), sevenDay(0.02, -3*d, false)},
			note:    "CI workload repeatedly exhausts the 5-hour window and recovers at each reset.",
		},
		// --- nexos-style (3), two share a team budget scope ---
		{
			id: "nexos-prod-agents", provider: ProviderNexos, alias: "production-agents", tags: []string{"production"},
			sharedScope: nexosTeamScope, seed: 101, base: 0.9, profile: profileOffice,
			spikeFrom: -2 * h, spikeTo: 2 * h, spikeExtra: 3.6,
			buckets: []bucketModel{keyBudget(400, 1), teamBudget(nexosTeamScope, 1500)},
			note:    "Shares the team budget with research; correlated abnormal consumption in the last 2 hours.",
		},
		{
			id: "nexos-research", provider: ProviderNexos, alias: "research", tags: []string{"research"},
			sharedScope: nexosTeamScope, seed: 102, base: 0.4, profile: profileOffice,
			buckets: []bucketModel{keyBudget(250, 1), teamBudget(nexosTeamScope, 1500)},
			note:    "Shares the team budget with production-agents; shared capacity is counted once.",
		},
		{
			id: "nexos-sandbox", provider: ProviderNexos, alias: "sandbox", tags: []string{"sandbox"},
			seed: 103, base: 0.2, profile: profileOffice,
			buckets: []bucketModel{keyBudget(60, 1)},
			note:    "Small key budget close to its limit.",
		},
		// --- monetary balance (1) ---
		{
			id: "deepseek-dev", provider: ProviderDeepSeek, alias: "dev-balance", tags: []string{"dev"},
			seed: 111, base: 0.7, profile: profileOffice, initialBalance: 420,
			topUps:  map[time.Duration]float64{-4 * d: 300},
			buckets: []bucketModel{balance()},
			note:    "Prepaid monetary balance (CNY); never combined with percentage quotas.",
		},
	}
}
