package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
)

// Claude Code's request shape (harness-wire-facts §1.6).
const claudeCodeAdaptive = `{"model":"m","max_tokens":32000,"messages":[{"role":"user","content":"hi"}],` +
	`"thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"high"},` +
	`"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`

func TestAnthropicParsesAdaptiveThinkingAndOutputEffort(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		want         *ir.ReasoningConfig
		wantExt      []string
		wantRequires bool
	}{
		{
			name:    "adaptive with output effort",
			body:    claudeCodeAdaptive,
			want:    &ir.ReasoningConfig{Enabled: ptr(true), Adaptive: true, OutputEffort: "high"},
			wantExt: []string{"anthropic_messages:thinking", "anthropic_messages:output_config", "anthropic_messages:context_management"},
		},
		{
			name:    "output effort alone",
			body:    `{"model":"m","max_tokens":10,"messages":[],"output_config":{"effort":"low","format":{"type":"json_schema","schema":{}}}}`,
			want:    &ir.ReasoningConfig{OutputEffort: "low"},
			wantExt: []string{"anthropic_messages:output_config"},
		},
		{
			name:         "budget and output effort",
			body:         `{"model":"m","max_tokens":5000,"messages":[],"thinking":{"type":"enabled","budget_tokens":2048},"output_config":{"effort":"max"}}`,
			want:         &ir.ReasoningConfig{Enabled: ptr(true), BudgetTokens: 2048, Effort: "minimal", OutputEffort: "max"},
			wantExt:      []string{"anthropic_messages:output_config"},
			wantRequires: true,
		},
		{
			name:    "output_config without effort",
			body:    `{"model":"m","max_tokens":10,"messages":[],"output_config":{"format":{"type":"json_schema","schema":{}}}}`,
			wantExt: []string{"anthropic_messages:output_config"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := anthropic.ParseRequest([]byte(tc.body), anthropic.DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(r.Reasoning)
			want, _ := json.Marshal(tc.want)
			if string(got) != string(want) {
				t.Errorf("reasoning = %s, want %s", got, want)
			}
			for _, k := range tc.wantExt {
				if _, ok := r.Ext[k]; !ok {
					t.Errorf("Ext lacks %s (the raw value must stay for the native path): %v", k, r.Ext)
				}
			}
			// Adaptive thinking and an output effort alone name no
			// reasoning level, so they must not narrow routing to models
			// that declare the reasoning capability.
			requires := false
			for _, c := range ir.Requirements(r) {
				requires = requires || c == "reasoning"
			}
			if requires != tc.wantRequires {
				t.Errorf("requires reasoning = %v, want %v", requires, tc.wantRequires)
			}
		})
	}
}

// The native path sends the client's adaptive thinking and output_config
// exactly as written: adaptive is never rewritten as an enabled budget.
func TestAnthropicNativeKeepsAdaptiveThinking(t *testing.T) {
	r, err := anthropic.ParseRequest([]byte(claudeCodeAdaptive), anthropic.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(losses) != 0 {
		t.Fatalf("native target lost features: %s", lossFeatures(losses))
	}
	var got struct {
		Thinking     json.RawMessage `json:"thinking"`
		OutputConfig json.RawMessage `json:"output_config"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Thinking) != `{"type":"adaptive","display":"updates"}` {
		t.Errorf("thinking = %s", got.Thinking)
	}
	if string(got.OutputConfig) != `{"effort":"high"}` {
		t.Errorf("output_config = %s", got.OutputConfig)
	}
}

// Until a model is configured to translate them, adaptive thinking and an
// output effort reach no foreign builder: in particular the Responses
// builder must not ask for encrypted reasoning because of them.
func TestAdaptiveOnlyIsNoReasoningForForeignBuilders(t *testing.T) {
	r, err := anthropic.ParseRequest([]byte(claudeCodeAdaptive), anthropic.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	builds := map[string]func() ([]byte, []ir.Loss, error){
		"chat": func() ([]byte, []ir.Loss, error) {
			return openaichat.BuildRequest(r, openaichat.BuildOptions{Model: "m"})
		},
		"responses": func() ([]byte, []ir.Loss, error) {
			return openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: "m", Stateless: true})
		},
		"gemini": func() ([]byte, []ir.Loss, error) { return gemini.BuildRequest(r) },
	}
	for name, b := range builds {
		t.Run(name, func(t *testing.T) {
			body, _, err := b()
			if err != nil {
				t.Fatal(err)
			}
			m := decode(t, body)
			for _, k := range []string{"reasoning", "reasoning_effort", "include", "thinking"} {
				if v, ok := m[k]; ok {
					t.Errorf("%s = %v, want absent", k, v)
				}
			}
			if gc, ok := m["generationConfig"].(map[string]any); ok && gc["thinkingConfig"] != nil {
				t.Errorf("thinkingConfig = %v, want absent", gc["thinkingConfig"])
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
