package openairesponses

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

type wireUsage struct {
	InputTokens        *int64 `json:"input_tokens"`
	OutputTokens       *int64 `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (u *wireUsage) toIR() *ir.Usage {
	if u == nil {
		return nil
	}
	out := &ir.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, InputIncludesCache: true, OutputIncludesReasoning: true}
	if u.InputTokensDetails != nil {
		out.CacheReadTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		out.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return out
}

func usageWire(u ir.Usage) map[string]any {
	in := ir.Val(u.InputTokens)
	if !u.InputIncludesCache {
		in += ir.Val(u.CacheReadTokens) + ir.Val(u.CacheWriteTokens)
	}
	out := ir.Val(u.OutputTokens)
	return map[string]any{"input_tokens": in, "output_tokens": out, "total_tokens": in + out,
		"input_tokens_details":  map[string]any{"cached_tokens": ir.Val(u.CacheReadTokens)},
		"output_tokens_details": map[string]any{"reasoning_tokens": ir.Val(u.ReasoningTokens)}}
}

type wireResponse struct {
	ID                string            `json:"id"`
	Model             string            `json:"model"`
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	Usage             *wireUsage        `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (w *wireResponse) toIR() (*ir.Response, error) {
	r := &ir.Response{ID: w.ID, Model: w.Model}
	if u := w.Usage.toIR(); u != nil {
		r.Usage = *u
	}
	hasCall := false
	for _, raw := range w.Output {
		parts, err := outputItemParts(raw)
		if err != nil {
			return nil, err
		}
		for _, p := range parts {
			if p.Kind == ir.PartToolCall {
				hasCall = true
			}
		}
		r.Parts = append(r.Parts, parts...)
	}
	r.StopReason = stopFor(w.Status, w.IncompleteDetails, hasCall)
	return r, nil
}

func stopFor(status string, inc *struct {
	Reason string `json:"reason"`
}, hasCall bool) ir.StopReason {
	switch status {
	case "incomplete":
		if inc != nil && inc.Reason == "content_filter" {
			return ir.StopContentFilter
		}
		return ir.StopMaxTokens
	case "failed":
		return ir.StopError
	}
	if hasCall {
		return ir.StopToolUse
	}
	return ir.StopEndTurn
}

// outputItemParts converts one output item to IR parts.
func outputItemParts(raw json.RawMessage) ([]ir.Part, error) {
	tmp := &ir.Request{}
	if err := appendItem(tmp, raw); err != nil {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream output item: " + err.Error()}
	}
	var out []ir.Part
	for _, m := range tmp.Messages {
		out = append(out, m.Parts...)
	}
	return out, nil
}

// ParseResponse converts an upstream non-streaming response into IR.
func ParseResponse(body []byte) (*ir.Response, error) {
	var w wireResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream returned an unparseable response"}
	}
	if w.Error != nil && w.Status == "failed" {
		return nil, &ir.Error{Kind: ir.ErrUpstream, Message: w.Error.Message, UpstreamType: w.Error.Code}
	}
	return w.toIR()
}

// outputItems renders IR parts as Responses output items.
func outputItems(parts []ir.Part, idPrefix string) []any {
	var out []any
	var text []any
	n := 0
	flush := func() {
		if len(text) == 0 {
			return
		}
		out = append(out, map[string]any{"type": "message", "id": fmt.Sprintf("msg_%s_%d", idPrefix, n), "status": "completed", "role": "assistant", "content": text})
		n++
		text = nil
	}
	for _, p := range parts {
		switch p.Kind {
		case ir.PartText:
			text = append(text, map[string]any{"type": "output_text", "text": p.Text, "annotations": []any{}})
		case ir.PartRefusal:
			text = append(text, map[string]any{"type": "refusal", "refusal": p.Text})
		case ir.PartToolCall:
			flush()
			tc := p.ToolCall
			id := itemID(p)
			if id == "" {
				id = fmt.Sprintf("fc_%s_%d", idPrefix, n)
				n++
			}
			if tc.Kind == "custom" {
				out = append(out, map[string]any{"type": "custom_tool_call", "id": id, "call_id": tc.ID, "name": tc.Name, "input": tc.Arguments, "status": "completed"})
			} else {
				out = append(out, map[string]any{"type": "function_call", "id": id, "call_id": tc.ID, "name": tc.Name, "arguments": argsString(tc.Arguments), "status": "completed"})
			}
		case ir.PartReasoning:
			flush()
			it := map[string]any{"type": "reasoning", "summary": []any{}}
			if p.Sealed != nil && p.Sealed.Family == P {
				it["id"] = p.Sealed.ItemID
				if p.Sealed.Data != "" {
					it["encrypted_content"] = p.Sealed.Data
				}
			} else {
				it["id"] = fmt.Sprintf("rs_%s_%d", idPrefix, n)
				n++
			}
			if p.Reasoning != nil && p.Reasoning.Text != "" {
				it["summary"] = []any{map[string]any{"type": "summary_text", "text": p.Reasoning.Text}}
			}
			out = append(out, it)
		case ir.PartSealed:
			if p.Sealed != nil && p.Sealed.Family == P && strings.HasPrefix(p.Sealed.Kind, "native_item:") {
				flush()
				var raw map[string]any
				if json.Unmarshal([]byte(p.Sealed.Data), &raw) == nil {
					out = append(out, raw)
				}
			}
		}
	}
	flush()
	if out == nil {
		out = []any{}
	}
	return out
}

func respID(id string) string {
	if strings.HasPrefix(id, "resp_") {
		return id
	}
	if id == "" {
		return "resp_hp"
	}
	return "resp_" + id
}

func statusOf(s ir.StopReason) (string, any) {
	switch s {
	case ir.StopMaxTokens:
		return "incomplete", map[string]any{"reason": "max_output_tokens"}
	case ir.StopContentFilter:
		return "incomplete", map[string]any{"reason": "content_filter"}
	}
	return "completed", nil
}

// EncodeResponse renders an IR response as a Responses API object.
func EncodeResponse(r *ir.Response, model string, created time.Time) []byte {
	status, inc := statusOf(r.StopReason)
	id := respID(r.ID)
	return wire.MustMarshal(map[string]any{"id": id, "object": "response", "created_at": created.Unix(), "status": status, "model": model,
		"output": outputItems(r.Parts, strings.TrimPrefix(id, "resp_")), "incomplete_details": inc, "usage": usageWire(r.Usage), "error": nil})
}

// ParseError converts an upstream error response (same envelope as Chat).
func ParseError(status int, body []byte, h http.Header) *ir.Error {
	return openaichat.ParseError(status, body, h)
}

// EncodeError renders an error body (same envelope as Chat).
func EncodeError(e *ir.Error) []byte { return openaichat.EncodeError(e) }

// ---------- streaming ----------

// StreamDecoder converts upstream Responses events into IR events.
type StreamDecoder struct {
	items    map[int]itemState // output_index -> state
	next     int
	hasCall  bool
	finished bool
}

type itemState struct {
	idx     int
	kind    ir.PartKind
	itemID  string
	started bool
}

// NewStreamDecoder creates a decoder.
func NewStreamDecoder() *StreamDecoder { return &StreamDecoder{items: map[int]itemState{}} }

// Decode converts one event payload (the "type" field names the event).
func (d *StreamDecoder) Decode(data []byte) (out []ir.Event, unknown string, err error) {
	var head struct {
		Type        string          `json:"type"`
		OutputIndex int             `json:"output_index"`
		Delta       string          `json:"delta"`
		Item        json.RawMessage `json:"item"`
		Response    *wireResponse   `json:"response"`
		Code        string          `json:"code"`
		Message     string          `json:"message"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, "", &ir.Error{Kind: ir.ErrProtocol, Message: "malformed stream event"}
	}
	switch head.Type {
	case "response.created":
		if head.Response != nil {
			return []ir.Event{{Kind: ir.EvStart, ResponseID: head.Response.ID, Model: head.Response.Model}}, "", nil
		}
		return []ir.Event{{Kind: ir.EvStart}}, "", nil
	case "response.in_progress", "response.queued", "response.content_part.added", "response.content_part.done",
		"response.output_text.done", "response.function_call_arguments.done", "response.reasoning_summary_part.added",
		"response.reasoning_summary_part.done", "response.reasoning_summary_text.done", "response.refusal.done",
		"response.output_text.annotation.added", "response.custom_tool_call_input.done":
		return nil, "", nil
	case "response.output_item.added":
		var it struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
		}
		_ = json.Unmarshal(head.Item, &it)
		st := itemState{idx: d.next, itemID: it.ID}
		d.next++
		var part *ir.Part
		switch it.Type {
		case "message":
			st.kind = ir.PartText
			part = &ir.Part{Kind: ir.PartText}
		case "function_call", "custom_tool_call":
			st.kind = ir.PartToolCall
			d.hasCall = true
			kind := "function"
			if it.Type == "custom_tool_call" {
				kind = "custom"
			}
			part = &ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: it.CallID, Name: it.Name, Kind: kind},
				Ext: map[string]json.RawMessage{P: wire.MustMarshal(map[string]string{"item_id": it.ID})}}
		case "reasoning":
			st.kind = ir.PartReasoning
			part = &ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Summary: true}}
		default:
			// Native items are emitted whole on output_item.done.
			st.kind = ir.PartSealed
			d.items[head.OutputIndex] = st
			return nil, "item:" + it.Type, nil
		}
		st.started = true
		d.items[head.OutputIndex] = st
		return []ir.Event{{Kind: ir.EvPartStart, Index: st.idx, Part: part}}, "", nil
	case "response.output_text.delta", "response.refusal.delta", "response.function_call_arguments.delta",
		"response.reasoning_summary_text.delta", "response.custom_tool_call_input.delta":
		st, ok := d.items[head.OutputIndex]
		if !ok || !st.started {
			return nil, "", nil
		}
		return []ir.Event{{Kind: ir.EvDelta, Index: st.idx, Delta: head.Delta}}, "", nil
	case "response.output_item.done":
		st, ok := d.items[head.OutputIndex]
		if !ok {
			return nil, "", nil
		}
		if st.kind == ir.PartSealed {
			parts, err := outputItemParts(head.Item)
			if err != nil || len(parts) == 0 {
				// A malformed native item is reported to Protocol Watch; it is
				// not fatal for the rest of the stream.
				return nil, "item_unparseable", nil //nolint:nilerr // reported via unknown
			}
			return []ir.Event{{Kind: ir.EvPartStart, Index: st.idx, Part: &parts[0]}, {Kind: ir.EvPartStop, Index: st.idx}}, "", nil
		}
		ev := ir.Event{Kind: ir.EvPartStop, Index: st.idx}
		if st.kind == ir.PartReasoning {
			var it struct {
				ID               string `json:"id"`
				EncryptedContent string `json:"encrypted_content"`
			}
			_ = json.Unmarshal(head.Item, &it)
			ev.Part = &ir.Part{Kind: ir.PartReasoning, Sealed: &ir.Sealed{Family: P, Kind: "reasoning_item", Data: it.EncryptedContent, ItemID: it.ID}}
		}
		return []ir.Event{ev}, "", nil
	case "response.completed", "response.incomplete", "response.failed":
		d.finished = true
		var out []ir.Event
		if head.Response != nil {
			if u := head.Response.Usage.toIR(); u != nil {
				out = append(out, ir.Event{Kind: ir.EvUsage, Usage: u})
			}
			if head.Type == "response.failed" {
				msg := "upstream response failed"
				typ := ""
				if head.Response.Error != nil {
					msg, typ = head.Response.Error.Message, head.Response.Error.Code
				}
				return append(out, ir.Event{Kind: ir.EvError, Err: &ir.Error{Kind: ir.ErrUpstream, Message: msg, UpstreamType: typ}}), "", nil
			}
			out = append(out, ir.Event{Kind: ir.EvStop, StopReason: stopFor(head.Response.Status, head.Response.IncompleteDetails, d.hasCall)})
		}
		return out, "", nil
	case "error":
		return []ir.Event{{Kind: ir.EvError, Err: &ir.Error{Kind: ir.ErrUpstream, Message: head.Message, UpstreamType: head.Code}}}, "", nil
	}
	return nil, "event:" + head.Type, nil
}

// Finished reports whether a terminal event was seen.
func (d *StreamDecoder) Finished() bool { return d.finished }

// StreamEncoder renders IR events as Responses API SSE events.
type StreamEncoder struct {
	ID      string
	Model   string
	Created time.Time
	seq     int
	started bool
	stopped bool
	items   map[int]*encItem // part index -> item
	nextOut int
	parts   []ir.Part // accumulated final output for response.completed
	usage   ir.Usage
	hasCall bool
}

type encItem struct {
	outIdx int
	id     string
	kind   ir.PartKind
	buf    strings.Builder
	part   ir.Part
}

// NewStreamEncoder creates an encoder.
func NewStreamEncoder(id, model string, created time.Time) *StreamEncoder {
	return &StreamEncoder{ID: respID(id), Model: model, Created: created, items: map[int]*encItem{}}
}

// SSE is one encoded event.
type SSE struct {
	Event string
	Data  []byte
}

func (e *StreamEncoder) emit(typ string, fields map[string]any) SSE {
	fields["type"] = typ
	fields["sequence_number"] = e.seq
	e.seq++
	return SSE{Event: typ, Data: wire.MustMarshal(fields)}
}

func (e *StreamEncoder) responseObj(status string) map[string]any {
	st, inc := status, any(nil)
	return map[string]any{"id": e.ID, "object": "response", "created_at": e.Created.Unix(), "status": st, "model": e.Model,
		"output": []any{}, "incomplete_details": inc, "usage": nil, "error": nil}
}

// Start emits response.created/in_progress once.
func (e *StreamEncoder) Start() []SSE {
	if e.started {
		return nil
	}
	e.started = true
	return []SSE{e.emit("response.created", map[string]any{"response": e.responseObj("in_progress")}),
		e.emit("response.in_progress", map[string]any{"response": e.responseObj("in_progress")})}
}

// Encode converts one IR event.
func (e *StreamEncoder) Encode(ev ir.Event) []SSE {
	out := e.Start()
	switch ev.Kind {
	case ir.EvPartStart:
		p := *ev.Part
		it := &encItem{outIdx: e.nextOut, kind: p.Kind, part: p}
		e.nextOut++
		short := strings.TrimPrefix(e.ID, "resp_")
		switch p.Kind {
		case ir.PartText, ir.PartRefusal:
			it.id = fmt.Sprintf("msg_%s_%d", short, it.outIdx)
			e.items[ev.Index] = it
			ctype := "output_text"
			if p.Kind == ir.PartRefusal {
				ctype = "refusal"
			}
			out = append(out,
				e.emit("response.output_item.added", map[string]any{"output_index": it.outIdx, "item": map[string]any{"type": "message", "id": it.id, "status": "in_progress", "role": "assistant", "content": []any{}}}),
				e.emit("response.content_part.added", map[string]any{"item_id": it.id, "output_index": it.outIdx, "content_index": 0, "part": contentStub(ctype)}))
		case ir.PartToolCall:
			e.hasCall = true
			it.id = itemID(p)
			if it.id == "" {
				it.id = fmt.Sprintf("fc_%s_%d", short, it.outIdx)
			}
			e.items[ev.Index] = it
			typ := "function_call"
			item := map[string]any{"type": typ, "id": it.id, "call_id": p.ToolCall.ID, "name": p.ToolCall.Name, "arguments": "", "status": "in_progress"}
			if p.ToolCall.Kind == "custom" {
				item = map[string]any{"type": "custom_tool_call", "id": it.id, "call_id": p.ToolCall.ID, "name": p.ToolCall.Name, "input": "", "status": "in_progress"}
			}
			out = append(out, e.emit("response.output_item.added", map[string]any{"output_index": it.outIdx, "item": item}))
		case ir.PartReasoning:
			it.id = fmt.Sprintf("rs_%s_%d", short, it.outIdx)
			e.items[ev.Index] = it
			out = append(out,
				e.emit("response.output_item.added", map[string]any{"output_index": it.outIdx, "item": map[string]any{"type": "reasoning", "id": it.id, "summary": []any{}}}),
				e.emit("response.reasoning_summary_part.added", map[string]any{"item_id": it.id, "output_index": it.outIdx, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}))
		case ir.PartSealed:
			if p.Sealed != nil && p.Sealed.Family == P && strings.HasPrefix(p.Sealed.Kind, "native_item:") {
				var raw map[string]any
				if json.Unmarshal([]byte(p.Sealed.Data), &raw) == nil {
					out = append(out, e.emit("response.output_item.added", map[string]any{"output_index": it.outIdx, "item": raw}),
						e.emit("response.output_item.done", map[string]any{"output_index": it.outIdx, "item": raw}))
					e.parts = append(e.parts, p)
				}
			}
			return out
		default:
			return out
		}
	case ir.EvDelta:
		it, ok := e.items[ev.Index]
		if !ok {
			return out
		}
		it.buf.WriteString(ev.Delta)
		switch it.kind {
		case ir.PartText:
			out = append(out, e.emit("response.output_text.delta", map[string]any{"item_id": it.id, "output_index": it.outIdx, "content_index": 0, "delta": ev.Delta, "logprobs": []any{}}))
		case ir.PartRefusal:
			out = append(out, e.emit("response.refusal.delta", map[string]any{"item_id": it.id, "output_index": it.outIdx, "content_index": 0, "delta": ev.Delta}))
		case ir.PartToolCall:
			name := "response.function_call_arguments.delta"
			if it.part.ToolCall.Kind == "custom" {
				name = "response.custom_tool_call_input.delta"
			}
			out = append(out, e.emit(name, map[string]any{"item_id": it.id, "output_index": it.outIdx, "delta": ev.Delta}))
		case ir.PartReasoning:
			out = append(out, e.emit("response.reasoning_summary_text.delta", map[string]any{"item_id": it.id, "output_index": it.outIdx, "summary_index": 0, "delta": ev.Delta}))
		}
	case ir.EvPartStop:
		it, ok := e.items[ev.Index]
		if !ok {
			return out
		}
		delete(e.items, ev.Index)
		out = append(out, e.closeItem(it, ev.Part)...)
	case ir.EvUsage:
		if ev.Usage != nil {
			e.usage.Merge(*ev.Usage)
		}
	case ir.EvStop:
		out = append(out, e.Finish(ev.StopReason)...)
	case ir.EvError:
		e.stopped = true
		out = append(out, e.emit("response.failed", map[string]any{"response": func() map[string]any {
			r := e.responseObj("failed")
			r["error"] = map[string]any{"code": "server_error", "message": ev.Err.Message}
			return r
		}()}))
	}
	return out
}

func contentStub(ctype string) map[string]any {
	if ctype == "refusal" {
		return map[string]any{"type": "refusal", "refusal": ""}
	}
	return map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
}

func (e *StreamEncoder) closeItem(it *encItem, final *ir.Part) []SSE {
	var out []SSE
	text := it.buf.String()
	switch it.kind {
	case ir.PartText, ir.PartRefusal:
		ctype, field := "output_text", "text"
		if it.kind == ir.PartRefusal {
			ctype, field = "refusal", "refusal"
		}
		done := map[string]any{"type": ctype, field: text}
		if ctype == "output_text" {
			done["annotations"] = []any{}
			out = append(out, e.emit("response.output_text.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "content_index": 0, "text": text, "logprobs": []any{}}))
		} else {
			out = append(out, e.emit("response.refusal.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "content_index": 0, "refusal": text}))
		}
		out = append(out, e.emit("response.content_part.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "content_index": 0, "part": done}),
			e.emit("response.output_item.done", map[string]any{"output_index": it.outIdx, "item": map[string]any{"type": "message", "id": it.id, "status": "completed", "role": "assistant", "content": []any{done}}}))
		e.parts = append(e.parts, ir.Part{Kind: it.kind, Text: text})
	case ir.PartToolCall:
		tc := *it.part.ToolCall
		tc.Arguments = text
		if tc.Kind == "custom" {
			out = append(out, e.emit("response.custom_tool_call_input.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "input": text}),
				e.emit("response.output_item.done", map[string]any{"output_index": it.outIdx, "item": map[string]any{"type": "custom_tool_call", "id": it.id, "call_id": tc.ID, "name": tc.Name, "input": text, "status": "completed"}}))
		} else {
			out = append(out, e.emit("response.function_call_arguments.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "arguments": argsString(text)}),
				e.emit("response.output_item.done", map[string]any{"output_index": it.outIdx, "item": map[string]any{"type": "function_call", "id": it.id, "call_id": tc.ID, "name": tc.Name, "arguments": argsString(text), "status": "completed"}}))
		}
		p := ir.Part{Kind: ir.PartToolCall, ToolCall: &tc, Ext: map[string]json.RawMessage{P: wire.MustMarshal(map[string]string{"item_id": it.id})}}
		e.parts = append(e.parts, p)
	case ir.PartReasoning:
		item := map[string]any{"type": "reasoning", "id": it.id, "summary": []any{map[string]any{"type": "summary_text", "text": text}}}
		p := ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Text: text, Summary: true}}
		if final != nil && final.Sealed != nil && final.Sealed.Family == P {
			item["id"] = final.Sealed.ItemID
			if final.Sealed.Data != "" {
				item["encrypted_content"] = final.Sealed.Data
			}
			p.Sealed = final.Sealed
		}
		out = append(out, e.emit("response.reasoning_summary_text.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "summary_index": 0, "text": text}),
			e.emit("response.reasoning_summary_part.done", map[string]any{"item_id": it.id, "output_index": it.outIdx, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": text}}),
			e.emit("response.output_item.done", map[string]any{"output_index": it.outIdx, "item": item}))
		e.parts = append(e.parts, p)
	}
	return out
}

// Finish closes open items and emits response.completed/incomplete.
func (e *StreamEncoder) Finish(stop ir.StopReason) []SSE {
	if e.stopped {
		return nil
	}
	out := e.Start()
	for idx, it := range e.items {
		out = append(out, e.closeItem(it, nil)...)
		delete(e.items, idx)
	}
	e.stopped = true
	if stop == ir.StopEndTurn && e.hasCall {
		stop = ir.StopToolUse
	}
	status, inc := statusOf(stop)
	r := e.responseObj(status)
	r["incomplete_details"] = inc
	r["output"] = outputItems(e.parts, strings.TrimPrefix(e.ID, "resp_"))
	r["usage"] = usageWire(e.usage)
	name := "response.completed"
	if status == "incomplete" {
		name = "response.incomplete"
	}
	return append(out, e.emit(name, map[string]any{"response": r}))
}

// Stopped reports whether the stream ended.
func (e *StreamEncoder) Stopped() bool { return e.stopped }
