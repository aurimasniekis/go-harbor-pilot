package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// TestReasoningPolicyBuild builds the reasoning fixtures for a model with a
// reasoning option (D-047) and checks the reasoning fields and losses of
// the upstream body.
func TestReasoningPolicyBuild(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fixture  string
		provider string
		spec     controlplane.ModelReasoningSpec
		// want maps top-level body keys to their compact JSON; "-" means
		// absent.
		want map[string]string
		// lossHas and lossLacks are loss features.
		lossHas, lossLacks []string
		// assistant checks the first assistant message's fields.
		assistant map[string]string
	}{
		{
			name: "claude code adaptive to openrouter style", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderOpenAICompatible,
			spec:      controlplane.ModelReasoningSpec{Mode: "client", Style: "openrouter_reasoning"},
			want:      map[string]string{"reasoning": `{"effort":"high"}`, "reasoning_effort": "-", "thinking": "-", "output_config": "-"},
			lossHas:   []string{"field:anthropic_messages:context_management"},
			lossLacks: []string{"field:anthropic_messages:thinking", "field:anthropic_messages:output_config"},
		},
		{
			name: "openrouter style clamps to the model's efforts", fixture: "anthropic_adaptive_max", provider: controlplane.ProviderOpenAICompatible,
			spec:    controlplane.ModelReasoningSpec{Mode: "client", Style: "openrouter_reasoning", Efforts: []string{"low", "medium", "high"}},
			want:    map[string]string{"reasoning": `{"effort":"high"}`},
			lossHas: []string{"reasoning_effort_clamped"},
		},
		{
			name: "openrouter style takes a budget", fixture: "anthropic_enabled_budget", provider: controlplane.ProviderOpenAICompatible,
			spec: controlplane.ModelReasoningSpec{Mode: "client", Style: "openrouter_reasoning", MaxBudgetTokens: 8000},
			want: map[string]string{"reasoning": `{"max_tokens":8000}`, "thinking": "-"}, lossHas: []string{"reasoning_budget"},
		},
		{
			// A fixed budget above the request's max_tokens (32000): the
			// router gets one below the output limit.
			name: "openrouter budget stays below max_tokens", fixture: "anthropic_enabled_budget", provider: controlplane.ProviderOpenAICompatible,
			spec:    controlplane.ModelReasoningSpec{Mode: "fixed", Style: "openrouter_reasoning", BudgetTokens: 60000},
			want:    map[string]string{"reasoning": `{"max_tokens":31999}`},
			lossHas: []string{"reasoning_budget"},
		},
		{
			name: "client effort for other compatible providers", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderOllama,
			spec:      controlplane.ModelReasoningSpec{Mode: "client"},
			want:      map[string]string{"reasoning_effort": `"high"`, "reasoning": "-"},
			lossLacks: []string{"field:anthropic_messages:thinking", "field:anthropic_messages:output_config"},
		},
		{
			name: "fixed level", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderOpenAICompatible,
			spec: controlplane.ModelReasoningSpec{Mode: "fixed", Style: "openrouter_reasoning", Effort: "low"},
			want: map[string]string{"reasoning": `{"effort":"low"}`}, lossHas: []string{"reasoning_policy"},
		},
		{
			name: "off", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderOpenAICompatible,
			spec: controlplane.ModelReasoningSpec{Mode: "off", Style: "deepseek_thinking"},
			want: map[string]string{"thinking": `{"type":"disabled"}`, "reasoning_effort": "-"}, lossHas: []string{"reasoning_policy"},
		},
		{
			name: "deepseek toggle", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderOpenAICompatible,
			spec: controlplane.ModelReasoningSpec{Mode: "client", Style: "deepseek_thinking"},
			want: map[string]string{"thinking": `{"type":"enabled"}`, "reasoning_effort": `"high"`},
		},
		{
			name: "output_config with a format is still dropped", fixture: "anthropic_output_config_format", provider: controlplane.ProviderOpenAICompatible,
			spec:    controlplane.ModelReasoningSpec{Mode: "client", Style: "openrouter_reasoning"},
			want:    map[string]string{"reasoning": `{"effort":"medium"}`},
			lossHas: []string{"field:anthropic_messages:output_config"},
		},
		{
			name: "history field replays reasoning", fixture: "chat_reasoning_content_history", provider: controlplane.ProviderOpenAICompatible,
			spec:      controlplane.ModelReasoningSpec{Mode: "client", Style: "deepseek_thinking", History: "field"},
			want:      map[string]string{"reasoning_effort": `"low"`},
			lossLacks: []string{"reasoning_history"},
			assistant: map[string]string{"reasoning_content": `"17 plus 25 is 42."`},
		},
		{
			name: "history field in auto mode keeps the built-in translation", fixture: "chat_reasoning_content_history", provider: controlplane.ProviderOllama,
			spec:      controlplane.ModelReasoningSpec{Mode: "auto", History: "field", HistoryField: "thoughts"},
			want:      map[string]string{"reasoning_effort": `"low"`},
			assistant: map[string]string{"thoughts": `"17 plus 25 is 42."`},
		},
		{
			name: "native anthropic keeps adaptive in client mode", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderAnthropic,
			spec: controlplane.ModelReasoningSpec{Mode: "client"},
			want: map[string]string{"thinking": `{"display":"updates","type":"adaptive"}`, "output_config": `{"effort":"high"}`},
		},
		{
			name: "native anthropic adaptive is not clamped", fixture: "anthropic_adaptive_max", provider: controlplane.ProviderAnthropic,
			spec:      controlplane.ModelReasoningSpec{Mode: "client", Efforts: []string{"low"}},
			want:      map[string]string{"thinking": `{"type":"adaptive"}`, "output_config": `{"effort":"max"}`},
			lossLacks: []string{"reasoning_effort_clamped"},
		},
		{
			name: "native anthropic off sends no thinking", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderAnthropic,
			spec: controlplane.ModelReasoningSpec{Mode: "off"},
			want: map[string]string{"thinking": "-", "output_config": `{"effort":"high"}`},
		},
		{
			name: "gemini takes the output effort as a budget", fixture: "anthropic_adaptive_effort", provider: controlplane.ProviderGemini,
			spec: controlplane.ModelReasoningSpec{Mode: "client"},
			want: map[string]string{"generationConfig.thinkingConfig": `{"thinkingBudget":24576}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(reasoningDir, tc.fixture+".json"))
			if err != nil {
				t.Fatal(err)
			}
			r := parseFixture(t, tc.fixture, src)
			target := goldenTargetFor(tc.provider)
			spec := tc.spec
			target.ModelSpec.Reasoning = &spec
			body, losses, err := build(r, target, UpstreamProtocol(tc.provider, r.Source), r.Stream)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatal(err)
			}
			for k, want := range tc.want {
				got, ok := lookup(m, k)
				switch {
				case want == "-" && ok:
					t.Errorf("%s = %s, want absent", k, got)
				case want != "-" && got != want:
					t.Errorf("%s = %s, want %s", k, got, want)
				}
			}
			features := map[string]bool{}
			for _, l := range losses {
				features[l.Feature] = true
			}
			for _, f := range tc.lossHas {
				if !features[f] {
					t.Errorf("losses lack %s: %v", f, lossStrings(losses))
				}
			}
			for _, f := range tc.lossLacks {
				if features[f] {
					t.Errorf("losses have %s: %v", f, lossStrings(losses))
				}
			}
			if tc.assistant != nil {
				var msgs []map[string]json.RawMessage
				_ = json.Unmarshal(m["messages"], &msgs)
				for _, msg := range msgs {
					if string(msg["role"]) != `"assistant"` {
						continue
					}
					for k, want := range tc.assistant {
						if got := string(msg[k]); got != want {
							t.Errorf("assistant %s = %s, want %s", k, got, want)
						}
					}
					break
				}
			}
		})
	}
}

// lookup reads a dotted path of object keys as compact JSON.
func lookup(m map[string]json.RawMessage, path string) (string, bool) {
	keys := strings.Split(path, ".")
	for i, k := range keys {
		raw, ok := m[k]
		if !ok {
			return "", false
		}
		if i == len(keys)-1 {
			var v any
			_ = json.Unmarshal(raw, &v)
			b, _ := json.Marshal(v)
			return string(b), true
		}
		m = nil
		if json.Unmarshal(raw, &m) != nil {
			return "", false
		}
	}
	return "", false
}
