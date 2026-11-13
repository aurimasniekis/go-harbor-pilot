package claudecode

import (
	"slices"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

func lossFeatures(ls []ir.Loss) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Feature)
	}
	slices.Sort(out)
	return out
}

// What the bridge carries instead of recording a loss: max_tokens as the
// CLI's output limit, and an effort-only output_config on a system entry as
// --effort (synthetic requests written from the public Messages API docs).
func TestShapeRequestCarriesOutputSettings(t *testing.T) {
	const effortEntry = `{"role":"system","content":[],"output_config":{"effort":"low"}}`
	for _, tc := range []struct {
		name       string
		body       string
		cap        int
		wantEffort string
		wantMax    int
		has, lacks []string
	}{
		{
			name: "effort-only system entry", wantEffort: "low", wantMax: 4096,
			body:  `{"model":"m","max_tokens":4096,"messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"},` + effortEntry + `]}`,
			lacks: []string{"max_output_tokens", "mid_conversation_system", "system_message_field:anthropic_messages:output_config"},
		},
		{
			name: "request effort, later entry wins", wantEffort: "low", wantMax: 1000,
			body:  `{"model":"m","max_tokens":1000,"output_config":{"effort":"high"},"messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"},` + effortEntry + `]}`,
			lacks: []string{"mid_conversation_system"},
		},
		{
			// Claude Code's own shape (harness-wire-facts §1.7): adaptive
			// thinking with a top-level output_config.effort.
			name: "top-level output_config effort", wantEffort: "medium", wantMax: 32000,
			body: `{"model":"m","max_tokens":32000,"thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"medium"},"messages":[{"role":"user","content":"q1"}]}`,
		},
		{
			name: "top-level effort beats the thinking budget", wantEffort: "low", wantMax: 32000,
			body: `{"model":"m","max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":31999},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"q1"}]}`,
		},
		{
			name: "thinking budget alone", wantEffort: "high", wantMax: 32000,
			body: `{"model":"m","max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":31999},"messages":[{"role":"user","content":"q1"}]}`,
		},
		{
			name: "system entry with text", wantEffort: "", wantMax: 512,
			body: `{"model":"m","max_tokens":512,"messages":[{"role":"user","content":"q1"},{"role":"system","content":"Keep it short."}]}`,
			has:  []string{"mid_conversation_system"}, lacks: []string{"max_output_tokens"},
		},
		{
			name: "output_config beyond effort", wantEffort: "low", wantMax: 512,
			body: `{"model":"m","max_tokens":512,"messages":[{"role":"user","content":"q1"},{"role":"system","content":[],"output_config":{"effort":"low","other":1}}]}`,
			has:  []string{"system_message_field:anthropic_messages:output_config"},
		},
		{
			name: "max_tokens above the model's limit", cap: 2000, wantEffort: "", wantMax: 2000,
			body: `{"model":"m","max_tokens":64000,"messages":[{"role":"user","content":"q"}]}`,
			has:  []string{"max_output_tokens"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parseCached(t, tc.body)
			c, losses, err := shapeRequest(r, "m", tc.cap)
			if err != nil {
				t.Fatal(err)
			}
			if c.Effort != tc.wantEffort || c.MaxOut != tc.wantMax {
				t.Fatalf("effort %q max %d, want %q %d", c.Effort, c.MaxOut, tc.wantEffort, tc.wantMax)
			}
			fs := lossFeatures(losses)
			for _, f := range tc.has {
				if !slices.Contains(fs, f) {
					t.Errorf("missing loss %s in %v", f, fs)
				}
			}
			for _, f := range tc.lacks {
				if slices.Contains(fs, f) {
					t.Errorf("unexpected loss %s in %v", f, fs)
				}
			}
			if !slices.Contains(fs, "system_prompt") {
				t.Errorf("the CLI's prompt prefix is always a loss: %v", fs)
			}
		})
	}
}

// End to end: the CLI sends the client's max_tokens and the effort of a
// settings-only system entry, and a process is only reused with the same
// output settings.
func TestBridgeOutputSettingsReachUpstream(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "a1"}, synthetic.Reply{Text: "a2"})
	res, _ := r.executeWith(t, []ir.Message{userText("q1")}, func(x *ir.Request) { x.MaxOutputTokens = ptr(3000) })
	body := r.up.Last().Body
	if mt, _ := body["max_tokens"].(float64); mt != 3000 {
		t.Fatalf("max_tokens %v", body["max_tokens"])
	}
	// The next turn lowers effort with a settings-only system entry.
	next := parseCached(t, `{"model":"x","max_tokens":3000,"messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"`+res.Parts[0].Text+`"},{"role":"user","content":"q2"},{"role":"system","content":[],"output_config":{"effort":"low"}}]}`)
	_, ex := r.executeWith(t, next.Messages, func(x *ir.Request) { x.MaxOutputTokens = ptr(3000) })
	oc, _ := r.up.Last().Body["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Fatalf("output_config %v", r.up.Last().Body["output_config"])
	}
	if provenancePath(ex) == pathLive {
		t.Fatal("a process started at the default effort continued at low")
	}
	if fs := lossFeatures(ex.Losses); slices.Contains(fs, "mid_conversation_system") || slices.ContainsFunc(fs, func(f string) bool { return strings.HasSuffix(f, ":output_config") }) {
		t.Fatalf("losses %v", fs)
	}
}

// A client's top-level output_config.effort (Claude Code sends one with
// adaptive thinking) reaches the upstream through the CLI's --effort.
func TestBridgeTopLevelOutputEffortReachesUpstream(t *testing.T) {
	r := newRig(t)
	r.up.Push(synthetic.Reply{Text: "a1"})
	req := parseCached(t, `{"model":"x","max_tokens":3000,"thinking":{"type":"adaptive"},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"q1"}]}`)
	r.executeWith(t, req.Messages, func(x *ir.Request) { x.MaxOutputTokens, x.Reasoning = ptr(3000), req.Reasoning })
	oc, _ := r.up.Last().Body["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Fatalf("output_config %v", r.up.Last().Body["output_config"])
	}
}

func ptr[T any](v T) *T { return &v }
