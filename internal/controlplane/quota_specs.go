package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
)

// QuotaMonitorSpec configures polling of a provider's quota or usage API.
// When AccountID is set, observations feed that account's routing health.
type QuotaMonitorSpec struct {
	Adapter   string         `json:"adapter" doc:"Quota adapter id; see GET /api/v1/quota/adapters."`
	AccountID string         `json:"account_id" required:"false" doc:"Provider account whose routing health this monitor feeds. Empty = monitor only."`
	Options   map[string]any `json:"options" required:"false" doc:"Adapter-specific, non-secret options."`
	// Secret references (env:NAME or file:/path) as alternatives to the
	// write-only stored secrets of the same name.
	APIKeyRef         string `json:"api_key_ref" required:"false"`
	ManagementKeyRef  string `json:"management_key_ref" required:"false"`
	AccessTokenRef    string `json:"access_token_ref" required:"false"`
	ServiceAccountRef string `json:"service_account_ref" required:"false"`
	PollIntervalSecs  int    `json:"poll_interval_secs" required:"false" minimum:"0" maximum:"86400" doc:"0 = the adapter's recommended interval."`
	Enabled           bool   `json:"enabled"`
	// AcknowledgeTermsRisk must be true for adapters that read undocumented
	// endpoints (tier unofficial).
	AcknowledgeTermsRisk bool   `json:"acknowledge_terms_risk" required:"false" doc:"Required for unofficial adapters, which use undocumented endpoints that may break or conflict with provider terms."`
	SharedScopeID        string `json:"shared_scope_id" required:"false" doc:"Monitors drawing on the same provider quota share an id so capacity is not double counted."`
	// AllowPrivateNetwork lets the adapter reach private and loopback
	// endpoints (self-hosted gateways, local tests). Metadata addresses stay
	// blocked.
	AllowPrivateNetwork bool `json:"allow_private_network" required:"false" doc:"Permit private or loopback endpoints for this monitor. Cloud metadata addresses stay blocked."`
	// ExhaustedBelow is the remaining fraction at or below which a global
	// bucket marks the linked account unavailable for routing.
	ExhaustedBelow float64 `json:"exhausted_below" required:"false" minimum:"0" maximum:"1" doc:"Remaining fraction at or below which the linked account is treated as exhausted. 0 = only when fully used."`
	StaleAfterSecs int     `json:"stale_after_secs" required:"false" minimum:"0" maximum:"604800" doc:"Observations older than this are stale. 0 = three poll intervals."`
}

// ValidateQuotaAdapter is installed by the quota module to check the adapter
// id, its options and the terms acknowledgement.
var ValidateQuotaAdapter func(v *Validator, s *QuotaMonitorSpec)

func validateQuotaMonitor(v *Validator, s *QuotaMonitorSpec) {
	v.Check(s.Adapter != "", "spec.adapter", "is required")
	for loc, ref := range map[string]string{"spec.api_key_ref": s.APIKeyRef, "spec.management_key_ref": s.ManagementKeyRef,
		"spec.access_token_ref": s.AccessTokenRef, "spec.service_account_ref": s.ServiceAccountRef} {
		checkSecretRef(v, loc, ref)
	}
	if s.SharedScopeID != "" {
		v.Check(refKeyRE.MatchString(s.SharedScopeID), "spec.shared_scope_id", "must be a lowercase identifier")
	}
	if ValidateQuotaAdapter != nil && s.Adapter != "" {
		ValidateQuotaAdapter(v, s)
	}
}

// StaleAfter returns the staleness horizon.
func (s QuotaMonitorSpec) StaleAfter(defaultInterval time.Duration) time.Duration {
	if s.StaleAfterSecs > 0 {
		return time.Duration(s.StaleAfterSecs) * time.Second
	}
	iv := defaultInterval
	if s.PollIntervalSecs > 0 {
		iv = time.Duration(s.PollIntervalSecs) * time.Second
	}
	if iv <= 0 {
		iv = 5 * time.Minute
	}
	return 3 * iv
}

// Budget scopes.
const (
	BudgetGlobal    = "global"
	BudgetWorkspace = "workspace"
	BudgetProject   = "project"
	BudgetBinding   = "binding"
	BudgetAccount   = "account"
)

// Cost kinds recorded in the ledger. They are separate categories and are
// never added together across kinds unless a budget explicitly opts in.
const (
	CostEstimatedAPI   = "estimated_api_charge"
	CostActualAPI      = "actual_api_charge"
	CostListEquivalent = "list_equivalent"
)

// BudgetSpec limits spending for a scope over a calendar period, or in
// total over one window for a one-time budget (period "once", D-044).
type BudgetSpec struct {
	Scope       string   `json:"scope" enum:"global,workspace,project,binding,account"`
	ScopeID     string   `json:"scope_id" required:"false" doc:"Id of the workspace, project, client binding or provider account. Empty for global."`
	Period      string   `json:"period" enum:"day,week,month,once" doc:"How the budget repeats: day, week (Monday start) or month; once never resets."`
	StartsAt    string   `json:"starts_at" required:"false" doc:"One-time budgets only: RFC 3339 time from which spend counts. Empty = when the budget was created. May be in the past (to count earlier spend) or the future. Changing a recurring budget to once with this empty counts all spend since the budget was created."`
	EndsAt      string   `json:"ends_at" required:"false" doc:"One-time budgets only: RFC 3339 time after which the budget no longer applies. Empty = never."`
	Limit       string   `json:"limit" doc:"Decimal amount in currency."`
	Currency    string   `json:"currency" doc:"ISO currency; only costs in this currency count."`
	CostKinds   []string `json:"cost_kinds" required:"false" doc:"Cost categories that count. Default: estimated_api_charge and actual_api_charge. list_equivalent (subscription usage priced at list rates) counts only when listed."`
	Enforcement string   `json:"enforcement" required:"false" enum:"block,warn" doc:"block rejects new attempts once the limit would be exceeded; warn only emits events."`
	WarnAt      float64  `json:"warn_at" required:"false" minimum:"0" maximum:"1" doc:"Fraction of the limit that emits a warning event. 0 = 0.8."`
	Timezone    string   `json:"timezone" required:"false" doc:"IANA timezone for period boundaries. Empty = UTC. Must be empty for one-time budgets."`
	Enabled     bool     `json:"enabled"`
}

func normalizeBudget(s *BudgetSpec) {
	if len(s.CostKinds) == 0 {
		s.CostKinds = []string{CostActualAPI, CostEstimatedAPI}
	}
	s.CostKinds = SortedUnique(s.CostKinds)
	s.Currency = strings.ToUpper(s.Currency)
	if s.Enforcement == "" {
		s.Enforcement = "block"
	}
	if s.WarnAt == 0 {
		s.WarnAt = 0.8
	}
	s.StartsAt = canonicalTime(s.StartsAt)
	s.EndsAt = canonicalTime(s.EndsAt)
}

// PeriodOnce is the period of a one-time budget: a total limit over one
// window that never resets by itself (D-044).
const PeriodOnce = "once"

// budgetTimeLayout is the canonical form of starts_at and ends_at.
const budgetTimeLayout = "2006-01-02T15:04:05Z"

// canonicalTime re-encodes an RFC 3339 time in UTC, truncated to the second.
// Values that do not parse are kept for the validator to report.
func canonicalTime(v string) string {
	if v == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return v
	}
	return t.UTC().Truncate(time.Second).Format(budgetTimeLayout)
}

// FormatBudgetTime renders t in the canonical starts_at/ends_at form.
func FormatBudgetTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(budgetTimeLayout)
}

func validateBudget(v *Validator, s *BudgetSpec) {
	v.Check(slices.Contains([]string{BudgetGlobal, BudgetWorkspace, BudgetProject, BudgetBinding, BudgetAccount}, s.Scope), "spec.scope", "must be global, workspace, project, binding or account")
	v.Check((s.Scope == BudgetGlobal) == (s.ScopeID == ""), "spec.scope_id", "is required for scoped budgets and must be empty for global ones")
	v.Check(slices.Contains([]string{"day", "week", "month", PeriodOnce}, s.Period), "spec.period", "must be day, week, month or once")
	if s.Period == PeriodOnce {
		v.Check(s.Timezone == "", "spec.timezone", "must be empty: one-time budgets have no period boundaries")
		start, serr := time.Parse(time.RFC3339, s.StartsAt)
		end, eerr := time.Parse(time.RFC3339, s.EndsAt)
		v.Check(s.StartsAt == "" || serr == nil, "spec.starts_at", "must be an RFC 3339 time")
		v.Check(s.EndsAt == "" || eerr == nil, "spec.ends_at", "must be an RFC 3339 time")
		if s.StartsAt != "" && s.EndsAt != "" && serr == nil && eerr == nil {
			v.Check(end.After(start), "spec.ends_at", "must be after starts_at")
		}
	} else {
		v.Check(s.StartsAt == "", "spec.starts_at", "only for one-time budgets")
		v.Check(s.EndsAt == "", "spec.ends_at", "only for one-time budgets")
	}
	if a, err := money.ParseDecimal(s.Limit); err != nil || a <= 0 {
		v.Fail("spec.limit", "must be a positive decimal")
	}
	v.Check(len(s.Currency) == 3, "spec.currency", "must be a 3-letter currency code")
	for i, k := range s.CostKinds {
		v.Check(slices.Contains([]string{CostEstimatedAPI, CostActualAPI, CostListEquivalent}, k), "spec.cost_kinds", "unknown cost kind at index "+itoaCP(i))
	}
	v.Check(s.Enforcement == "block" || s.Enforcement == "warn", "spec.enforcement", "must be block or warn")
	if s.Timezone != "" && s.Period != PeriodOnce {
		_, err := time.LoadLocation(s.Timezone)
		v.Check(err == nil, "spec.timezone", "unknown timezone")
	}
}

func itoaCP(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// PeriodStart returns the start of the budget period containing t.
func (s BudgetSpec) PeriodStart(t time.Time) time.Time {
	loc := time.UTC
	if s.Timezone != "" {
		if l, err := time.LoadLocation(s.Timezone); err == nil {
			loc = l
		}
	}
	lt := t.In(loc)
	day := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	switch s.Period {
	case "week":
		wd := (int(day.Weekday()) + 6) % 7 // Monday start
		return day.AddDate(0, 0, -wd)
	case "month":
		return time.Date(lt.Year(), lt.Month(), 1, 0, 0, 0, 0, loc)
	}
	return day
}

// Window returns the counting window at now. For a recurring budget it is
// the calendar period containing now. For a one-time budget it starts at
// starts_at, or at created when that is empty, and ends at ends_at; open is
// true (and end zero) when ends_at is empty. A one-time window never moves.
func (s BudgetSpec) Window(now, created time.Time) (start, end time.Time, open bool) {
	if s.Period != PeriodOnce {
		start = s.PeriodStart(now)
		return start, s.PeriodEnd(start), false
	}
	start = created.UTC()
	if t, err := time.Parse(time.RFC3339, s.StartsAt); err == nil {
		start = t.UTC()
	}
	t, err := time.Parse(time.RFC3339, s.EndsAt)
	if s.EndsAt == "" || err != nil {
		return start, time.Time{}, true
	}
	return start, t.UTC(), false
}

// PeriodEnd returns the end (exclusive) of the period starting at start.
func (s BudgetSpec) PeriodEnd(start time.Time) time.Time {
	switch s.Period {
	case "week":
		return start.AddDate(0, 0, 7)
	case "month":
		return start.AddDate(0, 1, 0)
	}
	return start.AddDate(0, 0, 1)
}

var (
	QuotaMonitorDef = &Def[QuotaMonitorSpec]{Name: KindQuotaMonitor, Plural: "quota-monitors", Prefix: ids.QuotaMonitor, Title: "Quota monitor",
		Secrets: []SecretField{
			{Name: "api_key", Description: "Provider API key used by the adapter. Write-only.", RefField: "api_key_ref"},
			{Name: "management_key", Description: "Management or admin key. Write-only.", RefField: "management_key_ref"},
			{Name: "access_token", Description: "OAuth access token. Write-only.", RefField: "access_token_ref"},
			{Name: "service_account", Description: "Service account JSON key. Write-only.", RefField: "service_account_ref"},
		},
		Normalize: func(s *QuotaMonitorSpec) { s.Options = EmptyMap(s.Options) },
		Validate:  validateQuotaMonitor,
		Refs: func(s *QuotaMonitorSpec) []Ref {
			return []Ref{{"spec.account_id", s.AccountID, []string{KindAccount}}}
		}}
	BudgetDef = &Def[BudgetSpec]{Name: KindBudget, Plural: "budgets", Prefix: ids.Budget, Title: "Budget",
		Normalize: normalizeBudget, Validate: validateBudget,
		Refs: func(s *BudgetSpec) []Ref {
			kinds := map[string]string{BudgetWorkspace: KindWorkspace, BudgetProject: KindProject, BudgetBinding: KindClientBinding, BudgetAccount: KindAccount}
			if k, ok := kinds[s.Scope]; ok {
				return []Ref{{"spec.scope_id", s.ScopeID, []string{k}}}
			}
			return nil
		}}
)

// ResetBudget starts a one-time budget's window over: it sets starts_at to
// the server's current time (to the second) through the normal mutation path,
// so If-Match, history, audit, outbox and the snapshot behave as for a
// replace. Spend before the new start stays in the ledger and stops counting
// toward the budget (D-044).
func (s *Service) ResetBudget(ctx context.Context, idOrName string, m Mutation) (*Object, error) {
	if m.ManagedBy == "" {
		m.ManagedBy = ManagedAPI
	}
	var id string
	err := s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		cur, err := s.get(ctx, tx, BudgetDef, idOrName)
		if err != nil {
			return "", err
		}
		if err := checkIfMatch(m.IfMatch, cur.Revision); err != nil {
			return "", err
		}
		spec, err := Decode[BudgetSpec](cur.Spec)
		if err != nil {
			return "", err
		}
		if spec.Period != PeriodOnce {
			return "", &ValidationError{Errors: []FieldError{{Location: "body.spec.period", Message: "only one-time budgets can be reset; recurring budgets reset each period"}}}
		}
		spec.StartsAt = FormatBudgetTime(now)
		if spec.EndsAt != "" {
			if end, err := time.Parse(time.RFC3339, spec.EndsAt); err == nil && !end.After(now.Truncate(time.Second)) {
				return "", &ValidationError{Errors: []FieldError{{Location: "body.spec.ends_at", Message: "the budget has ended; extend or clear ends_at first"}}}
			}
		}
		raw, err := json.Marshal(spec)
		if err != nil {
			return "", err
		}
		id, _, err = s.ReplaceTx(ctx, tx, now, rev, BudgetDef, cur.ID, "", raw, nil, m)
		return "reset budget " + cur.Name, err
	}, m)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, BudgetDef, id)
}
