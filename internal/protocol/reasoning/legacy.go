package reasoning

import "github.com/aurimasniekis/go-harbor-pilot/internal/ir"

// The four protocol defaults. Each WriteLegacy is the translation its
// builder used before methods existed, moved here unchanged, including its
// own effort↔budget table (the tables differ per wire and must keep doing
// so: changing one changes upstream bodies).

func init() {
	for _, m := range []Method{anthropicThinking{}, openAIReasoningEffort{}, openAIResponsesReasoning{}, geminiThinkingConfig{}} {
		Register(m)
	}
	setDefault(ir.AnthropicMessages, StyleAnthropicThinking)
	setDefault(ir.OpenAIChat, StyleOpenAIReasoningEffort)
	setDefault(ir.OpenAIResponses, StyleOpenAIResponsesReasoning)
	setDefault(ir.Gemini, StyleGeminiThinkingConfig)
}

// Method names.
const (
	StyleAnthropicThinking        = "anthropic_thinking"
	StyleOpenAIReasoningEffort    = "openai_reasoning_effort"
	StyleOpenAIResponsesReasoning = "openai_responses_reasoning"
	StyleGeminiThinkingConfig     = "gemini_thinking_config"
)

// AnthropicThinkingKey is the Ext key of a Messages request's raw thinking
// object (kept for adaptive thinking).
const AnthropicThinkingKey = string(ir.AnthropicMessages) + ":thinking"

// ---------- anthropic_thinking ----------

type anthropicThinking struct{}

func (anthropicThinking) Name() string          { return StyleAnthropicThinking }
func (anthropicThinking) Protocol() ir.Protocol { return ir.AnthropicMessages }
func (anthropicThinking) DefaultEfforts() []string {
	return []string{EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh}
}
func (anthropicThinking) DefaultHistoryField() string { return "" }

// EffortForThinkingBudget maps an Anthropic thinking budget to an effort
// level (how a Messages request's budget is read).
func EffortForThinkingBudget(b int) string {
	switch {
	case b <= 0:
		return ""
	case b < 4096:
		return EffortMinimal
	case b < 8192:
		return EffortLow
	case b < 20000:
		return EffortMedium
	case b < 32000:
		return EffortHigh
	}
	return EffortXHigh
}

// ThinkingBudgetForEffort maps an effort level to an Anthropic thinking
// budget (0 for a level with no budget).
func ThinkingBudgetForEffort(e string) int {
	switch e {
	case EffortMinimal:
		return 1024
	case EffortLow:
		return 4096
	case EffortMedium:
		return 10000
	case EffortHigh:
		return 24000
	case EffortXHigh:
		return 32000
	}
	return 0
}

func (anthropicThinking) WriteLegacy(rc *ir.ReasoningConfig, t *Target) Result {
	if rc == nil {
		return Result{}
	}
	switch {
	case rc.Enabled != nil && !*rc.Enabled, rc.Effort == EffortNone:
		// Default for Anthropic is no extended thinking; send nothing.
	case rc.BudgetTokens > 0 || rc.Effort != "":
		writeAnthropicBudget(rc.BudgetTokens, rc.Effort, t)
	}
	if rc.Summary != "" && rc.Summary != "none" && rc.Summary != "auto" {
		t.Losses.Add("reasoning_summary", "reasoning summary style is not configurable on Anthropic", ir.LossApproximate)
	}
	return Result{Active: true}
}

// writeAnthropicBudget writes an enabled thinking budget that fits below
// the request's max_tokens.
func writeAnthropicBudget(budget int, effort string, t *Target) {
	if budget == 0 {
		budget = ThinkingBudgetForEffort(effort)
	}
	if budget < 1024 {
		budget = 1024
	}
	if budget >= t.MaxTokens {
		// Thinking budget must be below max_tokens; keep the client's
		// output limit and shrink the budget.
		nb := t.MaxTokens - 1
		if nb < 1024 {
			t.Losses.Add("reasoning", "max_tokens is too small for extended thinking; thinking disabled", ir.LossDrop)
			return
		}
		t.Losses.Add("reasoning_budget", "thinking budget reduced to fit max_tokens", ir.LossApproximate)
		budget = nb
	}
	t.Body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
}

func (anthropicThinking) Write(p Plan, t *Target) Result {
	switch {
	case p.Off:
		// No thinking is Anthropic's default: send no thinking object, and
		// not the client's either.
		return Result{Consumed: []string{AnthropicThinkingKey}}
	case p.Adaptive:
		// Adaptive thinking is sent as the client wrote it, never
		// rewritten as an enabled budget.
		return Result{Active: true}
	case p.BudgetTokens > 0 || p.Effort != "":
		writeAnthropicBudget(p.BudgetTokens, p.Effort, t)
		return Result{Consumed: []string{AnthropicThinkingKey}, Active: true}
	}
	return Result{}
}

// ---------- openai_reasoning_effort ----------

type openAIReasoningEffort struct{}

func (openAIReasoningEffort) Name() string          { return StyleOpenAIReasoningEffort }
func (openAIReasoningEffort) Protocol() ir.Protocol { return ir.OpenAIChat }
func (openAIReasoningEffort) DefaultEfforts() []string {
	return []string{EffortLow, EffortMedium, EffortHigh}
}
func (openAIReasoningEffort) DefaultHistoryField() string { return "" }

// chatEffortForBudget maps a thinking budget to a Chat reasoning_effort.
func chatEffortForBudget(b int) string {
	switch {
	case b < 8192:
		return EffortLow
	case b < 24000:
		return EffortMedium
	}
	return EffortHigh
}

func (openAIReasoningEffort) WriteLegacy(rc *ir.ReasoningConfig, t *Target) Result {
	if rc == nil {
		return Result{}
	}
	switch {
	case rc.Effort != "":
		t.Body["reasoning_effort"] = rc.Effort
	case rc.BudgetTokens > 0:
		t.Body["reasoning_effort"] = chatEffortForBudget(rc.BudgetTokens)
		t.Losses.Add("reasoning_budget", "thinking budget mapped to a reasoning effort level", ir.LossApproximate)
	case rc.Enabled != nil && !*rc.Enabled:
		// Chat Completions has no explicit "off"; omit.
	}
	return Result{Active: true}
}

func (openAIReasoningEffort) Write(p Plan, t *Target) Result {
	switch {
	case p.Off:
		t.Body["reasoning_effort"] = EffortNone
	case p.Effort != "":
		t.Body["reasoning_effort"] = p.Effort
	case p.BudgetTokens > 0:
		t.Body["reasoning_effort"] = chatEffortForBudget(p.BudgetTokens)
		t.Losses.Add("reasoning_budget", "thinking budget mapped to a reasoning effort level", ir.LossApproximate)
	default:
		return Result{Active: p.Adaptive}
	}
	return Result{Active: true}
}

// ---------- openai_responses_reasoning ----------

type openAIResponsesReasoning struct{}

func (openAIResponsesReasoning) Name() string          { return StyleOpenAIResponsesReasoning }
func (openAIResponsesReasoning) Protocol() ir.Protocol { return ir.OpenAIResponses }
func (openAIResponsesReasoning) DefaultEfforts() []string {
	return []string{EffortMinimal, EffortLow, EffortMedium, EffortHigh}
}
func (openAIResponsesReasoning) DefaultHistoryField() string { return "" }

// responsesEffortForBudget maps a thinking budget to a Responses effort.
func responsesEffortForBudget(b int) string {
	switch {
	case b < 4096:
		return EffortMinimal
	case b < 8192:
		return EffortLow
	case b < 24000:
		return EffortMedium
	}
	return EffortHigh
}

func (openAIResponsesReasoning) WriteLegacy(rc *ir.ReasoningConfig, t *Target) Result {
	if rc == nil {
		return Result{}
	}
	rb := map[string]any{}
	switch {
	case rc.Effort != "":
		rb["effort"] = rc.Effort
	case rc.BudgetTokens > 0:
		rb["effort"] = responsesEffortForBudget(rc.BudgetTokens)
		t.Losses.Add("reasoning_budget", "thinking budget mapped to a reasoning effort level", ir.LossApproximate)
	case rc.Enabled != nil && !*rc.Enabled:
		rb["effort"] = EffortMinimal
		t.Losses.Add("reasoning_off", "reasoning cannot be fully disabled; minimal effort requested", ir.LossApproximate)
	}
	if rc.Summary != "" {
		rb["summary"] = rc.Summary
	}
	if len(rb) > 0 {
		t.Body["reasoning"] = rb
	}
	if rc.IncludeEncrypted && t.Include != nil {
		*t.Include = append(*t.Include, "reasoning.encrypted_content")
	}
	return Result{Active: true}
}

func (openAIResponsesReasoning) Write(p Plan, t *Target) Result {
	rb := map[string]any{}
	switch {
	case p.Off:
		rb["effort"] = EffortNone
	case p.Effort != "":
		rb["effort"] = p.Effort
	case p.BudgetTokens > 0:
		rb["effort"] = responsesEffortForBudget(p.BudgetTokens)
		t.Losses.Add("reasoning_budget", "thinking budget mapped to a reasoning effort level", ir.LossApproximate)
	}
	if p.Summary != "" && !p.Off {
		rb["summary"] = p.Summary
	}
	if len(rb) > 0 {
		t.Body["reasoning"] = rb
	}
	if p.IncludeEncrypted && t.Include != nil {
		*t.Include = append(*t.Include, "reasoning.encrypted_content")
	}
	return Result{Active: len(rb) > 0 || p.Adaptive || p.IncludeEncrypted}
}

// ---------- gemini_thinking_config ----------

type geminiThinkingConfig struct{}

func (geminiThinkingConfig) Name() string          { return StyleGeminiThinkingConfig }
func (geminiThinkingConfig) Protocol() ir.Protocol { return ir.Gemini }
func (geminiThinkingConfig) DefaultEfforts() []string {
	return []string{EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh}
}
func (geminiThinkingConfig) DefaultHistoryField() string { return "" }

// geminiBudgetForEffort maps an effort level to a Gemini thinking budget.
var geminiBudgetForEffort = map[string]int{EffortMinimal: 512, EffortLow: 2048, EffortMedium: 8192, EffortHigh: 24576, EffortXHigh: 32768}

// geminiDynamicBudget asks the model to choose its own budget.
const geminiDynamicBudget = -1

func (geminiThinkingConfig) WriteLegacy(rc *ir.ReasoningConfig, t *Target) Result {
	if rc == nil {
		return Result{}
	}
	tc := map[string]any{}
	switch {
	case rc.Enabled != nil && !*rc.Enabled, rc.Effort == EffortNone:
		tc["thinkingBudget"] = 0
	case rc.BudgetTokens > 0:
		tc["thinkingBudget"] = rc.BudgetTokens
	case rc.Effort != "":
		tc["thinkingBudget"] = geminiBudgetForEffort[rc.Effort]
		t.Losses.Add("reasoning_effort", "reasoning effort mapped to a thinking budget", ir.LossApproximate)
	}
	if rc.Summary != "" && rc.Summary != "none" {
		tc["includeThoughts"] = true
	}
	if len(tc) > 0 {
		t.Body["thinkingConfig"] = tc
	}
	return Result{Active: true}
}

func (geminiThinkingConfig) Write(p Plan, t *Target) Result {
	tc := map[string]any{}
	switch {
	case p.Off:
		tc["thinkingBudget"] = 0
	case p.BudgetTokens > 0:
		tc["thinkingBudget"] = p.BudgetTokens
	case p.Effort != "":
		tc["thinkingBudget"] = geminiBudgetForEffort[p.Effort]
		t.Losses.Add("reasoning_effort", "reasoning effort mapped to a thinking budget", ir.LossApproximate)
	case p.Adaptive:
		tc["thinkingBudget"] = geminiDynamicBudget
	}
	if p.Summary != "" && p.Summary != "none" && !p.Off {
		tc["includeThoughts"] = true
	}
	if len(tc) > 0 {
		t.Body["thinkingConfig"] = tc
	}
	return Result{Active: len(tc) > 0}
}
