// Package ir defines Harbor Pilot's canonical, protocol-neutral model of
// LLM requests, responses and streaming events.
//
// Design rules:
//   - Nothing is lost silently. Fields a protocol adapter does not model are
//     kept in Ext (keyed by protocol) and restored when the request goes back
//     out on the same protocol family.
//   - Opaque signed or encrypted state (thinking signatures, encrypted
//     reasoning, thought signatures) is carried as a Sealed part tagged with
//     its family and origin. It is never converted to text, and it is only
//     sent back to the same family; other targets drop it under an explicit
//     degradation policy, recorded as a capability loss.
//   - Reasoning is never invented. Only reasoning text an upstream actually
//     exposed is represented.
//   - Usage distinguishes unknown (nil) from zero.
package ir

import "encoding/json"

// Protocol identifies a wire protocol.
type Protocol string

// Wire protocols.
const (
	AnthropicMessages Protocol = "anthropic_messages"
	OpenAIChat        Protocol = "openai_chat"
	OpenAIResponses   Protocol = "openai_responses"
	Gemini            Protocol = "gemini"
	Ollama            Protocol = "ollama"
	ClaudeCodeBridge  Protocol = "claude_code_bridge"
	CodexBridge       Protocol = "codex_bridge"
)

// Family groups protocols that can exchange sealed state.
func (p Protocol) Family() string {
	switch p {
	case AnthropicMessages, ClaudeCodeBridge:
		return "anthropic"
	case OpenAIResponses, CodexBridge:
		return "openai_responses"
	case Gemini:
		return "gemini"
	}
	return string(p)
}

// Role of a message author.
type Role string

// Roles.
const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartKind identifies a content part.
type PartKind string

// Part kinds.
const (
	PartText       PartKind = "text"
	PartImage      PartKind = "image"
	PartDocument   PartKind = "document"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
	PartReasoning  PartKind = "reasoning" // exposed reasoning text or summary
	PartSealed     PartKind = "sealed"    // opaque provider state
	PartRefusal    PartKind = "refusal"
)

// Media is image or document content.
type Media struct {
	MimeType string `json:"mime_type,omitempty"`
	Data     []byte `json:"data,omitempty"` // decoded bytes for inline content
	URL      string `json:"url,omitempty"`  // remote reference (http(s) or provider file id)
	FileID   string `json:"file_id,omitempty"`
	Detail   string `json:"detail,omitempty"` // low|high|auto (image detail hint)
	Title    string `json:"title,omitempty"`
}

// ToolCall is a model-generated call to a client tool.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments is the raw JSON argument text exactly as generated (it may be
	// invalid JSON if the model produced it so; adapters never "fix" it).
	Arguments string `json:"arguments"`
	// Kind is "function" (JSON arguments) or "custom" (free-form input).
	Kind string `json:"kind,omitempty"`
}

// ToolResult is the client's result for a previous tool call.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name,omitempty"`
	Content []Part `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// Reasoning is reasoning text the provider exposed (full or summary).
type Reasoning struct {
	Text    string `json:"text"`
	Summary bool   `json:"summary,omitempty"`
}

// Sealed is opaque provider state that must round-trip byte-for-byte.
type Sealed struct {
	Family string `json:"family"` // anthropic | openai_responses | gemini
	// Kind: thinking_signature, redacted_thinking, encrypted_reasoning,
	// thought_signature, reasoning_item, compaction.
	Kind string `json:"kind"`
	Data string `json:"data"`
	// ItemID is a provider item id the state belongs to (Responses items).
	ItemID string `json:"item_id,omitempty"`
	// Attached is the text of the thinking block the signature covers, when
	// the protocol requires sending both (Anthropic thinking blocks).
	Attached string `json:"attached,omitempty"`
	// ForCallID links a signature to a tool call (Gemini thought signatures).
	ForCallID string `json:"for_call_id,omitempty"`
}

// CacheControl marks a prompt-cache breakpoint.
type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// Part is one piece of message content.
type Part struct {
	Kind         PartKind                   `json:"kind"`
	Text         string                     `json:"text,omitempty"`
	Media        *Media                     `json:"media,omitempty"`
	ToolCall     *ToolCall                  `json:"tool_call,omitempty"`
	ToolResult   *ToolResult                `json:"tool_result,omitempty"`
	Reasoning    *Reasoning                 `json:"reasoning,omitempty"`
	Sealed       *Sealed                    `json:"sealed,omitempty"`
	CacheControl *CacheControl              `json:"cache_control,omitempty"`
	Ext          map[string]json.RawMessage `json:"ext,omitempty"`
}

// Message is one conversation turn.
type Message struct {
	Role  Role                       `json:"role"`
	Name  string                     `json:"name,omitempty"`
	Parts []Part                     `json:"parts"`
	Ext   map[string]json.RawMessage `json:"ext,omitempty"`
}

// Tool is a tool definition offered to the model.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
	Strict      *bool           `json:"strict,omitempty"`
	// Kind: "function", "custom" (free-form) or "builtin" (provider-executed,
	// e.g. web search); BuiltinType names the provider tool type.
	Kind         string                     `json:"kind,omitempty"`
	BuiltinType  string                     `json:"builtin_type,omitempty"`
	CacheControl *CacheControl              `json:"cache_control,omitempty"`
	Ext          map[string]json.RawMessage `json:"ext,omitempty"`
}

// ToolChoice constrains tool use.
type ToolChoice struct {
	Mode string `json:"mode"` // auto | none | required | tool
	Name string `json:"name,omitempty"`
}

// ResponseFormat requests structured output.
type ResponseFormat struct {
	Type   string          `json:"type"` // text | json_object | json_schema
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
}

// ReasoningConfig requests model reasoning.
type ReasoningConfig struct {
	// Effort: none | minimal | low | medium | high | xhigh (empty = default).
	Effort string `json:"effort,omitempty"`
	// BudgetTokens is an explicit thinking budget (Anthropic, Gemini).
	BudgetTokens int `json:"budget_tokens,omitempty"`
	// Enabled is false when the client explicitly disabled reasoning.
	Enabled *bool `json:"enabled,omitempty"`
	// Summary: auto | concise | detailed | none.
	Summary string `json:"summary,omitempty"`
	// IncludeEncrypted asks for encrypted reasoning content (stateless use).
	IncludeEncrypted bool `json:"include_encrypted,omitempty"`
	// Adaptive is set when the client let the model decide whether and how
	// much to think (Anthropic thinking type "adaptive"). It names no level.
	Adaptive bool `json:"adaptive,omitempty"`
	// OutputEffort is the client's top-level output effort (Anthropic
	// output_config.effort). It is kept apart from Effort: on its own it
	// does not ask for reasoning and is not a routing requirement.
	OutputEffort string `json:"output_effort,omitempty"`
}

// Legacy is the configuration the protocol builders translate by default.
// A configuration that holds only Adaptive or OutputEffort counts as none:
// those were not modelled before, and an upstream that has not been told
// how to translate them gets exactly what it got then (the client's raw
// fields stay in Ext for the native path). Nil-safe.
func (rc *ReasoningConfig) Legacy() *ReasoningConfig {
	if rc == nil {
		return nil
	}
	if (rc.Adaptive || rc.OutputEffort != "") && rc.Effort == "" && rc.BudgetTokens == 0 &&
		rc.Summary == "" && !rc.IncludeEncrypted && (rc.Enabled == nil || *rc.Enabled) {
		return nil
	}
	return rc
}

// Request is a canonical model request.
type Request struct {
	Source             Protocol                   `json:"source"`
	Model              string                     `json:"model"`
	System             []Part                     `json:"system,omitempty"` // instructions (in order)
	SystemRoles        []Role                     `json:"system_roles,omitempty"`
	Messages           []Message                  `json:"messages"`
	Tools              []Tool                     `json:"tools,omitempty"`
	ToolChoice         *ToolChoice                `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                      `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens    *int                       `json:"max_output_tokens,omitempty"`
	Temperature        *float64                   `json:"temperature,omitempty"`
	TopP               *float64                   `json:"top_p,omitempty"`
	TopK               *int                       `json:"top_k,omitempty"`
	Stop               []string                   `json:"stop,omitempty"`
	Seed               *int64                     `json:"seed,omitempty"`
	Stream             bool                       `json:"stream"`
	ResponseFormat     *ResponseFormat            `json:"response_format,omitempty"`
	Reasoning          *ReasoningConfig           `json:"reasoning,omitempty"`
	Metadata           map[string]string          `json:"metadata,omitempty"`
	User               string                     `json:"user,omitempty"`
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Store              *bool                      `json:"store,omitempty"`
	CacheKey           string                     `json:"cache_key,omitempty"`
	ServiceTier        string                     `json:"service_tier,omitempty"`
	Ext                map[string]json.RawMessage `json:"ext,omitempty"` // unmodelled top-level fields, keyed "<protocol>:<field>"
}

// StopReason is why generation ended.
type StopReason string

// Stop reasons.
const (
	StopEndTurn       StopReason = "end_turn"
	StopMaxTokens     StopReason = "max_tokens"
	StopSequence      StopReason = "stop_sequence"
	StopToolUse       StopReason = "tool_use"
	StopContentFilter StopReason = "content_filter"
	StopRefusal       StopReason = "refusal"
	StopPause         StopReason = "pause_turn"
	StopError         StopReason = "error"
	StopUnknown       StopReason = "unknown"
)

// Usage counts tokens. Nil means the upstream did not report the category
// (unknown), which is different from zero.
type Usage struct {
	InputTokens      *int64 `json:"input_tokens,omitempty"`
	OutputTokens     *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoning_tokens,omitempty"`
	// InputIncludesCache reports whether InputTokens already includes cache
	// read/write tokens (OpenAI, Gemini) or excludes them (Anthropic).
	InputIncludesCache bool `json:"input_includes_cache"`
	// OutputIncludesReasoning reports whether OutputTokens includes reasoning.
	OutputIncludesReasoning bool `json:"output_includes_reasoning"`
}

// Known reports whether any category was reported.
func (u Usage) Known() bool {
	return u.InputTokens != nil || u.OutputTokens != nil || u.CacheReadTokens != nil || u.CacheWriteTokens != nil
}

// I64 returns a pointer to v.
func I64(v int64) *int64 { return &v }

// Val returns *p or 0.
func Val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// Merge overlays non-nil categories of o onto u (later usage reports win).
func (u *Usage) Merge(o Usage) {
	if o.InputTokens != nil {
		u.InputTokens = o.InputTokens
	}
	if o.OutputTokens != nil {
		u.OutputTokens = o.OutputTokens
	}
	if o.CacheReadTokens != nil {
		u.CacheReadTokens = o.CacheReadTokens
	}
	if o.CacheWriteTokens != nil {
		u.CacheWriteTokens = o.CacheWriteTokens
	}
	if o.ReasoningTokens != nil {
		u.ReasoningTokens = o.ReasoningTokens
	}
	u.InputIncludesCache = u.InputIncludesCache || o.InputIncludesCache
	u.OutputIncludesReasoning = u.OutputIncludesReasoning || o.OutputIncludesReasoning
}

// UncachedInput returns input tokens excluding cache categories, or nil.
func (u Usage) UncachedInput() *int64 {
	if u.InputTokens == nil {
		return nil
	}
	v := *u.InputTokens
	if u.InputIncludesCache {
		v -= Val(u.CacheReadTokens) + Val(u.CacheWriteTokens)
		if v < 0 {
			v = 0
		}
	}
	return &v
}

// Response is a complete (non-streaming) model response.
type Response struct {
	ID         string                     `json:"id"`
	Model      string                     `json:"model"`
	Parts      []Part                     `json:"parts"`
	StopReason StopReason                 `json:"stop_reason"`
	StopSeq    string                     `json:"stop_sequence,omitempty"`
	Usage      Usage                      `json:"usage"`
	Ext        map[string]json.RawMessage `json:"ext,omitempty"`
}

// EventKind identifies a streaming event.
type EventKind string

// Event kinds. A stream is: Start, then for each output part PartStart,
// zero or more Delta, PartStop; Usage may appear at any point; Stop ends a
// successful stream; Error ends a failed one.
const (
	EvStart     EventKind = "start"
	EvPartStart EventKind = "part_start"
	EvDelta     EventKind = "delta"
	EvPartStop  EventKind = "part_stop"
	EvUsage     EventKind = "usage"
	EvStop      EventKind = "stop"
	EvError     EventKind = "error"
)

// Event is one streaming event.
type Event struct {
	Kind EventKind `json:"kind"`
	// Index is the output part index this event refers to.
	Index int `json:"index"`
	// Part carries the part header on PartStart (kind, tool call id/name,
	// sealed state) and the final sealed data on PartStop when it arrives late.
	Part *Part `json:"part,omitempty"`
	// Delta is incremental text, reasoning text or tool argument JSON text.
	Delta string `json:"delta,omitempty"`
	// Usage on EvUsage (partial or final).
	Usage *Usage `json:"usage,omitempty"`
	// StopReason on EvStop.
	StopReason StopReason `json:"stop_reason,omitempty"`
	StopSeq    string     `json:"stop_sequence,omitempty"`
	// Err on EvError.
	Err *Error `json:"error,omitempty"`
	// ResponseID / Model on EvStart.
	ResponseID string `json:"response_id,omitempty"`
	Model      string `json:"model,omitempty"`
}

// ErrorKind classifies upstream and gateway errors.
type ErrorKind string

// Error kinds.
const (
	ErrInvalidRequest ErrorKind = "invalid_request"
	ErrAuthentication ErrorKind = "authentication"
	ErrPermission     ErrorKind = "permission"
	ErrNotFound       ErrorKind = "not_found"
	ErrRateLimited    ErrorKind = "rate_limited"
	ErrOverloaded     ErrorKind = "overloaded"
	ErrContextLength  ErrorKind = "context_length"
	ErrContentFilter  ErrorKind = "content_filter"
	ErrUpstream       ErrorKind = "upstream"
	ErrTimeout        ErrorKind = "timeout"
	ErrCanceled       ErrorKind = "canceled"
	ErrUnsupported    ErrorKind = "unsupported"
	ErrQuotaExhausted ErrorKind = "quota_exhausted"
	ErrBudget         ErrorKind = "budget_exceeded"
	ErrPolicy         ErrorKind = "policy_blocked"
	ErrInternal       ErrorKind = "internal"
	ErrProtocol       ErrorKind = "protocol" // unparseable/incompatible upstream output
)

// Error is a canonical error. Message is safe to show to clients; it never
// contains credentials or upstream request bodies.
type Error struct {
	Kind       ErrorKind `json:"kind"`
	Message    string    `json:"message"`
	HTTPStatus int       `json:"http_status,omitempty"`
	// RetryAfterSeconds is the provider's retry-after hint, if any.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
	// UpstreamType is the provider's own error type string, if any.
	UpstreamType string `json:"upstream_type,omitempty"`
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }

// StatusFor maps an error kind to the HTTP status a client should see.
func StatusFor(k ErrorKind) int {
	switch k {
	case ErrInvalidRequest, ErrContextLength, ErrUnsupported:
		return 400
	case ErrAuthentication:
		return 401
	case ErrPermission, ErrPolicy, ErrContentFilter:
		return 403
	case ErrNotFound:
		return 404
	case ErrRateLimited, ErrQuotaExhausted, ErrBudget:
		return 429
	case ErrOverloaded:
		return 529
	case ErrTimeout:
		return 504
	case ErrCanceled:
		return 499
	}
	return 502
}
