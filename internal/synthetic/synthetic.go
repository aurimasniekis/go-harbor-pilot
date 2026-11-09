// Package synthetic implements deterministic synthetic upstream providers
// (Anthropic Messages, OpenAI Chat/Responses, Gemini) used by tests and the
// demo. Responses are scripted and clearly synthetic; nothing here contacts a
// real provider.
package synthetic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Reply scripts one upstream response.
type Reply struct {
	Status     int           // non-200 returns an error body
	RetryAfter int           // seconds
	Text       string        // assistant text
	ToolName   string        // when set, the reply is a tool call
	ToolArgs   string        // JSON arguments for the tool call
	Delay      time.Duration // wait before responding
	// Chunks splits streamed text into this many deltas (default 3) and
	// ChunkDelay waits between them, to simulate long generations.
	Chunks     int
	ChunkDelay time.Duration
	ErrorEvent string // stream an error event (after headers) instead of content
	InputTok   int64
	OutputTok  int64
	CacheRead  int64
	// CacheWrite is reported as Anthropic cache_creation_input_tokens
	// (omitted when zero).
	CacheWrite int64
	// Headers are extra response headers (e.g. rate-limit headers).
	Headers map[string]string
	// Echo replies with the text of the last user message.
	Echo bool
	// Raw is written verbatim as the response body (SSE when it starts with
	// "event:" or "data:", JSON otherwise); used to simulate protocol drift.
	Raw string
}

// Upstream is a scripted synthetic provider (an http.Handler).
type Upstream struct {
	mu       sync.Mutex
	script   []Reply
	Default  Reply
	Requests []Captured
	inflight atomic.Int64
	MaxSeen  atomic.Int64
	Kind     string
}

// MaxCaptured bounds remembered requests.
var MaxCaptured = 1000

// Captured is a request the upstream received.
type Captured struct {
	Path   string
	Header http.Header
	Body   map[string]any
	Raw    string
}

// New creates a synthetic upstream of kind anthropic | openai | gemini.
func New(kind string) *Upstream {
	return &Upstream{Kind: kind, Default: Reply{Text: "ok", InputTok: 12, OutputTok: 3}}
}

// ServeHTTP implements http.Handler.
func (u *Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) { u.serve(w, r) }

// Push queues scripted replies (consumed in order, then Default).
func (u *Upstream) Push(r ...Reply) {
	u.mu.Lock()
	u.script = append(u.script, r...)
	u.mu.Unlock()
}

// Count returns the number of requests received.
func (u *Upstream) Count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.Requests)
}

// Last returns the last captured request.
func (u *Upstream) Last() Captured {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.Requests[len(u.Requests)-1]
}

// Snapshot returns a copy of the captured requests, oldest first.
func (u *Upstream) Snapshot() []Captured {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Captured(nil), u.Requests...)
}

func (u *Upstream) next() Reply {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.script) > 0 {
		r := u.script[0]
		u.script = u.script[1:]
		return r
	}
	return u.Default
}

func (u *Upstream) serve(w http.ResponseWriter, r *http.Request) {
	// Model lists (credential checks) are answered without consuming the
	// script or being captured as generation requests.
	if r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/models") || strings.HasSuffix(r.URL.Path, "/api/tags")) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"synthetic-model","object":"model"}],"models":[{"name":"synthetic-model"}]}`)
		return
	}
	n := u.inflight.Add(1)
	defer u.inflight.Add(-1)
	for {
		m := u.MaxSeen.Load()
		if n <= m || u.MaxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	u.mu.Lock()
	if len(u.Requests) >= MaxCaptured {
		u.Requests = u.Requests[1:]
	}
	u.Requests = append(u.Requests, Captured{Path: r.URL.Path + "?" + r.URL.RawQuery, Header: r.Header.Clone(), Body: body, Raw: string(raw)})
	u.mu.Unlock()
	if u.Kind == "anthropic" && strings.HasSuffix(r.URL.Path, "/messages") {
		msg := checkAnthropicRoles(body)
		if msg == "" {
			msg = checkAnthropicCache(body)
		}
		if msg != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, msg)
			return
		}
	}
	rep := u.next()
	if rep.Echo {
		rep.Text = lastUserText(body)
	}
	if rep.Delay > 0 {
		select {
		case <-time.After(rep.Delay):
		case <-r.Context().Done():
			return
		}
	}
	for k, v := range rep.Headers {
		w.Header().Set(k, v)
	}
	if rep.Raw != "" {
		ct := "application/json"
		if strings.HasPrefix(rep.Raw, "event:") || strings.HasPrefix(rep.Raw, "data:") {
			ct = "text/event-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, rep.Raw)
		return
	}
	if rep.Status >= 400 {
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(rep.RetryAfter))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.Status)
		switch u.Kind {
		case "anthropic":
			typ := map[int]string{429: "rate_limit_error", 401: "authentication_error", 529: "overloaded_error", 400: "invalid_request_error"}[rep.Status]
			if typ == "" {
				typ = "api_error"
			}
			fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"synthetic %d"}}`, typ, rep.Status)
		case "gemini":
			fmt.Fprintf(w, `{"error":{"code":%d,"message":"synthetic","status":"RESOURCE_EXHAUSTED"}}`, rep.Status)
		default:
			fmt.Fprintf(w, `{"error":{"message":"synthetic %d","type":"error"}}`, rep.Status)
		}
		return
	}
	stream, _ := body["stream"].(bool)
	if strings.Contains(r.URL.Path, "streamGenerateContent") {
		stream = true
	}
	switch {
	case u.Kind == "anthropic" && stream:
		u.anthropicStream(w, rep)
	case u.Kind == "anthropic":
		u.anthropicJSON(w, rep)
	case u.Kind == "openai" && strings.HasSuffix(r.URL.Path, "/responses"):
		u.responses(w, rep, stream)
	case u.Kind == "openai" && stream:
		u.chatStream(w, rep)
	case u.Kind == "openai":
		u.chatJSON(w, rep)
	case u.Kind == "gemini":
		u.gemini(w, rep, stream)
	}
}

func sse(w http.ResponseWriter, event, data string) {
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func (u *Upstream) anthropicStream(w http.ResponseWriter, r Reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	sse(w, "message_start", js(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fake", "type": "message", "role": "assistant",
		"model": "fake", "content": []any{}, "usage": r.anthropicUsage(r.InputTok, 1)}}))
	if r.ErrorEvent != "" {
		sse(w, "error", js(map[string]any{"type": "error", "error": map[string]any{"type": r.ErrorEvent, "message": "synthetic"}}))
		return
	}
	stop := "end_turn"
	if r.ToolName != "" {
		stop = "tool_use"
		sse(w, "content_block_start", js(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_fake1", "name": r.ToolName, "input": map[string]any{}}}))
		half := len(r.ToolArgs) / 2
		sse(w, "content_block_delta", js(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": r.ToolArgs[:half]}}))
		sse(w, "content_block_delta", js(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": r.ToolArgs[half:]}}))
	} else {
		sse(w, "content_block_start", js(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}))
		for i, chunk := range splitN(r.Text, r.chunks()) {
			r.pace(i)
			sse(w, "content_block_delta", js(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": chunk}}))
		}
	}
	sse(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
	sse(w, "message_delta", js(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": r.OutputTok}}))
	sse(w, "message_stop", `{"type":"message_stop"}`)
}

func (r Reply) anthropicUsage(in, out int64) map[string]any {
	u := map[string]any{"input_tokens": in, "output_tokens": out, "cache_read_input_tokens": r.CacheRead}
	if r.CacheWrite > 0 {
		u["cache_creation_input_tokens"] = r.CacheWrite
	}
	return u
}

func (u *Upstream) anthropicJSON(w http.ResponseWriter, r Reply) {
	content := []any{map[string]any{"type": "text", "text": r.Text}}
	stop := "end_turn"
	if r.ToolName != "" {
		var args any
		_ = json.Unmarshal([]byte(r.ToolArgs), &args)
		content = []any{map[string]any{"type": "tool_use", "id": "toolu_fake1", "name": r.ToolName, "input": args}}
		stop = "tool_use"
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, js(map[string]any{"id": "msg_fake", "type": "message", "role": "assistant", "model": "fake", "content": content,
		"stop_reason": stop, "usage": r.anthropicUsage(r.InputTok, r.OutputTok)}))
}

func (u *Upstream) chatStream(w http.ResponseWriter, r Reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	sse(w, "", js(map[string]any{"id": "c1", "model": "fake", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}}}}))
	finish := "stop"
	if r.ToolName != "" {
		finish = "tool_calls"
		sse(w, "", js(map[string]any{"id": "c1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_fake1", "type": "function", "function": map[string]any{"name": r.ToolName, "arguments": ""}}}}}}}))
		for _, chunk := range splitN(r.ToolArgs, 3) {
			sse(w, "", js(map[string]any{"id": "c1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": chunk}}}}}}}))
		}
	} else {
		for i, chunk := range splitN(r.Text, r.chunks()) {
			r.pace(i)
			sse(w, "", js(map[string]any{"id": "c1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": chunk}}}}))
		}
	}
	sse(w, "", js(map[string]any{"id": "c1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}}))
	sse(w, "", js(map[string]any{"id": "c1", "choices": []any{}, "usage": map[string]any{"prompt_tokens": r.InputTok + r.CacheRead, "completion_tokens": r.OutputTok,
		"prompt_tokens_details": map[string]any{"cached_tokens": r.CacheRead}}}))
	sse(w, "", "[DONE]")
}

func (u *Upstream) chatJSON(w http.ResponseWriter, r Reply) {
	msg := map[string]any{"role": "assistant", "content": r.Text}
	finish := "stop"
	if r.ToolName != "" {
		msg = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call_fake1", "type": "function", "function": map[string]any{"name": r.ToolName, "arguments": r.ToolArgs}}}}
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, js(map[string]any{"id": "c1", "object": "chat.completion", "model": "fake", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage": map[string]any{"prompt_tokens": r.InputTok + r.CacheRead, "completion_tokens": r.OutputTok, "prompt_tokens_details": map[string]any{"cached_tokens": r.CacheRead}}}))
}

func (u *Upstream) responses(w http.ResponseWriter, r Reply, stream bool) {
	usage := map[string]any{"input_tokens": r.InputTok + r.CacheRead, "output_tokens": r.OutputTok, "input_tokens_details": map[string]any{"cached_tokens": r.CacheRead}}
	var item map[string]any
	if r.ToolName != "" {
		item = map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_fake1", "name": r.ToolName, "arguments": r.ToolArgs, "status": "completed"}
	} else {
		item = map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": r.Text, "annotations": []any{}}}}
	}
	final := map[string]any{"id": "resp_fake", "object": "response", "status": "completed", "model": "fake", "output": []any{item}, "usage": usage}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, js(final))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	sse(w, "response.created", js(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_fake", "model": "fake", "status": "in_progress"}}))
	if r.ToolName != "" {
		sse(w, "", js(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_fake1", "name": r.ToolName, "arguments": ""}}))
		for _, c := range splitN(r.ToolArgs, 2) {
			sse(w, "", js(map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_1", "delta": c}))
		}
	} else {
		sse(w, "", js(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": []any{}}}))
		for _, c := range splitN(r.Text, 2) {
			sse(w, "", js(map[string]any{"type": "response.output_text.delta", "output_index": 0, "item_id": "msg_1", "content_index": 0, "delta": c}))
		}
	}
	sse(w, "", js(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}))
	sse(w, "", js(map[string]any{"type": "response.completed", "response": final}))
}

func (u *Upstream) gemini(w http.ResponseWriter, r Reply, stream bool) {
	part := map[string]any{"text": r.Text}
	if r.ToolName != "" {
		var args any
		_ = json.Unmarshal([]byte(r.ToolArgs), &args)
		part = map[string]any{"functionCall": map[string]any{"name": r.ToolName, "args": args}}
	}
	resp := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{part}}, "finishReason": "STOP"}},
		"usageMetadata": map[string]any{"promptTokenCount": r.InputTok + r.CacheRead, "candidatesTokenCount": r.OutputTok, "cachedContentTokenCount": r.CacheRead},
		"modelVersion":  "fake"}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, js(resp))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	sse(w, "", js(resp))
}

func (r Reply) chunks() int {
	if r.Chunks > 0 {
		return r.Chunks
	}
	return 3
}

// pace waits between streamed deltas (not before the first).
func (r Reply) pace(i int) {
	if i > 0 && r.ChunkDelay > 0 {
		time.Sleep(r.ChunkDelay)
	}
}

func splitN(s string, n int) []string {
	if len(s) < n || n <= 1 {
		return []string{s}
	}
	var out []string
	size := (len(s) + n - 1) / n
	for i := 0; i < len(s); i += size {
		end := min(i+size, len(s))
		out = append(out, s[i:end])
	}
	return out
}

// checkAnthropicRoles applies the documented role rules of a Messages
// request: messages are user, assistant or system, and a system message
// cannot open the conversation. Model-specific support is not simulated.
func checkAnthropicRoles(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	for i, m := range msgs {
		mm, _ := m.(map[string]any)
		switch mm["role"] {
		case "user", "assistant":
		case "system":
			if i == 0 {
				return "messages: a system message cannot be the first message"
			}
		default:
			return fmt.Sprintf("messages.%d.role: unexpected role %v", i, mm["role"])
		}
	}
	return ""
}

// checkAnthropicCache applies the documented prompt-cache rules: at most
// four cache_control breakpoints (a top-level automatic one included), and
// TTLs that do not increase in processing order (tools, system, messages):
// a 1h breakpoint cannot follow a 5m one.
func checkAnthropicCache(body map[string]any) string {
	type mark struct{ path, ttl string }
	var marks []mark
	add := func(path string, b map[string]any) {
		cc, ok := b["cache_control"].(map[string]any)
		if !ok {
			return
		}
		ttl, _ := cc["ttl"].(string)
		if ttl == "" {
			ttl = "5m"
		}
		marks = append(marks, mark{path + ".cache_control", ttl})
	}
	blocks := func(v any) []map[string]any {
		arr, _ := v.([]any)
		out := make([]map[string]any, 0, len(arr))
		for _, x := range arr {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	for i, t := range blocks(body["tools"]) {
		add(fmt.Sprintf("tools.%d", i), t)
	}
	for i, b := range blocks(body["system"]) {
		add(fmt.Sprintf("system.%d", i), b)
	}
	for i, m := range blocks(body["messages"]) {
		for j, b := range blocks(m["content"]) {
			p := fmt.Sprintf("messages.%d.content.%d", i, j)
			add(p, b)
			if b["type"] == "tool_result" {
				for k, nb := range blocks(b["content"]) {
					add(fmt.Sprintf("%s.content.%d", p, k), nb)
				}
			}
		}
	}
	n := len(marks)
	if _, ok := body["cache_control"]; ok {
		n++
	}
	if n > 4 {
		return fmt.Sprintf("A maximum of 4 blocks with cache_control may be provided. Found %d.", n)
	}
	short := false
	for _, m := range marks {
		if m.ttl == "1h" && short {
			return m.path + ".ttl: a ttl='1h' cache_control block must not come after a ttl='5m' cache_control block"
		}
		if m.ttl != "1h" {
			short = true
		}
	}
	return ""
}

// lastUserText extracts the last user message text from Anthropic, Chat or
// Responses bodies.
func lastUserText(body map[string]any) string {
	var text string
	collect := func(c any) string {
		switch v := c.(type) {
		case string:
			return v
		case []any:
			var out string
			for _, it := range v {
				m, _ := it.(map[string]any)
				if t, ok := m["text"].(string); ok {
					out += t
				}
			}
			return out
		}
		return ""
	}
	for _, key := range []string{"messages", "input"} {
		if arr, ok := body[key].([]any); ok {
			for _, it := range arr {
				m, _ := it.(map[string]any)
				if m["role"] == "user" {
					text = collect(m["content"])
				}
			}
		}
		if s, ok := body[key].(string); ok {
			text = s
		}
	}
	return text
}
