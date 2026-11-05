package privacy

import (
	"context"
	"strings"
	"testing"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// Synthetic credential-shaped values (not real secrets).
const (
	fakeAnthropic = "sk-ant-api03-SYNTHETICsyntheticSYNTHETIC0000"
	fakeGitHub    = "ghp_SYNTHETICtoken0000000000000000000000"
	fakeAWS       = "AKIASYNTHETIC0000000"
)

func policy(t *testing.T, s cp.PrivacyPolicySpec, secrets ...string) *Policy {
	t.Helper()
	if len(s.Detectors) == 0 {
		s.Detectors = cp.BuiltinDetectors
	}
	p, err := Compile("pvp_test", s, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectors(t *testing.T) {
	p := policy(t, cp.PrivacyPolicySpec{PersonalData: []string{"email", "credit_card", "iban"}, Supplementary: []string{"high_entropy"}}, "correct-horse-battery")
	tests := []struct {
		name, text, rule, action string
	}{
		{"anthropic key", "key " + fakeAnthropic + " end", "anthropic_key", "mask"},
		{"github token", fakeGitHub, "github_token", "mask"},
		{"aws access key", "id=" + fakeAWS, "aws_access_key", "mask"},
		{"private key", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", "private_key", "mask"},
		{"secret assignment", `DB_PASSWORD="hunter2hunter2"`, "secret_assignment", "mask"},
		{"credential url", "postgres://app:s3cretpw@localhost:5432/x", "credential_url", "mask"},
		{"configured secret", "the value correct-horse-battery leaked", "configured_secrets", "mask"},
		{"email", "mail bob@example.test now", "email", "mask"},
		{"credit card (luhn)", "card 4111 1111 1111 1111", "credit_card", "mask"},
		{"iban", "GB82 WEST 1234 5698 7654 32", "iban", "mask"},
		{"high entropy warns only", "blob Zq8xP2mW7vK4nR9tY3bL6cJ1hF5gD0sAeQwErTy", "high_entropy", "warn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := p.Scan(tt.text)
			if len(fs) != 1 || fs[0].Rule != tt.rule || fs[0].Action != tt.action {
				t.Fatalf("findings %+v", fs)
			}
		})
	}
	t.Run("no false positives on code", func(t *testing.T) {
		code := "func main() {\n\tfmt.Println(\"hello\")\n\tx := computeTotal(items, 42)\n}"
		if fs := p.Scan(code); len(fs) != 0 {
			t.Fatalf("%+v", fs)
		}
	})
	t.Run("invalid card number is ignored", func(t *testing.T) {
		if fs := p.Scan("4111 1111 1111 1112"); len(fs) != 0 {
			t.Fatalf("%+v", fs)
		}
	})
}

func TestPrecedence(t *testing.T) {
	p := policy(t, cp.PrivacyPolicySpec{
		Rules: []cp.PrivacyRule{{Name: "internal_host", Pattern: `prod-db-[0-9]+`, Action: "block"}, {Name: "ticket", Pattern: `TICKET-[0-9]+`, Action: "warn"}},
		Allow: []cp.PrivacyAllowRule{{Name: "test-keys", Pattern: `sk-ant-api03-SYNTHETIC.*`, Detectors: []string{"anthropic_key"}},
			{Name: "never-unblocks", Pattern: `prod-db-1`}},
	})
	fs := p.Scan("use " + fakeAnthropic + " on prod-db-1 for TICKET-9")
	got := map[string]string{}
	for _, f := range fs {
		got[f.Rule] = f.Action
	}
	if got["anthropic_key/test-keys"] != "allow" || got["internal_host"] != "block" || got["ticket"] != "warn" {
		t.Fatalf("%+v", fs)
	}
}

func TestMaskRequestPlaceholders(t *testing.T) {
	p := policy(t, cp.PrivacyPolicySpec{RestoreInResponses: true})
	v := NewVault(nil)
	req := &ir.Request{
		System: []ir.Part{{Kind: ir.PartText, Text: "token " + fakeGitHub}},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: "my key is " + fakeAnthropic}}},
			{Role: ir.RoleAssistant, Parts: []ir.Part{
				{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: "c1", Name: "run", Arguments: `{"cmd": "export KEY=` + fakeAnthropic + `\nrun", "n": 1}`}},
				{Kind: ir.PartSealed, Sealed: &ir.Sealed{Family: "anthropic", Kind: "thinking_signature", Data: fakeAnthropic}},
			}},
			{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartToolResult, ToolResult: &ir.ToolResult{CallID: "c1", Content: []ir.Part{{Kind: ir.PartText, Text: "ok " + fakeAnthropic}}}}}},
		}}
	orig := CloneRequest(req)
	sum, err := p.MaskRequest(req, v, "cbn_1|ses_1")
	if err != nil {
		t.Fatal(err)
	}
	user := req.Messages[0].Parts[0].Text
	ph := PlaceholderRE.FindString(user)
	if ph == "" || strings.Contains(user, fakeAnthropic) {
		t.Fatalf("not masked: %q", user)
	}
	args := req.Messages[1].Parts[0].ToolCall.Arguments
	if strings.Contains(args, fakeAnthropic) || !strings.Contains(args, ph) || !strings.Contains(args, `\nrun"`) || !strings.Contains(args, `"n": 1`) {
		t.Fatalf("JSON masking changed structure or missed the value: %s", args)
	}
	if req.Messages[1].Parts[1].Sealed.Data != fakeAnthropic {
		t.Fatal("sealed state was rewritten")
	}
	if !strings.Contains(req.Messages[2].Parts[0].ToolResult.Content[0].Text, ph) {
		t.Fatal("same value in the same scope must reuse the placeholder")
	}
	if sum.Masked != 4 || orig.Messages[0].Parts[0].Text != "my key is "+fakeAnthropic {
		t.Fatalf("summary %+v / original mutated", sum)
	}
	other, _ := p.MaskRequest(CloneRequest(orig), v, "cbn_2|ses_9")
	_ = other
	if ph2 := PlaceholderRE.FindString(CloneRequest(orig).Messages[0].Parts[0].Text); ph2 != "" {
		t.Fatal("clone shares state")
	}
	r2 := CloneRequest(orig)
	_, _ = p.MaskRequest(r2, v, "cbn_2|ses_9")
	if PlaceholderRE.FindString(r2.Messages[0].Parts[0].Text) == ph {
		t.Fatal("placeholders must be isolated between scopes")
	}
	// Placeholders from another scope are not restored.
	if out := p.NewRestorer(v, "cbn_2|ses_9").restoreText(ph); out != ph {
		t.Fatal("cross-scope restoration")
	}
}

func TestRestorerAcrossChunks(t *testing.T) {
	p := policy(t, cp.PrivacyPolicySpec{RestoreInResponses: true})
	v := NewVault(nil)
	req := &ir.Request{Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: fakeAnthropic}}}}}
	_, _ = p.MaskRequest(req, v, "s")
	ph := req.Messages[0].Parts[0].Text
	text := "Your key " + ph + " is set."
	for split := 1; split < len(text); split++ {
		r := p.NewRestorer(v, "s")
		var got strings.Builder
		for _, e := range append(append(r.Event(ir.Event{Kind: ir.EvDelta, Index: 0, Delta: text[:split]}),
			r.Event(ir.Event{Kind: ir.EvDelta, Index: 0, Delta: text[split:]})...), r.Event(ir.Event{Kind: ir.EvPartStop, Index: 0})...) {
			got.WriteString(e.Delta)
		}
		if got.String() != "Your key "+fakeAnthropic+" is set." {
			t.Fatalf("split %d: %q", split, got.String())
		}
	}
}

func TestToolArgRestorationIsGuarded(t *testing.T) {
	v := NewVault(nil)
	mk := func(s cp.PrivacyPolicySpec) (*Policy, string) {
		p := policy(t, s)
		req := &ir.Request{Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: fakeAnthropic}}}}}
		_, _ = p.MaskRequest(req, v, "s")
		return p, req.Messages[0].Parts[0].Text
	}
	run := func(p *Policy, tool, args string) string {
		r := p.NewRestorer(v, "s")
		var b strings.Builder
		for _, e := range r.Event(ir.Event{Kind: ir.EvPartStart, Index: 0, Part: &ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{Name: tool}}}) {
			b.WriteString(e.Delta)
		}
		for _, e := range r.Event(ir.Event{Kind: ir.EvDelta, Index: 0, Delta: args}) {
			b.WriteString(e.Delta)
		}
		for _, e := range r.Event(ir.Event{Kind: ir.EvPartStop, Index: 0}) {
			b.WriteString(e.Delta)
		}
		return b.String()
	}
	off, ph := mk(cp.PrivacyPolicySpec{})
	if got := run(off, "set_env", `{"value":"`+ph+`"}`); strings.Contains(got, fakeAnthropic) {
		t.Fatal("restoration must be off by default")
	}
	on, ph := mk(cp.PrivacyPolicySpec{ToolArgRestore: cp.ToolArgRestore{Enabled: true, Tools: []string{"set_env"}, Fields: []string{"value"}, AllowedHosts: []string{"api.internal.test"}}})
	if got := run(on, "set_env", `{"value":"`+ph+`"}`); !strings.Contains(got, fakeAnthropic) {
		t.Fatalf("authorized restoration failed: %s", got)
	}
	if got := run(on, "http_get", `{"value":"`+ph+`"}`); strings.Contains(got, fakeAnthropic) {
		t.Fatal("unlisted tool received the secret")
	}
	if got := run(on, "set_env", `{"other":"`+ph+`"}`); strings.Contains(got, fakeAnthropic) {
		t.Fatal("unlisted field received the secret")
	}
	if got := run(on, "set_env", `{"value":"https://evil.example/?k=`+ph+`"}`); strings.Contains(got, fakeAnthropic) {
		t.Fatal("secret injected into a URL to an unlisted host")
	}
	if got := run(on, "set_env", `{"value":"https://api.internal.test/?k=`+ph+`"}`); !strings.Contains(got, fakeAnthropic) {
		t.Fatal("allowlisted host should receive the secret")
	}
	_ = context.Background
}

func TestSandboxDoesNotRetain(t *testing.T) {
	p := policy(t, cp.PrivacyPolicySpec{})
	fs, masked, sum := p.Test("x " + fakeGitHub)
	if len(fs) != 1 || strings.Contains(masked, fakeGitHub) || sum.Masked != 1 {
		t.Fatalf("%+v %q %+v", fs, masked, sum)
	}
}
