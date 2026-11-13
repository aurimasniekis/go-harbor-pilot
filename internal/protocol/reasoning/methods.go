package reasoning

import "github.com/aurimasniekis/go-harbor-pilot/internal/ir"

// Methods that are no protocol's default: a model's reasoning option names
// them. Each wire shape is written from the upstream's public API
// documentation.

func init() {
	for _, m := range []Method{openRouterReasoning{}, deepSeekThinking{}, noReasoning{}} {
		Register(m)
	}
}

// Method names.
const (
	StyleOpenRouterReasoning = "openrouter_reasoning"
	StyleDeepSeekThinking    = "deepseek_thinking"
	StyleNone                = "none"
)

// ---------- openrouter_reasoning ----------

// openRouterReasoning writes the unified reasoning object OpenAI-compatible
// routers accept on Chat Completions: either an effort or a token budget
// (never both), effort "none" to turn reasoning off, enabled for the
// model's default.
type openRouterReasoning struct{}

func (openRouterReasoning) Name() string          { return StyleOpenRouterReasoning }
func (openRouterReasoning) Protocol() ir.Protocol { return ir.OpenAIChat }
func (openRouterReasoning) DefaultEfforts() []string {
	return []string{EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
}

// DefaultHistoryField: the router reads earlier plain-text reasoning from
// the assistant message's reasoning field.
func (openRouterReasoning) DefaultHistoryField() string { return "reasoning" }

func (openRouterReasoning) Write(p Plan, t *Target) Result {
	var rb map[string]any
	switch {
	case p.Off:
		rb = map[string]any{"effort": EffortNone}
	case p.BudgetTokens > 0 && t.MaxTokens > 0 && p.BudgetTokens >= t.MaxTokens:
		// The router needs the budget below the request's output limit.
		if t.MaxTokens > 1 {
			t.Losses.Add("reasoning_budget", "thinking budget reduced to fit max_tokens", ir.LossApproximate)
			rb = map[string]any{"max_tokens": t.MaxTokens - 1}
		} else if p.Effort != "" {
			t.Losses.Add("reasoning_budget", "max_tokens is too small for the thinking budget; its effort level was sent", ir.LossApproximate)
			rb = map[string]any{"effort": p.Effort}
		} else {
			t.Losses.Add("reasoning", "max_tokens is too small for a thinking budget; reasoning left at the upstream default", ir.LossDrop)
			return Result{}
		}
	case p.BudgetTokens > 0:
		rb = map[string]any{"max_tokens": p.BudgetTokens}
	case p.Effort != "":
		rb = map[string]any{"effort": p.Effort}
	case p.Adaptive:
		rb = map[string]any{"enabled": true}
	default:
		return Result{}
	}
	t.Body["reasoning"] = rb
	return Result{Active: !p.Off}
}

// ---------- deepseek_thinking ----------

// deepSeekThinking writes the thinking toggle and, when a level is known,
// reasoning_effort, as DeepSeek's OpenAI-format API documents them.
type deepSeekThinking struct{}

func (deepSeekThinking) Name() string          { return StyleDeepSeekThinking }
func (deepSeekThinking) Protocol() ir.Protocol { return ir.OpenAIChat }
func (deepSeekThinking) DefaultEfforts() []string {
	return []string{EffortLow, EffortHigh, EffortMax}
}
func (deepSeekThinking) DefaultHistoryField() string { return "reasoning_content" }

func (deepSeekThinking) Write(p Plan, t *Target) Result {
	switch {
	case p.Off:
		t.Body["thinking"] = map[string]any{"type": "disabled"}
		return Result{}
	case p.Effort != "":
		t.Body["thinking"] = map[string]any{"type": "enabled"}
		t.Body["reasoning_effort"] = p.Effort
		if p.BudgetTokens > 0 {
			t.Losses.Add("reasoning_budget", "this upstream takes no thinking budget; the budget's effort level was sent", ir.LossApproximate)
		}
	case p.BudgetTokens > 0:
		t.Body["thinking"] = map[string]any{"type": "enabled"}
		t.Losses.Add("reasoning_budget", "this upstream takes no thinking budget; thinking enabled at its default effort", ir.LossApproximate)
	case p.Adaptive:
		t.Body["thinking"] = map[string]any{"type": "enabled"}
	default:
		return Result{}
	}
	return Result{Active: true}
}

// ---------- none ----------

// noReasoning sends no reasoning field at all, whatever the client asked:
// for upstreams that reject every reasoning parameter.
type noReasoning struct{}

func (noReasoning) Name() string                { return StyleNone }
func (noReasoning) Protocol() ir.Protocol       { return "" }
func (noReasoning) DefaultEfforts() []string    { return nil }
func (noReasoning) DefaultHistoryField() string { return "" }

func (noReasoning) Write(Plan, *Target) Result {
	return Result{Consumed: []string{AnthropicThinkingKey}}
}
