package executor

import (
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
