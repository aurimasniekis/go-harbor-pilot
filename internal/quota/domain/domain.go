// Package domain defines provider-agnostic, normalized types. It contains no
// provider-specific parsing: adapters translate provider responses into these
// types and everything downstream (persistence, analytics, API) consumes them.
//
// Optional values are pointers. A nil pointer means "unknown" and is never
// interchangeable with zero.
package domain

import (
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
)

// ProviderID identifies an adapter type (e.g. "anthropic_admin", "demo_claude").
type ProviderID string

// AccountID is the operator-chosen stable account identifier from config.
type AccountID string

// BucketID is the stable identifier of a quota bucket: "<account>/<bucket key>".
type BucketID string

// SupportTier states how trustworthy an adapter's data source is.
type SupportTier string

const (
	// TierOfficial uses documented, supported provider APIs.
	TierOfficial SupportTier = "official"
	// TierUnofficial uses undocumented endpoints; may break or violate terms.
	TierUnofficial SupportTier = "unofficial"
	// TierClientReported comes from client instrumentation via ingestion.
	TierClientReported SupportTier = "client_reported"
	// TierDemo is synthetic deterministic demo data.
	TierDemo SupportTier = "demo"
)

// Capability is a metric family an adapter may provide.
type Capability string

// Known capabilities.
const (
	CapAccountIdentity   Capability = "account_identity"
	CapQuotaRemaining    Capability = "quota_remaining"
	CapQuotaUsed         Capability = "quota_used"
	CapQuotaLimit        Capability = "quota_limit"
	CapFixedReset        Capability = "fixed_reset_timestamp"
	CapRollingWindow     Capability = "rolling_window_quota"
	CapMonetaryBalance   Capability = "monetary_balance"
	CapMonetarySpend     Capability = "monetary_spend"
	CapTokenCounters     Capability = "token_counters"
	CapRequestCounters   Capability = "request_counters"
	CapModelQuota        Capability = "model_specific_quota"
	CapOrgProjectScope   Capability = "organization_project_scope"
	CapUsageAttribution  Capability = "usage_event_attribution"
	CapHistoricalBackfil Capability = "historical_backfill"
	CapAPIKeyInventory   Capability = "api_key_inventory"
	CapModelUsage        Capability = "model_level_usage"
	CapAPIKeyUsage       Capability = "api_key_level_usage"
	CapBudget            Capability = "budget_limits"
	CapModelPricing      Capability = "model_pricing"
)

// AllCapabilities lists capabilities in display order.
var AllCapabilities = []Capability{
	CapAccountIdentity, CapQuotaRemaining, CapQuotaUsed, CapQuotaLimit, CapFixedReset,
	CapRollingWindow, CapMonetaryBalance, CapMonetarySpend, CapTokenCounters,
	CapRequestCounters, CapModelQuota, CapOrgProjectScope, CapUsageAttribution,
	CapHistoricalBackfil, CapAPIKeyInventory, CapModelUsage, CapAPIKeyUsage,
	CapBudget, CapModelPricing,
}

// CapabilityStatus states whether a capability is available.
type CapabilityStatus string

// Capability statuses.
const (
	CapSupported   CapabilityStatus = "supported"
	CapPartial     CapabilityStatus = "partial"
	CapUnsupported CapabilityStatus = "unsupported"
	CapUnknown     CapabilityStatus = "unknown"
)

// CapabilityInfo describes one capability of an adapter.
type CapabilityInfo struct {
	Status CapabilityStatus `json:"status" enum:"supported,partial,unsupported,unknown"`
	Note   string           `json:"note,omitempty"`
}

// ProviderInfo is adapter metadata.
type ProviderInfo struct {
	ID              ProviderID                    `json:"id"`
	DisplayName     string                        `json:"display_name"`
	Family          string                        `json:"family"`
	AdapterVersion  string                        `json:"adapter_version"`
	Tier            SupportTier                   `json:"tier"`
	CredentialTypes []string                      `json:"credential_types"`
	Capabilities    map[Capability]CapabilityInfo `json:"capabilities"`
	Limitations     []string                      `json:"limitations"`
	DocsURLs        []string                      `json:"docs_urls"`
	// DefaultPollInterval is the adapter's conservative recommended interval.
	DefaultPollInterval time.Duration `json:"-"`
	// PushOnly adapters are never polled; data arrives via the ingestion API.
	PushOnly bool `json:"push_only"`
}

// Provenance records where an account's configuration came from, never its
// secret contents.
type Provenance struct {
	File           string `json:"file"`
	CredentialType string `json:"credential_type"`
	// CredentialRef is a non-secret description such as "env:NEXOS_KEY" or
	// "file:/run/secrets/x". Never the value.
	CredentialRef string `json:"credential_ref"`
}

// Account is a configured monitored account.
type Account struct {
	ID               AccountID   `json:"id"`
	ProviderID       ProviderID  `json:"provider_id"`
	Alias            string      `json:"alias"`
	MaskedIdentifier *string     `json:"masked_identifier"`
	Tags             []string    `json:"tags"`
	Enabled          bool        `json:"enabled"`
	Provenance       Provenance  `json:"provenance"`
	Demo             bool        `json:"demo"`
	Tier             SupportTier `json:"tier"`
	// SharedScopeID groups accounts drawing on the same provider quota (e.g.
	// several keys in one organization) so capacity is not double counted.
	SharedScopeID string `json:"shared_scope_id,omitempty"`
}

// BucketScope is what a quota bucket applies to.
type BucketScope string

// Bucket scopes.
const (
	ScopeAccount      BucketScope = "account"
	ScopeOrganization BucketScope = "organization"
	ScopeProject      BucketScope = "project"
	ScopeModel        BucketScope = "model"
	ScopeModelFamily  BucketScope = "model_family"
	ScopeAPIKey       BucketScope = "api_key"
)

// BucketKind is the quota mechanism.
type BucketKind string

// Bucket kinds.
const (
	KindFixedWindow   BucketKind = "fixed_window"
	KindRollingWindow BucketKind = "rolling_window"
	KindBalance       BucketKind = "balance"
	KindRateLimit     BucketKind = "rate_limit"
	KindBudget        BucketKind = "budget"
	KindUnknown       BucketKind = "unknown"
)

// Unit is the unit of a bucket's limit/used/remaining values.
type Unit string

// Units.
const (
	UnitPercent  Unit = "percent"
	UnitTokens   Unit = "tokens"
	UnitRequests Unit = "requests"
	UnitCurrency Unit = "currency"
	UnitCredits  Unit = "credits"
	UnitOther    Unit = "other"
)

// ResetSemantics describes how a bucket replenishes.
type ResetSemantics string

const (
	// ResetProviderTimestamp: provider reports the next reset time; future
	// resets after that are unknown.
	ResetProviderTimestamp ResetSemantics = "provider_timestamp"
	// ResetFixedPeriodic: resets occur at a documented fixed period after the
	// reported reset (e.g. a daily quota at midnight in a known timezone).
	ResetFixedPeriodic ResetSemantics = "fixed_periodic"
	// ResetRolling: continuous replenishment; no discrete resets.
	ResetRolling ResetSemantics = "rolling"
	// ResetNone: never resets (prepaid balance).
	ResetNone ResetSemantics = "none"
	// ResetUnknown: not known.
	ResetUnknown ResetSemantics = "unknown"
)

// Quality says how a value was obtained.
type Quality string

// Qualities.
const (
	QualityProviderReported Quality = "provider_reported"
	QualityLocallyMeasured  Quality = "locally_measured"
	QualityEstimated        Quality = "estimated"
	QualityClientReported   Quality = "client_reported"
	QualityDemo             Quality = "demo"
)

// Confidence is a coarse confidence classification.
type Confidence string

// Confidence levels.
const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
	ConfidenceNone   Confidence = "none"
)

// BucketSpec is the stable identity and semantics of a quota bucket.
type BucketSpec struct {
	// Key is adapter-stable within an account, e.g. "five_hour" or
	// "seven_day:model:opus".
	Key            string         `json:"key"`
	Label          string         `json:"label"`
	Scope          BucketScope    `json:"scope"`
	Kind           BucketKind     `json:"kind"`
	Window         time.Duration  `json:"-"`
	Unit           Unit           `json:"unit"`
	Currency       string         `json:"currency,omitempty"`
	Model          string         `json:"model,omitempty"`
	ModelFamily    string         `json:"model_family,omitempty"`
	SharedScopeID  string         `json:"shared_scope_id,omitempty"`
	ResetSemantics ResetSemantics `json:"reset_semantics"`
	// Period is the reset period for ResetFixedPeriodic.
	Period time.Duration `json:"-"`
	// Global is true when exhausting this bucket blocks every request of the
	// account (as opposed to only one model).
	Global bool `json:"global"`
	// Official is false for buckets parsed from undocumented endpoints.
	Official bool `json:"official"`
}

// WindowClass returns a coarse window label used for compatibility grouping.
func (s BucketSpec) WindowClass() string {
	switch {
	case s.Kind == KindBalance || s.Kind == KindBudget:
		return string(s.Kind)
	case s.Window == 5*time.Hour:
		return "5h"
	case s.Window == 7*24*time.Hour:
		return "7d"
	case s.Window == 24*time.Hour:
		return "1d"
	case s.Window == 30*24*time.Hour:
		return "30d"
	case s.Window > 0:
		return s.Window.String()
	default:
		return "other"
	}
}

// BucketObservation is one observed state of a bucket at a point in time.
type BucketObservation struct {
	Spec      BucketSpec `json:"spec"`
	Limit     *float64   `json:"limit"`
	Used      *float64   `json:"used"`
	Remaining *float64   `json:"remaining"`
	// Exact currency amounts for monetary buckets (aggregated exactly).
	LimitAmount     *money.Amount `json:"limit_amount_e10"`
	UsedAmount      *money.Amount `json:"used_amount_e10"`
	RemainingAmount *money.Amount `json:"remaining_amount_e10"`
	ResetAt         *time.Time    `json:"reset_at"`
	SourceTime      *time.Time    `json:"source_time"`
	Quality         Quality       `json:"quality"`
	Source          string        `json:"source"`
}

// RemainingFraction returns remaining/limit in [0,1] when derivable.
func (o BucketObservation) RemainingFraction() *float64 {
	if o.Spec.Unit == UnitPercent {
		if o.Remaining != nil {
			v := clamp01(*o.Remaining / 100)
			return &v
		}
		if o.Used != nil {
			v := clamp01(1 - *o.Used/100)
			return &v
		}
		return nil
	}
	if o.Limit == nil || *o.Limit <= 0 {
		return nil
	}
	if o.Remaining != nil {
		v := clamp01(*o.Remaining / *o.Limit)
		return &v
	}
	if o.Used != nil {
		v := clamp01(1 - *o.Used / *o.Limit)
		return &v
	}
	return nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Health is the account monitoring status derived from fetch outcomes.
type Health string

// Health states.
const (
	HealthHealthy     Health = "healthy"
	HealthDegraded    Health = "degraded"
	HealthAuthFailed  Health = "auth_failed"
	HealthStale       Health = "stale"
	HealthError       Health = "error"
	HealthDisabled    Health = "disabled"
	HealthPending     Health = "pending"
	HealthUnsupported Health = "unsupported"
)

// ErrorKind is a typed adapter error category.
type ErrorKind string

// Error kinds.
const (
	ErrAuthentication  ErrorKind = "authentication_failed"
	ErrPermission      ErrorKind = "permission_denied"
	ErrRateLimited     ErrorKind = "rate_limited"
	ErrUnavailable     ErrorKind = "temporarily_unavailable"
	ErrUnsupported     ErrorKind = "unsupported_metric"
	ErrInvalidResponse ErrorKind = "invalid_response"
	ErrNetwork         ErrorKind = "network_error"
	ErrConfiguration   ErrorKind = "configuration_error"
	ErrInternal        ErrorKind = "internal_error"
)

// FetchError is sanitized error metadata safe to persist and display.
type FetchError struct {
	Kind       ErrorKind `json:"kind"`
	Message    string    `json:"message"`
	HTTPStatus int       `json:"http_status,omitempty"`
}

// Snapshot is the normalized result of one poll of one account.
type Snapshot struct {
	AccountID        AccountID           `json:"account_id"`
	FetchedAt        time.Time           `json:"fetched_at"`
	SourceTime       *time.Time          `json:"source_time"`
	Buckets          []BucketObservation `json:"buckets"`
	Error            *FetchError         `json:"error"`
	Latency          time.Duration       `json:"-"`
	ConfigGeneration int64               `json:"config_generation"`
	Identity         *string             `json:"masked_identity"`
}

// Succeeded reports whether the fetch succeeded.
func (s Snapshot) Succeeded() bool { return s.Error == nil }
