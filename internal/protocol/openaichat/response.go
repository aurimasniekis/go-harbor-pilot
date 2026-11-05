package openaichat

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

type wireUsage struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *wireUsage) toIR() *ir.Usage {
	if u == nil {
		return nil
	}
	out := &ir.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, InputIncludesCache: true, OutputIncludesReasoning: true}
	if u.PromptTokensDetails != nil {
		out.CacheReadTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return out
}

func usageWire(u ir.Usage) map[string]any {
	in := ir.Val(u.InputTokens)
	if !u.InputIncludesCache {
		in += ir.Val(u.CacheReadTokens) + ir.Val(u.CacheWriteTokens)
	}
	out := ir.Val(u.OutputTokens)
	m := map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}
	if u.CacheReadTokens != nil {
		m["prompt_tokens_details"] = map[string]any{"cached_tokens": *u.CacheReadTokens}
	}
	if u.ReasoningTokens != nil {
		m["completion_tokens_details"] = map[string]any{"reasoning_tokens": *u.ReasoningTokens}
	}
	return m
}

// FinishToIR maps finish reasons.
func FinishToIR(s string) ir.StopReason {
	switch s {
	case "stop":
		return ir.StopEndTurn
	case "length":
		return ir.StopMaxTokens
	case "tool_calls", "function_call":
		return ir.StopToolUse
	case "content_filter":
		return ir.StopContentFilter
	case "":
		return ir.StopUnknown
	}
	return ir.StopReason(s)
}

// FinishFromIR maps canonical stop reasons to finish_reason.
func FinishFromIR(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "length"
	case ir.StopToolUse:
		return "tool_calls"
	case ir.StopContentFilter, ir.StopRefusal:
		return "content_filter"
	}
	return "stop"
}

type wireMessage struct {
	Role             string          `json:"role"`
	Content          *string         `json:"content"`
	Refusal          *string         `json:"refusal"`
	ReasoningContent *string         `json:"reasoning_content"`
	Reasoning        *string         `json:"reasoning"`
	ToolCalls        []wireToolCall  `json:"tool_calls"`
	Extra            json.RawMessage `json:"-"`
}

type wireToolCall struct {
	Index    *int   `json:"index"`
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

// ParseResponse converts an upstream non-streaming response into IR.
func ParseResponse(body []byte) (*ir.Response, error) {
	var w struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message      wireMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage *wireUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream returned an unparseable response"}
	}
	r := &ir.Response{ID: w.ID, Model: w.Model}
	if u := w.Usage.toIR(); u != nil {
		r.Usage = *u
	}
	if len(w.Choices) == 0 {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream response has no choices"}
	}
	c := w.Choices[0]
	r.StopReason = FinishToIR(c.FinishReason)
	m := c.Message
	if rc := firstNonNil(m.ReasoningContent, m.Reasoning); rc != nil && *rc != "" {
		r.Parts = append(r.Parts, ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Text: *rc}})
	}
	if m.Content != nil && *m.Content != "" {
		r.Parts = append(r.Parts, ir.Part{Kind: ir.PartText, Text: *m.Content})
	}
	if m.Refusal != nil && *m.Refusal != "" {
		r.Parts = append(r.Parts, ir.Part{Kind: ir.PartRefusal, Text: *m.Refusal})
		if r.StopReason == ir.StopEndTurn {
			r.StopReason = ir.StopRefusal
		}
	}
	for _, tc := range m.ToolCalls {
		call := &ir.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments, Kind: "function"}
		if tc.Type == "custom" {
			call.Name, call.Arguments, call.Kind = tc.Custom.Name, tc.Custom.Input, "custom"
		}
		r.Parts = append(r.Parts, ir.Part{Kind: ir.PartToolCall, ToolCall: call})
	}
	return r, nil
}

func firstNonNil(xs ...*string) *string {
	for _, x := range xs {
		if x != nil {
			return x
		}
	}
	return nil
}

// EncodeResponse renders an IR response as a chat.completion object.
func EncodeResponse(r *ir.Response, model string, created time.Time) []byte {
	msg := map[string]any{"role": "assistant", "content": nil}
	var text, reasoning []string
	var calls []any
	for _, p := range r.Parts {
		switch p.Kind {
		case ir.PartText:
			text = append(text, p.Text)
		case ir.PartRefusal:
			msg["refusal"] = p.Text
		case ir.PartReasoning:
			if p.Reasoning != nil && p.Reasoning.Text != "" {
				reasoning = append(reasoning, p.Reasoning.Text)
			}
		case ir.PartToolCall:
			tc := p.ToolCall
			if tc.Kind == "custom" {
				calls = append(calls, map[string]any{"id": tc.ID, "type": "custom", "custom": map[string]any{"name": tc.Name, "input": tc.Arguments}})
			} else {
				calls = append(calls, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": argsString(tc.Arguments)}})
			}
		}
	}
	if len(text) > 0 {
		msg["content"] = strings.Join(text, "")
	}
	if len(reasoning) > 0 {
		msg["reasoning_content"] = strings.Join(reasoning, "\n")
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	out := map[string]any{"id": chatID(r.ID), "object": "chat.completion", "created": created.Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": FinishFromIR(r.StopReason)}},
		"usage":   usageWire(r.Usage)}
	return wire.MustMarshal(out)
}

func chatID(id string) string {
	if strings.HasPrefix(id, "chatcmpl-") {
		return id
	}
	if id == "" {
		return "chatcmpl-hp"
	}
	return "chatcmpl-" + id
}

// ---------- errors ----------

// ParseError converts an upstream error response.
func ParseError(status int, body []byte, h http.Header) *ir.Error {
	var w struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &w)
	e := &ir.Error{HTTPStatus: status, UpstreamType: w.Error.Type, Message: w.Error.Message}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	code, _ := w.Error.Code.(string)
	switch {
	case code == "context_length_exceeded":
		e.Kind = ir.ErrContextLength
	case code == "insufficient_quota":
		e.Kind = ir.ErrQuotaExhausted
	case status == 400 || status == 422:
		e.Kind = ir.ErrInvalidRequest
	case status == 401:
		e.Kind = ir.ErrAuthentication
	case status == 403:
		e.Kind = ir.ErrPermission
	case status == 404:
		e.Kind = ir.ErrNotFound
	case status == 429:
		e.Kind = ir.ErrRateLimited
	case status == 503 || status == 529:
		e.Kind = ir.ErrOverloaded
	case status == 504 || status == 408:
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

// EncodeError renders an OpenAI-style error body.
func EncodeError(e *ir.Error) []byte {
	typ := "api_error"
	var code any
	switch e.Kind {
	case ir.ErrInvalidRequest, ir.ErrUnsupported:
		typ = "invalid_request_error"
	case ir.ErrContextLength:
		typ, code = "invalid_request_error", "context_length_exceeded"
	case ir.ErrAuthentication:
		typ, code = "invalid_request_error", "invalid_api_key"
	case ir.ErrPermission, ir.ErrPolicy:
		typ = "permission_error"
	case ir.ErrRateLimited:
		typ, code = "rate_limit_exceeded", "rate_limit_exceeded"
	case ir.ErrQuotaExhausted, ir.ErrBudget:
		typ, code = "insufficient_quota", "insufficient_quota"
	case ir.ErrNotFound:
		typ, code = "invalid_request_error", "model_not_found"
	}
	return wire.MustMarshal(map[string]any{"error": map[string]any{"message": e.Message, "type": typ, "param": nil, "code": code}})
}

// ---------- streaming ----------

// StreamDecoder converts upstream chunks into IR events.
type StreamDecoder struct {
	started   bool
	reasonIdx int
	textIdx   int
	tools     map[int]int // upstream tool index -> part index
	next      int
	finish    string
	done      bool
}

// NewStreamDecoder creates a decoder.
func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{tools: map[int]int{}, reasonIdx: -1, textIdx: -1}
}

// Decode converts one SSE data payload ("[DONE]" ends the stream).
func (d *StreamDecoder) Decode(data []byte) (out []ir.Event, unknown string, err error) {
	if strings.TrimSpace(string(data)) == "[DONE]" {
		return d.Close(), "", nil
	}
	var w struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int         `json:"index"`
			Delta        wireMessage `json:"delta"`
			FinishReason *string     `json:"finish_reason"`
		} `json:"choices"`
		Usage *wireUsage      `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, "", &ir.Error{Kind: ir.ErrProtocol, Message: "malformed stream chunk"}
	}
	if len(w.Error) > 0 {
		e := ParseError(0, append(append([]byte(`{"error":`), w.Error...), '}'), http.Header{})
		return []ir.Event{{Kind: ir.EvError, Err: e}}, "", nil
	}
	if !d.started {
		d.started = true
		out = append(out, ir.Event{Kind: ir.EvStart, ResponseID: w.ID, Model: w.Model})
	}
	for _, c := range w.Choices {
		if c.Index != 0 {
			continue
		}
		dl := c.Delta
		if rc := firstNonNil(dl.ReasoningContent, dl.Reasoning); rc != nil && *rc != "" {
			if d.reasonIdx < 0 {
				d.reasonIdx = d.next
				d.next++
				out = append(out, ir.Event{Kind: ir.EvPartStart, Index: d.reasonIdx, Part: &ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{}}})
			}
			out = append(out, ir.Event{Kind: ir.EvDelta, Index: d.reasonIdx, Delta: *rc})
		}
		if dl.Content != nil && *dl.Content != "" {
			if d.textIdx < 0 {
				out = append(out, d.closeReasoning()...)
				d.textIdx = d.next
				d.next++
				out = append(out, ir.Event{Kind: ir.EvPartStart, Index: d.textIdx, Part: &ir.Part{Kind: ir.PartText}})
			}
			out = append(out, ir.Event{Kind: ir.EvDelta, Index: d.textIdx, Delta: *dl.Content})
		}
		if dl.Refusal != nil && *dl.Refusal != "" {
			idx := d.next
			d.next++
			out = append(out, ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &ir.Part{Kind: ir.PartRefusal}},
				ir.Event{Kind: ir.EvDelta, Index: idx, Delta: *dl.Refusal}, ir.Event{Kind: ir.EvPartStop, Index: idx})
		}
		for _, tc := range dl.ToolCalls {
			ti := 0
			if tc.Index != nil {
				ti = *tc.Index
			}
			idx, ok := d.tools[ti]
			if !ok {
				out = append(out, d.closeReasoning()...)
				idx = d.next
				d.next++
				d.tools[ti] = idx
				call := &ir.ToolCall{ID: tc.ID, Name: tc.Function.Name, Kind: "function"}
				if tc.Type == "custom" {
					call.Name, call.Kind = tc.Custom.Name, "custom"
				}
				out = append(out, ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &ir.Part{Kind: ir.PartToolCall, ToolCall: call}})
			}
			args := tc.Function.Arguments
			if tc.Type == "custom" {
				args = tc.Custom.Input
			}
			if args != "" {
				out = append(out, ir.Event{Kind: ir.EvDelta, Index: idx, Delta: args})
			}
		}
		if c.FinishReason != nil && *c.FinishReason != "" {
			d.finish = *c.FinishReason
		}
	}
	if u := w.Usage.toIR(); u != nil {
		out = append(out, ir.Event{Kind: ir.EvUsage, Usage: u})
	}
	return out, "", nil
}

func (d *StreamDecoder) closeReasoning() []ir.Event {
	if d.reasonIdx >= 0 && d.textIdx < 0 && len(d.tools) == 0 {
		return []ir.Event{{Kind: ir.EvPartStop, Index: d.reasonIdx}}
	}
	return nil
}

// Close finalizes the stream (on [DONE] or upstream EOF).
func (d *StreamDecoder) Close() []ir.Event {
	if d.done {
		return nil
	}
	d.done = true
	var out []ir.Event
	if d.reasonIdx >= 0 {
		out = append(out, ir.Event{Kind: ir.EvPartStop, Index: d.reasonIdx})
	}
	if d.textIdx >= 0 {
		out = append(out, ir.Event{Kind: ir.EvPartStop, Index: d.textIdx})
	}
	for _, idx := range d.tools {
		out = append(out, ir.Event{Kind: ir.EvPartStop, Index: idx})
	}
	if d.finish != "" {
		out = append(out, ir.Event{Kind: ir.EvStop, StopReason: FinishToIR(d.finish)})
	}
	return out
}

// Finished reports whether a finish reason or [DONE] was seen.
func (d *StreamDecoder) Finished() bool { return d.finish != "" || d.done }

// StreamEncoder renders IR events as chat.completion.chunk SSE data.
type StreamEncoder struct {
	ID           string
	Model        string
	Created      time.Time
	IncludeUsage bool
	roleSent     bool
	tools        map[int]int // part index -> tool_calls index
	kinds        map[int]ir.PartKind
	nextTool     int
	usage        ir.Usage
	usageKnown   bool
	stopped      bool
}

// NewStreamEncoder creates an encoder.
func NewStreamEncoder(id, model string, created time.Time, includeUsage bool) *StreamEncoder {
	return &StreamEncoder{ID: chatID(id), Model: model, Created: created, IncludeUsage: includeUsage, tools: map[int]int{}, kinds: map[int]ir.PartKind{}}
}

func (e *StreamEncoder) chunk(delta map[string]any, finish any) []byte {
	if !e.roleSent {
		delta["role"] = "assistant"
		e.roleSent = true
	}
	return wire.MustMarshal(map[string]any{"id": e.ID, "object": "chat.completion.chunk", "created": e.Created.Unix(), "model": e.Model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
}

// Encode converts one IR event into zero or more data payloads.
func (e *StreamEncoder) Encode(ev ir.Event) [][]byte {
	switch ev.Kind {
	case ir.EvStart:
		return [][]byte{e.chunk(map[string]any{"content": ""}, nil)}
	case ir.EvPartStart:
		e.kinds[ev.Index] = ev.Part.Kind
		if ev.Part.Kind == ir.PartToolCall {
			ti := e.nextTool
			e.nextTool++
			e.tools[ev.Index] = ti
			tc := ev.Part.ToolCall
			call := map[string]any{"index": ti, "id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": ""}}
			if tc.Kind == "custom" {
				call = map[string]any{"index": ti, "id": tc.ID, "type": "custom", "custom": map[string]any{"name": tc.Name, "input": ""}}
			}
			return [][]byte{e.chunk(map[string]any{"tool_calls": []any{call}}, nil)}
		}
	case ir.EvDelta:
		switch e.kinds[ev.Index] {
		case ir.PartText:
			return [][]byte{e.chunk(map[string]any{"content": ev.Delta}, nil)}
		case ir.PartRefusal:
			return [][]byte{e.chunk(map[string]any{"refusal": ev.Delta}, nil)}
		case ir.PartReasoning:
			return [][]byte{e.chunk(map[string]any{"reasoning_content": ev.Delta}, nil)}
		case ir.PartToolCall:
			ti := e.tools[ev.Index]
			return [][]byte{e.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": ti, "function": map[string]any{"arguments": ev.Delta}}}}, nil)}
		}
	case ir.EvUsage:
		if ev.Usage != nil {
			e.usage.Merge(*ev.Usage)
			e.usageKnown = true
		}
	case ir.EvStop:
		return e.Finish(ev.StopReason)
	case ir.EvError:
		e.stopped = true
		return [][]byte{EncodeError(ev.Err)}
	}
	return nil
}

// Finish emits the final chunk(s) and [DONE].
func (e *StreamEncoder) Finish(stop ir.StopReason) [][]byte {
	if e.stopped {
		return nil
	}
	e.stopped = true
	out := [][]byte{e.chunk(map[string]any{}, FinishFromIR(stop))}
	if e.IncludeUsage {
		out = append(out, wire.MustMarshal(map[string]any{"id": e.ID, "object": "chat.completion.chunk", "created": e.Created.Unix(),
			"model": e.Model, "choices": []any{}, "usage": usageWire(e.usage)}))
	}
	return append(out, []byte("[DONE]"))
}

// Stopped reports whether the stream ended.
func (e *StreamEncoder) Stopped() bool { return e.stopped }
