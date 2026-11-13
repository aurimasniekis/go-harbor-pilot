package ir

import "testing"

func TestReasoningConfigLegacy(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		rc   *ReasoningConfig
		nil_ bool
	}{
		{"nil", nil, true},
		{"empty stays (a Gemini dynamic budget parses to it)", &ReasoningConfig{}, false},
		{"adaptive only", &ReasoningConfig{Enabled: &on, Adaptive: true}, true},
		{"output effort only", &ReasoningConfig{OutputEffort: "high"}, true},
		{"adaptive and output effort", &ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "high"}, true},
		{"budget with output effort", &ReasoningConfig{Enabled: &on, BudgetTokens: 2048, Effort: "minimal", OutputEffort: "high"}, false},
		{"disabled with output effort", &ReasoningConfig{Enabled: &off, Effort: "none", OutputEffort: "low"}, false},
		{"effort", &ReasoningConfig{Effort: "low"}, false},
		{"summary with output effort", &ReasoningConfig{Summary: "auto", OutputEffort: "low"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rc.Legacy(); (got == nil) != tc.nil_ {
				t.Errorf("Legacy() = %+v, want nil = %v", got, tc.nil_)
			}
		})
	}
}
