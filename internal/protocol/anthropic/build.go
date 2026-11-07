package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// BuildOptions tune request building.
type BuildOptions struct {
	Model string
	// DefaultMaxTokens applies when the IR request has no output limit
	// (Anthropic requires max_tokens).
	DefaultMaxTokens int
	Stream           bool
	// InlineSystemMessages renders mid-conversation system messages as user
	// text even when the client sent them natively, for consumers that only
	// understand user and assistant turns (the CLI bridge).
	InlineSystemMessages bool
}

// BuildRequest renders an IR request as an Anthropic Messages body. Features
// that cannot be represented are reported as losses; the caller decides,
// per policy, whether to send anyway or reject.
func BuildRequest(r *ir.Request, o BuildOptions) ([]byte, []ir.Loss, error) {
	var losses ir.Losses
	body := map[string]any{"model": o.Model, "stream": o.Stream}
	maxTok := o.DefaultMaxTokens
	if r.MaxOutputTokens != nil {
		maxTok = *r.MaxOutputTokens
	} else {
		if maxTok <= 0 {
			maxTok = 8192
		}
		losses.Add("max_tokens_defaulted", "max_tokens was not set by the client; applied model default", ir.LossApproximate)
	}
	body["max_tokens"] = maxTok

	var system []any
	for i, p := range r.System {
		if p.Kind != ir.PartText {
			losses.Add("system_non_text", "non-text system content dropped", ir.LossDrop)
			continue
		}
		b := map[string]any{"type": "text", "text": p.Text}
		if p.CacheControl != nil {
			b["cache_control"] = p.CacheControl
		}
		restorePart(b, p)
		_ = i
		system = append(system, b)
	}
	if len(system) > 0 {
		body["system"] = system
	}

	type msg struct {
		role   string
		blocks []any
		native map[string]any // a mid-conversation system message, sent as is
	}
	var msgs []msg
	push := func(role string, blocks []any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(msgs); n > 0 && msgs[n-1].role == role && msgs[n-1].native == nil {
			msgs[n-1].blocks = append(msgs[n-1].blocks, blocks...)
			return
		}
		msgs = append(msgs, msg{role: role, blocks: blocks})
	}
	nativeSystem := r.Source == ir.AnthropicMessages && !o.InlineSystemMessages
	for _, m := range r.Messages {
		role := "user"
		switch m.Role {
		case ir.RoleAssistant:
			role = "assistant"
		case ir.RoleSystem, ir.RoleDeveloper:
			if nativeSystem && m.Role == ir.RoleSystem {
				// The client sent a mid-conversation system message on the
				// Messages API: pass it through unchanged. The upstream
				// enforces which models accept it and where it may appear.
				msgs = append(msgs, msg{role: "system", native: buildSystemMessage(m, &losses)})
				continue
			}
			// Instructions from other protocols become user text: whether the
			// target model accepts an in-conversation system role, and where,
			// depends on the model, so the portable form is used.
			ir.SystemMessageLosses(m, "", &losses)
			var blocks []any
			for _, p := range m.Parts {
				if p.Kind == ir.PartText {
					blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
				}
			}
			losses.Add("mid_conversation_system", "a system/developer message inside the conversation was sent as user text", ir.LossApproximate)
			push("user", blocks)
			continue
		}
		var blocks []any
		for _, p := range m.Parts {
			b, ok := buildBlock(p, &losses)
			if ok {
				blocks = append(blocks, b)
			}
		}
		push(role, blocks)
	}
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		if m.native != nil {
			out = append(out, m.native)
			continue
		}
		out = append(out, map[string]any{"role": m.role, "content": m.blocks})
	}
	body["messages"] = out

	if len(r.Tools) > 0 {
		var tools []any
		for _, t := range r.Tools {
			switch t.Kind {
			case "builtin":
				if raw, ok := t.Ext[P]; ok {
					tools = append(tools, json.RawMessage(raw))
				} else {
					losses.Add("builtin_tool:"+t.BuiltinType, "provider tool "+t.BuiltinType+" is not available on Anthropic", ir.LossDrop)
				}
				continue
			case "custom":
				losses.Add("custom_tool", "free-form (custom) tool "+t.Name+" is not supported by Anthropic", ir.LossReject)
				continue
			}
			tb := map[string]any{"name": t.Name, "input_schema": wire.RawOrEmptyObject(t.Parameters)}
			if t.Description != "" {
				tb["description"] = t.Description
			}
			if t.CacheControl != nil {
				tb["cache_control"] = t.CacheControl
			}
			if t.Strict != nil && *t.Strict {
				losses.Add("strict_tools", "strict tool schemas are not enforced by Anthropic", ir.LossApproximate)
			}
			if raw, ok := t.Ext[P]; ok {
				var extra map[string]any
				if json.Unmarshal(raw, &extra) == nil {
					for k, v := range extra {
						if _, set := tb[k]; !set {
							tb[k] = v
						}
					}
				}
			}
			tools = append(tools, tb)
		}
		if len(tools) > 0 {
			body["tools"] = tools
		}
	}
	if tc := r.ToolChoice; tc != nil {
		choice := map[string]any{}
		switch tc.Mode {
		case "auto":
			choice["type"] = "auto"
		case "required":
			choice["type"] = "any"
		case "tool":
			choice["type"], choice["name"] = "tool", tc.Name
		case "none":
			choice["type"] = "none"
		}
		if r.ParallelToolCalls != nil && !*r.ParallelToolCalls {
			choice["disable_parallel_tool_use"] = true
		}
		body["tool_choice"] = choice
	} else if r.ParallelToolCalls != nil && !*r.ParallelToolCalls && len(r.Tools) > 0 {
		body["tool_choice"] = map[string]any{"type": "auto", "disable_parallel_tool_use": true}
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if r.TopK != nil {
		body["top_k"] = *r.TopK
	}
	if len(r.Stop) > 0 {
		body["stop_sequences"] = r.Stop
	}
	if r.User != "" {
		body["metadata"] = map[string]any{"user_id": r.User}
	}
	if r.ServiceTier != "" {
		body["service_tier"] = r.ServiceTier
	}
	if rc := r.Reasoning; rc != nil {
		switch {
		case rc.Enabled != nil && !*rc.Enabled, rc.Effort == "none":
			// Default for Anthropic is no extended thinking; send nothing.
		case rc.BudgetTokens > 0 || rc.Effort != "":
			budget := rc.BudgetTokens
			if budget == 0 {
				budget = BudgetForEffort(rc.Effort)
			}
			if budget < 1024 {
				budget = 1024
			}
			if budget >= maxTok {
				// Thinking budget must be below max_tokens; keep the client's
				// output limit and shrink the budget.
				nb := maxTok - 1
				if nb < 1024 {
					losses.Add("reasoning", "max_tokens is too small for extended thinking; thinking disabled", ir.LossDrop)
					break
				}
				losses.Add("reasoning_budget", "thinking budget reduced to fit max_tokens", ir.LossApproximate)
				budget = nb
			}
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		}
		if rc.Summary != "" && rc.Summary != "none" && rc.Summary != "auto" {
			losses.Add("reasoning_summary", "reasoning summary style is not configurable on Anthropic", ir.LossApproximate)
		}
	}
	if rf := r.ResponseFormat; rf != nil && rf.Type != "text" {
		losses.Add("structured_output", "response_format "+rf.Type+" is not translated to Anthropic", ir.LossReject)
	}
	if r.Seed != nil {
		losses.Add("seed", "seed is not supported by Anthropic", ir.LossDrop)
	}
	if r.PreviousResponseID != "" {
		losses.Add("previous_response_id", "server-side conversation state cannot be carried to Anthropic", ir.LossReject)
	}
	wire.Restore(body, r.Ext, P)
	for _, k := range wire.ForeignExtKeys(r.Ext, P) {
		losses.Add("field:"+k, "client field has no Anthropic equivalent", ir.LossDrop)
	}
	b, err := wire.Marshal(body)
	return b, losses.List(), err
}

// buildSystemMessage renders a mid-conversation system message in its
// native form. A single plain text part is sent as a string, the documented
// shape; anything else as blocks. Message-level fields (clear_at,
// output_config, …) are restored from Ext. An empty content array is kept:
// it is how a per-message output_config change is expressed.
func buildSystemMessage(m ir.Message, losses *ir.Losses) map[string]any {
	out := map[string]any{"role": "system"}
	if len(m.Parts) == 1 && m.Parts[0].Kind == ir.PartText && m.Parts[0].CacheControl == nil && len(m.Parts[0].Ext) == 0 {
		out["content"] = m.Parts[0].Text
	} else {
		blocks := []any{}
		for _, p := range m.Parts {
			if b, ok := buildBlock(p, losses); ok {
				blocks = append(blocks, b)
			}
		}
		out["content"] = blocks
	}
	wire.Restore(out, m.Ext, P)
	return out
}

func restorePart(b map[string]any, p ir.Part) {
	if raw, ok := p.Ext[P]; ok {
		var extra map[string]any
		if json.Unmarshal(raw, &extra) == nil {
			for k, v := range extra {
				if _, set := b[k]; !set {
					b[k] = v
				}
			}
		}
	}
}

func mediaSource(m *ir.Media) map[string]any {
	switch {
	case len(m.Data) > 0 && m.MimeType == "text/plain":
		return map[string]any{"type": "text", "media_type": "text/plain", "data": string(m.Data)}
	case len(m.Data) > 0:
		return map[string]any{"type": "base64", "media_type": m.MimeType, "data": base64.StdEncoding.EncodeToString(m.Data)}
	case m.FileID != "":
		return map[string]any{"type": "file", "file_id": m.FileID}
	default:
		return map[string]any{"type": "url", "url": m.URL}
	}
}

func buildBlock(p ir.Part, losses *ir.Losses) (map[string]any, bool) {
	var b map[string]any
	switch p.Kind {
	case ir.PartText:
		b = map[string]any{"type": "text", "text": p.Text}
	case ir.PartRefusal:
		b = map[string]any{"type": "text", "text": p.Text}
	case ir.PartImage:
		if p.Media == nil {
			return nil, false
		}
		b = map[string]any{"type": "image", "source": mediaSource(p.Media)}
	case ir.PartDocument:
		if p.Media == nil {
			return nil, false
		}
		b = map[string]any{"type": "document", "source": mediaSource(p.Media)}
		if p.Media.Title != "" {
			b["title"] = p.Media.Title
		}
	case ir.PartToolCall:
		tc := p.ToolCall
		if tc.Kind == "custom" {
			losses.Add("custom_tool_call", "free-form tool call history cannot be represented on Anthropic", ir.LossReject)
			return nil, false
		}
		b = map[string]any{"type": "tool_use", "id": sanitizeToolID(tc.ID), "name": tc.Name, "input": wire.ArgsObject(tc.Arguments)}
	case ir.PartToolResult:
		tr := p.ToolResult
		var content []any
		for _, c := range tr.Content {
			switch c.Kind {
			case ir.PartText:
				content = append(content, map[string]any{"type": "text", "text": c.Text})
			case ir.PartImage:
				if c.Media != nil {
					content = append(content, map[string]any{"type": "image", "source": mediaSource(c.Media)})
				}
			default:
				losses.Add("tool_result_content", "unsupported tool result content dropped", ir.LossDrop)
			}
		}
		b = map[string]any{"type": "tool_result", "tool_use_id": sanitizeToolID(tr.CallID)}
		if len(content) > 0 {
			b["content"] = content
		}
		if tr.IsError {
			b["is_error"] = true
		}
	case ir.PartReasoning:
		if p.Sealed != nil && p.Sealed.Family == "anthropic" && p.Sealed.Kind == "thinking_signature" {
			text := p.Sealed.Attached
			if text == "" && p.Reasoning != nil {
				text = p.Reasoning.Text
			}
			return map[string]any{"type": "thinking", "thinking": text, "signature": p.Sealed.Data}, true
		}
		losses.Add("reasoning_history", "reasoning from another provider cannot be replayed to Anthropic", ir.LossDrop)
		return nil, false
	case ir.PartSealed:
		s := p.Sealed
		if s == nil || s.Family != "anthropic" {
			fam := ""
			if s != nil {
				fam = s.Family
			}
			losses.Add("sealed_state:"+fam, "opaque "+fam+" state cannot be sent to Anthropic", ir.LossDrop)
			return nil, false
		}
		if s.Kind == "redacted_thinking" {
			return map[string]any{"type": "redacted_thinking", "data": s.Data}, true
		}
		if strings.HasPrefix(s.Kind, "native_block:") {
			var raw map[string]any
			if json.Unmarshal([]byte(s.Data), &raw) == nil {
				return raw, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
	if p.CacheControl != nil {
		b["cache_control"] = p.CacheControl
	}
	restorePart(b, p)
	return b, true
}

// sanitizeToolID keeps ids within Anthropic's allowed alphabet
// ([a-zA-Z0-9_-]); other characters are replaced deterministically so the
// call and its result still match.
func sanitizeToolID(id string) string {
	ok := true
	for _, c := range id {
		if !isIDChar(c) {
			ok = false
			break
		}
	}
	if ok && id != "" {
		return id
	}
	var b strings.Builder
	for _, c := range id {
		if isIDChar(c) {
			b.WriteRune(c)
		} else {
			b.WriteString("_")
		}
	}
	if b.Len() == 0 {
		return "toolu_empty"
	}
	return b.String()
}

func isIDChar(c rune) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
