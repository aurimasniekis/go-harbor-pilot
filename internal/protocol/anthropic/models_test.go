package anthropic

import "testing"

func TestSupportsMidConversationSystem(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  bool
	}{
		{"claude-opus-5-5", true},
		{"claude-sonnet-5-5", true},
		{"claude-fable-5-1", true},
		{"claude-opus-4-8", true},
		{"claude-sonnet-5", false},
		{"claude-sonnet-4-5", false},
		{"claude-sonnet-4-5-20250929", false},
		{"claude-opus-4-20250514", false},
		{"claude-opus-4-1-20250805", false},
		{"claude-3-5-sonnet-latest", false},
		{"Claude-3-Haiku-20240307", false},
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", false},
		{"anthropic.claude-haiku-4-5-20251001-v1:0", false},
		{"claude-sonnet-4-5@20250929", false},
		{"claude-sonnet-4-5[1m]", false},
		// Retired "-0" aliases and Vertex "-v<n>" versions without ":".
		{"claude-opus-4-0", false},
		{"claude-sonnet-4-0", false},
		{"claude-opus-4-0-20250514", false},
		{"claude-3-5-sonnet-v2@20241022", false},
		{"claude-3-5-sonnet-v2", false},
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", false},
		// Supported models keep their own name whatever the suffix: they
		// must not collapse onto a listed base.
		{"claude-opus-5-5-v1", true},
		{"claude-opus-5-5@20261001", true},
		{"us.anthropic.claude-sonnet-5-5-20261001-v1:0", true},
		{"claude-opus-4-8-v2@20260101", true},
		{"claude-sonnet-4-5-vx", true},      // not a version suffix
		{"claude-sonnet-4-5-preview", true}, // not a known identifier
		{"claude-future-9", true},
		{"", true},
	} {
		if got := SupportsMidConversationSystem(tc.model); got != tc.want {
			t.Errorf("SupportsMidConversationSystem(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
