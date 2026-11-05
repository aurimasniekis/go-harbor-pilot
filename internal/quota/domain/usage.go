package domain

import (
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
)

// Origin identifies where a usage record came from. Origins overlap: a
// provider report and client instrumentation can describe the same requests,
// so they are reconciled, never simply added.
type Origin string

// Usage origins, ordered from most to least authoritative for billing.
const (
	OriginProviderReported Origin = "provider_reported"
	OriginImported         Origin = "imported"
	OriginGatewayObserved  Origin = "gateway_observed"
	OriginClientReported   Origin = "client_reported"
	OriginEstimated        Origin = "estimated"
	OriginDemo             Origin = "demo"
)

// Authority returns a rank; lower is more authoritative.
func (o Origin) Authority() int {
	switch o {
	case OriginProviderReported:
		return 0
	case OriginImported:
		return 1
	case OriginGatewayObserved:
		return 2
	case OriginClientReported:
		return 3
	case OriginEstimated:
		return 4
	case OriginDemo:
		return 0
	default:
		return 9
	}
}

// Outcome is the normalized result of one request attempt.
type Outcome string

// Outcomes.
const (
	OutcomeSuccess   Outcome = "success"
	OutcomeError     Outcome = "error"
	OutcomeCancelled Outcome = "cancelled"
	OutcomePartial   Outcome = "partial" // e.g. stream ended without final usage
	OutcomeUnknown   Outcome = "unknown"
)

// TokenCounts holds per-category token counts. Categories may overlap
// depending on provider semantics; the Included* flags make that explicit so
// totals are never computed by blindly summing categories.
type TokenCounts struct {
	Input       *int64 `json:"input"`
	Output      *int64 `json:"output"`
	CacheRead   *int64 `json:"cache_read"`
	CacheWrite  *int64 `json:"cache_write"`
	Reasoning   *int64 `json:"reasoning"`
	ImageInput  *int64 `json:"image_input"`
	ImageOutput *int64 `json:"image_output"`

	// CacheReadIncludedInInput is true when Input already contains CacheRead
	// (e.g. OpenAI input_tokens includes input_cached_tokens).
	CacheReadIncludedInInput bool `json:"cache_read_included_in_input"`
	// CacheWriteIncludedInInput is true when Input already contains CacheWrite.
	CacheWriteIncludedInInput bool `json:"cache_write_included_in_input"`
	// ReasoningIncludedInOutput is true when Output already contains Reasoning.
	ReasoningIncludedInOutput bool `json:"reasoning_included_in_output"`
}

// TotalInput returns all input-side tokens counted once, or nil if unknown.
func (t TokenCounts) TotalInput() *int64 {
	if t.Input == nil && t.CacheRead == nil && t.CacheWrite == nil {
		return nil
	}
	var total int64
	if t.Input != nil {
		total += *t.Input
	}
	if t.CacheRead != nil && !t.CacheReadIncludedInInput {
		total += *t.CacheRead
	}
	if t.CacheWrite != nil && !t.CacheWriteIncludedInInput {
		total += *t.CacheWrite
	}
	return &total
}

// TotalOutput returns all output-side tokens counted once, or nil if unknown.
func (t TokenCounts) TotalOutput() *int64 {
	if t.Output == nil && t.Reasoning == nil {
		return nil
	}
	var total int64
	if t.Output != nil {
		total += *t.Output
	}
	if t.Reasoning != nil && !t.ReasoningIncludedInOutput {
		total += *t.Reasoning
	}
	return &total
}

// UncachedInput returns input tokens excluding cache reads/writes, if known.
func (t TokenCounts) UncachedInput() *int64 {
	if t.Input == nil {
		return nil
	}
	v := *t.Input
	if t.CacheReadIncludedInInput && t.CacheRead != nil {
		v -= *t.CacheRead
	}
	if t.CacheWriteIncludedInInput && t.CacheWrite != nil {
		v -= *t.CacheWrite
	}
	if v < 0 {
		v = 0
	}
	return &v
}

// UsageDimensions are the attribution dimensions of usage. Empty means "not
// attributed / unknown" for that dimension.
type UsageDimensions struct {
	Workspace        string `json:"workspace,omitempty"`
	Project          string `json:"project,omitempty"`
	APIKeyID         string `json:"api_key_id,omitempty"` // safe local or provider-issued non-secret ID
	User             string `json:"user,omitempty"`       // safe identifier, never a raw email by default
	RequestedModel   string `json:"requested_model,omitempty"`
	ResolvedModel    string `json:"resolved_model,omitempty"`
	UpstreamProvider string `json:"upstream_provider,omitempty"`
	Agent            string `json:"agent,omitempty"`
	Workload         string `json:"workload,omitempty"`
	Environment      string `json:"environment,omitempty"`
	ServiceTier      string `json:"service_tier,omitempty"`
}

// Model returns the best known model name (resolved over requested).
func (d UsageDimensions) Model() string {
	if d.ResolvedModel != "" {
		return d.ResolvedModel
	}
	return d.RequestedModel
}

// UsageEvent is one request attempt reported by a client, gateway or
// provider log. Prompts, completions, files and tool payloads are never part
// of this type.
type UsageEvent struct {
	ID                string            `json:"id"`
	SourceID          string            `json:"source_id"`
	IdempotencyKey    string            `json:"idempotency_key"`
	ProviderRequestID string            `json:"provider_request_id,omitempty"`
	OperationID       string            `json:"operation_id,omitempty"`
	AttemptID         string            `json:"attempt_id,omitempty"`
	StartedAt         *time.Time        `json:"started_at"`
	EndedAt           *time.Time        `json:"ended_at"`
	ObservedAt        time.Time         `json:"observed_at"`
	ProviderID        ProviderID        `json:"provider_id"`
	AccountID         AccountID         `json:"account_id,omitempty"`
	Dimensions        UsageDimensions   `json:"dimensions"`
	Outcome           Outcome           `json:"outcome"`
	HTTPStatus        *int              `json:"http_status"`
	ErrorCategory     string            `json:"error_category,omitempty"`
	Tokens            TokenCounts       `json:"tokens"`
	LatencyMS         *int64            `json:"latency_ms"`
	TTFTMS            *int64            `json:"ttft_ms"`
	ActualCost        *money.Money      `json:"actual_cost"`
	EstimatedCost     *money.Money      `json:"estimated_cost"`
	PricingVersion    string            `json:"pricing_version,omitempty"`
	Origin            Origin            `json:"origin"`
	Quality           Quality           `json:"quality"`
	Tags              map[string]string `json:"tags,omitempty"`
	Streaming         bool              `json:"streaming"`
	Retry             bool              `json:"retry"`
	Fallback          bool              `json:"fallback"`
	Demo              bool              `json:"demo"`
}

// Granularity of an aggregate interval.
type Granularity string

// Granularities.
const (
	GranMinute Granularity = "minute"
	GranHour   Granularity = "hour"
	GranDay    Granularity = "day"
	GranMonth  Granularity = "month"
	GranTotal  Granularity = "total"
)

// UsageAggregate is a provider-reported (or rolled-up) usage total for an
// interval and set of dimensions. Request-level events are never fabricated
// from aggregates.
type UsageAggregate struct {
	SourceID   string          `json:"source_id"`
	Origin     Origin          `json:"origin"`
	ProviderID ProviderID      `json:"provider_id"`
	AccountID  AccountID       `json:"account_id"`
	Dimensions UsageDimensions `json:"dimensions"`
	// Breakdown names the dimension set this row partitions: "total" (no
	// dimensions), "api_key", "model", "api_key_model", "workspace", ...
	// Rows of different breakdowns describe the same usage and must never be
	// added together.
	Breakdown     string       `json:"breakdown"`
	IntervalStart time.Time    `json:"interval_start"`
	IntervalEnd   time.Time    `json:"interval_end"`
	Granularity   Granularity  `json:"granularity"`
	Requests      *int64       `json:"requests"`
	Tokens        TokenCounts  `json:"tokens"`
	ActualCost    *money.Money `json:"actual_cost"`
	EstimatedCost *money.Money `json:"estimated_cost"`
	ErrorCount    *int64       `json:"error_count"`
	// Partial is true when the interval is still in progress or the source
	// states data may be incomplete.
	Partial bool `json:"partial"`
	// CurrencyVerified is false when the provider's currency labelling is
	// ambiguous (e.g. documented conflict).
	CurrencyVerified bool    `json:"currency_verified"`
	Quality          Quality `json:"quality"`
	Demo             bool    `json:"demo"`
}

// APIKeyRecord is non-secret metadata about a provider API key.
type APIKeyRecord struct {
	ProviderKeyID      string        `json:"provider_key_id"`
	Name               string        `json:"name"`
	Hint               string        `json:"hint,omitempty"` // provider-supplied partial/redacted hint
	Status             *string       `json:"status"`
	Workspace          string        `json:"workspace,omitempty"`
	Project            string        `json:"project,omitempty"`
	CreatedAt          *time.Time    `json:"created_at"`
	ExpiresAt          *time.Time    `json:"expires_at"`
	ProviderLastUsedAt *time.Time    `json:"provider_last_used_at"`
	BudgetUsed         *money.Amount `json:"budget_used_e10"`
	BudgetLimit        *money.Amount `json:"budget_limit_e10"`
	BudgetCurrency     string        `json:"budget_currency,omitempty"`
}

// PriceRecord is a provider-reported model price (per million units).
type PriceRecord struct {
	ProviderID    ProviderID   `json:"provider_id"`
	Model         string       `json:"model"`
	Category      string       `json:"category"` // input, output, cache_read, cache_write, reasoning, ...
	PerMillion    money.Amount `json:"per_million_e10"`
	Currency      string       `json:"currency"`
	EffectiveFrom time.Time    `json:"effective_from"`
	Source        string       `json:"source"`
}
