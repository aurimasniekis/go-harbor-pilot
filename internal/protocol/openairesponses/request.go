// Package openairesponses adapts the OpenAI Responses wire protocol to and
// from the canonical IR. It is written from the public API documentation.
package openairesponses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/reasoning"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// P is this protocol's identifier (also the Ext key prefix and family).
const P = string(ir.OpenAIResponses)

// Limits bound parser work.
type Limits struct {
	MaxItems int
	MaxTools int
}

// DefaultLimits are generous but bounded.
var DefaultLimits = Limits{MaxItems: 20000, MaxTools: 512}

func invalid(format string, args ...any) error {
	return &ir.Error{Kind: ir.ErrInvalidRequest, Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

// ParseRequest converts a Responses API request body into IR.
func ParseRequest(body []byte, lim Limits) (*ir.Request, error) {
	o, err := wire.ParseObject(body)
	if err != nil {
		return nil, invalid("request body must be a JSON object: %v", err)
	}
	r := &ir.Request{Source: ir.OpenAIResponses}
	if _, err := o.Take("model", &r.Model); err != nil {
		return nil, invalid("%v", err)
	}
	var instr string
	if ok, err := o.Take("instructions", &instr); err != nil {
		return nil, invalid("%v", err)
	} else if ok && instr != "" {
		r.System = []ir.Part{{Kind: ir.PartText, Text: instr}}
		r.SystemRoles = []ir.Role{ir.RoleSystem}
	}
	if raw, ok := o.TakeRaw("input"); ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			r.Messages = []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: s}}}}
		} else {
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, invalid("input must be a string or an array of items")
			}
			if len(items) > lim.MaxItems {
				return nil, invalid("too many input items")
			}
			for i, it := range items {
				if err := appendItem(r, it); err != nil {
					return nil, invalid("input[%d]: %v", i, err)
				}
			}
		}
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
		var s string
		if json.Unmarshal(raw, &s) == nil {
			r.ToolChoice = &ir.ToolChoice{Mode: s}
		} else {
			var w struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &w); err != nil {
				return nil, invalid("tool_choice: %v", err)
			}
			if w.Type != "function" && w.Type != "custom" {
				r.Ext = setExt(r.Ext, "tool_choice", raw)
			} else {
				r.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: w.Name}
			}
		}
	}
	var ptc bool
	if ok, err := o.Take("parallel_tool_calls", &ptc); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.ParallelToolCalls = &ptc
	}
	var maxTok int
	if ok, err := o.Take("max_output_tokens", &maxTok); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.MaxOutputTokens = &maxTok
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
	if _, err := o.Take("stream", &r.Stream); err != nil {
		return nil, invalid("%v", err)
	}
	if raw, ok := o.TakeRaw("reasoning"); ok {
		var w struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, invalid("reasoning: %v", err)
		}
		r.Reasoning = &ir.ReasoningConfig{Effort: w.Effort, Summary: w.Summary}
	}
	var include []string
	if _, err := o.Take("include", &include); err != nil {
		return nil, invalid("%v", err)
	}
	for _, inc := range include {
		if inc == "reasoning.encrypted_content" {
			if r.Reasoning == nil {
				r.Reasoning = &ir.ReasoningConfig{}
			}
			r.Reasoning.IncludeEncrypted = true
		} else {
			r.Ext = setExt(r.Ext, "include:"+inc, json.RawMessage(`true`))
		}
	}
	if raw, ok := o.TakeRaw("text"); ok {
		var w struct {
			Format *struct {
				Type   string          `json:"type"`
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
				Strict *bool           `json:"strict"`
			} `json:"format"`
			Verbosity string `json:"verbosity"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, invalid("text: %v", err)
		}
		if w.Format != nil {
			r.ResponseFormat = &ir.ResponseFormat{Type: w.Format.Type, Name: w.Format.Name, Schema: w.Format.Schema, Strict: w.Format.Strict}
		}
		if w.Verbosity != "" {
			r.Ext = setExt(r.Ext, "text.verbosity", wire.MustMarshal(w.Verbosity))
		}
	}
	var store bool
	if ok, err := o.Take("store", &store); err != nil {
		return nil, invalid("%v", err)
	} else if ok {
		r.Store = &store
	}
	if _, err := o.Take("previous_response_id", &r.PreviousResponseID); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("metadata", &r.Metadata); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("user", &r.User); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("prompt_cache_key", &r.CacheKey); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := o.Take("service_tier", &r.ServiceTier); err != nil {
		return nil, invalid("%v", err)
	}
	r.Ext = o.Ext(P, r.Ext)
	return r, nil
}

func setExt(m map[string]json.RawMessage, k string, v json.RawMessage) map[string]json.RawMessage {
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	m[P+":"+k] = v
	return m
}

// appendItem adds one input item, merging consecutive same-role content.
func appendItem(r *ir.Request, raw json.RawMessage) error {
	o, err := wire.ParseObject(raw)
	if err != nil {
		return err
	}
	var typ string
	if _, err := o.Take("type", &typ); err != nil {
		return err
	}
	add := func(role ir.Role, p ir.Part) {
		if n := len(r.Messages); n > 0 && r.Messages[n-1].Role == role && role != ir.RoleSystem && role != ir.RoleDeveloper {
			r.Messages[n-1].Parts = append(r.Messages[n-1].Parts, p)
			return
		}
		r.Messages = append(r.Messages, ir.Message{Role: role, Parts: []ir.Part{p}})
	}
	switch typ {
	case "message", "":
		var role string
		if _, err := o.Take("role", &role); err != nil {
			return err
		}
		irRole := map[string]ir.Role{"user": ir.RoleUser, "assistant": ir.RoleAssistant, "system": ir.RoleSystem, "developer": ir.RoleDeveloper}[role]
		if irRole == "" {
			return fmt.Errorf("unknown role %q", role)
		}
		var id string
		_, _ = o.Take("id", &id)
		_, _ = o.TakeRaw("status")
		raw, ok := o.TakeRaw("content")
		if !ok {
			return fmt.Errorf("message content is required")
		}
		parts, err := parseContent(raw)
		if err != nil {
			return err
		}
		if (irRole == ir.RoleSystem || irRole == ir.RoleDeveloper) && len(r.Messages) == 0 {
			r.System = append(r.System, parts...)
			for range parts {
				r.SystemRoles = append(r.SystemRoles, irRole)
			}
			return nil
		}
		for _, p := range parts {
			if id != "" {
				p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(map[string]string{"item_id": id})}
			}
			add(irRole, p)
		}
	case "function_call", "custom_tool_call":
		var callID, name, args, id string
		_, _ = o.Take("call_id", &callID)
		_, _ = o.Take("name", &name)
		_, _ = o.Take("id", &id)
		_, _ = o.TakeRaw("status")
		kind := "function"
		if typ == "custom_tool_call" {
			kind = "custom"
			_, _ = o.Take("input", &args)
		} else {
			_, _ = o.Take("arguments", &args)
		}
		p := ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: callID, Name: name, Arguments: args, Kind: kind}}
		if id != "" {
			p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(map[string]string{"item_id": id})}
		}
		add(ir.RoleAssistant, p)
	case "function_call_output", "custom_tool_call_output":
		var callID string
		_, _ = o.Take("call_id", &callID)
		_, _ = o.Take("id", new(string))
		_, _ = o.TakeRaw("status")
		out, _ := o.TakeRaw("output")
		var content []ir.Part
		var s string
		if json.Unmarshal(out, &s) == nil {
			content = []ir.Part{{Kind: ir.PartText, Text: s}}
		} else if len(out) > 0 {
			parts, err := parseContent(out)
			if err != nil {
				return err
			}
			content = parts
		}
		add(ir.RoleUser, ir.Part{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: callID, Content: content}})
	case "reasoning":
		var id, enc string
		var summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_, _ = o.Take("id", &id)
		_, _ = o.Take("encrypted_content", &enc)
		_, _ = o.Take("summary", &summary)
		_, _ = o.TakeRaw("status")
		var text []string
		for _, s := range summary {
			text = append(text, s.Text)
		}
		p := ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Text: strings.Join(text, "\n\n"), Summary: true},
			Sealed: &ir.Sealed{Family: P, Kind: "reasoning_item", Data: enc, ItemID: id}}
		if len(o) > 0 {
			p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
		}
		add(ir.RoleAssistant, p)
	case "item_reference":
		var id string
		_, _ = o.Take("id", &id)
		add(ir.RoleUser, ir.Part{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: P, Kind: "item_reference", Data: id}})
	default:
		// Provider-executed tool calls/outputs (web search, file search,
		// shell, computer use, …) and future item types stay native.
		o["type"] = wire.MustMarshal(typ)
		role := ir.RoleAssistant
		if strings.HasSuffix(typ, "_output") {
			role = ir.RoleUser
		}
		add(role, ir.Part{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: P, Kind: "native_item:" + typ, Data: string(wire.MustMarshal(o))}})
	}
	return nil
}

func parseContent(raw json.RawMessage) ([]ir.Part, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []ir.Part{{Kind: ir.PartText, Text: s}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("content must be a string or an array")
	}
	var out []ir.Part
	for _, it := range items {
		o, err := wire.ParseObject(it)
		if err != nil {
			return nil, err
		}
		var typ string
		_, _ = o.Take("type", &typ)
		switch typ {
		case "input_text", "output_text", "text":
			p := ir.Part{Kind: ir.PartText}
			_, _ = o.Take("text", &p.Text)
			if len(o) > 0 {
				p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
			}
			out = append(out, p)
		case "refusal":
			p := ir.Part{Kind: ir.PartRefusal}
			_, _ = o.Take("refusal", &p.Text)
			out = append(out, p)
		case "input_image":
			var url, fileID, detail string
			_, _ = o.Take("image_url", &url)
			_, _ = o.Take("file_id", &fileID)
			_, _ = o.Take("detail", &detail)
			m := &ir.Media{FileID: fileID, Detail: detail}
			if mime, data, ok := openaichat.ParseDataURL(url); ok {
				m.MimeType, m.Data = mime, data
			} else {
				m.URL = url
			}
			out = append(out, ir.Part{Kind: ir.PartImage, Media: m})
		case "input_file":
			var fileID, fileData, filename, fileURL string
			_, _ = o.Take("file_id", &fileID)
			_, _ = o.Take("file_data", &fileData)
			_, _ = o.Take("filename", &filename)
			_, _ = o.Take("file_url", &fileURL)
			m := &ir.Media{FileID: fileID, Title: filename, URL: fileURL}
			if mime, data, ok := openaichat.ParseDataURL(fileData); ok {
				m.MimeType, m.Data = mime, data
			}
			out = append(out, ir.Part{Kind: ir.PartDocument, Media: m})
		default:
			o["type"] = wire.MustMarshal(typ)
			out = append(out, ir.Part{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: P, Kind: "native_content:" + typ, Data: string(wire.MustMarshal(o))}})
		}
	}
	return out, nil
}

func parseTool(raw json.RawMessage) (*ir.Tool, error) {
	var w struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      *bool           `json:"strict"`
		Format      json.RawMessage `json:"format"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	switch w.Type {
	case "function":
		if w.Name == "" {
			return nil, fmt.Errorf("name is required")
		}
		return &ir.Tool{Kind: "function", Name: w.Name, Description: w.Description, Parameters: w.Parameters, Strict: w.Strict}, nil
	case "custom":
		t := &ir.Tool{Kind: "custom", Name: w.Name, Description: w.Description}
		if len(w.Format) > 0 {
			t.Ext = map[string]json.RawMessage{openaichat.P: w.Format, P: w.Format}
		}
		return t, nil
	}
	return &ir.Tool{Kind: "builtin", BuiltinType: w.Type, Name: w.Name, Ext: map[string]json.RawMessage{P: raw}}, nil
}

// ---------- build ----------

// BuildOptions tune request building.
type BuildOptions struct {
	Model  string
	Stream bool
	// Stateless sets store=false and asks for encrypted reasoning so the
	// conversation can be carried statelessly (the gateway default when the
	// client did not choose).
	Stateless bool
}

// BuildRequest renders IR as a Responses API body.
func BuildRequest(r *ir.Request, o BuildOptions) ([]byte, []ir.Loss, error) {
	var losses ir.Losses
	body := map[string]any{"model": o.Model, "stream": o.Stream}
	var instr []string
	for _, p := range r.System {
		if p.Kind == ir.PartText {
			instr = append(instr, p.Text)
		} else {
			losses.Add("system_non_text", "non-text instructions dropped", ir.LossDrop)
		}
	}
	if len(instr) > 0 {
		body["instructions"] = strings.Join(instr, "\n\n")
	}
	var input []any
	for _, m := range r.Messages {
		input = append(input, buildItems(m, &losses)...)
	}
	if input == nil {
		input = []any{}
	}
	body["input"] = input
	if len(r.Tools) > 0 {
		var tools []any
		for _, t := range r.Tools {
			switch t.Kind {
			case "builtin":
				if raw, ok := t.Ext[P]; ok {
					tools = append(tools, json.RawMessage(raw))
				} else {
					losses.Add("builtin_tool:"+t.BuiltinType, "provider tool "+t.BuiltinType+" is not available on Responses", ir.LossDrop)
				}
				continue
			case "custom":
				c := map[string]any{"type": "custom", "name": t.Name}
				if t.Description != "" {
					c["description"] = t.Description
				}
				if f, ok := t.Ext[P]; ok {
					c["format"] = f
				} else if f, ok := t.Ext[openaichat.P]; ok {
					c["format"] = f
				}
				tools = append(tools, c)
				continue
			}
			fn := map[string]any{"type": "function", "name": t.Name, "parameters": wire.RawOrEmptyObject(t.Parameters)}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			if t.Strict != nil {
				fn["strict"] = *t.Strict
			} else {
				fn["strict"] = false
			}
			tools = append(tools, fn)
		}
		if len(tools) > 0 {
			body["tools"] = tools
		}
	}
	if tc := r.ToolChoice; tc != nil {
		if tc.Mode == "tool" {
			body["tool_choice"] = map[string]any{"type": "function", "name": tc.Name}
		} else {
			body["tool_choice"] = tc.Mode
		}
	}
	if r.ParallelToolCalls != nil && len(r.Tools) > 0 {
		body["parallel_tool_calls"] = *r.ParallelToolCalls
	}
	if r.MaxOutputTokens != nil {
		body["max_output_tokens"] = *r.MaxOutputTokens
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if r.TopK != nil {
		losses.Add("top_k", "top_k is not supported by Responses", ir.LossDrop)
	}
	if len(r.Stop) > 0 {
		losses.Add("stop_sequences", "stop sequences are not supported by Responses", ir.LossReject)
	}
	if r.Seed != nil {
		losses.Add("seed", "seed is not supported by Responses", ir.LossDrop)
	}
	include := []string{}
	rres := reasoning.Apply(r, ir.OpenAIResponses, &reasoning.Target{Body: body, Losses: &losses, Include: &include})
	store := r.Store
	if store == nil && o.Stateless {
		f := false
		store = &f
	}
	if store != nil {
		body["store"] = *store
		if !*store && !contains(include, "reasoning.encrypted_content") && rres.Active {
			include = append(include, "reasoning.encrypted_content")
		}
	}
	for k := range r.Ext {
		if strings.HasPrefix(k, P+":include:") {
			include = append(include, strings.TrimPrefix(k, P+":include:"))
		}
	}
	if len(include) > 0 {
		body["include"] = include
	}
	text := map[string]any{}
	if rf := r.ResponseFormat; rf != nil {
		f := map[string]any{"type": rf.Type}
		if rf.Type == "json_schema" {
			f["name"] = nonEmpty(rf.Name, "response")
			f["schema"] = wire.RawOrEmptyObject(rf.Schema)
			if rf.Strict != nil {
				f["strict"] = *rf.Strict
			}
		}
		text["format"] = f
	}
	if v, ok := r.Ext[P+":text.verbosity"]; ok {
		text["verbosity"] = v
	}
	if len(text) > 0 {
		body["text"] = text
	}
	if r.PreviousResponseID != "" {
		body["previous_response_id"] = r.PreviousResponseID
	}
	if len(r.Metadata) > 0 {
		body["metadata"] = r.Metadata
	}
	if r.User != "" {
		body["user"] = r.User
	}
	if r.CacheKey != "" {
		body["prompt_cache_key"] = r.CacheKey
	}
	if r.ServiceTier != "" {
		body["service_tier"] = r.ServiceTier
	}
	for k, v := range r.Ext {
		if k == P+":tool_choice" {
			body["tool_choice"] = v
		}
	}
	restore := map[string]json.RawMessage{}
	for k, v := range r.Ext {
		if strings.HasPrefix(k, P+":") && !strings.Contains(strings.TrimPrefix(k, P+":"), ".") && !strings.HasPrefix(k, P+":include:") && k != P+":tool_choice" {
			restore[k] = v
		}
	}
	wire.Restore(body, restore, P)
	for _, k := range wire.ForeignExtKeys(r.Ext, P) {
		losses.Add("field:"+k, "client field has no Responses equivalent", ir.LossDrop)
	}
	b, err := wire.Marshal(body)
	return b, losses.List(), err
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func nonEmpty(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func itemID(p ir.Part) string {
	if raw, ok := p.Ext[P]; ok {
		var w struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal(raw, &w)
		return w.ItemID
	}
	return ""
}

func contentPart(p ir.Part, role ir.Role, losses *ir.Losses) (map[string]any, bool) {
	switch p.Kind {
	case ir.PartText:
		t := "input_text"
		if role == ir.RoleAssistant {
			t = "output_text"
		}
		return map[string]any{"type": t, "text": p.Text}, true
	case ir.PartRefusal:
		return map[string]any{"type": "refusal", "refusal": p.Text}, true
	case ir.PartImage:
		if p.Media == nil {
			return nil, false
		}
		m := map[string]any{"type": "input_image", "detail": nonEmpty(p.Media.Detail, "auto")}
		switch {
		case len(p.Media.Data) > 0:
			m["image_url"] = openaichat.DataURL(p.Media.MimeType, p.Media.Data)
		case p.Media.FileID != "":
			m["file_id"] = p.Media.FileID
		default:
			m["image_url"] = p.Media.URL
		}
		return m, true
	case ir.PartDocument:
		if p.Media == nil {
			return nil, false
		}
		m := map[string]any{"type": "input_file"}
		switch {
		case p.Media.FileID != "":
			m["file_id"] = p.Media.FileID
		case len(p.Media.Data) > 0:
			m["file_data"] = openaichat.DataURL(nonEmpty(p.Media.MimeType, "application/pdf"), p.Media.Data)
			m["filename"] = nonEmpty(p.Media.Title, "document.pdf")
		case p.Media.URL != "":
			m["file_url"] = p.Media.URL
		}
		return m, true
	case ir.PartSealed:
		if p.Sealed != nil && p.Sealed.Family == P && strings.HasPrefix(p.Sealed.Kind, "native_content:") {
			var raw map[string]any
			if json.Unmarshal([]byte(p.Sealed.Data), &raw) == nil {
				return raw, true
			}
		}
	}
	losses.Add("content:"+string(p.Kind), "content part not representable in Responses", ir.LossDrop)
	return nil, false
}

func buildItems(m ir.Message, losses *ir.Losses) []any {
	var out []any
	var content []any
	role := string(m.Role)
	if m.Role == ir.RoleTool {
		role = "user"
	}
	if m.Role == ir.RoleSystem || m.Role == ir.RoleDeveloper {
		ir.SystemMessageLosses(m, P, losses)
	}
	flush := func() {
		if len(content) == 0 {
			return
		}
		out = append(out, map[string]any{"type": "message", "role": role, "content": content})
		content = nil
	}
	for _, p := range m.Parts {
		switch p.Kind {
		case ir.PartToolCall:
			flush()
			tc := p.ToolCall
			if tc.Kind == "custom" {
				it := map[string]any{"type": "custom_tool_call", "call_id": tc.ID, "name": tc.Name, "input": tc.Arguments}
				if id := itemID(p); id != "" {
					it["id"] = id
				}
				out = append(out, it)
				continue
			}
			it := map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": argsString(tc.Arguments)}
			if id := itemID(p); id != "" {
				it["id"] = id
			}
			out = append(out, it)
		case ir.PartToolResult:
			flush()
			tr := p.ToolResult
			var text []string
			var rich []any
			for _, c := range tr.Content {
				if c.Kind == ir.PartText {
					text = append(text, c.Text)
				} else if cp, ok := contentPart(c, ir.RoleUser, losses); ok {
					rich = append(rich, cp)
				}
			}
			var output any = strings.Join(text, "\n")
			if len(rich) > 0 {
				items := []any{}
				for _, t := range text {
					items = append(items, map[string]any{"type": "input_text", "text": t})
				}
				output = append(items, rich...)
			}
			out = append(out, map[string]any{"type": "function_call_output", "call_id": tr.CallID, "output": output})
		case ir.PartReasoning:
			flush()
			if p.Sealed != nil && p.Sealed.Family == P {
				it := map[string]any{"type": "reasoning", "summary": []any{}}
				if p.Sealed.ItemID != "" {
					it["id"] = p.Sealed.ItemID
				}
				if p.Sealed.Data != "" {
					it["encrypted_content"] = p.Sealed.Data
				}
				if p.Reasoning != nil && p.Reasoning.Text != "" {
					it["summary"] = []any{map[string]any{"type": "summary_text", "text": p.Reasoning.Text}}
				}
				out = append(out, it)
			} else {
				losses.Add("reasoning_history", "reasoning from another provider cannot be replayed to Responses", ir.LossDrop)
			}
		case ir.PartSealed:
			s := p.Sealed
			if s == nil || s.Family != P {
				fam := ""
				if s != nil {
					fam = s.Family
				}
				losses.Add("sealed_state:"+fam, "opaque "+fam+" state cannot be sent to Responses", ir.LossDrop)
				continue
			}
			switch {
			case s.Kind == "item_reference":
				flush()
				out = append(out, map[string]any{"type": "item_reference", "id": s.Data})
			case strings.HasPrefix(s.Kind, "native_item:"):
				flush()
				var raw map[string]any
				if json.Unmarshal([]byte(s.Data), &raw) == nil {
					out = append(out, raw)
				}
			default:
				if cp, ok := contentPart(p, m.Role, losses); ok {
					content = append(content, cp)
				}
			}
		default:
			if cp, ok := contentPart(p, m.Role, losses); ok {
				content = append(content, cp)
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
