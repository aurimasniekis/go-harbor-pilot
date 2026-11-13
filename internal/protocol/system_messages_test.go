package protocol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
)

// Synthetic fixtures written from the public Messages API documentation
// (mid-conversation system messages); see testdata/protocol/README.md.
func systemFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "protocol", "anthropic", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sourceMessages returns the fixture's messages[] as generic JSON.
func sourceMessages(t *testing.T, body []byte) []any {
	t.Helper()
	return decode(t, body)["messages"].([]any)
}

// A system-role entry inside messages[] used to be rejected with a 400
// ("role must be user or assistant"). It is accepted and sent to Anthropic
// upstreams exactly as the client wrote it, with its position and
// message-level fields intact.
func TestAnthropicMidConversationSystemPassthrough(t *testing.T) {
	for _, name := range []string{"messages_system_mid_conversation.json", "messages_system_before_assistant.json", "messages_system_settings.json"} {
		t.Run(name, func(t *testing.T) {
			src := systemFixture(t, name)
			r, err := anthropic.ParseRequest(src, anthropic.DefaultLimits)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-opus-5-5"})
			if err != nil {
				t.Fatal(err)
			}
			if len(losses) != 0 {
				t.Fatalf("native target lost features: %s", lossFeatures(losses))
			}
			want, got := sourceMessages(t, src), decode(t, body)["messages"].([]any)
			if len(got) != len(want) {
				t.Fatalf("message count %d, want %d: %s", len(got), len(want), body)
			}
			for i := range want {
				w, g := want[i].(map[string]any), got[i].(map[string]any)
				if g["role"] != w["role"] {
					t.Fatalf("messages[%d].role = %v, want %v", i, g["role"], w["role"])
				}
				if w["role"] == "system" && !reflect.DeepEqual(g, w) {
					t.Fatalf("messages[%d] changed:\n got %v\nwant %v", i, g, w)
				}
			}
		})
	}
}

// The CLI bridge understands only user and assistant turns, so it asks for
// the portable form and is told what was dropped.
func TestAnthropicSystemMessagesInlinedOnRequest(t *testing.T) {
	r, err := anthropic.ParseRequest(systemFixture(t, "messages_system_settings.json"), anthropic.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "m", InlineSystemMessages: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range decode(t, body)["messages"].([]any) {
		if m.(map[string]any)["role"] == "system" {
			t.Fatalf("system role left in inlined body: %s", body)
		}
	}
	lf := lossFeatures(losses)
	for _, f := range []string{"mid_conversation_system(approximate)", "system_message_field:anthropic_messages:clear_at(drop)", "system_message_field:anthropic_messages:output_config(drop)"} {
		if !strings.Contains(lf, f) {
			t.Fatalf("losses %s, missing %s", lf, f)
		}
	}
}

func TestAnthropicSystemMessagesTranslated(t *testing.T) {
	parse := func(t *testing.T, name string) *ir.Request {
		t.Helper()
		r, err := anthropic.ParseRequest(systemFixture(t, name), anthropic.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Run("chat keeps an in-place system message", func(t *testing.T) {
		body, losses, err := openaichat.BuildRequest(parse(t, "messages_system_before_assistant.json"), openaichat.BuildOptions{Model: "gpt-x"})
		if err != nil {
			t.Fatal(err)
		}
		if ir.HasReject(losses) {
			t.Fatalf("losses %s", lossFeatures(losses))
		}
		msgs := decode(t, body)["messages"].([]any)
		var roles []string
		for _, m := range msgs {
			roles = append(roles, m.(map[string]any)["role"].(string))
		}
		if strings.Join(roles, ",") != "user,system,assistant,user" {
			t.Fatalf("roles %v", roles)
		}
		if c := msgs[1].(map[string]any)["content"]; c != "The reader is a new team member.\n\nAvoid internal jargon." {
			t.Fatalf("system content %q", c)
		}
	})
	t.Run("chat drops settings-only messages and reports fields", func(t *testing.T) {
		body, losses, err := openaichat.BuildRequest(parse(t, "messages_system_settings.json"), openaichat.BuildOptions{Model: "gpt-x"})
		if err != nil {
			t.Fatal(err)
		}
		msgs := decode(t, body)["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if len(msgs) != 4 || last["role"] != "system" || last["content"] != "Reminder: keep changes small." {
			t.Fatalf("messages %v", msgs)
		}
		lf := lossFeatures(losses)
		if !strings.Contains(lf, "system_message_field:anthropic_messages:clear_at(drop)") || !strings.Contains(lf, "system_message_field:anthropic_messages:output_config(drop)") || ir.HasReject(losses) {
			t.Fatalf("losses %s", lf)
		}
	})
	t.Run("responses keeps a system input item", func(t *testing.T) {
		body, losses, err := openairesponses.BuildRequest(parse(t, "messages_system_mid_conversation.json"), openairesponses.BuildOptions{Model: "gpt-x", Stateless: true})
		if err != nil {
			t.Fatal(err)
		}
		if ir.HasReject(losses) {
			t.Fatalf("losses %s", lossFeatures(losses))
		}
		m := decode(t, body)
		if m["instructions"] != "You are a careful assistant for a fictional bakery." {
			t.Fatalf("instructions %v", m["instructions"])
		}
		in := m["input"].([]any)
		last := in[len(in)-1].(map[string]any)
		text := last["content"].([]any)[0].(map[string]any)["text"]
		if len(in) != 4 || last["role"] != "system" || text != "Terse mode is on: answer in under 20 words." {
			t.Fatalf("input %v", in)
		}
	})
	t.Run("gemini sends user text and reports an approximation", func(t *testing.T) {
		body, losses, err := gemini.BuildRequest(parse(t, "messages_system_mid_conversation.json"), gemini.BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if lf := lossFeatures(losses); !strings.Contains(lf, "mid_conversation_system(approximate)") || ir.HasReject(losses) {
			t.Fatalf("losses %s", lf)
		}
		contents := decode(t, body)["contents"].([]any)
		last := contents[len(contents)-1].(map[string]any)
		raw, _ := json.Marshal(last)
		if last["role"] != "user" || !strings.Contains(string(raw), "Terse mode is on") || !strings.Contains(string(raw), "And on Sundays?") {
			t.Fatalf("last content %s", raw)
		}
	})
}

// A Chat client's later system message stays portable on an Anthropic
// upstream: whether the target model accepts the in-conversation system role
// is model dependent, so it is sent as user text and reported.
func TestChatSystemMessageToAnthropicIsUserText(t *testing.T) {
	body := `{"model":"gpt-x","messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"Hi"},{"role":"system","content":"Answer in French."}]}`
	r, err := openaichat.ParseRequest([]byte(body), openaichat.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	ab, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lossFeatures(losses), "mid_conversation_system(approximate)") {
		t.Fatalf("losses %s", lossFeatures(losses))
	}
	m := decode(t, ab)
	msgs := m["messages"].([]any)
	raw, _ := json.Marshal(msgs)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" || !strings.Contains(string(raw), "Answer in French.") {
		t.Fatalf("messages %s", raw)
	}
	if m["system"].([]any)[0].(map[string]any)["text"] != "Be brief." {
		t.Fatalf("system %v", m["system"])
	}
}

// Support for a system-role entry inside messages[] depends on the target
// model (D-042). A supporting model gets it unchanged; a model the public
// documentation lists as unsupported gets it as user text in place, with the
// same approximate loss as other non-supporting targets; a model the gateway
// knows nothing about keeps the pass-through, so the upstream decides.
func TestAnthropicMidConversationSystemPerModel(t *testing.T) {
	cases := []struct {
		name, model string
		native      bool
	}{
		{"supported", "claude-opus-5-5", true},
		{"supported older generation", "claude-opus-4-8", true},
		{"unsupported dated snapshot", "claude-sonnet-4-5-20250929", false},
		{"unsupported alias", "claude-sonnet-5", false},
		{"unknown", "claude-future-9", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := systemFixture(t, "messages_system_before_assistant.json")
			r, err := anthropic.ParseRequest(src, anthropic.DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: tc.model})
			if err != nil {
				t.Fatal(err)
			}
			got := decode(t, body)["messages"].([]any)
			if tc.native {
				if len(losses) != 0 {
					t.Fatalf("pass-through lost features: %s", lossFeatures(losses))
				}
				want := sourceMessages(t, src)
				if !reflect.DeepEqual(got[1], want[1]) || len(got) != len(want) {
					t.Fatalf("messages changed:\n got %v\nwant %v", got, want)
				}
				return
			}
			if lf := lossFeatures(losses); lf != "mid_conversation_system(approximate)" {
				t.Fatalf("losses %s, want only mid_conversation_system(approximate)", lf)
			}
			// The instruction joins the user turn it follows, in place,
			// ahead of the assistant reply.
			var roles []string
			for _, m := range got {
				roles = append(roles, m.(map[string]any)["role"].(string))
			}
			if strings.Join(roles, ",") != "user,assistant,user" {
				t.Fatalf("roles %v: %s", roles, body)
			}
			raw, _ := json.Marshal(got[0])
			for _, s := range []string{"Summarize the example ticket.", "The reader is a new team member.", "Avoid internal jargon."} {
				if !strings.Contains(string(raw), s) {
					t.Fatalf("first user turn %s, missing %q", raw, s)
				}
			}
		})
	}
	t.Run("unsupported drops message-level fields", func(t *testing.T) {
		r, err := anthropic.ParseRequest(systemFixture(t, "messages_system_settings.json"), anthropic.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: "claude-haiku-4-5-20251001"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), `"role":"system"`) {
			t.Fatalf("system role left in body: %s", body)
		}
		lf := lossFeatures(losses)
		for _, f := range []string{"mid_conversation_system(approximate)", "system_message_field:anthropic_messages:clear_at(drop)", "system_message_field:anthropic_messages:output_config(drop)"} {
			if !strings.Contains(lf, f) {
				t.Fatalf("losses %s, missing %s", lf, f)
			}
		}
	})
}
