// Package gemini adapts the Gemini generateContent wire protocol to and from
// the canonical IR. It is written from the public API documentation.
package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// P is this protocol's identifier (also the Ext key prefix and family).
const P = string(ir.Gemini)

// MaxContents bounds parser work.
const MaxContents = 20000

func invalid(format string, args ...any) error {
	return &ir.Error{Kind: ir.ErrInvalidRequest, Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

type wirePart struct {
	Text       *string `json:"text,omitempty"`
	Thought    bool    `json:"thought,omitempty"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData,omitempty"`
	FileData *struct {
		MimeType string `json:"mimeType"`
		FileURI  string `json:"fileUri"`
	} `json:"fileData,omitempty"`
	FunctionCall *struct {
		ID   string          `json:"id,omitempty"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall,omitempty"`
	FunctionResponse *struct {
		ID       string          `json:"id,omitempty"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse,omitempty"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

type wireContent struct {
	Role  string            `json:"role"`
	Parts []json.RawMessage `json:"parts"`
}

// partsFromWire converts Gemini parts to IR parts. callSeq numbers calls
// without ids so results can be matched.
func partsFromWire(raws []json.RawMessage, callSeq *int) ([]ir.Part, error) {
	var out []ir.Part
	for _, raw := range raws {
		o, err := wire.ParseObject(raw)
		if err != nil {
			return nil, err
		}
		var w wirePart
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		for _, k := range []string{"text", "thought", "inlineData", "fileData", "functionCall", "functionResponse", "thoughtSignature"} {
			delete(o, k)
		}
		var p ir.Part
		switch {
		case w.FunctionCall != nil:
			id := w.FunctionCall.ID
			if id == "" {
				*callSeq++
				id = "call_" + strconv.Itoa(*callSeq)
			}
			p = ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: id, Name: w.FunctionCall.Name, Arguments: string(wire.RawOrEmptyObject(w.FunctionCall.Args)), Kind: "function"}}
		case w.FunctionResponse != nil:
			id := w.FunctionResponse.ID
			if id == "" {
				id = "name:" + w.FunctionResponse.Name
			}
			p = ir.Part{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: id, Name: w.FunctionResponse.Name,
				Content: []ir.Part{{Kind: ir.PartText, Text: string(wire.RawOrEmptyObject(w.FunctionResponse.Response))}}}}
		case w.Thought && w.Text != nil:
			p = ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Text: *w.Text, Summary: true}}
		case w.Text != nil:
			p = ir.Part{Kind: ir.PartText, Text: *w.Text}
		case w.InlineData != nil:
			b, err := base64.StdEncoding.DecodeString(w.InlineData.Data)
			if err != nil {
				return nil, fmt.Errorf("inlineData is not valid base64")
			}
			kind := ir.PartImage
			if !strings.HasPrefix(w.InlineData.MimeType, "image/") {
				kind = ir.PartDocument
			}
			p = ir.Part{Kind: kind, Media: &ir.Media{MimeType: w.InlineData.MimeType, Data: b}}
		case w.FileData != nil:
			kind := ir.PartImage
			if !strings.HasPrefix(w.FileData.MimeType, "image/") {
				kind = ir.PartDocument
			}
			p = ir.Part{Kind: kind, Media: &ir.Media{MimeType: w.FileData.MimeType, URL: w.FileData.FileURI}}
		case w.ThoughtSignature != "":
			p = ir.Part{Kind: ir.PartSealed}
		default:
			p = ir.Part{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: P, Kind: "native_part", Data: string(raw)}}
			out = append(out, p)
			continue
		}
		if w.ThoughtSignature != "" {
			p.Sealed = &ir.Sealed{Family: P, Kind: "thought_signature", Data: w.ThoughtSignature}
			if p.ToolCall != nil {
				p.Sealed.ForCallID = p.ToolCall.ID
			}
		}
		if len(o) > 0 {
			p.Ext = map[string]json.RawMessage{P: wire.MustMarshal(o)}
		}
		out = append(out, p)
	}
	return out, nil
}

// ParseRequest converts a generateContent body into IR. model and stream
// come from the request path.
func ParseRequest(body []byte, model string, stream bool) (*ir.Request, error) {
	o, err := wire.ParseObject(body)
	if err != nil {
		return nil, invalid("request body must be a JSON object: %v", err)
	}
	r := &ir.Request{Source: ir.Gemini, Model: model, Stream: stream}
	if raw, ok := o.TakeRaw("systemInstruction"); ok {
		var c wireContent
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, invalid("systemInstruction: %v", err)
		}
		seq := 0
		parts, err := partsFromWire(c.Parts, &seq)
		if err != nil {
			return nil, invalid("systemInstruction: %v", err)
		}
		r.System = parts
		for range parts {
			r.SystemRoles = append(r.SystemRoles, ir.RoleSystem)
		}
	}
	var contents []wireContent
	if _, err := o.Take("contents", &contents); err != nil {
		return nil, invalid("%v", err)
	}
	if len(contents) > MaxContents {
		return nil, invalid("too many contents")
	}
	seq := 0
	calls := map[string][]string{} // name -> pending call ids (for id-less responses)
	for i, c := range contents {
		parts, err := partsFromWire(c.Parts, &seq)
		if err != nil {
			return nil, invalid("contents[%d]: %v", i, err)
		}
		role := ir.RoleUser
		if c.Role == "model" {
			role = ir.RoleAssistant
		}
		for j := range parts {
			if tc := parts[j].ToolCall; tc != nil {
				calls[tc.Name] = append(calls[tc.Name], tc.ID)
			}
			if tr := parts[j].ToolResult; tr != nil && strings.HasPrefix(tr.CallID, "name:") {
				if q := calls[tr.Name]; len(q) > 0 {
					tr.CallID, calls[tr.Name] = q[0], q[1:]
				}
			}
		}
		r.Messages = append(r.Messages, ir.Message{Role: role, Parts: parts})
	}
	var tools []map[string]json.RawMessage
	if _, err := o.Take("tools", &tools); err != nil {
		return nil, invalid("%v", err)
	}
	for _, t := range tools {
		for k, v := range t {
			if k != "functionDeclarations" {
				r.Tools = append(r.Tools, ir.Tool{Kind: "builtin", BuiltinType: k, Ext: map[string]json.RawMessage{P: wire.MustMarshal(map[string]json.RawMessage{k: v})}})
				continue
			}
			var decls []struct {
				Name                 string          `json:"name"`
				Description          string          `json:"description"`
				Parameters           json.RawMessage `json:"parameters"`
				ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
			}
			if err := json.Unmarshal(v, &decls); err != nil {
				return nil, invalid("functionDeclarations: %v", err)
			}
			for _, d := range decls {
				params := d.ParametersJSONSchema
				if len(params) == 0 {
					params = d.Parameters
				}
				r.Tools = append(r.Tools, ir.Tool{Kind: "function", Name: d.Name, Description: d.Description, Parameters: params})
			}
		}
	}
	if raw, ok := o.TakeRaw("toolConfig"); ok {
		var tc struct {
			FunctionCallingConfig struct {
				Mode                 string   `json:"mode"`
				AllowedFunctionNames []string `json:"allowedFunctionNames"`
			} `json:"functionCallingConfig"`
		}
		if err := json.Unmarshal(raw, &tc); err == nil {
			switch tc.FunctionCallingConfig.Mode {
			case "AUTO":
				r.ToolChoice = &ir.ToolChoice{Mode: "auto"}
			case "NONE":
				r.ToolChoice = &ir.ToolChoice{Mode: "none"}
			case "ANY":
				if len(tc.FunctionCallingConfig.AllowedFunctionNames) == 1 {
					r.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: tc.FunctionCallingConfig.AllowedFunctionNames[0]}
				} else {
					r.ToolChoice = &ir.ToolChoice{Mode: "required"}
				}
			}
		}
	}
	if raw, ok := o.TakeRaw("generationConfig"); ok {
		g, err := wire.ParseObject(raw)
		if err != nil {
			return nil, invalid("generationConfig: %v", err)
		}
		var temp, topP float64
		var topK, maxTok int
		var seed int64
		if ok, _ := g.Take("temperature", &temp); ok {
			r.Temperature = &temp
		}
		if ok, _ := g.Take("topP", &topP); ok {
			r.TopP = &topP
		}
		if ok, _ := g.Take("topK", &topK); ok {
			r.TopK = &topK
		}
		if ok, _ := g.Take("maxOutputTokens", &maxTok); ok {
			r.MaxOutputTokens = &maxTok
		}
		if ok, _ := g.Take("seed", &seed); ok {
			r.Seed = &seed
		}
		_, _ = g.Take("stopSequences", &r.Stop)
		var mime string
		_, _ = g.Take("responseMimeType", &mime)
		schema, hasSchema := g.TakeRaw("responseJsonSchema")
		if !hasSchema {
			schema, hasSchema = g.TakeRaw("responseSchema")
		}
		if hasSchema {
			r.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: schema}
		} else if mime == "application/json" {
			r.ResponseFormat = &ir.ResponseFormat{Type: "json_object"}
		}
		if raw, ok := g.TakeRaw("thinkingConfig"); ok {
			var tc struct {
				ThinkingBudget  *int   `json:"thinkingBudget"`
				IncludeThoughts bool   `json:"includeThoughts"`
				ThinkingLevel   string `json:"thinkingLevel"`
			}
			if err := json.Unmarshal(raw, &tc); err == nil {
				rc := &ir.ReasoningConfig{Effort: strings.ToLower(tc.ThinkingLevel)}
				if tc.ThinkingBudget != nil {
					if *tc.ThinkingBudget == 0 {
						f := false
						rc.Enabled, rc.Effort = &f, "none"
					} else if *tc.ThinkingBudget > 0 {
						rc.BudgetTokens = *tc.ThinkingBudget
					}
				}
				if tc.IncludeThoughts {
					rc.Summary = "auto"
				}
				r.Reasoning = rc
			}
		}
		var cands int
		if ok, _ := g.Take("candidateCount", &cands); ok && cands > 1 {
			return nil, &ir.Error{Kind: ir.ErrUnsupported, Message: "candidateCount > 1 is not supported by the gateway", HTTPStatus: 400}
		}
		if len(g) > 0 {
			r.Ext = map[string]json.RawMessage{P + ":generationConfig": wire.MustMarshal(g)}
		}
	}
	r.Ext = o.Ext(P, r.Ext)
	return r, nil
}

// ---------- build ----------

// BuildRequest renders IR as a generateContent body (model goes in the URL).
func BuildRequest(r *ir.Request) ([]byte, []ir.Loss, error) {
	var losses ir.Losses
	body := map[string]any{}
	var sys []any
	for _, p := range r.System {
		if p.Kind == ir.PartText {
			sys = append(sys, map[string]any{"text": p.Text})
		} else {
			losses.Add("system_non_text", "non-text system content dropped", ir.LossDrop)
		}
	}
	if len(sys) > 0 {
		body["systemInstruction"] = map[string]any{"parts": sys}
	}
	names := map[string]string{}
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			if p.ToolCall != nil {
				names[p.ToolCall.ID] = p.ToolCall.Name
			}
		}
	}
	var contents []any
	push := func(role string, parts []any) {
		if len(parts) == 0 {
			return
		}
		if n := len(contents); n > 0 {
			if last := contents[n-1].(map[string]any); last["role"] == role {
				last["parts"] = append(last["parts"].([]any), parts...)
				return
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	for _, m := range r.Messages {
		role := "user"
		switch m.Role {
		case ir.RoleAssistant:
			role = "model"
		case ir.RoleSystem, ir.RoleDeveloper:
			// Gemini has only one system instruction, ahead of the
			// conversation; a later instruction is kept in place as user text.
			ir.SystemMessageLosses(m, P, &losses)
			if len(m.Parts) > 0 {
				losses.Add("mid_conversation_system", "a system/developer message inside the conversation was sent as user text", ir.LossApproximate)
			}
		}
		var parts []any
		for _, p := range m.Parts {
			if wp, ok := buildPart(p, names, &losses); ok {
				parts = append(parts, wp)
			}
		}
		push(role, parts)
	}
	if contents == nil {
		contents = []any{}
	}
	body["contents"] = contents
	var decls []any
	var tools []any
	for _, t := range r.Tools {
		switch t.Kind {
		case "function":
			d := map[string]any{"name": t.Name, "parametersJsonSchema": wire.RawOrEmptyObject(t.Parameters)}
			if t.Description != "" {
				d["description"] = t.Description
			}
			decls = append(decls, d)
		case "builtin":
			if raw, ok := t.Ext[P]; ok {
				tools = append(tools, json.RawMessage(raw))
			} else {
				losses.Add("builtin_tool:"+t.BuiltinType, "provider tool "+t.BuiltinType+" is not available on Gemini", ir.LossDrop)
			}
		default:
			losses.Add("custom_tool", "free-form (custom) tool "+t.Name+" is not supported by Gemini", ir.LossReject)
		}
	}
	if len(decls) > 0 {
		tools = append(tools, map[string]any{"functionDeclarations": decls})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if tc := r.ToolChoice; tc != nil {
		fc := map[string]any{}
		switch tc.Mode {
		case "auto":
			fc["mode"] = "AUTO"
		case "none":
			fc["mode"] = "NONE"
		case "required":
			fc["mode"] = "ANY"
		case "tool":
			fc["mode"], fc["allowedFunctionNames"] = "ANY", []string{tc.Name}
		}
		body["toolConfig"] = map[string]any{"functionCallingConfig": fc}
	}
	if r.ParallelToolCalls != nil && !*r.ParallelToolCalls {
		losses.Add("parallel_tool_calls", "disabling parallel tool calls is not supported by Gemini", ir.LossApproximate)
	}
	gc := map[string]any{}
	if raw, ok := r.Ext[P+":generationConfig"]; ok {
		_ = json.Unmarshal(raw, &gc)
	}
	if r.Temperature != nil {
		gc["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		gc["topP"] = *r.TopP
	}
	if r.TopK != nil {
		gc["topK"] = *r.TopK
	}
	if r.MaxOutputTokens != nil {
		gc["maxOutputTokens"] = *r.MaxOutputTokens
	}
	if len(r.Stop) > 0 {
		gc["stopSequences"] = r.Stop
	}
	if r.Seed != nil {
		gc["seed"] = *r.Seed
	}
	if rf := r.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_object":
			gc["responseMimeType"] = "application/json"
		case "json_schema":
			gc["responseMimeType"] = "application/json"
			gc["responseJsonSchema"] = wire.RawOrEmptyObject(rf.Schema)
		}
	}
	if rc := r.Reasoning.Legacy(); rc != nil {
		tc := map[string]any{}
		switch {
		case rc.Enabled != nil && !*rc.Enabled, rc.Effort == "none":
			tc["thinkingBudget"] = 0
		case rc.BudgetTokens > 0:
			tc["thinkingBudget"] = rc.BudgetTokens
		case rc.Effort != "":
			tc["thinkingBudget"] = map[string]int{"minimal": 512, "low": 2048, "medium": 8192, "high": 24576, "xhigh": 32768}[rc.Effort]
			losses.Add("reasoning_effort", "reasoning effort mapped to a thinking budget", ir.LossApproximate)
		}
		if rc.Summary != "" && rc.Summary != "none" {
			tc["includeThoughts"] = true
		}
		if len(tc) > 0 {
			gc["thinkingConfig"] = tc
		}
	}
	if len(gc) > 0 {
		body["generationConfig"] = gc
	}
	if r.PreviousResponseID != "" {
		losses.Add("previous_response_id", "server-side conversation state cannot be carried to Gemini", ir.LossReject)
	}
	restore := map[string]json.RawMessage{}
	for k, v := range r.Ext {
		if k != P+":generationConfig" {
			restore[k] = v
		}
	}
	wire.Restore(body, restore, P)
	for _, k := range wire.ForeignExtKeys(r.Ext, P) {
		losses.Add("field:"+k, "client field has no Gemini equivalent", ir.LossDrop)
	}
	b, err := wire.Marshal(body)
	return b, losses.List(), err
}

func buildPart(p ir.Part, names map[string]string, losses *ir.Losses) (map[string]any, bool) {
	var w map[string]any
	switch p.Kind {
	case ir.PartText, ir.PartRefusal:
		w = map[string]any{"text": p.Text}
	case ir.PartImage, ir.PartDocument:
		if p.Media == nil {
			return nil, false
		}
		switch {
		case len(p.Media.Data) > 0:
			mime := p.Media.MimeType
			if mime == "" {
				mime = "application/octet-stream"
			}
			w = map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": base64.StdEncoding.EncodeToString(p.Media.Data)}}
		case p.Media.URL != "":
			w = map[string]any{"fileData": map[string]any{"mimeType": p.Media.MimeType, "fileUri": p.Media.URL}}
		default:
			losses.Add("media_file_id", "provider file references are not portable to Gemini", ir.LossDrop)
			return nil, false
		}
	case ir.PartToolCall:
		tc := p.ToolCall
		w = map[string]any{"functionCall": map[string]any{"name": tc.Name, "args": wire.ArgsObject(tc.Arguments)}}
	case ir.PartToolResult:
		tr := p.ToolResult
		name := tr.Name
		if name == "" {
			name = names[tr.CallID]
		}
		var text []string
		for _, c := range tr.Content {
			if c.Kind == ir.PartText {
				text = append(text, c.Text)
			} else {
				losses.Add("tool_result_media", "non-text tool result content dropped", ir.LossDrop)
			}
		}
		joined := strings.Join(text, "\n")
		var resp any
		if json.Valid([]byte(joined)) && strings.HasPrefix(strings.TrimSpace(joined), "{") {
			resp = json.RawMessage(joined)
		} else {
			key := "output"
			if tr.IsError {
				key = "error"
			}
			resp = map[string]any{key: joined}
		}
		w = map[string]any{"functionResponse": map[string]any{"name": name, "response": resp}}
	case ir.PartReasoning:
		if p.Sealed != nil && p.Sealed.Family == P && p.Reasoning != nil {
			w = map[string]any{"text": p.Reasoning.Text, "thought": true}
		} else {
			losses.Add("reasoning_history", "reasoning from another provider cannot be replayed to Gemini", ir.LossDrop)
			return nil, false
		}
	case ir.PartSealed:
		if p.Sealed != nil && p.Sealed.Family == P && p.Sealed.Kind == "native_part" {
			var raw map[string]any
			if json.Unmarshal([]byte(p.Sealed.Data), &raw) == nil {
				return raw, true
			}
		}
		if p.Sealed != nil && p.Sealed.Family == P && p.Sealed.Kind == "thought_signature" {
			return map[string]any{"thoughtSignature": p.Sealed.Data}, true
		}
		fam := ""
		if p.Sealed != nil {
			fam = p.Sealed.Family
		}
		losses.Add("sealed_state:"+fam, "opaque "+fam+" state cannot be sent to Gemini", ir.LossDrop)
		return nil, false
	default:
		return nil, false
	}
	if p.Sealed != nil && p.Sealed.Family == P && p.Sealed.Kind == "thought_signature" {
		w["thoughtSignature"] = p.Sealed.Data
	} else if p.Sealed != nil && p.Kind != ir.PartReasoning {
		losses.Add("sealed_state:"+p.Sealed.Family, "opaque state attached to content was dropped", ir.LossDrop)
	}
	if raw, ok := p.Ext[P]; ok {
		var extra map[string]any
		if json.Unmarshal(raw, &extra) == nil {
			for k, v := range extra {
				if _, set := w[k]; !set {
					w[k] = v
				}
			}
		}
	}
	return w, true
}

// ---------- responses ----------

type wireUsage struct {
	PromptTokenCount        *int64 `json:"promptTokenCount"`
	CandidatesTokenCount    *int64 `json:"candidatesTokenCount"`
	CachedContentTokenCount *int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      *int64 `json:"thoughtsTokenCount"`
}

func (u *wireUsage) toIR() *ir.Usage {
	if u == nil {
		return nil
	}
	out := &ir.Usage{InputTokens: u.PromptTokenCount, CacheReadTokens: u.CachedContentTokenCount, ReasoningTokens: u.ThoughtsTokenCount,
		InputIncludesCache: true, OutputIncludesReasoning: true}
	if u.CandidatesTokenCount != nil || u.ThoughtsTokenCount != nil {
		out.OutputTokens = ir.I64(ir.Val(u.CandidatesTokenCount) + ir.Val(u.ThoughtsTokenCount))
	}
	return out
}

type wireResponse struct {
	ResponseID string `json:"responseId"`
	Model      string `json:"modelVersion"`
	Candidates []struct {
		Content struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata  *wireUsage `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// FinishToIR maps Gemini finish reasons.
func FinishToIR(s string, hasCall bool) ir.StopReason {
	switch s {
	case "STOP":
		if hasCall {
			return ir.StopToolUse
		}
		return ir.StopEndTurn
	case "MAX_TOKENS":
		return ir.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return ir.StopContentFilter
	case "":
		return ir.StopUnknown
	}
	return ir.StopReason(strings.ToLower(s))
}

func finishFromIR(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "MAX_TOKENS"
	case ir.StopContentFilter, ir.StopRefusal:
		return "SAFETY"
	}
	return "STOP"
}

// ParseResponse converts an upstream non-streaming response into IR.
func ParseResponse(body []byte) (*ir.Response, error) {
	var w wireResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream returned an unparseable response"}
	}
	r := &ir.Response{ID: w.ResponseID, Model: w.Model}
	if u := w.UsageMetadata.toIR(); u != nil {
		r.Usage = *u
	}
	if len(w.Candidates) == 0 {
		if w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
			r.StopReason = ir.StopContentFilter
			return r, nil
		}
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream response has no candidates"}
	}
	seq := 0
	parts, err := partsFromWire(w.Candidates[0].Content.Parts, &seq)
	if err != nil {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: err.Error()}
	}
	hasCall := false
	for _, p := range parts {
		if p.Kind == ir.PartToolCall {
			hasCall = true
		}
	}
	r.Parts = parts
	r.StopReason = FinishToIR(w.Candidates[0].FinishReason, hasCall)
	return r, nil
}

func usageWire(u ir.Usage) map[string]any {
	in := ir.Val(u.InputTokens)
	if !u.InputIncludesCache {
		in += ir.Val(u.CacheReadTokens) + ir.Val(u.CacheWriteTokens)
	}
	thoughts := ir.Val(u.ReasoningTokens)
	cand := ir.Val(u.OutputTokens)
	if u.OutputIncludesReasoning {
		cand -= thoughts
		if cand < 0 {
			cand = 0
		}
	}
	m := map[string]any{"promptTokenCount": in, "candidatesTokenCount": cand, "totalTokenCount": in + cand + thoughts}
	if u.CacheReadTokens != nil {
		m["cachedContentTokenCount"] = *u.CacheReadTokens
	}
	if u.ReasoningTokens != nil {
		m["thoughtsTokenCount"] = thoughts
	}
	return m
}

// EncodeResponse renders an IR response as a GenerateContentResponse.
func EncodeResponse(r *ir.Response, model string) []byte {
	return wire.MustMarshal(responseChunk(r.Parts, r.StopReason, &r.Usage, model, r.ID))
}

func responseChunk(parts []ir.Part, stop ir.StopReason, u *ir.Usage, model, id string) map[string]any {
	var l ir.Losses
	wp := []any{}
	for _, p := range parts {
		if p.Kind == ir.PartReasoning {
			if p.Reasoning != nil && p.Reasoning.Text != "" {
				wp = append(wp, map[string]any{"text": p.Reasoning.Text, "thought": true})
			}
			continue
		}
		if w, ok := buildPart(p, nil, &l); ok {
			if p.Kind == ir.PartToolCall {
				fc := w["functionCall"].(map[string]any)
				fc["id"] = p.ToolCall.ID
			}
			wp = append(wp, w)
		}
	}
	cand := map[string]any{"content": map[string]any{"role": "model", "parts": wp}, "index": 0}
	if stop != "" {
		cand["finishReason"] = finishFromIR(stop)
	}
	out := map[string]any{"candidates": []any{cand}, "modelVersion": model}
	if id != "" {
		out["responseId"] = id
	}
	if u != nil {
		out["usageMetadata"] = usageWire(*u)
	}
	return out
}

// ParseError converts an upstream error response.
func ParseError(status int, body []byte, h http.Header) *ir.Error {
	var w struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &w)
	e := &ir.Error{HTTPStatus: status, UpstreamType: w.Error.Status, Message: w.Error.Message}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	switch {
	case w.Error.Status == "RESOURCE_EXHAUSTED" || status == 429:
		e.Kind = ir.ErrRateLimited
	case w.Error.Status == "UNAUTHENTICATED" || status == 401:
		e.Kind = ir.ErrAuthentication
	case w.Error.Status == "PERMISSION_DENIED" || status == 403:
		e.Kind = ir.ErrPermission
	case w.Error.Status == "NOT_FOUND" || status == 404:
		e.Kind = ir.ErrNotFound
	case w.Error.Status == "INVALID_ARGUMENT" || w.Error.Status == "FAILED_PRECONDITION" || status == 400:
		e.Kind = ir.ErrInvalidRequest
		if strings.Contains(strings.ToLower(e.Message), "token count") {
			e.Kind = ir.ErrContextLength
		}
	case w.Error.Status == "UNAVAILABLE" || status == 503:
		e.Kind = ir.ErrOverloaded
	case w.Error.Status == "DEADLINE_EXCEEDED" || status == 504:
		e.Kind = ir.ErrTimeout
	default:
		e.Kind = ir.ErrUpstream
	}
	if ra := h.Get("retry-after"); ra != "" {
		if n, err := strconv.Atoi(ra); err == nil {
			e.RetryAfterSeconds = n
		}
	}
	return e
}

// EncodeError renders a Google-style error body.
func EncodeError(e *ir.Error) []byte {
	status := map[ir.ErrorKind]string{ir.ErrInvalidRequest: "INVALID_ARGUMENT", ir.ErrContextLength: "INVALID_ARGUMENT", ir.ErrUnsupported: "INVALID_ARGUMENT",
		ir.ErrAuthentication: "UNAUTHENTICATED", ir.ErrPermission: "PERMISSION_DENIED", ir.ErrPolicy: "PERMISSION_DENIED", ir.ErrNotFound: "NOT_FOUND",
		ir.ErrRateLimited: "RESOURCE_EXHAUSTED", ir.ErrQuotaExhausted: "RESOURCE_EXHAUSTED", ir.ErrBudget: "RESOURCE_EXHAUSTED",
		ir.ErrOverloaded: "UNAVAILABLE", ir.ErrTimeout: "DEADLINE_EXCEEDED"}[e.Kind]
	if status == "" {
		status = "INTERNAL"
	}
	return wire.MustMarshal(map[string]any{"error": map[string]any{"code": ir.StatusFor(e.Kind), "message": e.Message, "status": status}})
}

// ---------- streaming ----------

// StreamDecoder converts streamed GenerateContentResponse chunks into IR.
type StreamDecoder struct {
	started  bool
	textIdx  int
	thinkIdx int
	next     int
	seq      int
	hasCall  bool
	finish   string
	done     bool
}

// NewStreamDecoder creates a decoder.
func NewStreamDecoder() *StreamDecoder { return &StreamDecoder{textIdx: -1, thinkIdx: -1} }

// Decode converts one SSE data payload.
func (d *StreamDecoder) Decode(data []byte) (out []ir.Event, unknown string, err error) {
	var w wireResponse
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, "", &ir.Error{Kind: ir.ErrProtocol, Message: "malformed stream chunk"}
	}
	if !d.started {
		d.started = true
		out = append(out, ir.Event{Kind: ir.EvStart, ResponseID: w.ResponseID, Model: w.Model})
	}
	if len(w.Candidates) > 0 {
		c := w.Candidates[0]
		parts, err := partsFromWire(c.Content.Parts, &d.seq)
		if err != nil {
			return nil, "", &ir.Error{Kind: ir.ErrProtocol, Message: err.Error()}
		}
		for _, p := range parts {
			switch p.Kind {
			case ir.PartReasoning:
				if d.thinkIdx < 0 {
					d.thinkIdx = d.next
					d.next++
					out = append(out, ir.Event{Kind: ir.EvPartStart, Index: d.thinkIdx, Part: &ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Summary: true}}})
				}
				out = append(out, ir.Event{Kind: ir.EvDelta, Index: d.thinkIdx, Delta: p.Reasoning.Text})
			case ir.PartText:
				if d.thinkIdx >= 0 {
					out = append(out, ir.Event{Kind: ir.EvPartStop, Index: d.thinkIdx})
					d.thinkIdx = -2
				}
				if d.textIdx < 0 {
					d.textIdx = d.next
					d.next++
					out = append(out, ir.Event{Kind: ir.EvPartStart, Index: d.textIdx, Part: &ir.Part{Kind: ir.PartText}})
				}
				out = append(out, ir.Event{Kind: ir.EvDelta, Index: d.textIdx, Delta: p.Text})
				if p.Sealed != nil {
					idx := d.next
					d.next++
					sp := ir.Part{Kind: ir.PartSealed, Sealed: p.Sealed}
					out = append(out, ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &sp}, ir.Event{Kind: ir.EvPartStop, Index: idx})
				}
			case ir.PartToolCall:
				d.hasCall = true
				idx := d.next
				d.next++
				hdr := p
				args := p.ToolCall.Arguments
				tc := *p.ToolCall
				tc.Arguments = ""
				hdr.ToolCall = &tc
				out = append(out, ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &hdr}, ir.Event{Kind: ir.EvDelta, Index: idx, Delta: args},
					ir.Event{Kind: ir.EvPartStop, Index: idx, Part: &ir.Part{Kind: ir.PartToolCall, Sealed: p.Sealed}})
			default:
				idx := d.next
				d.next++
				pp := p
				out = append(out, ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &pp}, ir.Event{Kind: ir.EvPartStop, Index: idx})
			}
		}
		if c.FinishReason != "" {
			d.finish = c.FinishReason
		}
	}
	if u := w.UsageMetadata.toIR(); u != nil {
		out = append(out, ir.Event{Kind: ir.EvUsage, Usage: u})
	}
	return out, "", nil
}

// Close finalizes the stream at upstream EOF.
func (d *StreamDecoder) Close() []ir.Event {
	if d.done {
		return nil
	}
	d.done = true
	var out []ir.Event
	if d.thinkIdx >= 0 {
		out = append(out, ir.Event{Kind: ir.EvPartStop, Index: d.thinkIdx})
	}
	if d.textIdx >= 0 {
		out = append(out, ir.Event{Kind: ir.EvPartStop, Index: d.textIdx})
	}
	if d.finish != "" {
		out = append(out, ir.Event{Kind: ir.EvStop, StopReason: FinishToIR(d.finish, d.hasCall)})
	}
	return out
}

// Finished reports whether a finish reason was seen.
func (d *StreamDecoder) Finished() bool { return d.finish != "" }

// StreamEncoder renders IR events as streamed GenerateContentResponse chunks.
// Text arrives incrementally; tool calls are emitted whole when complete
// (Gemini clients expect complete functionCall parts).
type StreamEncoder struct {
	Model   string
	ID      string
	kinds   map[int]ir.PartKind
	calls   map[int]*ir.ToolCall
	sealed  map[int]*ir.Sealed
	usage   ir.Usage
	stopped bool
}

// NewStreamEncoder creates an encoder.
func NewStreamEncoder(model, id string) *StreamEncoder {
	return &StreamEncoder{Model: model, ID: id, kinds: map[int]ir.PartKind{}, calls: map[int]*ir.ToolCall{}, sealed: map[int]*ir.Sealed{}}
}

// Encode converts one IR event into zero or more data payloads.
func (e *StreamEncoder) Encode(ev ir.Event) [][]byte {
	switch ev.Kind {
	case ir.EvPartStart:
		e.kinds[ev.Index] = ev.Part.Kind
		if ev.Part.Kind == ir.PartToolCall {
			tc := *ev.Part.ToolCall
			e.calls[ev.Index] = &tc
		}
	case ir.EvDelta:
		switch e.kinds[ev.Index] {
		case ir.PartText, ir.PartRefusal:
			return [][]byte{wire.MustMarshal(responseChunk([]ir.Part{{Kind: ir.PartText, Text: ev.Delta}}, "", nil, e.Model, e.ID))}
		case ir.PartReasoning:
			return [][]byte{wire.MustMarshal(responseChunk([]ir.Part{{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Text: ev.Delta}}}, "", nil, e.Model, e.ID))}
		case ir.PartToolCall:
			if tc := e.calls[ev.Index]; tc != nil {
				tc.Arguments += ev.Delta
			}
		}
	case ir.EvPartStop:
		if tc := e.calls[ev.Index]; tc != nil {
			delete(e.calls, ev.Index)
			p := ir.Part{Kind: ir.PartToolCall, ToolCall: tc}
			if ev.Part != nil && ev.Part.Sealed != nil {
				p.Sealed = ev.Part.Sealed
			}
			return [][]byte{wire.MustMarshal(responseChunk([]ir.Part{p}, "", nil, e.Model, e.ID))}
		}
	case ir.EvUsage:
		if ev.Usage != nil {
			e.usage.Merge(*ev.Usage)
		}
	case ir.EvStop:
		return e.Finish(ev.StopReason)
	case ir.EvError:
		e.stopped = true
		return [][]byte{EncodeError(ev.Err)}
	}
	return nil
}

// Finish emits the final chunk with finishReason and usage.
func (e *StreamEncoder) Finish(stop ir.StopReason) [][]byte {
	if e.stopped {
		return nil
	}
	e.stopped = true
	return [][]byte{wire.MustMarshal(responseChunk(nil, stop, &e.usage, e.Model, e.ID))}
}

// Stopped reports whether the stream ended.
func (e *StreamEncoder) Stopped() bool { return e.stopped }
