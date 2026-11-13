package protocol_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// Synthetic request written from the public Messages API documentation.
const anthropicReq = `{
  "model": "claude-sonnet-x",
  "max_tokens": 2048,
  "system": [{"type":"text","text":"You are a coding agent.","cache_control":{"type":"ephemeral"}}],
  "messages": [
    {"role":"user","content":"List files"},
    {"role":"assistant","content":[
      {"type":"thinking","thinking":"I should call ls.","signature":"SIG-OPAQUE-123"},
      {"type":"text","text":"Calling ls."},
      {"type":"tool_use","id":"toolu_01","name":"bash","input":{"command":"ls -la"}}
    ]},
    {"role":"user","content":[
      {"type":"tool_result","tool_use_id":"toolu_01","content":"a.go\nb.go"},
      {"type":"text","text":"Now summarize."}
    ]}
  ],
  "tools": [{"name":"bash","description":"Run a shell command","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
  "tool_choice": {"type":"auto","disable_parallel_tool_use":true},
  "thinking": {"type":"enabled","budget_tokens":1024},
  "stream": true,
  "context_management": {"edits":[]}
}`

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
	return m
}

func lossFeatures(ls []ir.Loss) string {
	var f []string
	for _, l := range ls {
		f = append(f, l.Feature+"("+string(l.Severity)+")")
	}
	return strings.Join(f, ",")
}

func TestAnthropicNativeRoundTrip(t *testing.T) {
	r, err := anthropic.ParseRequest([]byte(anthropicReq), anthropic.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := ir.Requirements(r); strings.Join(got, ",") != "reasoning,streaming,tools" {
		t.Fatalf("requirements %v", got)
	}
	body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-sonnet-x", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(losses) != 0 {
		t.Fatalf("native round trip lost features: %s", lossFeatures(losses))
	}
	m := decode(t, body)
	msgs := m["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	th := asst[0].(map[string]any)
	if th["type"] != "thinking" || th["signature"] != "SIG-OPAQUE-123" || th["thinking"] != "I should call ls." {
		t.Fatalf("thinking block not preserved: %v", th)
	}
	tu := asst[2].(map[string]any)
	if tu["input"].(map[string]any)["command"] != "ls -la" {
		t.Fatalf("tool input: %v", tu)
	}
	if _, ok := m["context_management"]; !ok {
		t.Fatal("unknown top-level field not preserved for native target")
	}
	sys := m["system"].([]any)[0].(map[string]any)
	if sys["cache_control"] == nil {
		t.Fatal("cache_control lost")
	}
	if m["tool_choice"].(map[string]any)["disable_parallel_tool_use"] != true {
		t.Fatal("disable_parallel_tool_use lost")
	}
}

func TestAnthropicToChatTranslation(t *testing.T) {
	r, err := anthropic.ParseRequest([]byte(anthropicReq), anthropic.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	body, losses, err := openaichat.BuildRequest(r, openaichat.BuildOptions{Model: "gpt-x", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "SIG-OPAQUE-123") {
		t.Fatal("opaque signature leaked into a foreign protocol")
	}
	lf := lossFeatures(losses)
	for _, want := range []string{"reasoning_history(drop)", "field:anthropic_messages:context_management(drop)"} {
		if !strings.Contains(lf, want) {
			t.Fatalf("expected loss %s in %s", want, lf)
		}
	}
	m := decode(t, body)
	msgs := m["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("system first: %v", msgs[0])
	}
	asst := msgs[2].(map[string]any)
	calls := asst["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	var args map[string]string
	_ = json.Unmarshal([]byte(fn["arguments"].(string)), &args)
	if fn["name"] != "bash" || args["command"] != "ls -la" {
		t.Fatalf("tool call: %v", fn)
	}
	tool := msgs[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "toolu_01" || tool["content"] != "a.go\nb.go" {
		t.Fatalf("tool result: %v", tool)
	}
	if user := msgs[4].(map[string]any); user["content"] != "Now summarize." {
		t.Fatalf("user text after tool result: %v", user)
	}
	if m["parallel_tool_calls"] != false || m["reasoning_effort"] == nil {
		t.Fatalf("tool/reasoning config: %v %v", m["parallel_tool_calls"], m["reasoning_effort"])
	}
}

const chatReq = `{
  "model":"gpt-x",
  "messages":[
    {"role":"developer","content":"Be terse."},
    {"role":"user","content":[{"type":"text","text":"What is in this image?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo=","detail":"low"}}]},
    {"role":"assistant","content":null,"tool_calls":[{"id":"call_9","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"cat\"}"}}]},
    {"role":"tool","tool_call_id":"call_9","content":"{\"result\":\"a cat\"}"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true}}],
  "tool_choice":"required",
  "max_completion_tokens":500,
  "stream":true,
  "stream_options":{"include_usage":true},
  "response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"strict":true}}
}`

func TestChatToAnthropicAndResponses(t *testing.T) {
	r, err := openaichat.ParseRequest([]byte(chatReq), openaichat.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ir.Requirements(r), ",") != "streaming,structured_output,tools,vision" {
		t.Fatalf("requirements %v", ir.Requirements(r))
	}
	ab, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-x", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ir.HasReject(losses) || !strings.Contains(lossFeatures(losses), "structured_output(reject)") {
		t.Fatalf("structured output must require explicit approval: %s", lossFeatures(losses))
	}
	am := decode(t, ab)
	if am["system"].([]any)[0].(map[string]any)["text"] != "Be terse." {
		t.Fatalf("developer instructions: %v", am["system"])
	}
	msgs := am["messages"].([]any)
	img := msgs[0].(map[string]any)["content"].([]any)[1].(map[string]any)
	if img["type"] != "image" || img["source"].(map[string]any)["media_type"] != "image/png" {
		t.Fatalf("image: %v", img)
	}
	if am["tool_choice"].(map[string]any)["type"] != "any" {
		t.Fatalf("tool_choice: %v", am["tool_choice"])
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "call_9" {
		t.Fatalf("tool result: %v", res)
	}

	rb, losses, err := openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: "gpt-x", Stream: true, Stateless: true})
	if err != nil {
		t.Fatal(err)
	}
	if ir.HasReject(losses) {
		t.Fatalf("chat->responses should be lossless here: %s", lossFeatures(losses))
	}
	rm := decode(t, rb)
	items := rm["input"].([]any)
	if rm["instructions"] != "Be terse." || rm["store"] != false {
		t.Fatalf("instructions/store: %v %v", rm["instructions"], rm["store"])
	}
	var types []string
	for _, it := range items {
		types = append(types, it.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "message,function_call,function_call_output" {
		t.Fatalf("items: %v", types)
	}
	if rm["text"].(map[string]any)["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("format: %v", rm["text"])
	}
}

const responsesReq = `{
  "model":"gpt-x",
  "instructions":"You are Codex.",
  "input":[
    {"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]},
    {"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Looking at code"}],"encrypted_content":"ENC-BLOB"},
    {"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{\"cmd\":[\"ls\"]}"},
    {"type":"function_call_output","call_id":"call_1","output":"main.go"}
  ],
  "tools":[{"type":"function","name":"shell","parameters":{"type":"object"},"strict":false}],
  "reasoning":{"effort":"high","summary":"auto"},
  "include":["reasoning.encrypted_content"],
  "store":false,
  "stream":true,
  "prompt_cache_key":"sess-1"
}`

func TestResponsesNativeAndForeign(t *testing.T) {
	r, err := openairesponses.ParseRequest([]byte(responsesReq), openairesponses.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if fam := ir.SealedFamilies(r); len(fam) != 1 || fam[0] != "openai_responses" {
		t.Fatalf("sealed families %v", fam)
	}
	native, losses, err := openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: "gpt-x", Stream: true})
	if err != nil || len(losses) != 0 {
		t.Fatalf("native: %v %s", err, lossFeatures(losses))
	}
	nm := decode(t, native)
	reasoning := nm["input"].([]any)[1].(map[string]any)
	if reasoning["encrypted_content"] != "ENC-BLOB" || reasoning["id"] != "rs_1" {
		t.Fatalf("reasoning item not preserved: %v", reasoning)
	}
	fc := nm["input"].([]any)[2].(map[string]any)
	if fc["id"] != "fc_1" || fc["call_id"] != "call_1" {
		t.Fatalf("function_call ids: %v", fc)
	}
	if nm["prompt_cache_key"] != "sess-1" {
		t.Fatal("cache key lost")
	}
	ab, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-x", DefaultMaxTokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ab), "ENC-BLOB") {
		t.Fatal("encrypted reasoning leaked to a foreign protocol")
	}
	if !strings.Contains(lossFeatures(losses), "reasoning_history(drop)") {
		t.Fatalf("losses %s", lossFeatures(losses))
	}
	gb, _, err := gemini.BuildRequest(r, gemini.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gm := decode(t, gb)
	contents := gm["contents"].([]any)
	last := contents[len(contents)-1].(map[string]any)["parts"].([]any)[0].(map[string]any)
	fr := last["functionResponse"].(map[string]any)
	if fr["name"] != "shell" {
		t.Fatalf("gemini function response must carry the call's name: %v", fr)
	}
}

// ---------- streaming ----------

const anthropicStream = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-x\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":90,\"output_tokens\":1}}}\n\n" +
	"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Plan: \"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"SIG\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello wörld <&>\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_9\",\"name\":\"bash\",\"input\":{}}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"comm\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"and\\\": \\\"ls\\\"}\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":42}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// decodeUpstream feeds a byte stream through the bounded SSE reader one byte
// at a time (worst-case chunk boundaries) into a protocol decoder.
func decodeUpstream(t *testing.T, p ir.Protocol, raw string) []ir.Event {
	t.Helper()
	rd := wire.NewSSEReader(iotest.OneByteReader(strings.NewReader(raw)), 1<<20)
	dec := protocol.NewDecoder(p)
	var out []ir.Event
	for {
		ev, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		evs, _, err := dec.Feed(ev)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, evs...)
	}
	out = append(out, dec.Close()...)
	if !dec.Finished() {
		t.Fatal("decoder did not see stream completion")
	}
	return out
}

type assembled struct {
	text      string
	toolName  string
	toolArgs  string
	toolID    string
	stop      ir.StopReason
	usage     ir.Usage
	reasoning string
	sealed    string
}

func assemble(evs []ir.Event) assembled {
	var a assembled
	kinds := map[int]ir.PartKind{}
	for _, e := range evs {
		switch e.Kind {
		case ir.EvPartStart:
			kinds[e.Index] = e.Part.Kind
			if e.Part.ToolCall != nil {
				a.toolName, a.toolID = e.Part.ToolCall.Name, e.Part.ToolCall.ID
			}
		case ir.EvDelta:
			switch kinds[e.Index] {
			case ir.PartText:
				a.text += e.Delta
			case ir.PartToolCall:
				a.toolArgs += e.Delta
			case ir.PartReasoning:
				a.reasoning += e.Delta
			}
		case ir.EvPartStop:
			if e.Part != nil && e.Part.Sealed != nil {
				a.sealed = e.Part.Sealed.Data
			}
		case ir.EvUsage:
			a.usage.Merge(*e.Usage)
		case ir.EvStop:
			a.stop = e.StopReason
		}
	}
	return a
}

func TestAnthropicStreamDecodes(t *testing.T) {
	a := assemble(decodeUpstream(t, ir.AnthropicMessages, anthropicStream))
	if a.text != "Hello wörld <&>" || a.toolName != "bash" || a.toolArgs != `{"command": "ls"}` || a.stop != ir.StopToolUse {
		t.Fatalf("assembled %+v", a)
	}
	if a.reasoning != "Plan: " || a.sealed != "SIG" {
		t.Fatalf("thinking/signature: %+v", a)
	}
	if ir.Val(a.usage.InputTokens) != 10 || ir.Val(a.usage.CacheReadTokens) != 90 || ir.Val(a.usage.OutputTokens) != 42 || a.usage.InputIncludesCache {
		t.Fatalf("usage semantics %+v", a.usage)
	}
}

// reencode runs IR events through a client encoder and back through the same
// protocol's decoder, checking the client stream is well-formed.
func reencode(t *testing.T, client ir.Protocol, evs []ir.Event, family string) assembled {
	t.Helper()
	rec := httptest.NewRecorder()
	w := wire.NewSSEWriter(rec)
	cs := protocol.NewClientStream(client, w, protocol.StreamMeta{ResponseID: "r1", Model: "coding/default", Created: time.Unix(1700000000, 0), IncludeUsage: true, UpstreamFamily: family})
	for _, e := range evs {
		if err := cs.Event(e); err != nil {
			t.Fatal(err)
		}
	}
	_ = cs.Finish(ir.StopEndTurn)
	if !cs.Done() {
		t.Fatal("client stream not terminated")
	}
	out := rec.Body.String()
	if strings.Contains(out, "Hello") && !strings.Contains(out, "<&>") {
		t.Fatalf("client stream must carry prompt text unescaped: %s", out)
	}
	return assemble(decodeUpstream(t, client, out))
}

func TestCrossProtocolStreaming(t *testing.T) {
	evs := decodeUpstream(t, ir.AnthropicMessages, anthropicStream)
	tests := []struct {
		name   string
		client ir.Protocol
	}{
		{name: "anthropic to chat", client: ir.OpenAIChat},
		{name: "anthropic to responses", client: ir.OpenAIResponses},
		{name: "anthropic to gemini", client: ir.Gemini},
		{name: "anthropic to anthropic", client: ir.AnthropicMessages},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := reencode(t, tt.client, evs, "anthropic")
			if a.text != "Hello wörld <&>" {
				t.Fatalf("text %q", a.text)
			}
			if a.toolName != "bash" || strings.ReplaceAll(a.toolArgs, " ", "") != `{"command":"ls"}` || a.toolID == "" {
				t.Fatalf("tool call %+v", a)
			}
			if a.stop != ir.StopToolUse {
				t.Fatalf("stop %q", a.stop)
			}
			if tt.client == ir.AnthropicMessages && a.sealed != "SIG" {
				t.Fatalf("native signature must round trip, got %q", a.sealed)
			}
			if tt.client != ir.AnthropicMessages && a.sealed == "SIG" {
				t.Fatal("anthropic signature exposed to a foreign client")
			}
			if in := a.usage.InputTokens; tt.client != ir.AnthropicMessages && (in == nil || *in != 100) {
				t.Fatalf("%s input tokens should include cache (100), got %v", tt.client, in)
			}
		})
	}
}

func TestChatUpstreamStreamParallelTools(t *testing.T) {
	raw := `data: {"id":"c1","model":"gpt-x","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":""}}]}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"grep","arguments":"{\"p\":"}}]}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"f\":\"x\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"y\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":20}}}` + "\n\n" +
		"data: [DONE]\n\n"
	evs := decodeUpstream(t, ir.OpenAIChat, raw)
	args := map[string]string{}
	idx := map[int]string{}
	for _, e := range evs {
		if e.Kind == ir.EvPartStart && e.Part.ToolCall != nil {
			idx[e.Index] = e.Part.ToolCall.Name
		}
		if e.Kind == ir.EvDelta {
			args[idx[e.Index]] += e.Delta
		}
	}
	if args["read"] != `{"f":"x"}` || args["grep"] != `{"p":"y"}` {
		t.Fatalf("interleaved parallel tool args: %v", args)
	}
	a := reencode(t, ir.AnthropicMessages, evs, "openai_chat")
	if a.stop != ir.StopToolUse {
		t.Fatalf("stop %q", a.stop)
	}
	if ir.Val(a.usage.CacheReadTokens) != 20 {
		t.Fatalf("cache usage lost: %+v", a.usage)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		p      ir.Protocol
		status int
		body   string
		want   ir.ErrorKind
	}{
		{ir.AnthropicMessages, 429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, ir.ErrRateLimited},
		{ir.AnthropicMessages, 529, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`, ir.ErrOverloaded},
		{ir.AnthropicMessages, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 300000 tokens"}}`, ir.ErrContextLength},
		{ir.OpenAIChat, 429, `{"error":{"message":"quota","type":"insufficient_quota","code":"insufficient_quota"}}`, ir.ErrQuotaExhausted},
		{ir.OpenAIChat, 401, `{"error":{"message":"bad key"}}`, ir.ErrAuthentication},
		{ir.Gemini, 429, `{"error":{"code":429,"message":"exhausted","status":"RESOURCE_EXHAUSTED"}}`, ir.ErrRateLimited},
	}
	for _, tt := range tests {
		e := protocol.ParseError(tt.p, tt.status, []byte(tt.body), map[string][]string{"Retry-After": {"7"}})
		if e.Kind != tt.want {
			t.Errorf("%s %d: got %s want %s", tt.p, tt.status, e.Kind, tt.want)
		}
		if e.RetryAfterSeconds != 7 {
			t.Errorf("%s: retry-after not parsed", tt.p)
		}
		body := protocol.EncodeError(ir.AnthropicMessages, e)
		if !json.Valid(body) {
			t.Errorf("invalid error body %s", body)
		}
	}
}

func TestParserLimits(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(`{"model":"m","max_tokens":1,"messages":[`)
	for i := range 20 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"role":"user","content":"x"}`)
	}
	b.WriteString(`]}`)
	_, err := anthropic.ParseRequest(b.Bytes(), anthropic.Limits{MaxMessages: 10, MaxParts: 100, MaxTools: 10})
	if err == nil || !strings.Contains(err.Error(), "too many messages") {
		t.Fatalf("limit not enforced: %v", err)
	}
	if _, err := openaichat.ParseRequest([]byte(`{"model":"m","messages":[],"n":3}`), openaichat.DefaultLimits); err == nil {
		t.Fatal("n>1 accepted")
	}
}

// ---------- fuzzing ----------

func FuzzParsers(f *testing.F) {
	for _, s := range []string{anthropicReq, chatReq, responsesReq, `{}`, `[]`, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"!!"}}]}]}`,
		claudeCodeAdaptive, `{"messages":[],"output_config":{"effort":7}}`, `{"messages":[],"output_config":"high","thinking":{"type":"adaptive"}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if r, err := anthropic.ParseRequest(data, anthropic.DefaultLimits); err == nil {
			_, _, _ = anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "m"})
			_, _, _ = openaichat.BuildRequest(r, openaichat.BuildOptions{Model: "m"})
			_, _, _ = openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: "m", Stateless: true})
			_, _, _ = gemini.BuildRequest(r, gemini.BuildOptions{})
		}
		if r, err := openaichat.ParseRequest(data, openaichat.DefaultLimits); err == nil {
			_, _, _ = anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "m"})
			_, _, _ = openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: "m"})
		}
		if r, err := openairesponses.ParseRequest(data, openairesponses.DefaultLimits); err == nil {
			_, _, _ = anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "m"})
		}
		if r, err := gemini.ParseRequest(data, "m", false); err == nil {
			_, _, _ = openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: "m"})
		}
		for _, p := range []ir.Protocol{ir.AnthropicMessages, ir.OpenAIChat, ir.OpenAIResponses, ir.Gemini} {
			_, _ = protocol.ParseResponse(p, data)
		}
	})
}

func FuzzStreamDecoders(f *testing.F) {
	f.Add([]byte(anthropicStream))
	f.Add([]byte("data: [DONE]\n\n"))
	f.Add([]byte("event: x\ndata: {\n\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, p := range []ir.Protocol{ir.AnthropicMessages, ir.OpenAIChat, ir.OpenAIResponses, ir.Gemini} {
			rd := wire.NewSSEReader(bytes.NewReader(data), 64<<10)
			dec := protocol.NewDecoder(p)
			for range 10000 {
				ev, err := rd.Next()
				if err != nil {
					break
				}
				_, _, _ = dec.Feed(ev)
			}
			_ = dec.Close()
		}
	})
}

func TestInterleavedToolCallsBecomeSequentialBlocks(t *testing.T) {
	evs := []ir.Event{
		{Kind: ir.EvStart, ResponseID: "x"},
		{Kind: ir.EvPartStart, Index: 0, Part: &ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: "a", Name: "read"}}},
		{Kind: ir.EvPartStart, Index: 1, Part: &ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: "b", Name: "grep"}}},
		{Kind: ir.EvDelta, Index: 1, Delta: `{"p":`},
		{Kind: ir.EvDelta, Index: 0, Delta: `{"f":1}`},
		{Kind: ir.EvDelta, Index: 1, Delta: `2}`},
		{Kind: ir.EvPartStop, Index: 0},
		{Kind: ir.EvPartStop, Index: 1},
		{Kind: ir.EvStop, StopReason: ir.StopToolUse},
	}
	rec := httptest.NewRecorder()
	cs := protocol.NewClientStream(ir.AnthropicMessages, wire.NewSSEWriter(rec), protocol.StreamMeta{Model: "m"})
	for _, e := range evs {
		if err := cs.Event(e); err != nil {
			t.Fatal(err)
		}
	}
	var seq []string
	rd := wire.NewSSEReader(strings.NewReader(rec.Body.String()), 1<<20)
	for {
		ev, err := rd.Next()
		if err != nil {
			break
		}
		var head struct {
			Index int `json:"index"`
		}
		_ = json.Unmarshal(ev.Data, &head)
		if strings.HasPrefix(ev.Event, "content_block") {
			seq = append(seq, strings.TrimPrefix(ev.Event, "content_block_")+":"+string(rune('0'+head.Index)))
		}
	}
	want := "start:0,delta:0,stop:0,start:1,delta:1,delta:1,stop:1"
	if got := strings.Join(seq, ","); got != want {
		t.Fatalf("block order\n got %s\nwant %s", got, want)
	}
}
