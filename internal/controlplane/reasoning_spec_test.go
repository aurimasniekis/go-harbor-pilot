package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelReasoningValidation(t *testing.T) {
	rs := func(r ModelReasoningSpec) ModelSpec { return ModelSpec{Reasoning: &r} }
	tests := []struct {
		name string
		typ  string
		m    ModelSpec
		want string // "" = accepted
	}{
		{"openrouter style", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Style: "openrouter_reasoning", Effort: "medium", Efforts: []string{"low", "medium", "high"}}), ""},
		{"deepseek off", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "off", Style: "deepseek_thinking"}), ""},
		{"client with the protocol default style", ProviderOpenAI, rs(ModelReasoningSpec{Mode: "client"}), ""},
		{"fixed budget", ProviderAnthropic, ModelSpec{MaxOutputTokens: 64000, Reasoning: &ModelReasoningSpec{Mode: "fixed", BudgetTokens: 8000}}, ""},
		{"history field with style default", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Style: "deepseek_thinking", History: "field"}), ""},
		{"history field in auto mode", ProviderOllama, rs(ModelReasoningSpec{History: "field", HistoryField: "reasoning_content"}), ""},
		{"none off", ProviderGemini, rs(ModelReasoningSpec{Mode: "off", Style: "none"}), ""},

		{"style not for the type", ProviderAnthropic, rs(ModelReasoningSpec{Mode: "client", Style: "openrouter_reasoning"}), "spec.models[0].reasoning.style"},
		{"unknown mode", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "sometimes"}), "spec.models[0].reasoning.mode"},
		{"style in auto mode", ProviderOpenAICompatible, rs(ModelReasoningSpec{Style: "openrouter_reasoning"}), "spec.models[0].reasoning.mode"},
		{"client with none", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Style: "none"}), "spec.models[0].reasoning.style"},
		{"fixed without a level", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "fixed", Style: "openrouter_reasoning"}), "spec.models[0].reasoning.effort"},
		{"effort and budget", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "fixed", Effort: "low", BudgetTokens: 2000}), "spec.models[0].reasoning.budget_tokens"},
		{"effort none", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Effort: "none"}), "spec.models[0].reasoning.effort"},
		{"effort outside efforts", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Effort: "max", Efforts: []string{"low", "high"}}), "spec.models[0].reasoning.effort"},
		{"unknown efforts entry", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Efforts: []string{"turbo"}}), "spec.models[0].reasoning.efforts"},
		{"budget above output cap", ProviderAnthropic, ModelSpec{MaxOutputTokens: 4000, Reasoning: &ModelReasoningSpec{Mode: "fixed", BudgetTokens: 4000}}, "spec.models[0].reasoning.budget_tokens"},
		{"budget above max budget", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", BudgetTokens: 9000, MaxBudgetTokens: 8000}), "spec.models[0].reasoning.budget_tokens"},
		{"off on a reasoning model", ProviderOpenAICompatible, ModelSpec{Capabilities: []string{"reasoning"}, Reasoning: &ModelReasoningSpec{Mode: "off"}}, "spec.models[0].reasoning.mode"},
		{"history field without a default", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", Style: "openai_reasoning_effort", History: "field"}), "spec.models[0].reasoning.history_field"},
		{"history field on a Responses provider", ProviderOpenAI, rs(ModelReasoningSpec{History: "field", HistoryField: "reasoning_content"}), "spec.models[0].reasoning.history"},
		{"reserved history field", ProviderOpenAICompatible, rs(ModelReasoningSpec{History: "field", HistoryField: "content"}), "spec.models[0].reasoning.history_field"},
		{"bad history field", ProviderOpenAICompatible, rs(ModelReasoningSpec{History: "field", HistoryField: "Bad-Name"}), "spec.models[0].reasoning.history_field"},
		{"history field without history", ProviderOpenAICompatible, rs(ModelReasoningSpec{Mode: "client", HistoryField: "x"}), "spec.models[0].reasoning.history_field"},
		{"on a CLI bridge", ProviderClaudeCode, rs(ModelReasoningSpec{Mode: "off"}), "spec.models[0].reasoning"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := NewValidator("", nil)
			s := modelFieldProvider(tc.typ, tc.m)
			normalizeProvider(&s)
			validateProvider(v, &s)
			if tc.want == "" {
				if len(v.Errors) != 0 {
					t.Fatalf("unexpected errors: %v", v.Errors)
				}
				return
			}
			var got []string
			for _, e := range v.Errors {
				if e.Location == "body."+tc.want {
					return
				}
				got = append(got, e.Location)
			}
			t.Fatalf("want an error at body.%s, got %v", tc.want, got)
		})
	}
}

func TestModelReasoningCanonicalization(t *testing.T) {
	for _, r := range []*ModelReasoningSpec{{}, {Mode: "auto"}, {Mode: "auto", History: "drop"}} {
		s := modelFieldProvider(ProviderOpenAICompatible, ModelSpec{Reasoning: r})
		normalizeProvider(&s)
		if s.Models[0].Reasoning != nil {
			t.Errorf("%+v should normalise to nil (the built-in translation)", r)
		}
	}
	s := modelFieldProvider(ProviderOpenAICompatible, ModelSpec{Reasoning: &ModelReasoningSpec{Mode: "client", Efforts: []string{"high", "low", "high", "medium"}}})
	normalizeProvider(&s)
	if got := strings.Join(s.Models[0].Reasoning.Efforts, ","); got != "low,medium,high" {
		t.Errorf("efforts = %s, want ladder order without duplicates", got)
	}
	// An unconfigured model does not grow a reasoning key on the wire.
	b, _ := json.Marshal(ModelSpec{ID: "m"})
	if strings.Contains(string(b), "reasoning") {
		t.Errorf("unset option serialised: %s", b)
	}
	p := s.Models[0].Reasoning.Policy()
	if p.Mode != "client" || len(p.Efforts) != 3 {
		t.Errorf("policy %+v", p)
	}
	if (*ModelReasoningSpec)(nil).Policy() != nil {
		t.Error("nil spec has no policy")
	}
}

// The request-field refusal of reasoning names the option to use instead.
func TestReasoningRequestFieldPointsAtTheOption(t *testing.T) {
	v := NewValidator("", nil)
	s := modelFieldProvider(ProviderOpenAICompatible, ModelSpec{RequestFields: map[string]json.RawMessage{"reasoning": json.RawMessage(`{}`)}})
	normalizeProvider(&s)
	validateProvider(v, &s)
	for _, e := range v.Errors {
		if strings.Contains(e.Message, "reasoning option") {
			return
		}
	}
	t.Fatalf("errors %v", v.Errors)
}
