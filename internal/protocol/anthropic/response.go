package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

type wireUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
}

func (u wireUsage) toIR() ir.Usage {
	return ir.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens, InputIncludesCache: false, OutputIncludesReasoning: true}
}

func usageWire(u ir.Usage) map[string]any {
	m := map[string]any{}
	if v := u.UncachedInput(); v != nil {
		m["input_tokens"] = *v
	} else {
		m["input_tokens"] = 0
	}
	m["output_tokens"] = ir.Val(u.OutputTokens)
	if u.CacheWriteTokens != nil {
		m["cache_creation_input_tokens"] = *u.CacheWriteTokens
	}
	if u.CacheReadTokens != nil {
		m["cache_read_input_tokens"] = *u.CacheReadTokens
	}
	return m
}

// StopToIR maps Anthropic stop reasons.
func StopToIR(s string) ir.StopReason {
	switch s {
	case "end_turn":
		return ir.StopEndTurn
	case "max_tokens", "model_context_window_exceeded":
		return ir.StopMaxTokens
	case "stop_sequence":
		return ir.StopSequence
	case "tool_use":
		return ir.StopToolUse
	case "pause_turn":
		return ir.StopPause
	case "refusal":
		return ir.StopRefusal
	case "":
		return ir.StopUnknown
	}
	return ir.StopReason(s)
}

// StopFromIR maps canonical stop reasons to Anthropic values.
func StopFromIR(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "max_tokens"
	case ir.StopSequence:
		return "stop_sequence"
	case ir.StopToolUse:
		return "tool_use"
	case ir.StopPause:
		return "pause_turn"
	case ir.StopRefusal, ir.StopContentFilter:
		return "refusal"
	}
	return "end_turn"
}

// ParseResponse converts an upstream non-streaming response into IR.
func ParseResponse(body []byte) (*ir.Response, error) {
	var w struct {
		ID           string            `json:"id"`
		Model        string            `json:"model"`
		Content      []json.RawMessage `json:"content"`
		StopReason   string            `json:"stop_reason"`
		StopSequence string            `json:"stop_sequence"`
		Usage        wireUsage         `json:"usage"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream returned an unparseable response"}
	}
	r := &ir.Response{ID: w.ID, Model: w.Model, StopReason: StopToIR(w.StopReason), StopSeq: w.StopSequence, Usage: w.Usage.toIR()}
	for _, raw := range w.Content {
		p, err := parseBlock(raw)
		if err != nil {
			return nil, &ir.Error{Kind: ir.ErrProtocol, Message: "upstream content block: " + err.Error()}
		}
		r.Parts = append(r.Parts, *p)
	}
	return r, nil
}

// EncodeResponse renders an IR response as an Anthropic Messages response.
func EncodeResponse(r *ir.Response, model string) []byte {
	content := []any{}
	var l ir.Losses
	for _, p := range r.Parts {
		if p.Kind == ir.PartReasoning && (p.Sealed == nil || p.Sealed.Family != "anthropic") {
			// Exposed reasoning from another provider: present as thinking
			// text without a signature only if it came with no seal; clients
			// must not replay it as signed thinking, so it is omitted.
			continue
		}
		if b, ok := buildBlock(p, &l); ok {
			content = append(content, b)
		}
	}
	out := map[string]any{"id": responseID(r.ID), "type": "message", "role": "assistant", "model": model, "content": content,
		"stop_reason": StopFromIR(r.StopReason), "stop_sequence": nullStr(r.StopSeq), "usage": usageWire(r.Usage)}
	return wire.MustMarshal(out)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func responseID(id string) string {
	if strings.HasPrefix(id, "msg_") {
		return id
	}
	if id == "" {
		return "msg_hp"
	}
	return "msg_" + id
}

// ---------- errors ----------

// ParseError converts an upstream error response.
func ParseError(status int, body []byte, h http.Header) *ir.Error {
	var w struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &w)
	e := &ir.Error{HTTPStatus: status, UpstreamType: w.Error.Type, Message: w.Error.Message}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	switch {
	case w.Error.Type == "invalid_request_error" && strings.Contains(strings.ToLower(w.Error.Message), "prompt is too long"):
		e.Kind = ir.ErrContextLength
	case w.Error.Type == "invalid_request_error" || status == 400 || status == 413:
		e.Kind = ir.ErrInvalidRequest
	case w.Error.Type == "authentication_error" || status == 401:
		e.Kind = ir.ErrAuthentication
	case w.Error.Type == "permission_error" || status == 403:
		e.Kind = ir.ErrPermission
	case w.Error.Type == "not_found_error" || status == 404:
		e.Kind = ir.ErrNotFound
	case w.Error.Type == "rate_limit_error" || status == 429:
		e.Kind = ir.ErrRateLimited
	case w.Error.Type == "overloaded_error" || status == 529:
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

// ErrorType maps canonical kinds to Anthropic error types.
func ErrorType(k ir.ErrorKind) string {
	switch k {
	case ir.ErrInvalidRequest, ir.ErrContextLength, ir.ErrUnsupported:
		return "invalid_request_error"
	case ir.ErrAuthentication:
		return "authentication_error"
	case ir.ErrPermission, ir.ErrPolicy, ir.ErrContentFilter:
		return "permission_error"
	case ir.ErrNotFound:
		return "not_found_error"
	case ir.ErrRateLimited, ir.ErrQuotaExhausted, ir.ErrBudget:
		return "rate_limit_error"
	case ir.ErrOverloaded:
		return "overloaded_error"
	}
	return "api_error"
}

// EncodeError renders an error body.
func EncodeError(e *ir.Error) []byte {
	return wire.MustMarshal(map[string]any{"type": "error", "error": map[string]any{"type": ErrorType(e.Kind), "message": e.Message}})
}

// ---------- streaming ----------

// StreamDecoder converts upstream SSE events into IR events.
type StreamDecoder struct {
	blocks map[int]string // index -> block type
	sigs   map[int]string // pending thinking signatures
	text   map[int]*strings.Builder
}

// NewStreamDecoder creates a decoder.
func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{blocks: map[int]string{}, sigs: map[int]string{}, text: map[int]*strings.Builder{}}
}

// Decode converts one SSE event. Unknown event types are returned in
// unknown (for Protocol Watch) and otherwise ignored.
func (d *StreamDecoder) Decode(event string, data []byte) (out []ir.Event, unknown string, err error) {
	switch event {
	case "ping":
		return nil, "", nil
	case "message_start":
		var w struct {
			Message struct {
				ID    string    `json:"id"`
				Model string    `json:"model"`
				Usage wireUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, "", protoErr(event, err)
		}
		u := w.Message.Usage.toIR()
		return []ir.Event{{Kind: ir.EvStart, ResponseID: w.Message.ID, Model: w.Message.Model}, {Kind: ir.EvUsage, Usage: &u}}, "", nil
	case "content_block_start":
		var w struct {
			Index        int             `json:"index"`
			ContentBlock json.RawMessage `json:"content_block"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, "", protoErr(event, err)
		}
		var head struct {
			Type     string          `json:"type"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Data     string          `json:"data"`
			Input    json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(w.ContentBlock, &head); err != nil {
			return nil, "", protoErr(event, err)
		}
		d.blocks[w.Index] = head.Type
		switch head.Type {
		case "text":
			ev := []ir.Event{{Kind: ir.EvPartStart, Index: w.Index, Part: &ir.Part{Kind: ir.PartText}}}
			if head.Text != "" {
				ev = append(ev, ir.Event{Kind: ir.EvDelta, Index: w.Index, Delta: head.Text})
			}
			return ev, "", nil
		case "tool_use":
			return []ir.Event{{Kind: ir.EvPartStart, Index: w.Index, Part: &ir.Part{Kind: ir.PartToolCall,
				ToolCall: &ir.ToolCall{ID: head.ID, Name: head.Name, Kind: "function"}}}}, "", nil
		case "thinking":
			d.text[w.Index] = &strings.Builder{}
			d.text[w.Index].WriteString(head.Thinking)
			ev := []ir.Event{{Kind: ir.EvPartStart, Index: w.Index, Part: &ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{}}}}
			if head.Thinking != "" {
				ev = append(ev, ir.Event{Kind: ir.EvDelta, Index: w.Index, Delta: head.Thinking})
			}
			return ev, "", nil
		case "redacted_thinking":
			return []ir.Event{{Kind: ir.EvPartStart, Index: w.Index, Part: &ir.Part{Kind: ir.PartSealed,
				Sealed: &ir.Sealed{Family: "anthropic", Kind: "redacted_thinking", Data: head.Data}}}}, "", nil
		default:
			// Provider-executed blocks (server tools, results) are carried
			// natively; they arrive complete in content_block_start.
			return []ir.Event{{Kind: ir.EvPartStart, Index: w.Index, Part: &ir.Part{Kind: ir.PartSealed,
				Sealed: &ir.Sealed{Family: "anthropic", Kind: "native_block:" + head.Type, Data: string(w.ContentBlock)}}}}, "content_block:" + head.Type, nil
		}
	case "content_block_delta":
		var w struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, "", protoErr(event, err)
		}
		switch w.Delta.Type {
		case "text_delta":
			return []ir.Event{{Kind: ir.EvDelta, Index: w.Index, Delta: w.Delta.Text}}, "", nil
		case "input_json_delta":
			return []ir.Event{{Kind: ir.EvDelta, Index: w.Index, Delta: w.Delta.PartialJSON}}, "", nil
		case "thinking_delta":
			if b := d.text[w.Index]; b != nil {
				b.WriteString(w.Delta.Thinking)
			}
			return []ir.Event{{Kind: ir.EvDelta, Index: w.Index, Delta: w.Delta.Thinking}}, "", nil
		case "signature_delta":
			d.sigs[w.Index] += w.Delta.Signature
			return nil, "", nil
		}
		return nil, "delta:" + w.Delta.Type, nil
	case "content_block_stop":
		var w struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, "", protoErr(event, err)
		}
		ev := ir.Event{Kind: ir.EvPartStop, Index: w.Index}
		if sig := d.sigs[w.Index]; sig != "" {
			text := ""
			if b := d.text[w.Index]; b != nil {
				text = b.String()
			}
			ev.Part = &ir.Part{Kind: ir.PartReasoning, Sealed: &ir.Sealed{Family: "anthropic", Kind: "thinking_signature", Data: sig, Attached: text}}
			delete(d.sigs, w.Index)
		}
		delete(d.text, w.Index)
		return []ir.Event{ev}, "", nil
	case "message_delta":
		var w struct {
			Delta struct {
				StopReason   string `json:"stop_reason"`
				StopSequence string `json:"stop_sequence"`
			} `json:"delta"`
			Usage wireUsage `json:"usage"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, "", protoErr(event, err)
		}
		u := w.Usage.toIR()
		out := []ir.Event{{Kind: ir.EvUsage, Usage: &u}}
		if w.Delta.StopReason != "" {
			out = append(out, ir.Event{Kind: ir.EvStop, StopReason: StopToIR(w.Delta.StopReason), StopSeq: w.Delta.StopSequence})
		}
		return out, "", nil
	case "message_stop":
		return nil, "", nil
	case "error":
		var w struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &w)
		e := ParseError(0, data, http.Header{})
		e.UpstreamType = w.Error.Type
		return []ir.Event{{Kind: ir.EvError, Err: e}}, "", nil
	}
	return nil, "event:" + event, nil
}

func protoErr(event string, err error) error {
	return &ir.Error{Kind: ir.ErrProtocol, Message: fmt.Sprintf("malformed %s event: %v", event, err)}
}

// StreamEncoder renders IR events as Anthropic SSE events.
type StreamEncoder struct {
	// UpstreamFamily is the protocol family producing the stream. Thinking
	// blocks are emitted only for the anthropic family, whose signatures
	// make them replayable; other providers' reasoning is not re-labelled.
	UpstreamFamily string
	Model          string
	ID             string
	started        bool
	open           map[int]ir.PartKind
	usage          ir.Usage
	stopped        bool
	idxMap         map[int]int
	nextIndex      int
}

// NewStreamEncoder creates an encoder for a client stream.
func NewStreamEncoder(model, id string) *StreamEncoder {
	return &StreamEncoder{Model: model, ID: id, open: map[int]ir.PartKind{}, idxMap: map[int]int{}}
}

// SSE is one encoded event.
type SSE struct {
	Event string
	Data  []byte
}

func (e *StreamEncoder) ev(name string, v any) SSE {
	return SSE{Event: name, Data: wire.MustMarshal(v)}
}

// Start emits message_start if not yet sent.
func (e *StreamEncoder) Start() []SSE {
	if e.started {
		return nil
	}
	e.started = true
	return []SSE{e.ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": responseID(e.ID), "type": "message", "role": "assistant", "model": e.Model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usageWire(e.usage)}})}
}

func (e *StreamEncoder) index(i int) int {
	if v, ok := e.idxMap[i]; ok {
		return v
	}
	v := e.nextIndex
	e.nextIndex++
	e.idxMap[i] = v
	return v
}

// Encode converts one IR event.
func (e *StreamEncoder) Encode(ev ir.Event) []SSE {
	out := e.Start()
	switch ev.Kind {
	case ir.EvStart:
		if ev.ResponseID != "" && e.ID == "" {
			e.ID = ev.ResponseID
		}
	case ir.EvPartStart:
		p := ev.Part
		var block map[string]any
		switch p.Kind {
		case ir.PartText, ir.PartRefusal:
			block = map[string]any{"type": "text", "text": ""}
		case ir.PartToolCall:
			block = map[string]any{"type": "tool_use", "id": sanitizeToolID(p.ToolCall.ID), "name": p.ToolCall.Name, "input": map[string]any{}}
		case ir.PartReasoning:
			// Reasoning is only surfaced as Anthropic thinking when it can be
			// signed by its producer (anthropic family); foreign reasoning
			// text is not invented into thinking blocks.
			if p.Sealed == nil && !e.allowForeignThinking() {
				return out
			}
			block = map[string]any{"type": "thinking", "thinking": ""}
		case ir.PartSealed:
			if p.Sealed != nil && p.Sealed.Family == "anthropic" {
				if p.Sealed.Kind == "redacted_thinking" {
					block = map[string]any{"type": "redacted_thinking", "data": p.Sealed.Data}
				} else if strings.HasPrefix(p.Sealed.Kind, "native_block:") {
					_ = json.Unmarshal([]byte(p.Sealed.Data), &block)
				}
			}
			if block == nil {
				return out
			}
		default:
			return out
		}
		idx := e.index(ev.Index)
		e.open[ev.Index] = p.Kind
		out = append(out, e.ev("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": block}))
	case ir.EvDelta:
		kind, ok := e.open[ev.Index]
		if !ok {
			return out
		}
		idx := e.index(ev.Index)
		var delta map[string]any
		switch kind {
		case ir.PartText, ir.PartRefusal:
			delta = map[string]any{"type": "text_delta", "text": ev.Delta}
		case ir.PartToolCall:
			delta = map[string]any{"type": "input_json_delta", "partial_json": ev.Delta}
		case ir.PartReasoning:
			delta = map[string]any{"type": "thinking_delta", "thinking": ev.Delta}
		default:
			return out
		}
		out = append(out, e.ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": delta}))
	case ir.EvPartStop:
		kind, ok := e.open[ev.Index]
		if !ok {
			return out
		}
		idx := e.index(ev.Index)
		if kind == ir.PartReasoning && ev.Part != nil && ev.Part.Sealed != nil && ev.Part.Sealed.Family == "anthropic" {
			out = append(out, e.ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": "signature_delta", "signature": ev.Part.Sealed.Data}}))
		}
		delete(e.open, ev.Index)
		out = append(out, e.ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx}))
	case ir.EvUsage:
		if ev.Usage != nil {
			e.usage.Merge(*ev.Usage)
		}
	case ir.EvStop:
		out = append(out, e.Finish(ev.StopReason, ev.StopSeq)...)
	case ir.EvError:
		out = append(out, e.ev("error", map[string]any{"type": "error", "error": map[string]any{"type": ErrorType(ev.Err.Kind), "message": ev.Err.Message}}))
		e.stopped = true
	}
	return out
}

func (e *StreamEncoder) allowForeignThinking() bool { return e.UpstreamFamily == "anthropic" }

// Finish closes open blocks and emits message_delta + message_stop.
func (e *StreamEncoder) Finish(stop ir.StopReason, seq string) []SSE {
	if e.stopped {
		return nil
	}
	out := e.Start()
	for i := range e.open {
		out = append(out, e.ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": e.index(i)}))
		delete(e.open, i)
	}
	usage := map[string]any{"output_tokens": ir.Val(e.usage.OutputTokens)}
	if e.usage.InputTokens != nil {
		if v := e.usage.UncachedInput(); v != nil {
			usage["input_tokens"] = *v
		}
	}
	if e.usage.CacheReadTokens != nil {
		usage["cache_read_input_tokens"] = *e.usage.CacheReadTokens
	}
	if e.usage.CacheWriteTokens != nil {
		usage["cache_creation_input_tokens"] = *e.usage.CacheWriteTokens
	}
	out = append(out, e.ev("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": StopFromIR(stop), "stop_sequence": nullStr(seq)}, "usage": usage}))
	out = append(out, e.ev("message_stop", map[string]any{"type": "message_stop"}))
	e.stopped = true
	return out
}

// Ping returns a keepalive ping event.
func (e *StreamEncoder) Ping() SSE { return e.ev("ping", map[string]any{"type": "ping"}) }

// Stopped reports whether the stream has been terminated.
func (e *StreamEncoder) Stopped() bool { return e.stopped }
