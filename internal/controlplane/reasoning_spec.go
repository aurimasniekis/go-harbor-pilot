package controlplane

import (
	"regexp"
	"slices"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/reasoning"
)

// ModelReasoningSpec is a model's reasoning option (D-047): how the client's
// reasoning settings (Anthropic thinking and output_config.effort, Chat
// reasoning_effort, Responses reasoning, Gemini thinkingConfig) are
// translated for this model's upstream. Leaving it out keeps the built-in
// translation byte for byte.
type ModelReasoningSpec struct {
	Mode            string   `json:"mode" enum:"auto,client,fixed,off," required:"false" doc:"auto (default): the built-in translation. client: follow the client's settings, written in this model's style, with effort/budget_tokens as the default when the client names no level. fixed: always ask for effort or budget_tokens. off: always ask the upstream not to reason."`
	Style           string   `json:"style" enum:"anthropic_thinking,openai_reasoning_effort,openai_responses_reasoning,openrouter_reasoning,deepseek_thinking,gemini_thinking_config,none," required:"false" doc:"The upstream's reasoning wire shape. Empty = the upstream protocol's default. Allowed values depend on the provider type."`
	Effort          string   `json:"effort" enum:"minimal,low,medium,high,xhigh,max," required:"false" doc:"Default (client) or fixed (fixed) effort level."`
	BudgetTokens    int      `json:"budget_tokens" minimum:"0" required:"false" doc:"Default (client) or fixed (fixed) thinking budget in tokens, instead of effort."`
	MaxBudgetTokens int      `json:"max_budget_tokens" minimum:"0" required:"false" doc:"Larger client thinking budgets are reduced to this. 0 = no cap."`
	Efforts         []string `json:"efforts" required:"false" doc:"The effort levels the model accepts; other levels are clamped to the nearest lower one. Empty = the style's defaults."`
	History         string   `json:"history" enum:"drop,field," required:"false" doc:"Earlier reasoning in a Chat history: drop (default) or field (sent back in history_field)."`
	HistoryField    string   `json:"history_field" required:"false" doc:"Assistant message field that carries earlier reasoning back when history is field. Empty = the style's default (reasoning_content for deepseek_thinking, reasoning for openrouter_reasoning)."`
}

// reasoningStyles are the styles each provider type accepts.
var reasoningStyles = map[string][]string{
	ProviderAnthropic:        {reasoning.StyleAnthropicThinking, reasoning.StyleNone},
	ProviderOpenAI:           {reasoning.StyleOpenAIReasoningEffort, reasoning.StyleOpenAIResponsesReasoning, reasoning.StyleNone},
	ProviderGemini:           {reasoning.StyleGeminiThinkingConfig, reasoning.StyleNone},
	ProviderOpenAICompatible: {reasoning.StyleOpenAIReasoningEffort, reasoning.StyleOpenRouterReasoning, reasoning.StyleDeepSeekThinking, reasoning.StyleNone},
	ProviderOllama:           {reasoning.StyleOpenAIReasoningEffort, reasoning.StyleNone},
}

// ReasoningStyles lists the styles a provider type accepts.
func ReasoningStyles(providerType string) []string {
	return slices.Clone(reasoningStyles[providerType])
}

// historyFieldRE is the shape of a history field name.
var historyFieldRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// reservedMessageFields are Chat message fields a history field must not
// overwrite.
var reservedMessageFields = []string{"role", "content", "name", "tool_calls", "tool_call_id", "refusal", "audio", "function_call"}

// normalizeReasoning folds an option that changes nothing into nil, so an
// unconfigured model and one saved with defaults store the same spec.
func normalizeReasoning(m *ModelSpec) {
	r := m.Reasoning
	if r == nil {
		return
	}
	r.Mode = strings.TrimSpace(r.Mode)
	if r.Mode == "" {
		r.Mode = reasoning.ModeAuto
	}
	r.Efforts = SortedUnique(r.Efforts)
	slices.SortFunc(r.Efforts, func(a, b string) int { return reasoning.Rank(a) - reasoning.Rank(b) })
	if r.History == reasoning.HistoryDrop {
		r.History = ""
	}
	if r.Mode == reasoning.ModeAuto && r.Style == "" && r.Effort == "" && r.BudgetTokens == 0 && r.MaxBudgetTokens == 0 &&
		len(r.Efforts) == 0 && r.History == "" && r.HistoryField == "" {
		m.Reasoning = nil
	}
}

func validateModelReasoning(v *Validator, loc string, providerType string, t ProviderType, m ModelSpec) {
	r := m.Reasoning
	if r == nil {
		return
	}
	loc += ".reasoning"
	if t.Execution == "cli_bridge" {
		v.Fail(loc, "CLI bridge providers run the vendor CLI, which chooses its own thinking; a reasoning option cannot be set")
		return
	}
	modes := []string{reasoning.ModeAuto, reasoning.ModeClient, reasoning.ModeFixed, reasoning.ModeOff}
	if !slices.Contains(modes, r.Mode) {
		v.Fail(loc+".mode", "must be auto, client, fixed or off")
		return
	}
	styles := reasoningStyles[providerType]
	if r.Style != "" && !slices.Contains(styles, r.Style) {
		v.Fail(loc+".style", "must be one of "+strings.Join(styles, ", ")+" for "+providerType+" providers")
	}
	for _, e := range r.Efforts {
		v.Check(reasoning.Rank(e) > 0, loc+".efforts", "unknown effort level "+e+" (minimal, low, medium, high, xhigh, max)")
	}
	if r.Effort != "" {
		v.Check(reasoning.Rank(r.Effort) > 0, loc+".effort", "must be minimal, low, medium, high, xhigh or max (use mode off to turn reasoning off)")
		v.Check(len(r.Efforts) == 0 || slices.Contains(r.Efforts, r.Effort), loc+".effort", "must be one of the model's efforts")
	}
	v.Check(r.BudgetTokens >= 0, loc+".budget_tokens", "must not be negative")
	v.Check(r.MaxBudgetTokens >= 0, loc+".max_budget_tokens", "must not be negative")
	if m.MaxOutputTokens > 0 {
		v.Check(r.BudgetTokens < m.MaxOutputTokens, loc+".budget_tokens", "must be below the model's max_output_tokens")
		v.Check(r.MaxBudgetTokens < m.MaxOutputTokens, loc+".max_budget_tokens", "must be below the model's max_output_tokens")
	}
	v.Check(r.MaxBudgetTokens == 0 || r.BudgetTokens <= r.MaxBudgetTokens, loc+".budget_tokens", "must not exceed max_budget_tokens")
	v.Check(r.Effort == "" || r.BudgetTokens == 0, loc+".budget_tokens", "set effort or budget_tokens, not both")
	switch r.Mode {
	case reasoning.ModeAuto:
		// The built-in translation: only the history setting applies.
		v.Check(r.Style == "" && r.Effort == "" && r.BudgetTokens == 0 && r.MaxBudgetTokens == 0 && len(r.Efforts) == 0,
			loc+".mode", "style, effort, budgets and efforts need mode client, fixed or off")
	case reasoning.ModeClient:
		v.Check(r.Style != reasoning.StyleNone, loc+".style", "mode client cannot use style none (use mode off)")
	case reasoning.ModeFixed:
		v.Check(r.Style != reasoning.StyleNone, loc+".style", "mode fixed cannot use style none (use mode off)")
		v.Check((r.Effort != "") != (r.BudgetTokens > 0), loc+".effort", "mode fixed needs exactly one of effort or budget_tokens")
	case reasoning.ModeOff:
		v.Check(!slices.Contains(m.Capabilities, "reasoning"), loc+".mode", "mode off cannot be used on a model that declares the reasoning capability")
		v.Check(r.Effort == "" && r.BudgetTokens == 0 && r.MaxBudgetTokens == 0 && len(r.Efforts) == 0, loc+".mode", "mode off takes no effort, budgets or efforts")
	}
	switch r.History {
	case "":
		v.Check(r.HistoryField == "", loc+".history_field", "needs history field")
	case reasoning.HistoryField:
		v.Check(providerType == ProviderOpenAICompatible || providerType == ProviderOllama, loc+".history",
			"history field applies to Chat Completions upstreams (openai_compatible, ollama)")
		if r.HistoryField != "" {
			v.Check(historyFieldRE.MatchString(r.HistoryField), loc+".history_field", "must be a lowercase identifier")
			v.Check(!slices.Contains(reservedMessageFields, r.HistoryField), loc+".history_field", "is a standard message field")
		} else {
			m := reasoning.MethodFor(r.Style, ir.OpenAIChat)
			v.Check(m != nil && m.DefaultHistoryField() != "", loc+".history_field", "is required: this style has no default history field")
		}
	default:
		v.Fail(loc+".history", "must be drop or field")
	}
}

// Policy converts the option for the protocol builders (nil when unset).
func (r *ModelReasoningSpec) Policy() *reasoning.Policy {
	if r == nil {
		return nil
	}
	return &reasoning.Policy{
		Mode: r.Mode, Style: r.Style, Effort: r.Effort, BudgetTokens: r.BudgetTokens, MaxBudgetTokens: r.MaxBudgetTokens,
		Efforts: slices.Clone(r.Efforts), History: r.History, HistoryField: r.HistoryField,
	}
}
