package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
)

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body)
	}
	return m
}

const compat = controlplane.ProviderOpenAICompatible

func raws(kv ...string) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = json.RawMessage(kv[i+1])
	}
	return m
}

func TestApplyRequestFieldsFillsMissing(t *testing.T) {
	body := []byte(`{"model":"m","stream":true}`)
	spec := &controlplane.ModelSpec{RequestFields: raws("transforms", `["middle-out"]`)}
	var losses ir.Losses
	out, err := applyRequestFields(body, compat, spec, nil, &losses)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBody(t, out)
	if tr, _ := got["transforms"].([]any); len(tr) != 1 || tr[0] != "middle-out" {
		t.Errorf("transforms = %v", got["transforms"])
	}
	if got["model"] != "m" {
		t.Errorf("the gateway's own fields must survive, model = %v", got["model"])
	}
	if len(losses.List()) != 0 {
		t.Errorf("a fill is not a loss, got %v", losses.List())
	}
}

func TestApplyRequestFieldsPolicies(t *testing.T) {
	spec := func(policy string) *controlplane.ModelSpec {
		return &controlplane.ModelSpec{RequestFields: raws("provider", `{"order":["configured"]}`), RequestFieldPolicy: policy}
	}
	client := []byte(`{"provider":{"order":["client"]}}`)
	tests := []struct {
		name    string
		policy  string
		want    string // rendered provider.order[0]
		wantErr bool
		loss    bool
	}{
		{"configured wins over the client", controlplane.FieldPolicyConfigured, "configured", false, true},
		{"client wins", controlplane.FieldPolicyClient, "client", false, true},
		{"reject refuses", controlplane.FieldPolicyReject, "", true, false},
		{"empty policy means configured", "", "configured", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var losses ir.Losses
			out, err := applyRequestFields(client, compat, spec(tc.policy), ClientSettingsOf(nil, client), &losses)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %s", out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := decodeBody(t, out)["provider"].(map[string]any)["order"].([]any)[0]
			if got != tc.want {
				t.Errorf("provider.order[0] = %v, want %v", got, tc.want)
			}
			ls := losses.List()
			if has := len(ls) > 0; has != tc.loss {
				t.Fatalf("losses recorded = %v, want %v (%v)", has, tc.loss, ls)
			}
			if tc.loss && ls[0].Feature != "request_field:provider" {
				t.Errorf("loss feature = %q, want request_field:provider", ls[0].Feature)
			}
		})
	}
}

// TestGatewayDefaultIsNotAClientConflict covers H2: a field the gateway put
// in the body itself is not the client's, so no policy treats it as a
// conflict, and reject never refuses a request that did not set the field.
func TestGatewayDefaultIsNotAClientConflict(t *testing.T) {
	built := []byte(`{"model":"m","temperature":1}`) // as if the builder had defaulted it
	clientRaw := []byte(`{"model":"alias","messages":[]}`)
	for _, policy := range []string{controlplane.FieldPolicyConfigured, controlplane.FieldPolicyClient, controlplane.FieldPolicyReject} {
		t.Run(policy, func(t *testing.T) {
			spec := &controlplane.ModelSpec{RequestFields: raws("temperature", `0.2`), RequestFieldPolicy: policy}
			cs := ClientSettingsOf(chatIR(ir.OpenAIChat), clientRaw)
			if name, refused := RefusedRequestField(compat, spec, cs); refused {
				t.Fatalf("a gateway default must not be refused, got %q", name)
			}
			var losses ir.Losses
			out, err := applyRequestFields(built, compat, spec, cs, &losses)
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeBody(t, out)["temperature"]; got != 0.2 {
				t.Errorf("temperature = %v, want the configured 0.2", got)
			}
			if len(losses.List()) != 0 {
				t.Errorf("replacing a gateway default is not a client conflict: %v", losses.List())
			}
		})
	}
}

// TestClientSettingFoundThroughTheCanonicalRequest: a client that set stop
// sequences on another protocol still owns the translated "stop".
func TestClientSettingFoundThroughTheCanonicalRequest(t *testing.T) {
	r := chatIR(ir.AnthropicMessages)
	r.Stop = []string{"END"}
	built := []byte(`{"model":"m","stop":["END"]}`)
	clientRaw := []byte(`{"model":"alias","stop_sequences":["END"],"messages":[]}`)
	spec := &controlplane.ModelSpec{RequestFields: raws("stop", `["CONF"]`), RequestFieldPolicy: controlplane.FieldPolicyClient}
	var losses ir.Losses
	out, err := applyRequestFields(built, compat, spec, ClientSettingsOf(r, clientRaw), &losses)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeBody(t, out)["stop"].([]any)[0]; got != "END" {
		t.Errorf("stop = %v, want the client's own", got)
	}
	if ls := losses.List(); len(ls) != 1 || ls[0].Feature != "request_field:stop" {
		t.Errorf("losses = %v, want request_field:stop", ls)
	}
	reject := &controlplane.ModelSpec{RequestFields: raws("stop", `["CONF"]`), RequestFieldPolicy: controlplane.FieldPolicyReject}
	if name, ok := RefusedRequestField(compat, reject, ClientSettingsOf(r, clientRaw)); !ok || name != "stop" {
		t.Errorf("reject should refuse stop, got %q %v", name, ok)
	}
}

func TestClientSettingsOf(t *testing.T) {
	seed := int64(1)
	r := &ir.Request{Seed: &seed, User: "u"}
	cs := ClientSettingsOf(r, []byte(`{"Top_P":1,"safety_settings":[],"generationConfig":{"stopSequences":["x"],"maxOutputTokens":5}}`))
	for _, want := range []string{"seed", "user", "topp", "safetysettings", "generationconfig", "stop", "maxoutputtokens"} {
		if !cs[want] {
			t.Errorf("%s missing from %v", want, cs)
		}
	}
	if cs["temperature"] {
		t.Error("temperature was never set")
	}
	if len(ClientSettingsOf(nil, []byte(`not json`))) != 0 {
		t.Error("an unreadable body sets nothing")
	}
}

func TestApplyRequestFieldsNoopIsByteIdentical(t *testing.T) {
	body := []byte("{\n  \"model\": \"m\",\n  \"zzz\": 1\n}")
	for _, spec := range []*controlplane.ModelSpec{nil, {}, {RequestFields: map[string]json.RawMessage{}},
		// Not on the allowlist for this type, so nothing to apply.
		{RequestFields: raws("transforms", `["x"]`)},
		// A provider pin applies to openai_compatible only.
		{ProviderRouting: &controlplane.UpstreamProviderSpec{Order: []string{"a"}}}} {
		out, err := applyRequestFields(body, controlplane.ProviderAnthropic, spec, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(body) {
			t.Errorf("a model with nothing to apply must leave the body untouched, got %s", out)
		}
	}
}

// TestUnlistedFieldsAreNeverSent: a spec that skipped validation (an old
// database row) still cannot put a forbidden field on the wire.
func TestUnlistedFieldsAreNeverSent(t *testing.T) {
	spec := &controlplane.ModelSpec{RequestFields: raws(
		"model", `"hijacked"`, "messages", `[]`, "max_tokens", `100000`, "models", `["x"]`, "cache_control", `{"type":"ephemeral"}`)}
	if fs, ok := configuredFields(compat, spec); ok {
		t.Fatalf("forbidden fields must be ignored at send time too, got %v", fs.fields)
	}
}

// TestMergeRemovesEverySpelling covers L1: when the configured value wins,
// no other spelling of the key survives.
func TestMergeRemovesEverySpelling(t *testing.T) {
	client := []byte(`{"Provider":{"order":["a"]},"PROVIDER":{"order":["b"]},"top_P":1,"topP":1}`)
	spec := &controlplane.ModelSpec{RequestFields: raws("provider", `{"order":["configured"]}`, "topP", `0.5`)}
	var losses ir.Losses
	out, err := applyRequestFields(client, compat, spec, ClientSettingsOf(nil, client), &losses)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBody(t, out)
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "provider,top_p" {
		t.Errorf("keys = %v, want exactly provider,top_p", keys)
	}
	if got["top_p"] != 0.5 {
		t.Errorf("top_p = %v", got["top_p"])
	}
	if n := len(losses.List()); n != 2 {
		t.Errorf("want a loss per overridden field, got %v", losses.List())
	}
}

// TestGeminiGenerationConfigDeepMerge: only the configured sampling keys are
// set; the client's other generationConfig keys (the output cap included)
// stay as they were.
func TestGeminiGenerationConfigDeepMerge(t *testing.T) {
	spec := func(policy string) *controlplane.ModelSpec {
		return &controlplane.ModelSpec{RequestFieldPolicy: policy,
			RequestFields: raws("generation_config", `{"temperature":0.2,"top_p":0.5}`, "safetySettings", `[{"category":"c","threshold":"t"}]`)}
	}
	client := []byte(`{"contents":[],"generation_config":{"temperature":0.9,"maxOutputTokens":100,"responseMimeType":"text/plain"}}`)
	t.Run("configured", func(t *testing.T) {
		var losses ir.Losses
		out, err := passthroughBody(client, target(controlplane.ProviderGemini, "https://x", spec("")), ir.Gemini, false, nil, &losses)
		if err != nil {
			t.Fatal(err)
		}
		got := decodeBody(t, out)
		if _, dup := got["generation_config"]; dup {
			t.Fatal("the client's snake_case spelling must be merged into one generationConfig")
		}
		gc := got["generationConfig"].(map[string]any)
		if gc["temperature"] != 0.2 || gc["topP"] != 0.5 {
			t.Errorf("configured keys not applied: %v", gc)
		}
		if gc["maxOutputTokens"] != float64(100) || gc["responseMimeType"] != "text/plain" {
			t.Errorf("the client's other keys must be kept: %v", gc)
		}
		if _, ok := got["safetySettings"]; !ok {
			t.Error("safetySettings missing")
		}
		if ls := losses.List(); len(ls) != 1 || ls[0].Feature != "request_field:generationConfig.temperature" {
			t.Errorf("losses = %v, want only generationConfig.temperature", ls)
		}
	})
	t.Run("client", func(t *testing.T) {
		out, err := passthroughBody(client, target(controlplane.ProviderGemini, "https://x", spec(controlplane.FieldPolicyClient)), ir.Gemini, false, nil, &ir.Losses{})
		if err != nil {
			t.Fatal(err)
		}
		gc := decodeBody(t, out)["generationConfig"].(map[string]any)
		if gc["temperature"] != 0.9 || gc["topP"] != 0.5 {
			t.Errorf("want the client's temperature and the configured topP: %v", gc)
		}
	})
	t.Run("reject names the sub-key", func(t *testing.T) {
		name, ok := RefusedRequestField(controlplane.ProviderGemini, spec(controlplane.FieldPolicyReject), ClientSettingsOf(nil, client))
		if !ok || name != "generationConfig.temperature" {
			t.Errorf("refused = %q %v", name, ok)
		}
	})
	t.Run("translated body without generationConfig", func(t *testing.T) {
		out, err := applyRequestFields([]byte(`{"contents":[]}`), controlplane.ProviderGemini, spec(""), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		gc := decodeBody(t, out)["generationConfig"].(map[string]any)
		if len(gc) != 2 {
			t.Errorf("generationConfig = %v, want only the configured keys", gc)
		}
	})
}

// TestExecuteSendsConfiguredFields covers the real path: a request goes out
// with the model's fields, for every protocol that reaches the HTTP executor.
func TestExecuteSendsConfiguredFields(t *testing.T) {
	type want struct {
		path string
		key  string
	}
	tests := []struct {
		name     string
		inbound  ir.Protocol
		provider string
		want     want
	}{
		{"chat completions", ir.OpenAIChat, controlplane.ProviderOpenAICompatible, want{"/chat/completions", "provider"}},
		{"anthropic messages", ir.AnthropicMessages, controlplane.ProviderAnthropic, want{"/v1/messages", "top_k"}},
		{"openai responses", ir.OpenAIResponses, controlplane.ProviderOpenAI, want{"/responses", "user"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.want.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.want.path)
				}
				raw, _ := io.ReadAll(r.Body)
				bodies <- decodeBody(t, raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(completeResponse(tc.inbound)))
			}))
			defer srv.Close()

			spec := &controlplane.ModelSpec{RequestFields: map[string]json.RawMessage{}}
			switch tc.want.key {
			case "provider":
				spec.ProviderRouting = &controlplane.UpstreamProviderSpec{Order: []string{"provider-a"}, MaxPrice: &controlplane.UpstreamMaxPrice{}}
			default:
				spec.RequestFields[tc.want.key] = json.RawMessage(`"configured-value"`)
			}
			ex := New("test-agent")
			ex.AllowedPrivate = nil
			res, aerr := ex.Execute(context.Background(), target(tc.provider, srv.URL, spec), testSecret(),
				executor.Request{IR: chatIR(tc.inbound), Inbound: tc.inbound}, false)
			if aerr != nil {
				t.Fatalf("execute: %v", aerr.Err)
			}
			if res.Response == nil {
				t.Fatal("expected a parsed response")
			}
			got := <-bodies
			val, ok := got[tc.want.key]
			if !ok {
				t.Fatalf("%q missing from the upstream body: %v", tc.want.key, got)
			}
			if tc.want.key == "provider" {
				order, _ := val.(map[string]any)["order"].([]any)
				if len(order) != 1 || order[0] != "provider-a" {
					t.Errorf("provider = %v", val)
				}
			} else if val != "configured-value" {
				t.Errorf("%s = %v", tc.want.key, val)
			}
		})
	}
}

func target(providerType, baseURL string, spec *controlplane.ModelSpec) executor.Target {
	return executor.Target{
		Provider: &controlplane.Res[controlplane.ProviderSpec]{ID: "prv_test", Name: "test",
			Spec: controlplane.ProviderSpec{Type: providerType, BaseURL: baseURL, AllowPrivateNetwork: true}},
		Account:   &controlplane.Res[controlplane.AccountSpec]{ID: "acc_test", Name: "key"},
		Model:     "m",
		ModelSpec: spec,
	}
}

func testSecret() credentials.Secret { return credentials.NewSecret("sk-test-secret") }

func chatIR(p ir.Protocol) *ir.Request {
	n := 16
	return &ir.Request{Source: p, Model: "m", MaxOutputTokens: &n,
		Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: "hi"}}}}}
}

// completeResponse is the smallest valid non-streaming response per protocol.
func completeResponse(p ir.Protocol) string {
	switch p {
	case ir.AnthropicMessages:
		return `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	case ir.OpenAIResponses:
		return `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	}
	return `{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
}

// TestPassthroughAppliesConfiguredFields proves the client's own bytes cannot
// bypass a model's pin.
func TestPassthroughAppliesConfiguredFields(t *testing.T) {
	spec := &controlplane.ModelSpec{ProviderRouting: &controlplane.UpstreamProviderSpec{Order: []string{"provider-a"}}}
	raw := []byte(`{"model":"client-model","stream":true,"provider":{"order":["client"]}}`)
	var losses ir.Losses
	out, err := passthroughBody(raw, target(controlplane.ProviderOpenAICompatible, "https://x", spec), ir.OpenAIChat, true, nil, &losses)
	if err != nil {
		t.Fatal(err)
	}
	o := decodeBody(t, out)
	if o["model"] != "m" {
		t.Errorf("model = %v, want the routed model", o["model"])
	}
	order := o["provider"].(map[string]any)["order"].([]any)
	if len(order) != 1 || order[0] != "provider-a" {
		t.Errorf("provider.order = %v, want the configured pin", order)
	}
	if _, ok := o["stream_options"]; !ok {
		t.Error("stream_options must still be set for a streaming chat request")
	}
}

func TestPassthroughWithoutFieldsAddsNothing(t *testing.T) {
	raw := []byte(`{"model":"client-model","stream":true}`)
	out, err := passthroughBody(raw, target(controlplane.ProviderOpenAICompatible, "https://x", nil), ir.OpenAIChat, true, nil, &ir.Losses{})
	if err != nil {
		t.Fatal(err)
	}
	o := decodeBody(t, out)
	// Passthrough always rewrites the model and requests usage for a stream;
	// with no configured fields nothing else may appear.
	want := []string{"model", "stream", "stream_options"}
	got := make([]string, 0, len(o))
	for k := range o {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("body keys = %v, want %v", got, want)
	}
}

// TestPassthroughGeminiAppliesFields covers the branch that used to return the
// raw body without any changes.
func TestPassthroughGeminiAppliesFields(t *testing.T) {
	spec := &controlplane.ModelSpec{RequestFields: raws("safetySettings", `[]`)}
	raw := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	out, err := passthroughBody(raw, target(controlplane.ProviderGemini, "https://x", spec), ir.Gemini, false, nil, &ir.Losses{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeBody(t, out)["safetySettings"]; !ok {
		t.Errorf("safetySettings missing: %s", out)
	}
	// Without configured fields the raw bytes are forwarded exactly.
	out, err = passthroughBody(raw, target(controlplane.ProviderGemini, "https://x", nil), ir.Gemini, false, nil, &ir.Losses{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Errorf("gemini passthrough body changed: %s", out)
	}
}

func TestRequestFieldErrorKinds(t *testing.T) {
	rej := requestFieldError(&rejectedFieldError{name: "provider"})
	if rej.Kind != ir.ErrInvalidRequest {
		t.Errorf("a refused override must be a 400, got %s", rej.Kind)
	}
	if !strings.Contains(rej.Message, "provider") {
		t.Errorf("the message should name the field: %q", rej.Message)
	}
	// A body that cannot be parsed is the gateway's own problem, so it stays
	// internal and never echoes bytes back to the client.
	if got := requestFieldError(errors.New("unexpected end of JSON input")); got.Kind != ir.ErrInternal {
		t.Errorf("a merge failure must be internal, got %s", got.Kind)
	} else if strings.Contains(got.Message, "JSON") {
		t.Errorf("an internal message must not echo parser detail: %q", got.Message)
	}
}

func TestBuildRequestStillWorksForChat(t *testing.T) {
	// A guard that the chat builder (used above) keeps its own contract.
	b, _, err := openaichat.BuildRequest(chatIR(ir.OpenAIChat), openaichat.BuildOptions{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if decodeBody(t, b)["model"] != "m" {
		t.Fatal("unexpected builder output")
	}
}
