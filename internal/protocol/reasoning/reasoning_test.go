package reasoning

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

func TestRegistry(t *testing.T) {
	for _, n := range []string{StyleAnthropicThinking, StyleOpenAIReasoningEffort, StyleOpenAIResponsesReasoning, StyleGeminiThinkingConfig} {
		m, ok := Lookup(n)
		if !ok || m.Name() != n {
			t.Errorf("Lookup(%q) = %v, %v", n, m, ok)
		}
		if !slices.Contains(Names(), n) {
			t.Errorf("Names() lacks %s", n)
		}
		for _, e := range m.DefaultEfforts() {
			if Rank(e) < 0 {
				t.Errorf("%s: default effort %q is not on the ladder", n, e)
			}
		}
	}
	if !slices.IsSorted(Names()) {
		t.Errorf("Names() not sorted: %v", Names())
	}
	for p, want := range map[ir.Protocol]string{
		ir.AnthropicMessages: StyleAnthropicThinking,
		ir.OpenAIChat:        StyleOpenAIReasoningEffort,
		ir.OpenAIResponses:   StyleOpenAIResponsesReasoning,
		ir.Gemini:            StyleGeminiThinkingConfig,
	} {
		m := DefaultFor(p)
		if m == nil || m.Name() != want || m.Protocol() != p {
			t.Errorf("DefaultFor(%s) = %v, want %s", p, m, want)
		}
		if _, ok := m.(legacyWriter); !ok {
			t.Errorf("%s: a protocol default must keep its legacy translation", want)
		}
		if !slices.ContainsFunc(ForProtocol(p), func(x Method) bool { return x.Name() == want }) {
			t.Errorf("ForProtocol(%s) lacks %s", p, want)
		}
	}
	if DefaultFor("nope") != nil {
		t.Error("an unknown protocol has no default")
	}
	defer func() {
		if recover() == nil {
			t.Error("registering a name twice must panic")
		}
	}()
	Register(anthropicThinking{})
}

func TestRank(t *testing.T) {
	for i, e := range Ladder {
		if Rank(e) != i {
			t.Errorf("Rank(%s) = %d, want %d", e, Rank(e), i)
		}
	}
	if Rank("adaptive") != -1 || Rank("") != -1 {
		t.Error("unknown levels rank -1")
	}
}

func TestEffortBudgetTables(t *testing.T) {
	for _, tc := range []struct {
		budget int
		effort string
	}{{0, ""}, {1024, "minimal"}, {4096, "low"}, {10000, "medium"}, {24000, "high"}, {31999, "high"}, {32000, "xhigh"}} {
		if got := EffortForThinkingBudget(tc.budget); got != tc.effort {
			t.Errorf("EffortForThinkingBudget(%d) = %q, want %q", tc.budget, got, tc.effort)
		}
	}
	for _, e := range []string{"minimal", "low", "medium", "high", "xhigh"} {
		if EffortForThinkingBudget(ThinkingBudgetForEffort(e)) != e {
			t.Errorf("budget round trip for %s", e)
		}
	}
	if ThinkingBudgetForEffort("max") != 0 {
		t.Error("max has no Anthropic budget")
	}
}

func write(t *testing.T, name string, p Plan, maxTokens int) (string, []ir.Loss, Result) {
	t.Helper()
	m, ok := Lookup(name)
	if !ok {
		t.Fatalf("no method %s", name)
	}
	var losses ir.Losses
	include := []string{}
	tg := &Target{Body: map[string]any{}, Losses: &losses, MaxTokens: maxTokens, Include: &include}
	res := m.Write(p, tg)
	if len(include) > 0 {
		tg.Body["include"] = include
	}
	b, err := json.Marshal(tg.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), losses.List(), res
}

func TestLegacyMethodsWritePlans(t *testing.T) {
	for _, tc := range []struct {
		method   string
		plan     Plan
		max      int
		want     string
		consumed bool
	}{
		{StyleAnthropicThinking, Plan{Off: true}, 8000, `{}`, true},
		{StyleAnthropicThinking, Plan{Adaptive: true}, 8000, `{}`, false},
		{StyleAnthropicThinking, Plan{Effort: "medium"}, 32000, `{"thinking":{"budget_tokens":10000,"type":"enabled"}}`, true},
		{StyleAnthropicThinking, Plan{BudgetTokens: 9000}, 4000, `{"thinking":{"budget_tokens":3999,"type":"enabled"}}`, true},
		{StyleOpenAIReasoningEffort, Plan{Off: true}, 0, `{"reasoning_effort":"none"}`, false},
		{StyleOpenAIReasoningEffort, Plan{Effort: "high"}, 0, `{"reasoning_effort":"high"}`, false},
		{StyleOpenAIReasoningEffort, Plan{BudgetTokens: 10000}, 0, `{"reasoning_effort":"medium"}`, false},
		{StyleOpenAIReasoningEffort, Plan{Adaptive: true}, 0, `{}`, false},
		{StyleOpenAIResponsesReasoning, Plan{Effort: "low", Summary: "auto", IncludeEncrypted: true}, 0, `{"include":["reasoning.encrypted_content"],"reasoning":{"effort":"low","summary":"auto"}}`, false},
		{StyleOpenAIResponsesReasoning, Plan{Off: true, Summary: "auto"}, 0, `{"reasoning":{"effort":"none"}}`, false},
		{StyleGeminiThinkingConfig, Plan{Adaptive: true}, 0, `{"thinkingConfig":{"thinkingBudget":-1}}`, false},
		{StyleGeminiThinkingConfig, Plan{Off: true}, 0, `{"thinkingConfig":{"thinkingBudget":0}}`, false},
		{StyleGeminiThinkingConfig, Plan{Effort: "high", Summary: "auto"}, 0, `{"thinkingConfig":{"includeThoughts":true,"thinkingBudget":24576}}`, false},
	} {
		t.Run(tc.method+"/"+tc.want, func(t *testing.T) {
			got, _, res := write(t, tc.method, tc.plan, tc.max)
			if got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
			if c := slices.Contains(res.Consumed, AnthropicThinkingKey); c != tc.consumed {
				t.Errorf("consumed thinking = %v, want %v", c, tc.consumed)
			}
		})
	}
}

// Apply without a policy is each protocol's legacy translation; an
// adaptive-only configuration counts as none.
func TestApplyLegacy(t *testing.T) {
	on := true
	for _, tc := range []struct {
		p    ir.Protocol
		rc   *ir.ReasoningConfig
		want string
	}{
		{ir.OpenAIChat, &ir.ReasoningConfig{Effort: "minimal"}, `{"reasoning_effort":"minimal"}`},
		{ir.OpenAIChat, &ir.ReasoningConfig{Enabled: &on, Adaptive: true, OutputEffort: "high"}, `{}`},
		{ir.AnthropicMessages, &ir.ReasoningConfig{Enabled: &on, Adaptive: true}, `{}`},
		{ir.Gemini, &ir.ReasoningConfig{Effort: "max"}, `{"thinkingConfig":{"thinkingBudget":0}}`},
	} {
		var losses ir.Losses
		body := map[string]any{}
		Apply(&ir.Request{Reasoning: tc.rc}, tc.p, &Target{Body: body, Losses: &losses, MaxTokens: 8000})
		b, _ := json.Marshal(body)
		if string(b) != tc.want {
			t.Errorf("%s %+v: %s, want %s", tc.p, tc.rc, b, tc.want)
		}
	}
}
