package httpapi

import (
	"net/http"
	"slices"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

func TestClientHeadersDropOAuthBeta(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"none", nil, nil},
		{"plain flags kept", []string{"interleaved-thinking-2025-05-14,context-management-2025-06-27"},
			[]string{"interleaved-thinking-2025-05-14,context-management-2025-06-27"}},
		{"oauth in the middle", []string{"claude-code-20250219, oauth-2025-04-20 ,interleaved-thinking-2025-05-14"},
			[]string{"claude-code-20250219,interleaved-thinking-2025-05-14"}},
		{"oauth alone drops the header", []string{"oauth-2025-04-20"}, nil},
		{"later version and case", []string{"OAuth-2027-01-01", "files-api-2025-04-14"}, []string{"files-api-2025-04-14"}},
		{"repeated lines", []string{"a-1", "oauth-2025-04-20,b-2"}, []string{"a-1", "b-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := http.Header{}
			for _, v := range c.in {
				src.Add("anthropic-beta", v)
			}
			src.Set("anthropic-version", "2023-06-01")
			dst := http.Header{}
			ClientHeaders(dst, src, ir.AnthropicMessages)
			if got := dst.Values("anthropic-beta"); !slices.Equal(got, c.want) {
				t.Fatalf("anthropic-beta = %q, want %q", got, c.want)
			}
			if dst.Get("anthropic-version") != "2023-06-01" {
				t.Fatalf("anthropic-version not forwarded: %v", dst)
			}
		})
	}
	t.Run("not forwarded to other protocols", func(t *testing.T) {
		src := http.Header{"Anthropic-Beta": {"a-1"}}
		dst := http.Header{}
		ClientHeaders(dst, src, ir.OpenAIChat)
		if len(dst) != 0 {
			t.Fatalf("headers leaked to an OpenAI upstream: %v", dst)
		}
	})
}
