package controlplane

import (
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
		if ref != "" {
			kind, val, ok := strings.Cut(ref, ":")
			v.Check(ok && val != "" && (kind == "env" || kind == "file"), loc, "must be env:NAME or file:/path")
		}
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

// BudgetSpec limits spending for a scope over a calendar period.
type BudgetSpec struct {
	Scope       string   `json:"scope" enum:"global,workspace,project,binding,account"`
	ScopeID     string   `json:"scope_id" required:"false" doc:"Id of the workspace, project, client binding or provider account. Empty for global."`
	Period      string   `json:"period" enum:"day,week,month"`
	Limit       string   `json:"limit" doc:"Decimal amount in currency."`
	Currency    string   `json:"currency" doc:"ISO currency; only costs in this currency count."`
	CostKinds   []string `json:"cost_kinds" required:"false" doc:"Cost categories that count. Default: estimated_api_charge and actual_api_charge. list_equivalent (subscription usage priced at list rates) counts only when listed."`
	Enforcement string   `json:"enforcement" required:"false" enum:"block,warn" doc:"block rejects new attempts once the limit would be exceeded; warn only emits events."`
	WarnAt      float64  `json:"warn_at" required:"false" minimum:"0" maximum:"1" doc:"Fraction of the limit that emits a warning event. 0 = 0.8."`
	Timezone    string   `json:"timezone" required:"false" doc:"IANA timezone for period boundaries. Empty = UTC."`
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
}

func validateBudget(v *Validator, s *BudgetSpec) {
	v.Check(slices.Contains([]string{BudgetGlobal, BudgetWorkspace, BudgetProject, BudgetBinding, BudgetAccount}, s.Scope), "spec.scope", "must be global, workspace, project, binding or account")
	v.Check((s.Scope == BudgetGlobal) == (s.ScopeID == ""), "spec.scope_id", "is required for scoped budgets and must be empty for global ones")
	v.Check(slices.Contains([]string{"day", "week", "month"}, s.Period), "spec.period", "must be day, week or month")
	if a, err := money.ParseDecimal(s.Limit); err != nil || a <= 0 {
		v.Fail("spec.limit", "must be a positive decimal")
	}
	v.Check(len(s.Currency) == 3, "spec.currency", "must be a 3-letter currency code")
	for i, k := range s.CostKinds {
		v.Check(slices.Contains([]string{CostEstimatedAPI, CostActualAPI, CostListEquivalent}, k), "spec.cost_kinds", "unknown cost kind at index "+itoaCP(i))
	}
	v.Check(s.Enforcement == "block" || s.Enforcement == "warn", "spec.enforcement", "must be block or warn")
	if s.Timezone != "" {
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
