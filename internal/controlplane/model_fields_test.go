package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
)

// modelFieldProvider builds a provider spec with one model carrying the given
// request-field settings, for validation and canonicalization tests.
func modelFieldProvider(typ string, m ModelSpec) ProviderSpec {
	base := map[string]string{
		ProviderAnthropic:        "https://api.anthropic.com",
		ProviderOpenAI:           "https://api.openai.com/v1",
		ProviderOpenAICompatible: "https://openrouter.example.com/v1",
		ProviderOllama:           "http://127.0.0.1:11434",
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
		{"free-form field", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"metadata": json.RawMessage(`{"a":1}`)}}, ""},
		{"typed pin on a compatible provider", ProviderOpenAICompatible,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{Order: []string{"relace/fp4"}}}, ""},
		{"typed pin on an official provider", ProviderOpenAI,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{Order: []string{"x"}}}, "spec.models[0].provider_routing"},
		{"typed pin plus a provider field", ProviderOpenAICompatible,
			ModelSpec{ProviderRouting: &UpstreamProviderSpec{Only: []string{"x"}}, RequestFields: map[string]json.RawMessage{"provider": json.RawMessage(`{}`)}},
			"spec.models[0].provider_routing"},
		{"structural field", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"messages": json.RawMessage(`[]`)}}, "spec.models[0].request_fields.messages"},
		{"structural field in any case", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"Stream": json.RawMessage(`true`)}}, "spec.models[0].request_fields.Stream"},
		{"invalid json", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"x": json.RawMessage(`{`)}}, "spec.models[0].request_fields.x"},
		{"empty value", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"x": json.RawMessage(` `)}}, "spec.models[0].request_fields.x"},
		{"oversize value", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"x": json.RawMessage(`"` + strings.Repeat("a", 9<<10) + `"`)}}, "spec.models[0].request_fields.x"},
		{"invalid field name", ProviderAnthropic, ModelSpec{RequestFields: map[string]json.RawMessage{"bad name": json.RawMessage(`1`)}}, "spec.models[0].request_fields.bad name"},
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
	raw, ok := (&UpstreamProviderSpec{Order: []string{"relace/fp4"}, AllowFallbacks: &allow, MaxPrice: &UpstreamMaxPrice{Prompt: ptr(0.0)}}).Wire()
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
