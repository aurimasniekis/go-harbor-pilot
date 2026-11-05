package executor

import (
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

func TestScrubText(t *testing.T) {
	cred := credentials.NewSecret("custom-key-7f3a91")
	tests := []struct {
		name, in, want string
	}{
		{"attempt credential", "invalid key custom-key-7f3a91 supplied", "invalid key [redacted] supplied"},
		{"openai-style key", "Incorrect API key provided: sk-proj-abcDEF123456", "Incorrect API key provided: [redacted]"},
		{"anthropic-style key", "invalid x-api-key: sk-ant-api03-XYZxyz0987654321", "invalid x-api-key: [redacted]"},
		{"gateway token", "token hpk_ab12cd34_secretpart", "token [redacted]"},
		{"google key", "API key AIzaSyA1234567890abcdefghij not valid", "API key [redacted] not valid"},
		{"bearer token", "header was Bearer abcdefghijklmnop1234", "header was Bearer [redacted]"},
		{"github fine-grained token", "token github_pat_11ABCDEFG0123456789_abcdefghijklmnop rejected", "token [redacted] rejected"},
		{"slack token", "xoxb-1234567890-abcdefghij is invalid", "[redacted] is invalid"},
		{"jwt access token", "expired eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.c2lnbmF0dXJl here", "expired [redacted] here"},
		{"base64 bearer", "got Bearer dGhpcyBpcyBh+c2VjcmV0/dG9rZW4== back", "got Bearer [redacted] back"},
		{"ordinary text", "rate limit reached for model sk", "rate limit reached for model sk"},
		{"short words survive", "task-scoped error in sk-1", "task-scoped error in sk-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScrubText(tt.in, cred); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	e := &ir.Error{Message: "bad custom-key-7f3a91", UpstreamType: "x"}
	ScrubError(e, cred)
	if e.Message != "bad [redacted]" {
		t.Fatalf("ScrubError: %q", e.Message)
	}
	ScrubError(nil, cred)
}

// The attempt's credential is also removed in escaped forms an upstream may
// echo it in.
func TestScrubEscapedCredential(t *testing.T) {
	cred := credentials.NewSecret(`pa"ss<wo rd>+1/x`)
	tests := []struct{ name, in string }{
		{"plain", `key pa"ss<wo rd>+1/x rejected`},
		{"json escaped", `{"error":"key pa\"ss\u003cwo rd\u003e+1/x rejected"}`},
		{"query escaped", "url ?k=pa%22ss%3Cwo+rd%3E%2B1%2Fx rejected"},
		{"path escaped", "url /k/pa%22ss%3Cwo%20rd%3E+1%2Fx rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScrubText(tt.in, cred)
			if !strings.Contains(got, Redacted) || strings.Contains(got, "rd") {
				t.Fatalf("credential survived: %q", got)
			}
		})
	}
}
