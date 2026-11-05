// Package openaichat adapts the OpenAI Chat Completions wire protocol (and
// compatible servers) to and from the canonical IR. It is written from the
// public API documentation.
package openaichat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// P is this protocol's identifier (also the Ext key prefix).
const P = string(ir.OpenAIChat)

// Limits bound parser work.
type Limits struct {
	MaxMessages int
	MaxParts    int
	MaxTools    int
}

// DefaultLimits are generous but bounded.
var DefaultLimits = Limits{MaxMessages: 10000, MaxParts: 100000, MaxTools: 512}

func invalid(format string, args ...any) error {
	return &ir.Error{Kind: ir.ErrInvalidRequest, Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

// ParseRequest converts a Chat Completions request body into IR.
func ParseRequest(body []byte, lim Limits) (*ir.Request, error) {
	o, err := wire.ParseObject(body)
	if err != nil {
		return nil, invalid("request body must be a JSON object: %v", err)
	}
	r := &ir.Request{Source: ir.OpenAIChat}
	if _, err := o.Take("model", &r.Model); err != nil {
		return nil, invalid("%v", err)
	}
	var msgs []json.RawMessage
	if _, err := o.Take("messages", &msgs); err != nil {
		return nil, invalid("%v", err)
	}
	if len(msgs) > lim.MaxMessages {
		return nil, invalid("too many messages")
	}
	parts := 0
	for i, raw := range msgs {
		m, sys, err := parseMessage(raw)
		if err != nil {
			return nil, invalid("messages[%d]: %v", i, err)
		}
		parts += len(m.Parts)
		if parts > lim.MaxParts {
			return nil, invalid("too many content parts")
		}
		// Leading system/developer messages become instructions; later ones
		// stay in the conversation in order.
		if sys && len(r.Messages) == 0 {
			r.System = append(r.System, m.Parts...)
			for range m.Parts {
				r.SystemRoles = append(r.SystemRoles, m.Role)
			}
			continue
		}
		r.Messages = append(r.Messages, *m)
	}
	var tools []json.RawMessage
	if _, err := o.Take("tools", &tools); err != nil {
		return nil, invalid("%v", err)
	}
	if len(tools) > lim.MaxTools {
		return nil, invalid("too many tools")
	}
	for i, raw := range tools {
		t, err := parseTool(raw)
		if err != nil {
			return nil, invalid("tools[%d]: %v", i, err)
		}
		r.Tools = append(r.Tools, *t)
	}
	if raw, ok := o.TakeRaw("tool_choice"); ok {
		tc, err := parseToolChoice(raw)
		if err != nil {
			return nil, invalid("tool_choice: %v", err)
		}
		r.ToolChoice = tc
	}
	var ptc bool
	if ok, err := o.Take("parallel_tool_calls", &ptc); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.ParallelToolCalls = &ptc
	}
	var maxTok int
	if ok, err := o.Take("max_completion_tokens", &maxTok); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.MaxOutputTokens = &maxTok
	}
	var legacyMax int
	if ok, err := o.Take("max_tokens", &legacyMax); err != nil {
		return nil, invalid("%v", err)
	} else if ok && r.MaxOutputTokens == nil {
		r.MaxOutputTokens = &legacyMax
	}
	var temp, topP float64
	if ok, err := o.Take("temperature", &temp); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.Temperature = &temp
	}
	if ok, err := o.Take("top_p", &topP); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.TopP = &topP
	}
	if raw, ok := o.TakeRaw("stop"); ok {
		var one string
		if json.Unmarshal(raw, &one) == nil {
			r.Stop = []string{one}
		} else if err := json.Unmarshal(raw, &r.Stop); err != nil {
			return nil, invalid("stop: must be a string or array of strings")
		}
	}
	var seed int64
	if ok, err := o.Take("seed", &seed); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.Seed = &seed
	}
	if _, err := o.Take("stream", &r.Stream); err != nil {
		return nil, invalid("%v", err)
	}
	// stream_options only controls usage reporting, which the gateway always
	// requests upstream and returns when the client asked.
	if raw, ok := o.TakeRaw("stream_options"); ok {
		r.Ext = map[string]json.RawMessage{P + ":stream_options": raw}
	}
	if raw, ok := o.TakeRaw("response_format"); ok {
		var rf struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
				Strict *bool           `json:"strict"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(raw, &rf); err != nil {
			return nil, invalid("response_format: %v", err)
		}
		r.ResponseFormat = &ir.ResponseFormat{Type: rf.Type, Name: rf.JSONSchema.Name, Schema: rf.JSONSchema.Schema, Strict: rf.JSONSchema.Strict}
	}
	var effort string
	if ok, err := o.Take("reasoning_effort", &effort); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.Reasoning = &ir.ReasoningConfig{Effort: effort}
	}
	if _, err := o.Take("user", &r.User); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("metadata", &r.Metadata); err != nil {
		return nil, invalid("%v", err)
	}
	var store bool
	if ok, err := o.Take("store", &store); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.Store = &store
	}
	if _, err := o.Take("service_tier", &r.ServiceTier); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("prompt_cache_key", &r.CacheKey); err != nil {
		return nil, invalid("%v", err)
	}
	var n int
	if ok, _ := o.Take("n", &n); ok && n > 1 {
		return nil, &ir.Error{Kind: ir.ErrUnsupported, Message: "n > 1 is not supported by the gateway", HTTPStatus: 400}
	}
	r.Ext = o.Ext(P, r.Ext)
	return r, nil
}

func parseMessage(raw json.RawMessage) (*ir.Message, bool, error) {
	o, err := wire.ParseObject(raw)
	if err != nil {
		return nil, false, err
	}
	var role string
	if _, err := o.Take("role", &role); err != nil {
		return nil, false, err
	}
	m := &ir.Message{}
	sys := false
	switch role {
	case "system":
		m.Role, sys = ir.RoleSystem, true
	case "developer":
		m.Role, sys = ir.RoleDeveloper, true
	case "user":
		m.Role = ir.RoleUser
	case "assistant":
		m.Role = ir.RoleAssistant
	case "tool", "function":
		m.Role = ir.RoleTool
	default:
		return nil, false, fmt.Errorf("unknown role %q", role)
	}
	if _, err := o.Take("name", &m.Name); err != nil {
		return nil, false, err
	}
	if m.Role == ir.RoleTool {
		var callID string
		if _, err := o.Take("tool_call_id", &callID); err != nil {
			return nil, false, err
		}
		content, err := parseContent(o)
		if err != nil {
			return nil, false, err
		}
		m.Role = ir.RoleUser
		m.Parts = []ir.Part{{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: callID, Name: m.Name, Content: content}}}
		m.Name = ""
		m.Ext = o.Ext(P, nil)
		return m, false, nil
	}
	if m.Role == ir.RoleAssistant {
		var reasoning string
		if ok, _ := o.Take("reasoning_content", &reasoning); ok && reasoning != "" {
			m.Parts = append(m.Parts, ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Text: reasoning}})
		}
	}
	content, err := parseContent(o)
	if err != nil {
		return nil, false, err
	}
	m.Parts = append(m.Parts, content...)
	if m.Role == ir.RoleAssistant {
		var refusal string
		if ok, _ := o.Take("refusal", &refusal); ok && refusal != "" {
			m.Parts = append(m.Parts, ir.Part{Kind: ir.PartRefusal, Text: refusal})
		}
		var calls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
			Custom struct {
				Name  string `json:"name"`
				Input string `json:"input"`
			} `json:"custom"`
		}
		if _, err := o.Take("tool_calls", &calls); err != nil {
			return nil, false, err
		}
		for _, c := range calls {
			tc := &ir.ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments, Kind: "function"}
			if c.Type == "custom" {
				tc.Name, tc.Arguments, tc.Kind = c.Custom.Name, c.Custom.Input, "custom"
			}
			m.Parts = append(m.Parts, ir.Part{Kind: ir.PartToolCall, ToolCall: tc})
		}
	}
	m.Ext = o.Ext(P, nil)
	return m, sys, nil
}

func parseContent(o wire.Object) ([]ir.Part, error) {
	raw, ok := o.TakeRaw("content")
	if !ok {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []ir.Part{{Kind: ir.PartText, Text: s}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("content must be a string or an array of parts")
	}
	var out []ir.Part
	for i, it := range items {
		p, err := parsePart(it)
		if err != nil {
			return nil, fmt.Errorf("content[%d]: %v", i, err)
		}
		out = append(out, *p)
	}
	return out, nil
}

// ParseDataURL decodes data:<mime>;base64,<data>.
func ParseDataURL(u string) (mime string, data []byte, ok bool) {
	if !strings.HasPrefix(u, "data:") {
		return "", nil, false
	}
	meta, payload, found := strings.Cut(u[5:], ",")
	if !found || !strings.HasSuffix(meta, ";base64") {
		return "", nil, false
	}
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, false
	}
	return strings.TrimSuffix(meta, ";base64"), b, true
}

// DataURL encodes bytes as a data URL.
func DataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func parsePart(raw json.RawMessage) (*ir.Part, error) {
	o, err := wire.ParseObject(raw)
	if err != nil {
		return nil, err
	}
	var typ string
	if _, err := o.Take("type", &typ); err != nil {
		return nil, err
	}
	p := &ir.Part{}
	switch typ {
	case "text":
		p.Kind = ir.PartText
		if _, err := o.Take("text", &p.Text); err != nil {
			return nil, err
		}
	case "refusal":
		p.Kind = ir.PartRefusal
		if _, err := o.Take("refusal", &p.Text); err != nil {
			return nil, err
		}
	case "image_url":
		var iu struct {
			URL    string `json:"url"`
			Detail string `json:"detail"`
		}
		if _, err := o.Take("image_url", &iu); err != nil {
			return nil, err
		}
		p.Kind = ir.PartImage
		p.Media = &ir.Media{Detail: iu.Detail}
		if mime, data, ok := ParseDataURL(iu.URL); ok {
			p.Media.MimeType, p.Media.Data = mime, data
		} else {
			p.Media.URL = iu.URL
		}
	case "file":
		var f struct {
			FileID   string `json:"file_id"`
			FileData string `json:"file_data"`
			Filename string `json:"filename"`
		}
		if _, err := o.Take("file", &f); err != nil {
			return nil, err
		}
		p.Kind = ir.PartDocument
		p.Media = &ir.Media{FileID: f.FileID, Title: f.Filename}
		if mime, data, ok := ParseDataURL(f.FileData); ok {
			p.Media.MimeType, p.Media.Data = mime, data
		}
	default:
		// input_audio and future part types stay native to this family.
		o["type"] = wire.MustMarshal(typ)
		return &ir.Part{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: P, Kind: "native_part:" + typ, Data: string(wire.MustMarshal(o))}}, nil
	}
	if len(o) > 0 {
		p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
	}
	return p, nil
}

func parseTool(raw json.RawMessage) (*ir.Tool, error) {
	var w struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Strict      *bool           `json:"strict"`
		} `json:"function"`
		Custom struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Format      json.RawMessage `json:"format"`
		} `json:"custom"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	switch w.Type {
	case "function", "":
		if w.Function.Name == "" {
			return nil, fmt.Errorf("function.name is required")
		}
		return &ir.Tool{Kind: "function", Name: w.Function.Name, Description: w.Function.Description,
			Parameters: w.Function.Parameters, Strict: w.Function.Strict}, nil
	case "custom":
		t := &ir.Tool{Kind: "custom", Name: w.Custom.Name, Description: w.Custom.Description}
		if len(w.Custom.Format) > 0 {
			t.Ext = map[string]json.RawMessage{P: w.Custom.Format}
		}
		return t, nil
	}
	return &ir.Tool{Kind: "builtin", BuiltinType: w.Type, Ext: map[string]json.RawMessage{P: raw}}, nil
}

func parseToolChoice(raw json.RawMessage) (*ir.ToolChoice, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto", "none", "required":
			return &ir.ToolChoice{Mode: s}, nil
		}
		return nil, fmt.Errorf("unknown tool_choice %q", s)
	}
	var w struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Custom struct {
			Name string `json:"name"`
		} `json:"custom"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	name := w.Function.Name
	if w.Type == "custom" {
		name = w.Custom.Name
	}
	if name == "" {
		return nil, fmt.Errorf("tool_choice must name a tool")
	}
	return &ir.ToolChoice{Mode: "tool", Name: name}, nil
}

// ---------- build ----------

// BuildOptions tune request building.
type BuildOptions struct {
	Model  string
	Stream bool
	// LegacyMaxTokens sends max_tokens instead of max_completion_tokens
	// (for older OpenAI-compatible servers).
	LegacyMaxTokens bool
	// ReasoningField names the non-standard assistant reasoning field some
	// compatible servers accept in history (e.g. reasoning_content); empty
	// drops prior reasoning text.
	ReasoningField string
}

// BuildRequest renders IR as a Chat Completions body.
func BuildRequest(r *ir.Request, o BuildOptions) ([]byte, []ir.Loss, error) {
	var losses ir.Losses
	body := map[string]any{"model": o.Model, "stream": o.Stream}
	if o.Stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	var msgs []any
	for i, p := range r.System {
		role := "system"
		if i < len(r.SystemRoles) && r.SystemRoles[i] == ir.RoleDeveloper {
			role = "developer"
		}
		if p.Kind != ir.PartText {
			losses.Add("system_non_text", "non-text system content dropped", ir.LossDrop)
			continue
		}
		msgs = append(msgs, map[string]any{"role": role, "content": p.Text})
	}
	for _, m := range r.Messages {
		msgs = append(msgs, buildMessages(m, o, &losses)...)
	}
	body["messages"] = msgs
	if len(r.Tools) > 0 {
		var tools []any
		for _, t := range r.Tools {
			switch t.Kind {
			case "builtin":
				if raw, ok := t.Ext[P]; ok {
					tools = append(tools, json.RawMessage(raw))
				} else {
					losses.Add("builtin_tool:"+t.BuiltinType, "provider tool "+t.BuiltinType+" is not available on this endpoint", ir.LossDrop)
				}
				continue
			case "custom":
				c := map[string]any{"name": t.Name}
				if t.Description != "" {
					c["description"] = t.Description
				}
				if f, ok := t.Ext[P]; ok {
					c["format"] = f
				}
				tools = append(tools, map[string]any{"type": "custom", "custom": c})
				continue
			}
			fn := map[string]any{"name": t.Name, "parameters": wire.RawOrEmptyObject(t.Parameters)}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			if t.Strict != nil {
				fn["strict"] = *t.Strict
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
			if t.CacheControl != nil {
				losses.Add("cache_control", "explicit cache breakpoints are not supported; provider caching is automatic", ir.LossApproximate)
			}
		}
		if len(tools) > 0 {
			body["tools"] = tools
		}
	}
	if tc := r.ToolChoice; tc != nil {
		switch tc.Mode {
		case "tool":
			body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
		default:
			body["tool_choice"] = tc.Mode
		}
	}
	if r.ParallelToolCalls != nil && len(r.Tools) > 0 {
		body["parallel_tool_calls"] = *r.ParallelToolCalls
	}
	if r.MaxOutputTokens != nil {
		if o.LegacyMaxTokens {
			body["max_tokens"] = *r.MaxOutputTokens
		} else {
			body["max_completion_tokens"] = *r.MaxOutputTokens
		}
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if r.TopK != nil {
		losses.Add("top_k", "top_k is not supported by Chat Completions", ir.LossDrop)
	}
	if len(r.Stop) > 0 {
		body["stop"] = r.Stop
	}
	if r.Seed != nil {
		body["seed"] = *r.Seed
	}
	if rf := r.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_schema":
			js := map[string]any{"name": nonEmpty(rf.Name, "response"), "schema": wire.RawOrEmptyObject(rf.Schema)}
			if rf.Strict != nil {
				js["strict"] = *rf.Strict
			}
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": js}
		default:
			body["response_format"] = map[string]any{"type": rf.Type}
		}
	}
	if rc := r.Reasoning; rc != nil {
		switch {
		case rc.Effort != "":
			body["reasoning_effort"] = rc.Effort
		case rc.BudgetTokens > 0:
			body["reasoning_effort"] = effortFromBudget(rc.BudgetTokens)
			losses.Add("reasoning_budget", "thinking budget mapped to a reasoning effort level", ir.LossApproximate)
		case rc.Enabled != nil && !*rc.Enabled:
			// Chat Completions has no explicit "off"; omit.
		}
	}
	if r.User != "" {
		body["user"] = r.User
	}
	if len(r.Metadata) > 0 {
		body["metadata"] = r.Metadata
	}
	if r.Store != nil {
		body["store"] = *r.Store
	}
	if r.ServiceTier != "" {
		body["service_tier"] = r.ServiceTier
	}
	if r.CacheKey != "" {
		body["prompt_cache_key"] = r.CacheKey
	}
	if r.PreviousResponseID != "" {
		losses.Add("previous_response_id", "server-side conversation state cannot be carried to Chat Completions", ir.LossReject)
	}
	wire.Restore(body, r.Ext, P)
	delete(body, "stream_options")
	if o.Stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	for _, k := range wire.ForeignExtKeys(r.Ext, P) {
		losses.Add("field:"+k, "client field has no Chat Completions equivalent", ir.LossDrop)
	}
	b, err := wire.Marshal(body)
	return b, losses.List(), err
}

func nonEmpty(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func effortFromBudget(b int) string {
	switch {
	case b < 8192:
		return "low"
	case b < 24000:
		return "medium"
	}
	return "high"
}

func buildMessages(m ir.Message, o BuildOptions, losses *ir.Losses) []any {
	switch m.Role {
	case ir.RoleSystem, ir.RoleDeveloper:
		var text []string
		for _, p := range m.Parts {
			if p.Kind == ir.PartText {
				text = append(text, p.Text)
			}
		}
		return []any{map[string]any{"role": string(m.Role), "content": strings.Join(text, "\n\n")}}
	case ir.RoleAssistant:
		msg := map[string]any{"role": "assistant"}
		var content []string
		var calls []any
		var reasoning []string
		for _, p := range m.Parts {
			switch p.Kind {
			case ir.PartText:
				content = append(content, p.Text)
			case ir.PartRefusal:
				msg["refusal"] = p.Text
			case ir.PartToolCall:
				tc := p.ToolCall
				if tc.Kind == "custom" {
					calls = append(calls, map[string]any{"id": tc.ID, "type": "custom", "custom": map[string]any{"name": tc.Name, "input": tc.Arguments}})
				} else {
					calls = append(calls, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": argsString(tc.Arguments)}})
				}
			case ir.PartReasoning:
				if o.ReasoningField != "" && p.Reasoning != nil && p.Reasoning.Text != "" && (p.Sealed == nil) {
					reasoning = append(reasoning, p.Reasoning.Text)
				} else {
					losses.Add("reasoning_history", "prior reasoning is not replayed to Chat Completions", ir.LossDrop)
				}
			case ir.PartSealed:
				fam := ""
				if p.Sealed != nil {
					fam = p.Sealed.Family
				}
				if fam != P {
					losses.Add("sealed_state:"+fam, "opaque "+fam+" state cannot be sent to Chat Completions", ir.LossDrop)
				}
			default:
				losses.Add("assistant_media", "non-text assistant content dropped", ir.LossDrop)
			}
		}
		if len(content) > 0 {
			msg["content"] = strings.Join(content, "")
		} else {
			msg["content"] = nil
		}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		if len(reasoning) > 0 {
			msg[o.ReasoningField] = strings.Join(reasoning, "\n")
		}
		if m.Name != "" {
			msg["name"] = m.Name
		}
		return []any{msg}
	}
	// user (and tool results carried in user turns)
	var out []any
	var parts []any
	flush := func() {
		if len(parts) == 0 {
			return
		}
		msg := map[string]any{"role": "user", "content": parts}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok && t["type"] == "text" {
				msg["content"] = t["text"]
			}
		}
		if m.Name != "" {
			msg["name"] = m.Name
		}
		out = append(out, msg)
		parts = nil
	}
	for _, p := range m.Parts {
		switch p.Kind {
		case ir.PartToolResult:
			flush()
			tr := p.ToolResult
			var text []string
			for _, c := range tr.Content {
				switch c.Kind {
				case ir.PartText:
					text = append(text, c.Text)
				default:
					losses.Add("tool_result_media", "non-text tool result content dropped (Chat Completions tool messages are text)", ir.LossDrop)
				}
			}
			content := strings.Join(text, "\n")
			if tr.IsError && content == "" {
				content = "error"
			}
			out = append(out, map[string]any{"role": "tool", "tool_call_id": tr.CallID, "content": content})
		case ir.PartText:
			parts = append(parts, map[string]any{"type": "text", "text": p.Text})
		case ir.PartImage:
			if p.Media == nil {
				continue
			}
			url := p.Media.URL
			if len(p.Media.Data) > 0 {
				url = DataURL(p.Media.MimeType, p.Media.Data)
			}
			if url == "" {
				losses.Add("image_file_id", "file-referenced images are not supported here", ir.LossDrop)
				continue
			}
			iu := map[string]any{"url": url}
			if p.Media.Detail != "" {
				iu["detail"] = p.Media.Detail
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": iu})
		case ir.PartDocument:
			if p.Media == nil {
				continue
			}
			f := map[string]any{}
			if p.Media.FileID != "" {
				f["file_id"] = p.Media.FileID
			} else if len(p.Media.Data) > 0 {
				f["file_data"] = DataURL(nonEmpty(p.Media.MimeType, "application/pdf"), p.Media.Data)
				f["filename"] = nonEmpty(p.Media.Title, "document.pdf")
			} else {
				losses.Add("document_url", "URL-referenced documents are not supported by Chat Completions", ir.LossDrop)
				continue
			}
			parts = append(parts, map[string]any{"type": "file", "file": f})
		case ir.PartSealed:
			if p.Sealed != nil && p.Sealed.Family == P {
				var raw map[string]any
				if json.Unmarshal([]byte(p.Sealed.Data), &raw) == nil {
					parts = append(parts, raw)
				}
			} else {
				losses.Add("native_content", "provider-native content cannot be sent to Chat Completions", ir.LossDrop)
			}
		}
	}
	flush()
	return out
}

func argsString(a string) string {
	if strings.TrimSpace(a) == "" {
		return "{}"
	}
	return a
}
