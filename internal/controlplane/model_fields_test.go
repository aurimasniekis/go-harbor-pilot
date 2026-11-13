package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// modelFieldProvider builds a provider spec with one model carrying the given
// request-field settings, for validation and canonicalization tests.
func modelFieldProvider(typ string, m ModelSpec) ProviderSpec {
	base := map[string]string{
		ProviderAnthropic:        "https://api.anthropic.com",
		ProviderOpenAI:           "https://api.openai.com/v1",
		ProviderOpenAICompatible: "https://router.example.com/v1",
		ProviderOllama:           "http://127.0.0.1:11434",
		ProviderGemini:           "https://generativelanguage.googleapis.com",
		ProviderClaudeCode:       "",
	}[typ]
	m.ID = "m1"
	return ProviderSpec{Type: typ, DisplayName: "p", BaseURL: base, AllowPrivateNetwork: typ == ProviderOllama, Models: []ModelSpec{m}}
}

func TestModelRequestFieldsValidation(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		m    ModelSpec
		want string // "" = accepted
	}{
		{"free-form field", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"metadata": json.RawMessage(`{"user_id":"u"}`)}}, ""},
		{"camelCase spelling of an allowed field", ProviderOpenAICompatible, ModelSpec{RequestFields: map[string]json.RawMessage{"topP": json.RawMessage(`0.5`)}}, ""},
		{"typed pin on a compatible provider", ProviderOpenAICompatible,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{Order: []string{"provider-a"}}}, ""},
		{"typed pin on an official provider", ProviderOpenAI,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{Order: []string{"x"}}}, "spec.models[0].provider_routing"},
		{"typed pin plus a provider field", ProviderOpenAICompatible,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{Only: []string{"x"}}, RequestFields: map[string]json.RawMessage{"Provider": json.RawMessage(`{}`)}},
			"spec.models[0].provider_routing"},
		{"not on this type's allowlist", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"transforms": json.RawMessage(`[]`)}}, "spec.models[0].request_fields.transforms"},
		{"unknown name", ProviderOpenAICompatible, ModelSpec{RequestFields: map[string]json.RawMessage{"x_custom": json.RawMessage(`1`)}}, "spec.models[0].request_fields.x_custom"},
		{"two spellings of one field", ProviderOpenAICompatible,
			ModelSpec{RequestFields: map[string]json.RawMessage{"top_p": json.RawMessage(`1`), "topP": json.RawMessage(`1`)}}, "spec.models[0].request_fields.top_p"},
		{"gemini sampling sub-key", ProviderGemini, ModelSpec{RequestFields: map[string]json.RawMessage{"generationConfig": json.RawMessage(`{"temperature":0.1,"top_k":4}`)}}, ""},
		{"gemini output cap sub-key", ProviderGemini,
			ModelSpec{RequestFields: map[string]json.RawMessage{"generationConfig": json.RawMessage(`{"maxOutputTokens":10}`)}}, "spec.models[0].request_fields.generationConfig.maxOutputTokens"},
		{"gemini candidate count sub-key, snake case", ProviderGemini,
			ModelSpec{RequestFields: map[string]json.RawMessage{"generation_config": json.RawMessage(`{"candidate_count":2}`)}}, "spec.models[0].request_fields.generation_config.candidate_count"},
		{"gemini unknown sub-key", ProviderGemini,
			ModelSpec{RequestFields: map[string]json.RawMessage{"generationConfig": json.RawMessage(`{"seed":2}`)}}, "spec.models[0].request_fields.generationConfig.seed"},
		{"gemini generationConfig not an object", ProviderGemini,
			ModelSpec{RequestFields: map[string]json.RawMessage{"generationConfig": json.RawMessage(`[]`)}}, "spec.models[0].request_fields.generationConfig"},
		{"invalid json", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"temperature": json.RawMessage(`{`)}}, "spec.models[0].request_fields.temperature"},
		{"empty value", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"temperature": json.RawMessage(` `)}}, "spec.models[0].request_fields.temperature"},
		{"oversize value", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"metadata": json.RawMessage(`"` + strings.Repeat("a", 9<<10) + `"`)}}, "spec.models[0].request_fields.metadata"},
		{"invalid field name", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"bad name": json.RawMessage(`1`)}}, "spec.models[0].request_fields.bad name"},
		{"too many fields", ProviderOpenAICompatible, ModelSpec{RequestFields: manyFields(33)}, "spec.models[0].request_fields"},
		{"bad conflict policy", ProviderAnthropic, ModelSpec{RequestFieldPolicy: "maybe"}, "spec.models[0].request_field_policy"},
		{"bad sort", ProviderOpenAICompatible, ModelSpec{ProviderRouting: &UpstreamProviderSpec{Sort: "cheapest"}}, "spec.models[0].provider_routing.sort"},
		{"bad data collection", ProviderOpenAICompatible, ModelSpec{ProviderRouting: &UpstreamProviderSpec{DataCollection: "maybe"}}, "spec.models[0].provider_routing.data_collection"},
		{"negative max price", ProviderOpenAICompatible,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{MaxPrice: &UpstreamMaxPrice{Prompt: ptr(-1.0)}}}, "spec.models[0].provider_routing.max_price.prompt"},
		{"fields on a CLI bridge", ProviderClaudeCode,
			ModelSpec{RequestFields: map[string]json.RawMessage{"metadata": json.RawMessage(`{}`)}}, "spec.models[0].request_fields"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := NewValidator("", nil)
			s := modelFieldProvider(tc.typ, tc.m)
			// Canonicalize runs normalize then validate, as the service does.
			normalizeProvider(&s)
			validateProvider(v, &s)
			var got []string
			for _, e := range v.Errors {
				got = append(got, e.Location)
			}
			if tc.want == "" {
				if len(v.Errors) != 0 {
					t.Fatalf("unexpected errors: %v", v.Errors)
				}
				return
			}
			for _, loc := range got {
				if loc == "body."+tc.want {
					return
				}
			}
			t.Fatalf("want an error at body.%s, got %v", tc.want, got)
		})
	}
}

func TestModelRequestFieldsCanonicalization(t *testing.T) {
	s := modelFieldProvider(ProviderOpenAICompatible, ModelSpec{
		RequestFields:      map[string]json.RawMessage{"transforms": json.RawMessage(`["middle-out"]`)},
		ProviderRouting:    &UpstreamProviderSpec{Order: []string{" b ", "a", "b", ""}, Only: []string{"z", "z"}, Sort: "price"},
		RequestFieldPolicy: "",
	})
	normalizeProvider(&s)
	m := s.Models[0]
	if m.RequestFieldPolicy != FieldPolicyConfigured {
		t.Errorf("empty policy should default to configured, got %q", m.RequestFieldPolicy)
	}
	if got, want := strings.Join(m.ProviderRouting.Order, ","), "b,a"; got != want {
		t.Errorf("order = %q, want %q (order preserved, blanks and duplicates dropped)", got, want)
	}
	if got, want := strings.Join(m.ProviderRouting.Only, ","), "z"; got != want {
		t.Errorf("only = %q, want %q", got, want)
	}

	// An empty block is dropped so a saved spec never carries a no-op.
	empty := modelFieldProvider(ProviderOpenAICompatible, ModelSpec{ProviderRouting: &UpstreamProviderSpec{}})
	normalizeProvider(&empty)
	if empty.Models[0].ProviderRouting != nil {
		t.Errorf("an empty provider_routing should be dropped, got %+v", empty.Models[0].ProviderRouting)
	}
	if empty.Models[0].RequestFields == nil {
		t.Error("request_fields must never be null")
	}
}

func TestUpstreamProviderWire(t *testing.T) {
	if _, ok := (*UpstreamProviderSpec)(nil).Wire(); ok {
		t.Fatal("a nil spec must render nothing")
	}
	if _, ok := (&UpstreamProviderSpec{}).Wire(); ok {
		t.Fatal("an empty spec must render nothing")
	}
	allow := false
	raw, ok := (&UpstreamProviderSpec{Order: []string{"provider-a"}, AllowFallbacks: &allow, MaxPrice: &UpstreamMaxPrice{Prompt: ptr(0.0)}}).Wire()
	if !ok {
		t.Fatal("expected a rendered provider block")
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["allow_fallbacks"] != false {
		t.Errorf("allow_fallbacks = %v, want false (a set false must survive)", got["allow_fallbacks"])
	}
	if _, set := got["sort"]; set {
		t.Error("an unset sort must be omitted, not sent as an empty string")
	}
	b, _ := json.Marshal(got["max_price"])
	if string(b) != `{"prompt":0}` {
		t.Errorf("max_price = %s, want {\"prompt\":0}", b)
	}
}

func ptr[T any](v T) *T { return &v }

func manyFields(n int) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for i := range n {
		m[fmt.Sprintf("f%d", i)] = json.RawMessage(`1`)
	}
	return m
}

// spellingsOf returns a field name as written, in camelCase, in upper case
// and with dashes, so every check covers the names a normalizer folds.
func spellingsOf(name string) []string {
	parts := strings.Split(name, "_")
	camel := parts[0]
	for _, p := range parts[1:] {
		camel += strings.ToUpper(p[:1]) + p[1:]
	}
	return []string{name, camel, strings.ToUpper(name), strings.ReplaceAll(name, "_", "-")}
}

// TestForbiddenRequestFieldsAreRefused: every always-forbidden field is
// refused on every HTTP provider type, in every spelling, with the reason.
func TestForbiddenRequestFieldsAreRefused(t *testing.T) {
	forbidden := []string{
		"max_tokens", "max_completion_tokens", "max_output_tokens", "n", "candidate_count", "models", "route", "model",
		"tools", "tool_choice", "tool_config", "mcp_servers", "plugins", "web_search_options", "store", "previous_response_id",
		"conversation", "prompt", "cached_content", "cache_control", "stream", "stream_options", "messages", "input",
		"contents", "system", "system_instruction", "instructions", "parallel_tool_calls", "response_format", "reasoning",
		"thinking", "service_tier", "usage",
	}
	for _, typ := range []string{ProviderOpenAICompatible, ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderOllama} {
		for _, f := range forbidden {
			for _, name := range spellingsOf(f) {
				t.Run(typ+"/"+name, func(t *testing.T) {
					v := NewValidator("", nil)
					s := modelFieldProvider(typ, ModelSpec{RequestFields: map[string]json.RawMessage{name: json.RawMessage(`1`)}})
					normalizeProvider(&s)
					validateProvider(v, &s)
					want := "body.spec.models[0].request_fields." + name
					for _, e := range v.Errors {
						if e.Location == want {
							if !strings.Contains(e.Message, "cannot be configured") {
								t.Errorf("message should give the reason: %q", e.Message)
							}
							return
						}
					}
					t.Fatalf("%s accepted on %s: %v", name, typ, v.Errors)
				})
			}
		}
	}
}

// TestUnknownRequestFieldListsTheAllowedOnes: the error tells the operator
// what they may use instead.
func TestUnknownRequestFieldListsTheAllowedOnes(t *testing.T) {
	v := NewValidator("", nil)
	s := modelFieldProvider(ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"frequency_penalty": json.RawMessage(`1`)}})
	normalizeProvider(&s)
	validateProvider(v, &s)
	if len(v.Errors) != 1 {
		t.Fatalf("errors = %v", v.Errors)
	}
	msg := v.Errors[0].Message
	for _, want := range []string{"anthropic", "metadata", "temperature", "top_p", "top_k", "stop_sequences"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should mention %q", msg, want)
		}
	}
}

// TestAllowlistedFieldsAreAccepted: each allowlist entry saves, in snake and
// camel case.
func TestAllowlistedFieldsAreAccepted(t *testing.T) {
	for typ, rules := range requestFieldAllowlist {
		for _, r := range rules {
			val := `1`
			if len(r.Sub) > 0 {
				val = `{"` + r.Sub[0] + `":1}`
			}
			for _, name := range spellingsOf(r.Name)[:2] {
				v := NewValidator("", nil)
				s := modelFieldProvider(typ, ModelSpec{RequestFields: map[string]json.RawMessage{name: json.RawMessage(val)}})
				normalizeProvider(&s)
				validateProvider(v, &s)
				if len(v.Errors) != 0 {
					t.Errorf("%s/%s refused: %v", typ, name, v.Errors)
				}
			}
		}
	}
}

// TestAllowlistHasNoForbiddenField keeps a future allowlist edit from
// re-opening a field that must never be configurable.
func TestAllowlistHasNoForbiddenField(t *testing.T) {
	for typ, rules := range requestFieldAllowlist {
		if ProviderTypes[typ].Execution == "cli_bridge" {
			t.Errorf("%s runs a CLI and cannot carry request fields", typ)
		}
		for _, r := range rules {
			if why, bad := forbiddenRequestFields[NormalizeFieldName(r.Name)]; bad {
				t.Errorf("%s allows %s, which is forbidden: %s", typ, r.Name, why)
			}
			for _, sub := range r.Sub {
				if why, bad := forbiddenRequestFields[NormalizeFieldName(sub)]; bad {
					t.Errorf("%s allows %s.%s, which is forbidden: %s", typ, r.Name, sub, why)
				}
			}
		}
	}
}

func TestNormalizeFieldName(t *testing.T) {
	for _, tc := range [][2]string{{"system_instruction", "systeminstruction"}, {"systemInstruction", "systeminstruction"},
		{"Top-P", "topp"}, {"top_p", "topp"}} {
		if got := NormalizeFieldName(tc[0]); got != tc[1] {
			t.Errorf("NormalizeFieldName(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
	if FieldConcept("stopSequences") != "stop" || FieldConcept("stop_sequences") != "stop" {
		t.Error("stop sequences are the stop setting")
	}
}
