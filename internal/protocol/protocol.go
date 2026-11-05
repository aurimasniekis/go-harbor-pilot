// Package protocol provides uniform access to the wire-protocol adapters:
// inbound (client-facing) parsing and encoding, and outbound (upstream)
// building and decoding.
package protocol

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// ClientStream writes IR events to a client in its protocol.
type ClientStream interface {
	// Event writes one IR event (it may produce zero or more SSE events).
	Event(ev ir.Event) error
	// Finish terminates a successful stream (idempotent).
	Finish(stop ir.StopReason) error
	// Fail terminates the stream with an in-band error (idempotent).
	Fail(e *ir.Error) error
	// Keepalive writes protocol-appropriate keepalive bytes.
	Keepalive() error
	// Done reports whether the stream has been terminated.
	Done() bool
}

// StreamMeta seeds client stream encoders.
type StreamMeta struct {
	ResponseID     string
	Model          string // model name echoed to the client (the requested one)
	Created        time.Time
	IncludeUsage   bool   // chat: stream_options.include_usage
	UpstreamFamily string // family producing the events
}

// NewClientStream returns a stream encoder for an inbound protocol.
func NewClientStream(p ir.Protocol, w *wire.SSEWriter, m StreamMeta) ClientStream {
	return newSerial(newClientStream(p, w, m))
}

func newClientStream(p ir.Protocol, w *wire.SSEWriter, m StreamMeta) ClientStream {
	switch p {
	case ir.AnthropicMessages:
		enc := anthropic.NewStreamEncoder(m.Model, m.ResponseID)
		enc.UpstreamFamily = m.UpstreamFamily
		return &anthropicStream{w: w, enc: enc}
	case ir.OpenAIChat:
		return &dataStream{w: w, enc: openaichat.NewStreamEncoder(m.ResponseID, m.Model, m.Created, m.IncludeUsage), errBody: openaichat.EncodeError}
	case ir.OpenAIResponses:
		return &responsesStream{w: w, enc: openairesponses.NewStreamEncoder(m.ResponseID, m.Model, m.Created)}
	case ir.Gemini:
		return &dataStream{w: w, enc: gemini.NewStreamEncoder(m.Model, m.ResponseID), errBody: gemini.EncodeError}
	}
	return nil
}

type anthropicStream struct {
	w   *wire.SSEWriter
	enc *anthropic.StreamEncoder
}

func (s *anthropicStream) write(evs []anthropic.SSE) error {
	for _, e := range evs {
		if err := s.w.Write(e.Event, e.Data); err != nil {
			return err
		}
	}
	return nil
}
func (s *anthropicStream) Event(ev ir.Event) error         { return s.write(s.enc.Encode(ev)) }
func (s *anthropicStream) Finish(stop ir.StopReason) error { return s.write(s.enc.Finish(stop, "")) }
func (s *anthropicStream) Fail(e *ir.Error) error {
	return s.write(s.enc.Encode(ir.Event{Kind: ir.EvError, Err: e}))
}
func (s *anthropicStream) Keepalive() error { return s.write([]anthropic.SSE{s.enc.Ping()}) }
func (s *anthropicStream) Done() bool       { return s.enc.Stopped() }

type dataEncoder interface {
	Encode(ev ir.Event) [][]byte
	Finish(stop ir.StopReason) [][]byte
	Stopped() bool
}

type dataStream struct {
	w       *wire.SSEWriter
	enc     dataEncoder
	errBody func(*ir.Error) []byte
}

func (s *dataStream) write(chunks [][]byte) error {
	for _, c := range chunks {
		if err := s.w.Write("", c); err != nil {
			return err
		}
	}
	return nil
}
func (s *dataStream) Event(ev ir.Event) error         { return s.write(s.enc.Encode(ev)) }
func (s *dataStream) Finish(stop ir.StopReason) error { return s.write(s.enc.Finish(stop)) }
func (s *dataStream) Fail(e *ir.Error) error {
	if s.enc.Stopped() {
		return nil
	}
	return s.write(s.enc.Encode(ir.Event{Kind: ir.EvError, Err: e}))
}
func (s *dataStream) Keepalive() error { return s.w.Comment("keepalive") }
func (s *dataStream) Done() bool       { return s.enc.Stopped() }

type responsesStream struct {
	w   *wire.SSEWriter
	enc *openairesponses.StreamEncoder
}

func (s *responsesStream) write(evs []openairesponses.SSE) error {
	for _, e := range evs {
		if err := s.w.Write(e.Event, e.Data); err != nil {
			return err
		}
	}
	return nil
}
func (s *responsesStream) Event(ev ir.Event) error         { return s.write(s.enc.Encode(ev)) }
func (s *responsesStream) Finish(stop ir.StopReason) error { return s.write(s.enc.Finish(stop)) }
func (s *responsesStream) Fail(e *ir.Error) error {
	return s.write(s.enc.Encode(ir.Event{Kind: ir.EvError, Err: e}))
}
func (s *responsesStream) Keepalive() error { return s.w.Comment("keepalive") }
func (s *responsesStream) Done() bool       { return s.enc.Stopped() }

// EncodeResponse renders a complete response for an inbound protocol.
func EncodeResponse(p ir.Protocol, r *ir.Response, model string, created time.Time) []byte {
	switch p {
	case ir.AnthropicMessages:
		return anthropic.EncodeResponse(r, model)
	case ir.OpenAIChat:
		return openaichat.EncodeResponse(r, model, created)
	case ir.OpenAIResponses:
		return openairesponses.EncodeResponse(r, model, created)
	case ir.Gemini:
		return gemini.EncodeResponse(r, model)
	}
	return nil
}

// EncodeError renders an error body for an inbound protocol.
func EncodeError(p ir.Protocol, e *ir.Error) []byte {
	switch p {
	case ir.AnthropicMessages:
		return anthropic.EncodeError(e)
	case ir.Gemini:
		return gemini.EncodeError(e)
	}
	return openaichat.EncodeError(e)
}

// Decoder converts upstream stream events into IR events.
type Decoder interface {
	// Feed consumes one SSE event. unknown names an unrecognized event or
	// field (for Protocol Watch); it is not an error.
	Feed(ev *wire.SSEEvent) (out []ir.Event, unknown string, err error)
	// Close finalizes the stream at upstream EOF.
	Close() []ir.Event
	// Finished reports whether the upstream signalled completion.
	Finished() bool
}

// NewDecoder returns a stream decoder for an upstream protocol.
func NewDecoder(p ir.Protocol) Decoder {
	switch p {
	case ir.AnthropicMessages:
		return &anthropicDecoder{d: anthropic.NewStreamDecoder()}
	case ir.OpenAIChat:
		return &chatDecoder{d: openaichat.NewStreamDecoder()}
	case ir.OpenAIResponses:
		return &responsesDecoder{d: openairesponses.NewStreamDecoder()}
	case ir.Gemini:
		return &geminiDecoder{d: gemini.NewStreamDecoder()}
	}
	return nil
}

type anthropicDecoder struct {
	d       *anthropic.StreamDecoder
	stopped bool
}

func (a *anthropicDecoder) Feed(ev *wire.SSEEvent) ([]ir.Event, string, error) {
	name := ev.Event
	if name == "" {
		var head struct {
			Type string `json:"type"`
		}
		_ = jsonUnmarshal(ev.Data, &head)
		name = head.Type
	}
	out, unknown, err := a.d.Decode(name, ev.Data)
	for _, e := range out {
		if e.Kind == ir.EvStop {
			a.stopped = true
		}
	}
	if name == "message_stop" {
		a.stopped = true
	}
	return out, unknown, err
}
func (a *anthropicDecoder) Close() []ir.Event { return nil }
func (a *anthropicDecoder) Finished() bool    { return a.stopped }

type chatDecoder struct{ d *openaichat.StreamDecoder }

func (c *chatDecoder) Feed(ev *wire.SSEEvent) ([]ir.Event, string, error) { return c.d.Decode(ev.Data) }
func (c *chatDecoder) Close() []ir.Event                                  { return c.d.Close() }
func (c *chatDecoder) Finished() bool                                     { return c.d.Finished() }

type responsesDecoder struct {
	d *openairesponses.StreamDecoder
}

func (r *responsesDecoder) Feed(ev *wire.SSEEvent) ([]ir.Event, string, error) {
	return r.d.Decode(ev.Data)
}
func (r *responsesDecoder) Close() []ir.Event { return nil }
func (r *responsesDecoder) Finished() bool    { return r.d.Finished() }

type geminiDecoder struct{ d *gemini.StreamDecoder }

func (g *geminiDecoder) Feed(ev *wire.SSEEvent) ([]ir.Event, string, error) {
	return g.d.Decode(ev.Data)
}
func (g *geminiDecoder) Close() []ir.Event { return g.d.Close() }
func (g *geminiDecoder) Finished() bool    { return g.d.Finished() }

// ParseResponse parses a non-streaming upstream response.
func ParseResponse(p ir.Protocol, body []byte) (*ir.Response, error) {
	switch p {
	case ir.AnthropicMessages:
		return anthropic.ParseResponse(body)
	case ir.OpenAIChat:
		return openaichat.ParseResponse(body)
	case ir.OpenAIResponses:
		return openairesponses.ParseResponse(body)
	case ir.Gemini:
		return gemini.ParseResponse(body)
	}
	return nil, &ir.Error{Kind: ir.ErrUnsupported, Message: "unsupported upstream protocol " + string(p)}
}

// ParseError parses an upstream error response.
func ParseError(p ir.Protocol, status int, body []byte, h http.Header) *ir.Error {
	switch p {
	case ir.AnthropicMessages:
		return anthropic.ParseError(status, body, h)
	case ir.Gemini:
		return gemini.ParseError(status, body, h)
	}
	return openaichat.ParseError(status, body, h)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
