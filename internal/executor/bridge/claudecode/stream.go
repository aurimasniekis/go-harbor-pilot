package claudecode

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
)

type doneKind int

const (
	notDone doneKind = iota
	doneTurn
	doneTools
	doneError
)

// translator merges the CLI's per-API-call message streams into one client
// turn: one start event, parts re-indexed across calls, usage summed, and
// only client tool calls exposed.
type translator struct {
	c        *conv
	model    string
	dec      *anthropic.StreamDecoder
	started  bool
	semantic bool
	offset   int
	msgMax   int
	hidden   map[int]bool
	msgUsage ir.Usage
	total    ir.Usage
	haveUse  bool
	lastStop ir.StopReason
	// toolIDs are client tool calls of the current message.
	toolIDs []string
	// blocks is the delivered assistant content (for the fingerprint).
	blocks []map[string]any
	cur    map[int]map[string]any
	text   map[int]*strings.Builder
}

func newTranslator(c *conv, model string) *translator {
	return &translator{c: c, model: model, hidden: map[int]bool{}, cur: map[int]map[string]any{}, text: map[int]*strings.Builder{}}
}

func addUsage(dst *ir.Usage, u ir.Usage) {
	add := func(d **int64, v *int64) {
		if v == nil {
			return
		}
		if *d == nil {
			z := int64(0)
			*d = &z
		}
		**d += *v
	}
	add(&dst.InputTokens, u.InputTokens)
	add(&dst.OutputTokens, u.OutputTokens)
	add(&dst.CacheReadTokens, u.CacheReadTokens)
	add(&dst.CacheWriteTokens, u.CacheWriteTokens)
	add(&dst.ReasoningTokens, u.ReasoningTokens)
}

func (t *translator) assistantMessage() json.RawMessage {
	b, _ := json.Marshal(map[string]any{"role": "assistant", "content": t.blocks})
	return b
}

func (t *translator) finish(stop ir.StopReason) []ir.Event {
	var out []ir.Event
	if !t.started {
		out = append(out, ir.Event{Kind: ir.EvStart, Model: t.model})
		t.started = true
	}
	u := t.total
	if t.haveUse {
		out = append(out, ir.Event{Kind: ir.EvUsage, Usage: &u})
	}
	return append(out, ir.Event{Kind: ir.EvStop, StopReason: stop})
}

// cliError maps the CLI's API error classification.
func cliError(kind string, status int, msg string, retryAt int64, now time.Time) *ir.Error {
	e := &ir.Error{Message: msg, HTTPStatus: status, UpstreamType: kind}
	switch kind {
	case "rate_limit":
		e.Kind = ir.ErrRateLimited
	case "authentication_failed", "oauth_org_not_allowed":
		e.Kind = ir.ErrAuthentication
	case "billing_error":
		e.Kind = ir.ErrQuotaExhausted
	case "invalid_request":
		e.Kind = ir.ErrInvalidRequest
	case "server_error", "overloaded":
		e.Kind = ir.ErrOverloaded
	default:
		switch {
		case status == 429:
			e.Kind = ir.ErrRateLimited
		case status == 401:
			e.Kind = ir.ErrAuthentication
		case status == 403:
			e.Kind = ir.ErrPermission
		case status >= 500:
			e.Kind = ir.ErrOverloaded
		default:
			e.Kind = ir.ErrUpstream
		}
	}
	if e.Kind == ir.ErrRateLimited && retryAt > 0 {
		if s := int(time.Unix(retryAt, 0).Sub(now).Seconds()); s > 0 {
			e.RetryAfterSeconds = s
		}
	}
	return e
}

func (t *translator) feed(l line, retryAt int64, now time.Time) ([]ir.Event, doneKind, *ir.Error) {
	switch l.head.Type {
	case "stream_event":
		var w struct {
			Event json.RawMessage `json:"event"`
		}
		if json.Unmarshal(l.raw, &w) != nil {
			return nil, notDone, nil //nolint:nilerr // lines that are not protocol JSON are diagnostics and skipped
		}
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(w.Event, &head)
		return t.streamEvent(head.Type, w.Event)
	case "assistant":
		var w struct {
			Error          string `json:"error"`
			APIErrorStatus int    `json:"api_error_status"`
			Message        struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(l.raw, &w) != nil || (w.Error == "" && w.APIErrorStatus == 0) {
			return nil, notDone, nil //nolint:nilerr // complete assistant messages duplicate the stream; only API errors matter here
		}
		msg := "the upstream rejected the request"
		for _, c := range w.Message.Content {
			if c.Type == "text" && c.Text != "" {
				msg = c.Text
				break
			}
		}
		return nil, doneError, cliError(w.Error, w.APIErrorStatus, msg, retryAt, now)
	case "result":
		var w struct {
			IsError        bool   `json:"is_error"`
			Result         string `json:"result"`
			APIErrorStatus int    `json:"api_error_status"`
			APIError       string `json:"api_error"`
			Subtype        string `json:"subtype"`
		}
		_ = json.Unmarshal(l.raw, &w)
		if w.IsError && !t.semantic {
			kind := ""
			if w.APIError == "usage_limit_reached" {
				kind = "rate_limit"
			}
			return nil, doneError, cliError(kind, w.APIErrorStatus, w.Result, retryAt, now)
		}
		stop := t.lastStop
		if stop == "" || stop == ir.StopToolUse {
			stop = ir.StopEndTurn
		}
		return t.finish(stop), doneTurn, nil
	}
	return nil, notDone, nil
}

func (t *translator) streamEvent(name string, data json.RawMessage) ([]ir.Event, doneKind, *ir.Error) {
	if name == "message_start" {
		t.dec = anthropic.NewStreamDecoder()
		t.hidden = map[int]bool{}
		t.msgMax, t.msgUsage, t.toolIDs = 0, ir.Usage{}, nil
	}
	if t.dec == nil {
		return nil, notDone, nil
	}
	evs, _, err := t.dec.Decode(name, data)
	if err != nil {
		return nil, doneError, &ir.Error{Kind: ir.ErrProtocol, Message: "the CLI produced an unreadable stream event"}
	}
	var out []ir.Event
	for _, e := range evs {
		switch e.Kind {
		case ir.EvStart:
			if !t.started {
				t.started = true
				e.Model = t.model
				out = append(out, e)
			}
		case ir.EvUsage:
			if e.Usage != nil {
				t.msgUsage.Merge(*e.Usage)
			}
		case ir.EvStop:
			t.lastStop = e.StopReason
		case ir.EvError:
			return out, doneError, e.Err
		case ir.EvPartStart:
			if e.Index+1 > t.msgMax {
				t.msgMax = e.Index + 1
			}
			p := *e.Part
			if p.ToolCall != nil {
				client, ok := t.c.nameMap[p.ToolCall.Name]
				if !ok {
					t.hidden[e.Index] = true
					continue
				}
				tc := *p.ToolCall
				tc.Name = client
				p.ToolCall = &tc
				t.toolIDs = append(t.toolIDs, tc.ID)
				t.cur[e.Index] = map[string]any{"type": "tool_use", "id": tc.ID, "name": client}
				t.text[e.Index] = &strings.Builder{}
			} else if p.Kind == ir.PartText {
				t.cur[e.Index] = map[string]any{"type": "text"}
				t.text[e.Index] = &strings.Builder{}
			}
			e.Part = &p
			e.Index += t.offset
			t.semantic = true
			out = append(out, e)
		case ir.EvDelta, ir.EvPartStop:
			if t.hidden[e.Index] {
				continue
			}
			if b := t.text[e.Index]; b != nil && e.Kind == ir.EvDelta {
				b.WriteString(e.Delta)
			}
			if e.Kind == ir.EvPartStop {
				if blk := t.cur[e.Index]; blk != nil {
					if blk["type"] == "text" {
						blk["text"] = t.text[e.Index].String()
					}
					t.blocks = append(t.blocks, blk)
					delete(t.cur, e.Index)
				}
			}
			e.Index += t.offset
			out = append(out, e)
		}
	}
	if name == "message_stop" {
		if t.msgUsage.Known() {
			addUsage(&t.total, t.msgUsage)
			t.haveUse = true
		}
		t.offset += t.msgMax
		if t.lastStop == ir.StopToolUse && len(t.toolIDs) > 0 {
			return append(out, t.finish(ir.StopToolUse)...), doneTools, nil
		}
	}
	return out, notDone, nil
}
