package acceptance_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// Synthetic fixtures written from the public Messages API documentation.
func systemFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "protocol", "anthropic", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sentRoles lists the roles an upstream received (Messages and Chat
// "messages", or Responses "input" items).
func sentRoles(c synthetic.Captured) string {
	msgs, ok := c.Body["messages"].([]any)
	if !ok {
		msgs, _ = c.Body["input"].([]any)
	}
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	return strings.Join(roles, ",")
}

// A /v1/messages request with system-role entries in messages[] reaches an
// Anthropic upstream unchanged (it used to be rejected with a 400 before
// routing). Translation to an OpenAI upstream keeps the entry as a system
// message in place. Placement rules are left to the upstream.
func TestMessagesSystemRoleEntries(t *testing.T) {
	e := apptest.New(t)
	ant := apptest.NewUpstream(t, "anthropic")
	oai := apptest.NewUpstream(t, "openai")
	pa := e.Provider("ant", "anthropic", ant.Srv.URL, apptest.Model{ID: "claude-opus-5-5", MaxOutput: 4096}, apptest.Model{ID: "claude-sonnet-4-5-20250929", MaxOutput: 4096})
	po := e.Provider("oai", "openai", oai.Srv.URL, apptest.Model{ID: "gpt-x", MaxOutput: 4096})
	aa := e.Account("a", pa, apptest.AccountOpts{})
	ao := e.Account("o", po, apptest.AccountOpts{})
	alias := e.Alias("claude", e.Pool("claude", "ordered", apptest.Member{Account: aa, Model: "claude-opus-5-5"}))
	chat := e.Alias("chat", e.Pool("chat", "ordered", apptest.Member{Account: ao, Model: "gpt-x"}))
	tok := e.Token(e.Binding("c", e.Plan("p", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	// A plan that maps the client's model to one the public documentation
	// lists as not accepting the role (D-042).
	older := e.Alias("older", e.Pool("older", "ordered", apptest.Member{Account: aa, Model: "claude-sonnet-4-5-20250929"}))
	olderTok := e.Token(e.Binding("c3", e.Plan("p3", apptest.PlanOpts{DefaultAlias: older}), apptest.BindingOpts{}))
	chatTok := e.Token(e.Binding("c2", e.Plan("p2", apptest.PlanOpts{DefaultAlias: chat}), apptest.BindingOpts{}))

	for _, tc := range []struct{ fixture, roles string }{
		{"messages_system_mid_conversation.json", "user,assistant,user,system"},
		{"messages_system_before_assistant.json", "user,system,assistant,user"},
		{"messages_system_settings.json", "user,assistant,user,system,system"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			r := e.Gateway("POST", "/v1/messages", tok, systemFixture(t, tc.fixture))
			if b := apptest.ReadAll(r); r.StatusCode != 200 {
				t.Fatalf("%d %s", r.StatusCode, b)
			}
			sent := ant.Last()
			if got := sentRoles(sent); got != tc.roles {
				t.Fatalf("upstream roles %s, want %s: %s", got, tc.roles, sent.Raw)
			}
		})
	}
	t.Run("message-level fields reach the upstream", func(t *testing.T) {
		r := e.Gateway("POST", "/v1/messages", tok, systemFixture(t, "messages_system_settings.json"))
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
		raw := ant.Last().Raw
		if !strings.Contains(raw, `"clear_at":"next_user_message"`) || !strings.Contains(raw, `"output_config":{"effort":"low"}`) {
			t.Fatalf("fields lost: %s", raw)
		}
	})
	t.Run("placement is checked by the upstream", func(t *testing.T) {
		n := ant.Count()
		body := `{"model":"claude-opus-5-5","max_tokens":64,"messages":[{"role":"system","content":"First."},{"role":"user","content":"Hi"}]}`
		r := e.Gateway("POST", "/v1/messages", tok, body)
		b := apptest.ReadAll(r)
		if r.StatusCode != 400 || ant.Count() != n+1 {
			t.Fatalf("want the upstream's 400, got %d (upstream calls %d -> %d): %s", r.StatusCode, n, ant.Count(), b)
		}
	})
	t.Run("inlined as user text for a model without support", func(t *testing.T) {
		r := e.Gateway("POST", "/v1/messages", olderTok, systemFixture(t, "messages_system_before_assistant.json"))
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
		sent := ant.Last()
		if got := sentRoles(sent); got != "user,assistant,user" || !strings.Contains(sent.Raw, "Avoid internal jargon.") || sent.Body["model"] != "claude-sonnet-4-5-20250929" {
			t.Fatalf("upstream got %s: %s", got, sent.Raw)
		}
		e.Flush()
		var losses string
		if err := e.App.DB.R.QueryRow(`SELECT losses FROM requests ORDER BY id DESC LIMIT 1`).Scan(&losses); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(losses, "mid_conversation_system") {
			t.Fatalf("losses %s, want mid_conversation_system", losses)
		}
	})
	t.Run("translated for an OpenAI upstream", func(t *testing.T) {
		r := e.Gateway("POST", "/v1/messages", chatTok, systemFixture(t, "messages_system_before_assistant.json"))
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
		if got := sentRoles(oai.Last()); got != "user,system,assistant,user" {
			t.Fatalf("OpenAI roles %s: %s", got, oai.Last().Raw)
		}
	})
}

// The same request against `serve --demo`: the demo's synthetic Anthropic
// upstream accepts it and receives the system entry in place.
func TestDemoAcceptsMessagesSystemRole(t *testing.T) {
	clk := clock.NewFake(time.Now())
	e := apptest.New(t, apptest.WithClock(clk), apptest.WithConfig(func(c *config.Config) { c.Demo.Enabled = true }))
	ups := e.App.DemoUpstreams
	// Let the demo's own scripted traffic (18 requests, with queued 429s on
	// the Anthropic upstream) finish first; it takes several seconds.
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		e.Flush()
		if e.QueryInt(`SELECT count(*) FROM requests`) >= 18 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("demo traffic did not finish")
		}
	}
	// Its 429s put the Anthropic accounts in a 30s cooldown; move past it so
	// the request is routed to the Anthropic upstream, not failed over.
	clk.Advance(time.Minute)
	b, err := e.App.CP.Get(context.Background(), cp.BindingDef, "claude-code-laptop")
	if err != nil {
		t.Fatal(err)
	}
	tok := e.Token(b.ID)
	r := e.Gateway("POST", "/v1/messages", tok, systemFixture(t, "messages_system_mid_conversation.json"))
	if body := apptest.ReadAll(r); r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	var found bool
	for _, c := range ups.Anthropic.Snapshot() {
		if sentRoles(c) == "user,assistant,user,system" && strings.Contains(c.Raw, "Terse mode is on") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the demo Anthropic upstream did not receive the system entry; last OpenAI request: %s", ups.OpenAI.Last().Raw)
	}
}
