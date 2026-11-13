package reasoning

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

func TestClamp(t *testing.T) {
	lmh := []string{EffortLow, EffortMedium, EffortHigh}
	for _, tc := range []struct {
		effort  string
		allowed []string
		want    string
	}{
		{"high", lmh, "high"},
		{"xhigh", lmh, "high"},
		{"max", lmh, "high"},
		{"minimal", lmh, "low"},
		{"bogus", lmh, "medium"},
		{"medium", []string{EffortLow, EffortHigh, EffortMax}, "low"},
		{"xhigh", []string{EffortLow, EffortHigh, EffortMax}, "high"},
		{"bogus", []string{EffortHigh}, "high"},
	} {
		if got := Clamp(tc.effort, tc.allowed); got != tc.want {
			t.Errorf("Clamp(%s, %v) = %s, want %s", tc.effort, tc.allowed, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	on, off := true, false
	adaptiveHigh := &ir.ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "high"}
	or, _ := Lookup(StyleOpenRouterReasoning)
	chat, _ := Lookup(StyleOpenAIReasoningEffort)
	for _, tc := range []struct {
		name   string
		rc     *ir.ReasoningConfig
		pol    Policy
		m      Method
		want   Plan
		losses string
	}{
		{"client adaptive with output effort", adaptiveHigh, Policy{Mode: ModeClient}, or, Plan{Effort: "high", Adaptive: true}, ""},
		{"client adaptive alone takes the model default", &ir.ReasoningConfig{Enabled: &on, Adaptive: true}, Policy{Mode: ModeClient, Effort: "medium"}, or, Plan{Effort: "medium", Adaptive: true}, ""},
		{"client adaptive alone without default", &ir.ReasoningConfig{Enabled: &on, Adaptive: true}, Policy{Mode: ModeClient}, or, Plan{Adaptive: true}, ""},
		{"client nothing takes the model default", nil, Policy{Mode: ModeClient, Effort: "low"}, or, Plan{Effort: "low"}, ""},
		{"client nothing without default", nil, Policy{Mode: ModeClient}, or, Plan{}, ""},
		{"client budget beats output effort", &ir.ReasoningConfig{Enabled: &on, BudgetTokens: 5000, Effort: "low", OutputEffort: "max"}, Policy{Mode: ModeClient}, or, Plan{Effort: "low", BudgetTokens: 5000}, "reasoning_effort"},
		{"client disabled", &ir.ReasoningConfig{Enabled: &off, Effort: "none", OutputEffort: "high"}, Policy{Mode: ModeClient, Effort: "high"}, or, Plan{Off: true}, ""},
		{"client unknown output effort is ignored", &ir.ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "ultra"}, Policy{Mode: ModeClient}, or, Plan{Adaptive: true}, "reasoning_effort"},
		{"client output effort none is ignored", &ir.ReasoningConfig{OutputEffort: "none"}, Policy{Mode: ModeClient, Effort: "low"}, or, Plan{Effort: "low"}, "reasoning_effort"},
		{"client effort clamped to the model's list", adaptiveHigh, Policy{Mode: ModeClient, Efforts: []string{"low", "medium"}}, or, Plan{Effort: "medium", Adaptive: true}, "reasoning_effort_clamped"},
		{"client effort clamped to the style's defaults", &ir.ReasoningConfig{Effort: "max"}, Policy{Mode: ModeClient}, chat, Plan{Effort: "high"}, "reasoning_effort_clamped"},
		{"client budget capped", &ir.ReasoningConfig{BudgetTokens: 40000}, Policy{Mode: ModeClient, MaxBudgetTokens: 16000}, or, Plan{BudgetTokens: 16000}, "reasoning_budget"},
		// A Messages budget carries the level the parser read from it; the
		// level follows the cap for styles that send a level.
		{"capped budget lowers its derived effort", &ir.ReasoningConfig{Enabled: &on, BudgetTokens: 31999, Effort: "high"}, Policy{Mode: ModeClient, MaxBudgetTokens: 5000}, chat, Plan{Effort: "low", BudgetTokens: 5000}, "reasoning_budget"},
		{"capped budget keeps an explicit effort", &ir.ReasoningConfig{Enabled: &on, BudgetTokens: 31999, Effort: "low"}, Policy{Mode: ModeClient, MaxBudgetTokens: 5000}, chat, Plan{Effort: "low", BudgetTokens: 5000}, "reasoning_budget"},
		{"fixed ignores the client", adaptiveHigh, Policy{Mode: ModeFixed, Effort: "low"}, or, Plan{Effort: "low"}, "reasoning_policy"},
		{"fixed without a client setting", nil, Policy{Mode: ModeFixed, BudgetTokens: 2048}, or, Plan{BudgetTokens: 2048}, ""},
		{"off", adaptiveHigh, Policy{Mode: ModeOff}, or, Plan{Off: true}, "reasoning_policy"},
		{"off without a client setting", nil, Policy{Mode: ModeOff}, or, Plan{Off: true}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var losses ir.Losses
			got := Resolve(tc.rc, &tc.pol, tc.m, &losses)
			if !planEqual(got, tc.want) {
				t.Errorf("plan = %+v, want %+v", got, tc.want)
			}
			var fs []string
			for _, l := range losses.List() {
				fs = append(fs, l.Feature)
			}
			if strings.Join(fs, ",") != tc.losses {
				t.Errorf("losses = %v, want %q", fs, tc.losses)
			}
		})
	}
}

func planEqual(a, b Plan) bool {
	return a.Off == b.Off && a.Effort == b.Effort && a.BudgetTokens == b.BudgetTokens && a.Adaptive == b.Adaptive &&
		a.Summary == b.Summary && a.IncludeEncrypted == b.IncludeEncrypted
}

func TestNewMethodsWritePlans(t *testing.T) {
	for _, tc := range []struct {
		method string
		plan   Plan
		want   string
	}{
		{StyleOpenRouterReasoning, Plan{Effort: "high", Adaptive: true}, `{"reasoning":{"effort":"high"}}`},
		{StyleOpenRouterReasoning, Plan{BudgetTokens: 8000, Effort: "medium"}, `{"reasoning":{"max_tokens":8000}}`},
		{StyleOpenRouterReasoning, Plan{Adaptive: true}, `{"reasoning":{"enabled":true}}`},
		{StyleOpenRouterReasoning, Plan{Off: true}, `{"reasoning":{"effort":"none"}}`},
		{StyleOpenRouterReasoning, Plan{}, `{}`},
		{StyleDeepSeekThinking, Plan{Effort: "high"}, `{"reasoning_effort":"high","thinking":{"type":"enabled"}}`},
		{StyleDeepSeekThinking, Plan{Adaptive: true}, `{"thinking":{"type":"enabled"}}`},
		{StyleDeepSeekThinking, Plan{BudgetTokens: 4000}, `{"thinking":{"type":"enabled"}}`},
		{StyleDeepSeekThinking, Plan{Off: true}, `{"thinking":{"type":"disabled"}}`},
		{StyleNone, Plan{Effort: "high"}, `{}`},
		{StyleAnthropicThinking, Plan{Effort: "high", Adaptive: true}, `{}`},
	} {
		t.Run(tc.method+"/"+tc.want, func(t *testing.T) {
			got, _, _ := write(t, tc.method, tc.plan, 32000)
			if got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMethodForAndHistory(t *testing.T) {
	if m := MethodFor(StyleOpenRouterReasoning, ir.OpenAIChat); m.Name() != StyleOpenRouterReasoning {
		t.Errorf("chat style on a chat upstream = %s", m.Name())
	}
	// A Chat style on a Responses upstream (an openai provider serving a
	// Responses client) falls back to the protocol's own method.
	if m := MethodFor(StyleOpenAIReasoningEffort, ir.OpenAIResponses); m.Name() != StyleOpenAIResponsesReasoning {
		t.Errorf("fallback = %s", m.Name())
	}
	if m := MethodFor(StyleNone, ir.Gemini); m.Name() != StyleNone {
		t.Errorf("none applies everywhere, got %s", m.Name())
	}
	for _, tc := range []struct {
		pol  *Policy
		want string
	}{
		{nil, ""},
		{&Policy{Mode: ModeClient, Style: StyleDeepSeekThinking}, ""},
		{&Policy{Mode: ModeClient, Style: StyleDeepSeekThinking, History: HistoryField}, "reasoning_content"},
		{&Policy{Mode: ModeAuto, History: HistoryField, HistoryField: "thoughts"}, "thoughts"},
		{&Policy{Mode: ModeClient, Style: StyleOpenRouterReasoning, History: HistoryField}, "reasoning"},
	} {
		if got := HistoryFieldFor(tc.pol, ir.OpenAIChat); got != tc.want {
			t.Errorf("HistoryFieldFor(%+v) = %q, want %q", tc.pol, got, tc.want)
		}
	}
}

// Apply with a policy consumes the client's Messages reasoning fields for a
// foreign upstream, but output_config only when effort is all it holds.
func TestApplyPolicyConsumesSourceFields(t *testing.T) {
	on := true
	for _, tc := range []struct {
		oc       string
		consumed []string
	}{
		{`{"effort":"high"}`, []string{AnthropicThinkingKey, OutputConfigKey}},
		// The effort is translated; the loss names only what was dropped.
		{`{"effort":"high","format":{"type":"json_schema"}}`, []string{AnthropicThinkingKey, OutputConfigKey}},
	} {
		r := &ir.Request{
			Reasoning: &ir.ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "high"},
			Ext: map[string]json.RawMessage{
				AnthropicThinkingKey: json.RawMessage(`{"type":"adaptive"}`),
				OutputConfigKey:      json.RawMessage(tc.oc),
			},
		}
		var losses ir.Losses
		body := map[string]any{}
		res := Apply(r, ir.OpenAIChat, &Target{Body: body, Losses: &losses, Policy: &Policy{Mode: ModeClient, Style: StyleOpenRouterReasoning}})
		b, _ := json.Marshal(body)
		if string(b) != `{"reasoning":{"effort":"high"}}` {
			t.Errorf("body %s", b)
		}
		if !slices.Equal(res.Consumed, tc.consumed) {
			t.Errorf("output_config %s: consumed %v, want %v", tc.oc, res.Consumed, tc.consumed)
		}
		// The native path never consumes output_config, and adaptive stays.
		res = Apply(r, ir.AnthropicMessages, &Target{Body: map[string]any{}, Losses: &losses, MaxTokens: 32000, Policy: &Policy{Mode: ModeClient}})
		if len(res.Consumed) != 0 {
			t.Errorf("native consumed %v", res.Consumed)
		}
	}
	// ModeAuto is the legacy translation.
	var losses ir.Losses
	body := map[string]any{}
	Apply(&ir.Request{Reasoning: &ir.ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "high"}}, ir.OpenAIChat,
		&Target{Body: body, Losses: &losses, Policy: &Policy{Mode: ModeAuto, History: HistoryField}})
	if len(body) != 0 {
		t.Errorf("auto wrote %v", body)
	}
}

// M1: a router budget stays below the request's output limit.
func TestOpenRouterBudgetFitsMaxTokens(t *testing.T) {
	for _, tc := range []struct {
		plan  Plan
		max   int
		want  string
		lossF string
	}{
		{Plan{BudgetTokens: 5000}, 2000, `{"reasoning":{"max_tokens":1999}}`, "reasoning_budget"},
		{Plan{BudgetTokens: 5000}, 0, `{"reasoning":{"max_tokens":5000}}`, ""},
		{Plan{BudgetTokens: 1500}, 2000, `{"reasoning":{"max_tokens":1500}}`, ""},
		{Plan{BudgetTokens: 5000, Effort: "medium"}, 1, `{"reasoning":{"effort":"medium"}}`, "reasoning_budget"},
		{Plan{BudgetTokens: 5000}, 1, `{}`, "reasoning"},
	} {
		got, losses, _ := write(t, StyleOpenRouterReasoning, tc.plan, tc.max)
		if got != tc.want {
			t.Errorf("%+v max %d: %s, want %s", tc.plan, tc.max, got, tc.want)
		}
		var fs []string
		for _, l := range losses {
			fs = append(fs, l.Feature)
		}
		if strings.Join(fs, ",") != tc.lossF {
			t.Errorf("%+v max %d: losses %v, want %q", tc.plan, tc.max, fs, tc.lossF)
		}
	}
}

// M3: effort-only styles say a budget was not sent as one.
func TestEffortStylesReportBudget(t *testing.T) {
	for _, m := range []string{StyleDeepSeekThinking, StyleOpenAIReasoningEffort, StyleOpenAIResponsesReasoning} {
		_, losses, _ := write(t, m, Plan{Effort: "high", BudgetTokens: 31999}, 32000)
		if len(losses) != 1 || losses[0].Feature != "reasoning_budget" {
			t.Errorf("%s: losses %v, want reasoning_budget", m, losses)
		}
	}
}

// L2 and L4: with a policy the client's own raw reasoning fields are
// replaced, and loss details say what really happened.
func TestApplyPolicyOwnsRawFields(t *testing.T) {
	r := &ir.Request{Ext: map[string]json.RawMessage{"openai_chat:reasoning": json.RawMessage(`{"effort":"high"}`)}}
	var losses ir.Losses
	body := map[string]any{}
	res := Apply(r, ir.OpenAIChat, &Target{Body: body, Losses: &losses, Policy: &Policy{Mode: ModeOff}})
	if !slices.Contains(res.Consumed, "openai_chat:reasoning") {
		t.Errorf("consumed %v", res.Consumed)
	}
	if len(losses.List()) != 1 || losses.List()[0].Feature != "reasoning_policy" {
		t.Errorf("losses %v", losses.List())
	}

	on := true
	r = &ir.Request{Reasoning: &ir.ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "none"},
		Ext: map[string]json.RawMessage{OutputConfigKey: json.RawMessage(`{"effort":"none","format":{}}`)}}
	losses = ir.Losses{}
	Apply(r, ir.OpenAIChat, &Target{Body: map[string]any{}, Losses: &losses, Policy: &Policy{Mode: ModeClient, Style: StyleOpenRouterReasoning}})
	var details []string
	for _, l := range losses.List() {
		details = append(details, l.Feature+": "+l.Detail)
	}
	all := strings.Join(details, "\n")
	if !strings.Contains(all, `output effort "none" is not translated`) || !strings.Contains(all, "only output_config.effort is translated; format dropped") {
		t.Errorf("details:\n%s", all)
	}
}

// M-A: in client mode a raw field the method did not write is passed on.
func TestClientModeKeepsUnwrittenRawFields(t *testing.T) {
	ext := map[string]json.RawMessage{
		"openai_chat:reasoning": json.RawMessage(`{"max_tokens":4000}`),
		"openai_chat:thinking":  json.RawMessage(`{"type":"enabled"}`),
	}
	for _, tc := range []struct {
		pol      Policy
		consumed []string
	}{
		{Policy{Mode: ModeClient, Style: StyleOpenRouterReasoning}, nil},
		{Policy{Mode: ModeClient, Style: StyleOpenRouterReasoning, Effort: "low"}, []string{"openai_chat:reasoning"}},
		{Policy{Mode: ModeClient, Style: StyleDeepSeekThinking}, nil},
		{Policy{Mode: ModeOff, Style: StyleOpenRouterReasoning}, []string{"openai_chat:reasoning", "openai_chat:thinking"}},
	} {
		var losses ir.Losses
		res := Apply(&ir.Request{Ext: ext}, ir.OpenAIChat, &Target{Body: map[string]any{}, Losses: &losses, Policy: &tc.pol})
		if !slices.Equal(res.Consumed, tc.consumed) {
			t.Errorf("%+v: consumed %v, want %v", tc.pol, res.Consumed, tc.consumed)
		}
		// A replaced field is recorded (losses are kept once per feature).
		if n := len(losses.List()); (n > 0) != (len(tc.consumed) > 0) {
			t.Errorf("%+v: %d losses, want a reasoning_policy loss exactly when a field is replaced", tc.pol, n)
		}
	}
}
