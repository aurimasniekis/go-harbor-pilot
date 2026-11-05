package ir

import "sort"

// LossSeverity classifies a translation loss.
type LossSeverity string

// Loss severities.
const (
	// LossApproximate: the feature was mapped with slightly different
	// semantics (e.g. defaulted max_tokens).
	LossApproximate LossSeverity = "approximate"
	// LossDrop: optional information was removed (e.g. foreign reasoning
	// history, unknown fields).
	LossDrop LossSeverity = "drop"
	// LossReject: the request depends on the feature; sending it without
	// would change behaviour materially. Policy must explicitly allow it.
	LossReject LossSeverity = "reject"
)

// Loss records a feature that could not be carried to the target protocol.
type Loss struct {
	Feature  string       `json:"feature"`
	Detail   string       `json:"detail"`
	Severity LossSeverity `json:"severity"`
}

// Losses accumulates de-duplicated losses.
type Losses struct {
	byFeature map[string]Loss
}

// Add records a loss (the most severe wins per feature).
func (l *Losses) Add(feature, detail string, sev LossSeverity) {
	if l.byFeature == nil {
		l.byFeature = map[string]Loss{}
	}
	if cur, ok := l.byFeature[feature]; ok && rank(cur.Severity) >= rank(sev) {
		return
	}
	l.byFeature[feature] = Loss{Feature: feature, Detail: detail, Severity: sev}
}

// List returns losses sorted by feature.
func (l *Losses) List() []Loss {
	out := make([]Loss, 0, len(l.byFeature))
	for _, v := range l.byFeature {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Feature < out[j].Feature })
	return out
}

func rank(s LossSeverity) int {
	switch s {
	case LossReject:
		return 3
	case LossDrop:
		return 2
	case LossApproximate:
		return 1
	}
	return 0
}

// HasReject reports whether any loss requires explicit policy approval.
func HasReject(ls []Loss) bool {
	for _, l := range ls {
		if l.Severity == LossReject {
			return true
		}
	}
	return false
}

// Requirements derives capabilities a request demonstrably needs. Only
// observable facts count; nothing is guessed from prompt content.
func Requirements(r *Request) []string {
	set := map[string]bool{}
	if r.Stream {
		set["streaming"] = true
	}
	if len(r.Tools) > 0 {
		set["tools"] = true
	}
	if r.ResponseFormat != nil && r.ResponseFormat.Type == "json_schema" {
		set["structured_output"] = true
	}
	if r.Reasoning != nil && (r.Reasoning.BudgetTokens > 0 || (r.Reasoning.Effort != "" && r.Reasoning.Effort != "none")) &&
		(r.Reasoning.Enabled == nil || *r.Reasoning.Enabled) {
		set["reasoning"] = true
	}
	visit := func(p Part) {
		switch p.Kind {
		case PartImage:
			set["vision"] = true
		case PartDocument:
			if p.Media != nil && (p.Media.MimeType == "application/pdf" || p.Media.MimeType == "") {
				set["pdf"] = true
			}
		}
	}
	for _, p := range r.System {
		visit(p)
	}
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			visit(p)
			if p.ToolResult != nil {
				for _, c := range p.ToolResult.Content {
					visit(c)
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SealedFamilies lists opaque-state families present in a request history.
func SealedFamilies(r *Request) []string {
	set := map[string]bool{}
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			if p.Sealed != nil {
				set[p.Sealed.Family] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ToolObservation summarizes tool activity visible in a request.
type ToolObservation struct {
	Offered        []string // tool names offered
	CallsInHistory int      // assistant tool calls present in history
	ResultsSeen    int      // tool results present
	ResultErrors   int      // tool results flagged as errors
}

// ObserveTools inspects a request's tool definitions and history.
func ObserveTools(r *Request) ToolObservation {
	var o ToolObservation
	for _, t := range r.Tools {
		o.Offered = append(o.Offered, t.Name)
	}
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			switch p.Kind {
			case PartToolCall:
				o.CallsInHistory++
			case PartToolResult:
				o.ResultsSeen++
				if p.ToolResult != nil && p.ToolResult.IsError {
					o.ResultErrors++
				}
			}
		}
	}
	return o
}

// HasRejects reports whether any loss blocks sending without approval.
func HasRejects(ls []Loss) bool {
	for _, l := range ls {
		if l.Severity == LossReject {
			return true
		}
	}
	return false
}
