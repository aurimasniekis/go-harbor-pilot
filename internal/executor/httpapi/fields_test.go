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
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body)
	}
	return m
}

func TestApplyRequestFieldsFillsMissing(t *testing.T) {
	body := []byte(`{"model":"m","stream":true}`)
	spec := &controlplane.ModelSpec{RequestFields: map[string]json.RawMessage{
		"transforms": json.RawMessage(`["middle-out"]`),
	}}
	var losses ir.Losses
	out, err := applyRequestFields(body, spec, &losses)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBody(t, out)
	if len(got["transforms"].([]any)) != 1 || got["transforms"].([]any)[0] != "middle-out" {
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
		return &controlplane.ModelSpec{
			RequestFields:      map[string]json.RawMessage{"provider": json.RawMessage(`{"order":["configured"]}`)},
			RequestFieldPolicy: policy,
		}
	}
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
			out, err := applyRequestFields([]byte(`{"provider":{"order":["client"]}}`), spec(tc.policy), &losses)
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
			if has := len(losses.List()) > 0; has != tc.loss {
				t.Errorf("losses recorded = %v, want %v (%v)", has, tc.loss, losses.List())
			}
		})
	}
}

func TestApplyRequestFieldsNoopIsByteIdentical(t *testing.T) {
	body := []byte("{\n  \"model\": \"m\",\n  \"zzz\": 1\n}")
	for _, spec := range []*controlplane.ModelSpec{nil, {}, {RequestFields: map[string]json.RawMessage{}}} {
		out, err := applyRequestFields(body, spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(body) {
			t.Errorf("a model with no fields must leave the body untouched, got %s", out)
		}
	}
}

func TestMergeFieldsIgnoresStructuralNames(t *testing.T) {
	spec := &controlplane.ModelSpec{RequestFields: map[string]json.RawMessage{
		"model":    json.RawMessage(`"hijacked"`),
		"messages": json.RawMessage(`[]`),
	}}
	fs, ok := configuredFields(spec)
	if ok {
		t.Fatalf("structural fields must be ignored at send time too, got %v", fs.fields)
	}
}

func TestMergeFieldsMatchesExistingKeyCaseInsensitively(t *testing.T) {
	o := wire.Object{"Provider": json.RawMessage(`{"order":["client"]}`)}
	fs := requestFieldSet{fields: map[string]json.RawMessage{"provider": json.RawMessage(`{"order":["configured"]}`)},
		policy: controlplane.FieldPolicyConfigured}
	if err := mergeFields(o, fs, nil); err != nil {
		t.Fatal(err)
	}
	if _, dup := o["Provider"]; dup {
		t.Error("the client's key must be replaced, not duplicated under a second spelling")
	}
	if string(o["provider"]) != `{"order":["configured"]}` {
		t.Errorf("provider = %s", o["provider"])
	}
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
		{"anthropic messages", ir.AnthropicMessages, controlplane.ProviderAnthropic, want{"/v1/messages", "metadata"}},
		{"openai responses", ir.OpenAIResponses, controlplane.ProviderOpenAI, want{"/responses", "service_tier"}},
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
				spec.ProviderRouting = &controlplane.UpstreamProviderSpec{Order: []string{"relace/fp4"}, MaxPrice: &controlplane.UpstreamMaxPrice{}}
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
				if len(order) != 1 || order[0] != "relace/fp4" {
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
	spec := &controlplane.ModelSpec{ProviderRouting: &controlplane.UpstreamProviderSpec{Order: []string{"relace/fp4"}}}
	raw := []byte(`{"model":"client-model","stream":true,"provider":{"order":["client"]}}`)
	var losses ir.Losses
	out, err := passthroughBody(raw, target(controlplane.ProviderOpenAICompatible, "https://x", spec), ir.OpenAIChat, true, &losses)
	if err != nil {
		t.Fatal(err)
	}
	o := decodeBody(t, out)
	if o["model"] != "m" {
		t.Errorf("model = %v, want the routed model", o["model"])
	}
	order := o["provider"].(map[string]any)["order"].([]any)
	if len(order) != 1 || order[0] != "relace/fp4" {
		t.Errorf("provider.order = %v, want the configured pin", order)
	}
	if _, ok := o["stream_options"]; !ok {
		t.Error("stream_options must still be set for a streaming chat request")
	}
}

func TestPassthroughWithoutFieldsAddsNothing(t *testing.T) {
	raw := []byte(`{"model":"client-model","stream":true}`)
	out, err := passthroughBody(raw, target(controlplane.ProviderOpenAICompatible, "https://x", nil), ir.OpenAIChat, true, &ir.Losses{})
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
	spec := &controlplane.ModelSpec{RequestFields: map[string]json.RawMessage{"safetySettings": json.RawMessage(`[]`)}}
	raw := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	out, err := passthroughBody(raw, target(controlplane.ProviderGemini, "https://x", spec), ir.Gemini, false, &ir.Losses{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeBody(t, out)["safetySettings"]; !ok {
		t.Errorf("safetySettings missing: %s", out)
	}
	// Without configured fields the raw bytes are forwarded exactly.
	out, err = passthroughBody(raw, target(controlplane.ProviderGemini, "https://x", nil), ir.Gemini, false, &ir.Losses{})
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
