// Package anthropic adapts the Anthropic Messages wire protocol to and from
// the canonical IR. It is written from the public API documentation.
package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// P is this protocol's identifier (also the Ext key prefix).
const P = string(ir.AnthropicMessages)

// Limits bound parser work.
type Limits struct {
	MaxMessages int
	MaxParts    int
	MaxTools    int
}

// DefaultLimits are generous but bounded.
var DefaultLimits = Limits{MaxMessages: 10000, MaxParts: 100000, MaxTools: 512}

// ParseRequest converts an Anthropic Messages request body into IR.
func ParseRequest(body []byte, lim Limits) (*ir.Request, error) {
	o, err := wire.ParseObject(body)
	if err != nil {
		return nil, invalid("request body must be a JSON object: %v", err)
	}
	r := &ir.Request{Source: ir.AnthropicMessages}
	if _, err := o.Take("model", &r.Model); err != nil {
		return nil, invalid("%v", err)
	}
	var maxTokens int
	if ok, err := o.Take("max_tokens", &maxTokens); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.MaxOutputTokens = &maxTokens
	}
	if raw, ok := o.TakeRaw("system"); ok {
		parts, err := parseSystem(raw)
		if err != nil {
			return nil, err
		}
		r.System = parts
		for range parts {
			r.SystemRoles = append(r.SystemRoles, ir.RoleSystem)
		}
	}
	var msgs []json.RawMessage
	if _, err := o.Take("messages", &msgs); err != nil {
		return nil, invalid("%v", err)
	}
	if len(msgs) > lim.MaxMessages {
		return nil, invalid("too many messages (%d > %d)", len(msgs), lim.MaxMessages)
	}
	parts := 0
	for i, raw := range msgs {
		m, err := parseMessage(raw)
		if err != nil {
			return nil, invalid("messages[%d]: %v", i, err)
		}
		parts += len(m.Parts)
		if parts > lim.MaxParts {
			return nil, invalid("too many content blocks")
		}
		r.Messages = append(r.Messages, *m)
	}
	var tools []json.RawMessage
	if _, err := o.Take("tools", &tools); err != nil {
		return nil, invalid("%v", err)
	}
	if len(tools) > lim.MaxTools {
		return nil, invalid("too many tools (%d > %d)", len(tools), lim.MaxTools)
	}
	for i, raw := range tools {
		t, err := parseTool(raw)
		if err != nil {
			return nil, invalid("tools[%d]: %v", i, err)
		}
		r.Tools = append(r.Tools, *t)
	}
	if raw, ok := o.TakeRaw("tool_choice"); ok {
		var tc struct {
			Type                   string `json:"type"`
			Name                   string `json:"name"`
			DisableParallelToolUse bool   `json:"disable_parallel_tool_use"`
		}
		if err := json.Unmarshal(raw, &tc); err != nil {
			return nil, invalid("tool_choice: %v", err)
		}
		switch tc.Type {
		case "auto":
			r.ToolChoice = &ir.ToolChoice{Mode: "auto"}
		case "any":
			r.ToolChoice = &ir.ToolChoice{Mode: "required"}
		case "tool":
			r.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: tc.Name}
		case "none":
			r.ToolChoice = &ir.ToolChoice{Mode: "none"}
		default:
			return nil, invalid("tool_choice.type %q is not supported", tc.Type)
		}
		if tc.DisableParallelToolUse {
			f := false
			r.ParallelToolCalls = &f
		}
	}
	var temp, topP float64
	var topK int
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
	if ok, err := o.Take("top_k", &topK); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.TopK = &topK
	}
	if _, err := o.Take("stop_sequences", &r.Stop); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("stream", &r.Stream); err != nil {
		return nil, invalid("%v", err)
	}
	if raw, ok := o.TakeRaw("metadata"); ok {
		var md struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(raw, &md); err != nil {
			return nil, invalid("metadata: %v", err)
		}
		r.User = md.UserID
	}
	if raw, ok := o.TakeRaw("thinking"); ok {
		var th struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		}
		if err := json.Unmarshal(raw, &th); err != nil {
			return nil, invalid("thinking: %v", err)
		}
		switch th.Type {
		case "enabled":
			t := true
			r.Reasoning = &ir.ReasoningConfig{Enabled: &t, BudgetTokens: th.BudgetTokens, Effort: EffortForBudget(th.BudgetTokens)}
		case "disabled":
			f := false
			r.Reasoning = &ir.ReasoningConfig{Enabled: &f, Effort: "none"}
		case "adaptive":
			// The model decides how much to think. The raw object stays in
			// Ext so a native upstream gets it unchanged; it is never
			// rewritten as an enabled budget.
			t := true
			r.Reasoning = &ir.ReasoningConfig{Enabled: &t, Adaptive: true}
			r.Ext = map[string]json.RawMessage{P + ":thinking": raw}
		default:
			// Preserve unknown thinking modes for native use.
			r.Ext = map[string]json.RawMessage{P + ":thinking": raw}
		}
	}
	if _, err := o.Take("service_tier", &r.ServiceTier); err != nil {
		return nil, invalid("%v", err)
	}
	// output_config is read, not taken: its effort is known to the IR, and
	// the whole object (with any other keys) stays in Ext for native use.
	if raw, ok := o["output_config"]; ok {
		var oc struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(raw, &oc) == nil && oc.Effort != "" {
			if r.Reasoning == nil {
				r.Reasoning = &ir.ReasoningConfig{}
			}
			r.Reasoning.OutputEffort = oc.Effort
		}
	}
	r.Ext = o.Ext(P, r.Ext)
	return r, nil
}

// EffortForBudget maps a thinking budget to an effort level.
func EffortForBudget(b int) string {
	switch {
	case b <= 0:
		return ""
	case b < 4096:
		return "minimal"
	case b < 8192:
		return "low"
	case b < 20000:
		return "medium"
	case b < 32000:
		return "high"
	}
	return "xhigh"
}

// BudgetForEffort maps an effort level to a thinking budget.
func BudgetForEffort(e string) int {
	switch e {
	case "minimal":
		return 1024
	case "low":
		return 4096
	case "medium":
		return 10000
	case "high":
		return 24000
	case "xhigh":
		return 32000
	}
	return 0
}

func parseSystem(raw json.RawMessage) ([]ir.Part, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ir.Part{{Kind: ir.PartText, Text: s}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, invalid("system must be a string or an array of text blocks")
	}
	out := make([]ir.Part, 0, len(blocks))
	for i, b := range blocks {
		p, err := parseBlock(b)
		if err != nil {
			return nil, invalid("system[%d]: %v", i, err)
		}
		out = append(out, *p)
	}
	return out, nil
}

func parseMessage(raw json.RawMessage) (*ir.Message, error) {
	o, err := wire.ParseObject(raw)
	if err != nil {
		return nil, err
	}
	m := &ir.Message{}
	var role string
	if _, err := o.Take("role", &role); err != nil {
		return nil, err
	}
	switch role {
	case "user":
		m.Role = ir.RoleUser
	case "assistant":
		m.Role = ir.RoleAssistant
	case "system":
		// A mid-conversation system message (operator instruction appended
		// to the history). Its placement and content rules are model
		// dependent and enforced by the upstream; extra fields such as
		// clear_at and output_config stay in Ext for native use.
		m.Role = ir.RoleSystem
	default:
		return nil, fmt.Errorf("role must be user, assistant or system")
	}
	content, ok := o.TakeRaw("content")
	if !ok {
		return nil, fmt.Errorf("content is required")
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		m.Parts = []ir.Part{{Kind: ir.PartText, Text: s}}
	} else {
		var blocks []json.RawMessage
		if err := json.Unmarshal(content, &blocks); err != nil {
			return nil, fmt.Errorf("content must be a string or an array of blocks")
		}
		for i, b := range blocks {
			p, err := parseBlock(b)
			if err != nil {
				return nil, fmt.Errorf("content[%d]: %v", i, err)
			}
			m.Parts = append(m.Parts, *p)
		}
	}
	m.Ext = o.Ext(P, nil)
	return m, nil
}

type source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
	FileID    string `json:"file_id"`
}

func parseSource(raw json.RawMessage) (*ir.Media, error) {
	var s source
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	m := &ir.Media{MimeType: s.MediaType}
	switch s.Type {
	case "base64":
		b, err := base64.StdEncoding.DecodeString(s.Data)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 data")
		}
		m.Data = b
	case "url":
		m.URL = s.URL
	case "file":
		m.FileID = s.FileID
	case "text":
		m.Data = []byte(s.Data)
		if m.MimeType == "" {
			m.MimeType = "text/plain"
		}
	default:
		return nil, fmt.Errorf("unsupported source type %q", s.Type)
	}
	return m, nil
}

func parseBlock(raw json.RawMessage) (*ir.Part, error) {
	o, err := wire.ParseObject(raw)
	if err != nil {
		return nil, err
	}
	var typ string
	if _, err := o.Take("type", &typ); err != nil {
		return nil, err
	}
	p := &ir.Part{}
	var cc ir.CacheControl
	if ok, err := o.Take("cache_control", &cc); err != nil {
		return nil, err
	} else if ok {
		p.CacheControl = &cc
	}
	switch typ {
	case "text":
		p.Kind = ir.PartText
		if _, err := o.Take("text", &p.Text); err != nil {
			return nil, err
		}
	case "image", "document":
		p.Kind = ir.PartImage
		if typ == "document" {
			p.Kind = ir.PartDocument
		}
		src, ok := o.TakeRaw("source")
		if !ok {
			return nil, fmt.Errorf("%s.source is required", typ)
		}
		if typ == "document" {
			// Content-block documents and other rich sources stay native.
			var st struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(src, &st)
			if st.Type == "content" {
				o["source"] = src
				return nativePart(typ, raw), nil
			}
		}
		media, err := parseSource(src)
		if err != nil {
			return nil, err
		}
		if typ == "document" {
			_, _ = o.Take("title", &media.Title)
		}
		p.Media = media
	case "tool_use":
		p.Kind = ir.PartToolCall
		tc := &ir.ToolCall{Kind: "function"}
		if _, err := o.Take("id", &tc.ID); err != nil {
			return nil, err
		}
		if _, err := o.Take("name", &tc.Name); err != nil {
			return nil, err
		}
		in, _ := o.TakeRaw("input")
		tc.Arguments = string(wire.RawOrEmptyObject(in))
		p.ToolCall = tc
	case "tool_result":
		p.Kind = ir.PartToolResult
		tr := &ir.ToolResult{}
		if _, err := o.Take("tool_use_id", &tr.CallID); err != nil {
			return nil, err
		}
		if _, err := o.Take("is_error", &tr.IsError); err != nil {
			return nil, err
		}
		if c, ok := o.TakeRaw("content"); ok {
			var s string
			if err := json.Unmarshal(c, &s); err == nil {
				tr.Content = []ir.Part{{Kind: ir.PartText, Text: s}}
			} else {
				var blocks []json.RawMessage
				if err := json.Unmarshal(c, &blocks); err != nil {
					return nil, fmt.Errorf("tool_result.content must be a string or blocks")
				}
				for _, b := range blocks {
					sub, err := parseBlock(b)
					if err != nil {
						return nil, err
					}
					tr.Content = append(tr.Content, *sub)
				}
			}
		}
		p.ToolResult = tr
	case "thinking":
		p.Kind = ir.PartReasoning
		var text, sig string
		if _, err := o.Take("thinking", &text); err != nil {
			return nil, err
		}
		if _, err := o.Take("signature", &sig); err != nil {
			return nil, err
		}
		p.Reasoning = &ir.Reasoning{Text: text}
		if sig != "" {
			p.Sealed = &ir.Sealed{Family: "anthropic", Kind: "thinking_signature", Data: sig, Attached: text}
		}
	case "redacted_thinking":
		p.Kind = ir.PartSealed
		var data string
		if _, err := o.Take("data", &data); err != nil {
			return nil, err
		}
		p.Sealed = &ir.Sealed{Family: "anthropic", Kind: "redacted_thinking", Data: data}
	default:
		// Provider-executed tool blocks and future block types are preserved
		// verbatim and can only be sent back to Anthropic.
		return nativePart(typ, raw), nil
	}
	if len(o) > 0 {
		p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
	}
	return p, nil
}

func nativePart(typ string, raw json.RawMessage) *ir.Part {
	return &ir.Part{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: "anthropic", Kind: "native_block:" + typ, Data: string(raw)}}
}

func parseTool(raw json.RawMessage) (*ir.Tool, error) {
	o, err := wire.ParseObject(raw)
	if err != nil {
		return nil, err
	}
	t := &ir.Tool{Kind: "function"}
	var typ string
	if _, err := o.Take("type", &typ); err != nil {
		return nil, err
	}
	if _, err := o.Take("name", &t.Name); err != nil {
		return nil, err
	}
	if typ != "" && typ != "custom" {
		// Provider-defined tools (web search, code execution, …) are native.
		t.Kind = "builtin"
		t.BuiltinType = typ
		o["name"] = wire.MustMarshal(t.Name)
		o["type"] = wire.MustMarshal(typ)
		t.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
		return t, nil
	}
	if _, err := o.Take("description", &t.Description); err != nil {
		return nil, err
	}
	if s, ok := o.TakeRaw("input_schema"); ok {
		t.Parameters = s
	}
	var cc ir.CacheControl
	if ok, err := o.Take("cache_control", &cc); err != nil {
		return nil, err
	} else if ok {
		t.CacheControl = &cc
	}
	if len(o) > 0 {
		t.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
	}
	if strings.TrimSpace(t.Name) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	return t, nil
}

func invalid(format string, args ...any) error {
	return &ir.Error{Kind: ir.ErrInvalidRequest, Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}
