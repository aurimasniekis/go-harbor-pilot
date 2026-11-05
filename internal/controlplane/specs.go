package controlplane

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
)

// Kind names.
const (
	KindWorkspace     = "workspace"
	KindProject       = "project"
	KindProvider      = "provider"
	KindAccount       = "provider_account"
	KindEntitlement   = "subscription_entitlement"
	KindPool          = "pool"
	KindAlias         = "model_alias"
	KindPlan          = "routing_plan"
	KindClientBinding = "client_binding"
	KindQuotaMonitor  = "quota_monitor"
	KindBudget        = "budget"
	KindPrivacyPolicy = "privacy_policy"
	KindTracePolicy   = "trace_policy"
)

var refKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var headerNameRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func checkRefs(v *Validator, loc string, m map[string]string) {
	if len(m) > 32 {
		v.Fail(loc, "at most 32 entries")
	}
	for k, val := range m {
		v.Check(refKeyRE.MatchString(k), loc+"."+k, "keys must be lowercase identifiers such as spaceman.space or git.repo")
		v.Check(len(val) <= 256, loc+"."+k, "value too long (max 256)")
	}
}

func checkText(v *Validator, loc, s string, max int) {
	v.Check(len(s) <= max, loc, fmt.Sprintf("must be at most %d characters", max))
}

// ---------- workspace / project ----------

// WorkspaceSpec groups projects, budgets and bindings.
type WorkspaceSpec struct {
	DisplayName string `json:"display_name" maxLength:"120"`
	Description string `json:"description" maxLength:"1000"`
}

// ProjectSpec is an attribution unit inside a workspace.
type ProjectSpec struct {
	WorkspaceID string `json:"workspace_id" doc:"Workspace this project belongs to."`
	DisplayName string `json:"display_name" maxLength:"120"`
	Description string `json:"description" maxLength:"1000"`
	// ExternalRefs are trusted references configured by an operator, e.g.
	// {"spaceman.space":"spc_…","git.repo":"github.com/org/repo"}.
	ExternalRefs map[string]string `json:"external_refs" doc:"Operator-configured external references (trusted)."`
}

// ---------- providers ----------

// Provider types.
const (
	ProviderAnthropic        = "anthropic"
	ProviderOpenAI           = "openai"
	ProviderGemini           = "gemini"
	ProviderOpenAICompatible = "openai_compatible"
	ProviderOllama           = "ollama"
	ProviderClaudeCode       = "claude_code"
	ProviderCodex            = "codex"
)

// ProviderType describes built-in knowledge about a provider type.
type ProviderType struct {
	Type           string
	DefaultBaseURL string
	Protocols      []string // native wire protocols the upstream speaks
	Classification string
	Execution      string // http | local | cli_bridge
	CredentialKind string // api_key | none | setup_token | cli_login
}

// ProviderTypes is the catalog of supported provider types.
var ProviderTypes = map[string]ProviderType{
	ProviderAnthropic:        {ProviderAnthropic, "https://api.anthropic.com", []string{"anthropic_messages"}, "official", "http", "api_key"},
	ProviderOpenAI:           {ProviderOpenAI, "https://api.openai.com/v1", []string{"openai_chat", "openai_responses"}, "official", "http", "api_key"},
	ProviderGemini:           {ProviderGemini, "https://generativelanguage.googleapis.com", []string{"gemini"}, "official", "http", "api_key"},
	ProviderOpenAICompatible: {ProviderOpenAICompatible, "", []string{"openai_chat"}, "partial", "http", "api_key"},
	ProviderOllama:           {ProviderOllama, "http://127.0.0.1:11434", []string{"ollama", "openai_chat"}, "local", "local", "none"},
	ProviderClaudeCode:       {ProviderClaudeCode, "", []string{"claude_code_bridge"}, "official", "cli_bridge", "setup_token"},
	ProviderCodex:            {ProviderCodex, "", []string{"codex_bridge"}, "official", "cli_bridge", "cli_login"},
}

// Capability names used by models, aliases and requirements.
var Capabilities = []string{"streaming", "tools", "parallel_tools", "tool_streaming", "vision", "reasoning", "structured_output", "prompt_cache", "pdf"}

// ModelPricing is list pricing per million units (decimal strings).
type ModelPricing struct {
	Currency          string `json:"currency" doc:"ISO 4217 currency, e.g. USD."`
	InputPerMTok      string `json:"input_per_mtok" doc:"Decimal price per million input tokens."`
	OutputPerMTok     string `json:"output_per_mtok"`
	CacheReadPerMTok  string `json:"cache_read_per_mtok"`
	CacheWritePerMTok string `json:"cache_write_per_mtok"`
	PriceVersion      string `json:"price_version" doc:"Operator label for this price list, recorded on every cost estimate."`
}

// ModelSpec declares a model served by a provider and its capabilities.
type ModelSpec struct {
	ID              string        `json:"id" doc:"Upstream model identifier."`
	DisplayName     string        `json:"display_name"`
	ContextWindow   int           `json:"context_window" minimum:"0"`
	MaxOutputTokens int           `json:"max_output_tokens" minimum:"0"`
	Capabilities    []string      `json:"capabilities"`
	Pricing         *ModelPricing `json:"pricing,omitempty" doc:"List prices for estimates; omit when unknown."`
}

// ProviderSpec configures an upstream provider endpoint.
type ProviderSpec struct {
	Type                string            `json:"type" enum:"anthropic,openai,gemini,openai_compatible,ollama,claude_code,codex"`
	DisplayName         string            `json:"display_name" maxLength:"120"`
	BaseURL             string            `json:"base_url" doc:"Upstream base URL. Defaults per type; required for openai_compatible."`
	AllowPrivateNetwork bool              `json:"allow_private_network" doc:"Explicitly authorize a private or loopback destination (local models). Cloud metadata addresses stay blocked."`
	Classification      string            `json:"classification" enum:"official,partial,unofficial,local," doc:"Integration classification shown wherever this provider is used. Empty = the type default."`
	Headers             map[string]string `json:"headers" doc:"Static non-secret headers sent upstream (authorization headers are refused)."`
	RequestTimeoutSecs  int               `json:"request_timeout_seconds" minimum:"0" maximum:"3600" doc:"Overall upstream request timeout; 0 = default (600s)."`
	IdleTimeoutSecs     int               `json:"stream_idle_timeout_seconds" minimum:"0" maximum:"3600" doc:"Abort a stream with no upstream bytes for this long; 0 = default (300s)."`
	Models              []ModelSpec       `json:"models"`
}

var forbiddenHeaders = []string{"authorization", "x-api-key", "x-goog-api-key", "proxy-authorization", "cookie", "host", "content-length", "transfer-encoding", "connection"}

// ValidateBaseURL checks a provider base URL (no credentials, http(s)).
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("must be an absolute http(s) URL")
	}
	if u.User != nil {
		return fmt.Errorf("must not embed credentials; use the account credential instead")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must not contain a query or fragment")
	}
	return nil
}

func normalizeProvider(s *ProviderSpec) {
	t, ok := ProviderTypes[s.Type]
	if !ok {
		return
	}
	if s.BaseURL == "" {
		s.BaseURL = t.DefaultBaseURL
	}
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if s.Classification == "" {
		s.Classification = t.Classification
	}
	s.Headers = EmptyMap(s.Headers)
	if s.Models == nil {
		s.Models = []ModelSpec{}
	}
	sort.Slice(s.Models, func(i, j int) bool { return s.Models[i].ID < s.Models[j].ID })
	for i := range s.Models {
		s.Models[i].Capabilities = SortedUnique(s.Models[i].Capabilities)
	}
}

func validateProvider(v *Validator, s *ProviderSpec) {
	t, ok := ProviderTypes[s.Type]
	if !ok {
		v.Fail("spec.type", "unknown provider type")
		return
	}
	checkText(v, "spec.display_name", s.DisplayName, 120)
	if t.Execution == "cli_bridge" {
		v.Check(s.BaseURL == "", "spec.base_url", "CLI bridge providers have no base URL")
	} else if s.BaseURL == "" {
		v.Fail("spec.base_url", "is required for this provider type")
	} else if err := ValidateBaseURL(s.BaseURL); err != nil {
		v.Fail("spec.base_url", err.Error())
	}
	v.Check(slices.Contains([]string{"official", "partial", "unofficial", "local"}, s.Classification), "spec.classification", "must be official, partial, unofficial or local")
	for k := range s.Headers {
		v.Check(headerNameRE.MatchString(k), "spec.headers."+k, "invalid header name")
		v.Check(!slices.Contains(forbiddenHeaders, strings.ToLower(k)), "spec.headers."+k, "authentication and hop-by-hop headers cannot be configured here")
	}
	v.Check(len(s.Models) <= 500, "spec.models", "at most 500 models")
	seen := map[string]bool{}
	for i, m := range s.Models {
		loc := fmt.Sprintf("spec.models[%d]", i)
		v.Check(m.ID != "" && len(m.ID) <= 200 && !strings.ContainsAny(m.ID, " \t\n"), loc+".id", "model id is required and must not contain whitespace")
		v.Check(!seen[m.ID], loc+".id", "duplicate model id")
		seen[m.ID] = true
		for _, c := range m.Capabilities {
			v.Check(slices.Contains(Capabilities, c), loc+".capabilities", "unknown capability "+c)
		}
		if p := m.Pricing; p != nil {
			v.Check(len(p.Currency) == 3 && strings.ToUpper(p.Currency) == p.Currency, loc+".pricing.currency", "must be an ISO 4217 code such as USD")
			for f, val := range map[string]string{"input_per_mtok": p.InputPerMTok, "output_per_mtok": p.OutputPerMTok, "cache_read_per_mtok": p.CacheReadPerMTok, "cache_write_per_mtok": p.CacheWritePerMTok} {
				if val == "" {
					continue
				}
				if a, err := money.ParseDecimal(val); err != nil || a < 0 {
					v.Fail(loc+".pricing."+f, "must be a non-negative decimal")
				}
			}
		}
	}
}

// ---------- provider accounts ----------

// Billing classes.
const (
	BillingMetered      = "metered_api"
	BillingSubscription = "subscription"
	BillingFree         = "free"
	BillingLocal        = "local"
)

// AccountSpec configures one authorized credential/account at a provider.
type AccountSpec struct {
	ProviderID string `json:"provider_id"`
	// DisplayName is shown in the UI; never the credential.
	DisplayName string `json:"display_name" maxLength:"120"`
	// CredentialRef points at the credential (env:NAME or file:/path) as an
	// alternative to a stored write-only credential secret.
	CredentialRef     string            `json:"credential_ref" doc:"env:NAME or file:/path. Alternative to the write-only 'credential' secret."`
	Billing           string            `json:"billing" enum:"metered_api,subscription,free,local" doc:"How usage on this account is paid for. Paid fallback rules use this."`
	AllowedModels     []string          `json:"allowed_models" doc:"Restrict to these provider model ids; empty = all models of the provider."`
	MaxConcurrency    int               `json:"max_concurrency" minimum:"0" maximum:"10000" doc:"Local concurrency cap enforced by reservations; 0 = unlimited."`
	RequestsPerMinute int               `json:"requests_per_minute" minimum:"0" maximum:"1000000" doc:"Local request rate cap; 0 = unlimited."`
	Enabled           bool              `json:"enabled"`
	Labels            map[string]string `json:"labels" doc:"Free-form labels available to routing expressions as account.labels."`
}

func normalizeAccount(s *AccountSpec) {
	s.AllowedModels = SortedUnique(s.AllowedModels)
	s.Labels = EmptyMap(s.Labels)
}

func validateAccount(v *Validator, s *AccountSpec) {
	v.Check(s.ProviderID != "", "spec.provider_id", "is required")
	checkText(v, "spec.display_name", s.DisplayName, 120)
	v.Check(slices.Contains([]string{BillingMetered, BillingSubscription, BillingFree, BillingLocal}, s.Billing), "spec.billing", "must be metered_api, subscription, free or local")
	if s.CredentialRef != "" {
		kind, val, ok := strings.Cut(s.CredentialRef, ":")
		v.Check(ok && val != "" && (kind == "env" || kind == "file"), "spec.credential_ref", "must be env:NAME or file:/path")
	}
	for k, val := range s.Labels {
		v.Check(refKeyRE.MatchString(k), "spec.labels."+k, "label keys must be lowercase identifiers")
		v.Check(len(val) <= 128, "spec.labels."+k, "label values are at most 128 characters")
	}
	v.Check(len(s.AllowedModels) <= 500, "spec.allowed_models", "at most 500 entries")
}

// ---------- subscription entitlements ----------

// WindowSpec declares a quota window of a subscription plan.
type WindowSpec struct {
	Name    string `json:"name" doc:"e.g. five_hour, seven_day"`
	Seconds int64  `json:"seconds" minimum:"60"`
	Unit    string `json:"unit" enum:"percent,tokens,requests,credits,unknown"`
	Limit   *int64 `json:"limit,omitempty" doc:"Known limit in unit; omit when the provider does not publish one."`
}

// EntitlementSpec records what an account's subscription includes and costs.
type EntitlementSpec struct {
	AccountID string       `json:"account_id"`
	PlanName  string       `json:"plan_name" maxLength:"120"`
	Fee       string       `json:"fee" doc:"Subscription fee per period as a decimal string."`
	Currency  string       `json:"currency"`
	Period    string       `json:"period" enum:"monthly,yearly"`
	StartsOn  string       `json:"starts_on" doc:"YYYY-MM-DD"`
	EndsOn    string       `json:"ends_on" doc:"YYYY-MM-DD, empty = open-ended"`
	Windows   []WindowSpec `json:"windows"`
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func normalizeEntitlement(s *EntitlementSpec) {
	if s.Windows == nil {
		s.Windows = []WindowSpec{}
	}
	sort.Slice(s.Windows, func(i, j int) bool { return s.Windows[i].Seconds < s.Windows[j].Seconds })
}

func validateEntitlement(v *Validator, s *EntitlementSpec) {
	v.Check(s.AccountID != "", "spec.account_id", "is required")
	v.Check(s.PlanName != "", "spec.plan_name", "is required")
	if a, err := money.ParseDecimal(s.Fee); err != nil || a < 0 {
		v.Fail("spec.fee", "must be a non-negative decimal")
	}
	v.Check(len(s.Currency) == 3 && strings.ToUpper(s.Currency) == s.Currency, "spec.currency", "must be an ISO 4217 code")
	v.Check(s.Period == "monthly" || s.Period == "yearly", "spec.period", "must be monthly or yearly")
	v.Check(dateRE.MatchString(s.StartsOn), "spec.starts_on", "must be YYYY-MM-DD")
	v.Check(s.EndsOn == "" || (dateRE.MatchString(s.EndsOn) && s.EndsOn >= s.StartsOn), "spec.ends_on", "must be YYYY-MM-DD on or after starts_on")
	for i, w := range s.Windows {
		loc := fmt.Sprintf("spec.windows[%d]", i)
		v.Check(refKeyRE.MatchString(w.Name), loc+".name", "must be a lowercase identifier")
		v.Check(w.Seconds >= 60, loc+".seconds", "must be at least 60")
		v.Check(slices.Contains([]string{"percent", "tokens", "requests", "credits", "unknown"}, w.Unit), loc+".unit", "invalid unit")
	}
}

// ---------- pools and aliases ----------

// PoolMember is one (account, model) candidate.
type PoolMember struct {
	AccountID string `json:"account_id"`
	Model     string `json:"model" doc:"Upstream model id at the account's provider."`
	Priority  int    `json:"priority" doc:"Lower is preferred for ordered strategies."`
	Weight    int    `json:"weight" minimum:"0" maximum:"1000" doc:"Relative weight for the weighted strategy."`
}

// PoolSpec is a set of eligible account/model candidates.
type PoolSpec struct {
	DisplayName string       `json:"display_name" maxLength:"120"`
	Strategy    string       `json:"strategy" enum:"ordered,weighted,reset_soonest,least_used,round_robin," doc:"Default ranking inside the pool (empty = ordered); plans may override."`
	Affinity    string       `json:"affinity" enum:"session,none," doc:"Empty = session. Keep a conversation on the same member while it stays eligible (prompt-cache friendly)."`
	Members     []PoolMember `json:"members"`
}

func normalizePool(s *PoolSpec) {
	if s.Strategy == "" {
		s.Strategy = "ordered"
	}
	if s.Affinity == "" {
		s.Affinity = "session"
	}
	if s.Members == nil {
		s.Members = []PoolMember{}
	}
	for i := range s.Members {
		if s.Members[i].Weight == 0 {
			s.Members[i].Weight = 1
		}
	}
	sort.SliceStable(s.Members, func(i, j int) bool {
		a, b := s.Members[i], s.Members[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		return a.Model < b.Model
	})
}

func validatePool(v *Validator, s *PoolSpec) {
	v.Check(slices.Contains([]string{"ordered", "weighted", "reset_soonest", "least_used", "round_robin"}, s.Strategy), "spec.strategy", "invalid strategy")
	v.Check(s.Affinity == "session" || s.Affinity == "none", "spec.affinity", "must be session or none")
	v.Check(len(s.Members) <= 200, "spec.members", "at most 200 members")
	seen := map[string]bool{}
	for i, m := range s.Members {
		loc := fmt.Sprintf("spec.members[%d]", i)
		v.Check(m.AccountID != "", loc+".account_id", "is required")
		v.Check(m.Model != "", loc+".model", "is required")
		key := m.AccountID + "|" + m.Model
		v.Check(!seen[key], loc, "duplicate account/model member")
		seen[key] = true
	}
}

// AliasSpec maps a client-facing model name (the alias name, e.g.
// coding/default) to a pool or another alias.
type AliasSpec struct {
	Description          string   `json:"description" maxLength:"500"`
	PoolID               string   `json:"pool_id" doc:"Target pool (exactly one of pool_id or alias_id)."`
	AliasID              string   `json:"alias_id" doc:"Target alias (chains are allowed; cycles are rejected)."`
	RequiredCapabilities []string `json:"required_capabilities" doc:"Capabilities every candidate must have in addition to what a request demands."`
}

func normalizeAlias(s *AliasSpec) { s.RequiredCapabilities = SortedUnique(s.RequiredCapabilities) }

func validateAlias(v *Validator, s *AliasSpec) {
	v.Check((s.PoolID == "") != (s.AliasID == ""), "spec", "exactly one of pool_id or alias_id must be set")
	v.Check(s.AliasID == "" || s.AliasID != v.Self(), "spec.alias_id", "an alias cannot target itself")
	for _, c := range s.RequiredCapabilities {
		v.Check(slices.Contains(Capabilities, c), "spec.required_capabilities", "unknown capability "+c)
	}
}

// ---------- routing plans ----------

// PlanSpec holds plan metadata; the routing logic lives in versioned plan
// documents (see versions.go).
type PlanSpec struct {
	DisplayName string `json:"display_name" maxLength:"120"`
	Description string `json:"description" maxLength:"1000"`
}

// ---------- client bindings ----------

// Harness identifiers.
var Harnesses = []string{"claude_code", "codex", "opencode", "pi", "generic"}

// BindingLimits are enforceable local limits.
type BindingLimits struct {
	RequestsPerMinute int `json:"requests_per_minute" minimum:"0" doc:"0 = unlimited."`
	MaxConcurrency    int `json:"max_concurrency" minimum:"0" doc:"0 = unlimited."`
	MaxRequestBytes   int `json:"max_request_bytes" minimum:"0" doc:"0 = server default."`
}

// BindingTrust controls which client-supplied hints are honoured. Hints are
// always recorded as untrusted claims; trust only affects attribution.
type BindingTrust struct {
	SessionHints bool `json:"session_hints" doc:"Use harness session headers to group requests into sessions."`
	ProjectHints bool `json:"project_hints" doc:"Allow X-HP-Project to select one of allowed_project_ids."`
	IntentHints  bool `json:"intent_hints" doc:"Accept declared intent (X-HP-Intent) as trusted."`
}

// BindingSpec connects a principal to a routing plan with limits and
// telemetry policy. Gateway credentials are issued against a binding.
type BindingSpec struct {
	DisplayName       string            `json:"display_name" maxLength:"120"`
	OwnerKind         string            `json:"owner_kind" enum:"user,service,none," doc:"Who is accountable for this client (empty = none)."`
	OwnerID           string            `json:"owner_id" doc:"usr_… or pri_… (empty when owner_kind is none)."`
	PlanID            string            `json:"plan_id"`
	ProjectID         string            `json:"project_id" doc:"Default project for attribution (optional)."`
	AllowedProjectIDs []string          `json:"allowed_project_ids" doc:"Projects a trusted project hint may select."`
	ExpectedHarness   string            `json:"expected_harness" enum:"claude_code,codex,opencode,pi,generic," doc:"Informational; mismatches are recorded, never enforced as identity."`
	AllowedModels     []string          `json:"allowed_models" doc:"Requested model names (aliases or plan mappings) this client may use; empty = any the plan maps."`
	Limits            BindingLimits     `json:"limits"`
	Trust             BindingTrust      `json:"trust"`
	ExternalRefs      map[string]string `json:"external_refs" doc:"Operator-configured trusted references."`
	Enabled           bool              `json:"enabled"`
}

func normalizeBinding(s *BindingSpec) {
	if s.OwnerKind == "" {
		s.OwnerKind = "none"
	}
	s.AllowedProjectIDs = SortedUnique(s.AllowedProjectIDs)
	s.AllowedModels = SortedUnique(s.AllowedModels)
	s.ExternalRefs = EmptyMap(s.ExternalRefs)
}

func validateBinding(v *Validator, s *BindingSpec) {
	switch s.OwnerKind {
	case "none":
		v.Check(s.OwnerID == "", "spec.owner_id", "must be empty when owner_kind is none")
	case "user":
		v.Check(ids.Valid(ids.User, s.OwnerID), "spec.owner_id", "must be a user id (usr_…)")
	case "service":
		v.Check(ids.Valid(ids.Principal, s.OwnerID), "spec.owner_id", "must be a service principal id (pri_…)")
	default:
		v.Fail("spec.owner_kind", "must be user, service or none")
	}
	v.Check(s.PlanID != "", "spec.plan_id", "is required")
	v.Check(s.ExpectedHarness == "" || slices.Contains(Harnesses, s.ExpectedHarness), "spec.expected_harness", "unknown harness")
	v.Check(s.Limits.RequestsPerMinute >= 0 && s.Limits.MaxConcurrency >= 0 && s.Limits.MaxRequestBytes >= 0, "spec.limits", "limits must be non-negative")
	v.Check(s.Limits.MaxRequestBytes <= 64<<20, "spec.limits.max_request_bytes", "at most 64 MiB")
	checkRefs(v, "spec.external_refs", s.ExternalRefs)
	v.Check(len(s.AllowedModels) <= 200, "spec.allowed_models", "at most 200 entries")
	if s.ProjectID != "" && len(s.AllowedProjectIDs) > 0 {
		v.Check(slices.Contains(s.AllowedProjectIDs, s.ProjectID), "spec.project_id", "must be one of allowed_project_ids when both are set")
	}
}

// ---------- registry ----------

// Defs for every kind.
var (
	WorkspaceDef = &Def[WorkspaceSpec]{Name: KindWorkspace, Plural: "workspaces", Prefix: ids.Workspace, Title: "Workspace",
		Validate: func(v *Validator, s *WorkspaceSpec) {
			checkText(v, "spec.display_name", s.DisplayName, 120)
			checkText(v, "spec.description", s.Description, 1000)
		}}
	ProjectDef = &Def[ProjectSpec]{Name: KindProject, Plural: "projects", Prefix: ids.Project, Title: "Project",
		Normalize: func(s *ProjectSpec) { s.ExternalRefs = EmptyMap(s.ExternalRefs) },
		Validate: func(v *Validator, s *ProjectSpec) {
			v.Check(s.WorkspaceID != "", "spec.workspace_id", "is required")
			checkText(v, "spec.display_name", s.DisplayName, 120)
			checkRefs(v, "spec.external_refs", s.ExternalRefs)
		},
		Refs: func(s *ProjectSpec) []Ref {
			return []Ref{{"spec.workspace_id", s.WorkspaceID, []string{KindWorkspace}}}
		}}
	ProviderDef = &Def[ProviderSpec]{Name: KindProvider, Plural: "providers", Prefix: ids.Provider, Title: "Provider",
		Normalize: normalizeProvider, Validate: validateProvider}
	AccountDef = &Def[AccountSpec]{Name: KindAccount, Plural: "provider-accounts", Prefix: ids.Account, Title: "Provider account",
		Secrets:   []SecretField{{Name: "credential", Description: "API key, setup token or other credential. Write-only.", RefField: "credential_ref"}},
		Normalize: normalizeAccount, Validate: validateAccount,
		Refs: func(s *AccountSpec) []Ref { return []Ref{{"spec.provider_id", s.ProviderID, []string{KindProvider}}} }}
	EntitlementDef = &Def[EntitlementSpec]{Name: KindEntitlement, Plural: "subscription-entitlements", Prefix: ids.Entitlement, Title: "Subscription entitlement",
		Normalize: normalizeEntitlement, Validate: validateEntitlement,
		Refs: func(s *EntitlementSpec) []Ref { return []Ref{{"spec.account_id", s.AccountID, []string{KindAccount}}} }}
	PoolDef = &Def[PoolSpec]{Name: KindPool, Plural: "pools", Prefix: ids.Pool, Title: "Pool",
		Normalize: normalizePool, Validate: validatePool,
		Refs: func(s *PoolSpec) []Ref {
			out := make([]Ref, 0, len(s.Members))
			for i, m := range s.Members {
				out = append(out, Ref{fmt.Sprintf("spec.members[%d].account_id", i), m.AccountID, []string{KindAccount}})
			}
			return out
		}}
	AliasDef = &Def[AliasSpec]{Name: KindAlias, Plural: "model-aliases", Prefix: ids.Alias, Title: "Model alias",
		NameRE: PathNameRE, NameHint: "lowercase path segments such as coding/default or review/deep (up to 4 segments)",
		Normalize: normalizeAlias, Validate: validateAlias,
		Refs: func(s *AliasSpec) []Ref {
			return []Ref{{"spec.pool_id", s.PoolID, []string{KindPool}}, {"spec.alias_id", s.AliasID, []string{KindAlias}}}
		}}
	PlanDef = &Def[PlanSpec]{Name: KindPlan, Plural: "routing-plans", Prefix: ids.Plan, Title: "Routing plan",
		Validate: func(v *Validator, s *PlanSpec) { checkText(v, "spec.description", s.Description, 1000) }}
	BindingDef = &Def[BindingSpec]{Name: KindClientBinding, Plural: "client-bindings", Prefix: ids.ClientBinding, Title: "Client binding",
		Normalize: normalizeBinding, Validate: validateBinding,
		Refs: func(s *BindingSpec) []Ref {
			out := []Ref{{"spec.plan_id", s.PlanID, []string{KindPlan}}, {"spec.project_id", s.ProjectID, []string{KindProject}}}
			for i, p := range s.AllowedProjectIDs {
				out = append(out, Ref{fmt.Sprintf("spec.allowed_project_ids[%d]", i), p, []string{KindProject}})
			}
			return out
		}}
)

// Registry lists every kind in dependency order (export/import order).
var Registry = []Kind{WorkspaceDef, ProjectDef, ProviderDef, AccountDef, EntitlementDef, QuotaMonitorDef, PrivacyPolicyDef, TracePolicyDef, PoolDef, AliasDef, PlanDef, BindingDef, BudgetDef}

// KindByName returns a registered kind.
func KindByName(name string) (Kind, bool) {
	for _, k := range Registry {
		if k.KindName() == name {
			return k, true
		}
	}
	return nil, false
}

// RegisterKind adds a kind (feature packages register theirs at init).
func RegisterKind(k Kind) { Registry = append(Registry, k) }
