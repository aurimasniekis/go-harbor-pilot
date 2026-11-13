package reasoning

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// Policy modes: how a model's reasoning option treats the client's
// reasoning settings.
const (
	// ModeAuto keeps each protocol's built-in translation (the default).
	ModeAuto = "auto"
	// ModeClient follows the client's settings, translated with the
	// model's style; the model's level is the default when the client
	// names none.
	ModeClient = "client"
	// ModeFixed always asks for the model's configured level.
	ModeFixed = "fixed"
	// ModeOff always asks the upstream not to reason.
	ModeOff = "off"
)

// History settings: what happens to earlier reasoning in a Chat history.
const (
	HistoryDrop  = "drop"
	HistoryField = "field"
)

// OutputConfigKey is the Ext key of a Messages request's raw output_config
// object.
const OutputConfigKey = string(ir.AnthropicMessages) + ":output_config"

// Policy is a model's reasoning option (D-047) in protocol terms. A nil
// policy, or one in ModeAuto, keeps the legacy translation byte for byte.
type Policy struct {
	Mode            string
	Style           string
	Effort          string
	BudgetTokens    int
	MaxBudgetTokens int
	// Efforts are the levels the model accepts (empty = the method's
	// defaults); other levels are clamped to the nearest lower one.
	Efforts      []string
	History      string
	HistoryField string
}

// active reports that the policy replaces the legacy translation.
func (p *Policy) active() bool {
	return p != nil && p.Mode != "" && p.Mode != ModeAuto
}

// MethodFor is the method a policy's style resolves to for protocol p: the
// named method when it writes p's bodies (or writes nothing), else p's
// default.
func MethodFor(style string, p ir.Protocol) Method {
	if style != "" {
		if m, ok := Lookup(style); ok && (m.Protocol() == "" || m.Protocol() == p) {
			return m
		}
	}
	return DefaultFor(p)
}

// HistoryFieldFor is the assistant message field that carries earlier
// reasoning text to protocol p's upstream ("" = drop it, the default).
func HistoryFieldFor(pol *Policy, p ir.Protocol) string {
	if pol == nil || pol.History != HistoryField {
		return ""
	}
	if pol.HistoryField != "" {
		return pol.HistoryField
	}
	if m := MethodFor(pol.Style, p); m != nil {
		return m.DefaultHistoryField()
	}
	return ""
}

// asked reports that the client asked for reasoning in any form.
func asked(rc *ir.ReasoningConfig) bool {
	return rc != nil && !disabled(rc) && (rc.Effort != "" || rc.BudgetTokens > 0 || rc.Adaptive || rc.OutputEffort != "")
}

// disabled reports that the client turned reasoning off.
func disabled(rc *ir.ReasoningConfig) bool {
	return rc != nil && ((rc.Enabled != nil && !*rc.Enabled) || rc.Effort == EffortNone)
}

// Resolve turns the client's reasoning settings and a model policy into a
// plan for method m, recording what the policy changed in losses.
func Resolve(rc *ir.ReasoningConfig, pol *Policy, m Method, losses *ir.Losses) Plan {
	var p Plan
	switch pol.Mode {
	case ModeOff:
		if asked(rc) {
			losses.Add("reasoning_policy", "the model's reasoning option turns reasoning off", ir.LossApproximate)
		}
		return Plan{Off: true}
	case ModeFixed:
		p = Plan{Effort: pol.Effort, BudgetTokens: pol.BudgetTokens}
		if asked(rc) || disabled(rc) {
			losses.Add("reasoning_policy", "the model's reasoning option sets a fixed level; the client's setting is not used", ir.LossApproximate)
		}
	default: // ModeClient
		if disabled(rc) {
			return Plan{Off: true}
		}
		if rc != nil {
			p.Effort, p.BudgetTokens = rc.Effort, rc.BudgetTokens
			if rc.OutputEffort != "" && (rc.Adaptive || rc.Effort == "") {
				p.Effort = rc.OutputEffort
			}
			p.Summary, p.IncludeEncrypted = rc.Summary, rc.IncludeEncrypted
			// Kept with a level too: a native upstream receives adaptive
			// thinking as the client wrote it.
			p.Adaptive = rc.Adaptive
		}
		if p.Effort == "" && p.BudgetTokens == 0 {
			// The client named no level: the model's default (methods
			// without one let the model decide when Adaptive is set).
			p.Effort, p.BudgetTokens = pol.Effort, pol.BudgetTokens
		}
	}
	clamp(&p, pol, m, losses)
	return p
}

// clamp keeps a plan's level within what the model accepts.
func clamp(p *Plan, pol *Policy, m Method, losses *ir.Losses) {
	if pol.MaxBudgetTokens > 0 && p.BudgetTokens > pol.MaxBudgetTokens {
		losses.Add("reasoning_budget", fmt.Sprintf("thinking budget reduced to the model's maximum of %d tokens", pol.MaxBudgetTokens), ir.LossApproximate)
		p.BudgetTokens = pol.MaxBudgetTokens
	}
	allowed := pol.Efforts
	if len(allowed) == 0 && m != nil {
		allowed = m.DefaultEfforts()
	}
	if p.Effort == "" || len(allowed) == 0 || slices.Contains(allowed, p.Effort) {
		return
	}
	to := Clamp(p.Effort, allowed)
	losses.Add("reasoning_effort_clamped", fmt.Sprintf("reasoning effort %q is not accepted by this model; %s requested", p.Effort, to), ir.LossApproximate)
	p.Effort = to
}

// Clamp maps an effort level onto allowed: the highest allowed level not
// above it, else the lowest allowed level. An unknown level is treated as
// medium.
func Clamp(effort string, allowed []string) string {
	r := Rank(effort)
	if r < 0 {
		r = Rank(EffortMedium)
	}
	best, lowest := "", ""
	for _, a := range allowed {
		ar := Rank(a)
		if ar < 0 {
			continue
		}
		if lowest == "" || ar < Rank(lowest) {
			lowest = a
		}
		if ar <= r && (best == "" || ar > Rank(best)) {
			best = a
		}
	}
	if best == "" {
		return lowest
	}
	return best
}

// sourceKeys are the client's raw reasoning fields a policy translated for
// a foreign upstream: they are not reported as dropped. output_config
// counts only when effort is all it carries.
func sourceKeys(ext map[string]json.RawMessage) []string {
	var out []string
	if _, ok := ext[AnthropicThinkingKey]; ok {
		out = append(out, AnthropicThinkingKey)
	}
	if raw, ok := ext[OutputConfigKey]; ok {
		var oc map[string]json.RawMessage
		if json.Unmarshal(raw, &oc) == nil && len(oc) == 1 && oc["effort"] != nil {
			out = append(out, OutputConfigKey)
		}
	}
	return out
}
