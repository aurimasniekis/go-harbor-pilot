package traces

import (
	"reflect"
	"testing"
)

func TestSplitSnippet(t *testing.T) {
	tests := []struct {
		name, in, plain string
		parts           []SnippetPart
	}{
		{"no match", "plain text", "plain text", []SnippetPart{{Text: "plain text"}}},
		{"brackets in content stay content", "[" + markOpen + "synthetic" + markClose + "] note", "[synthetic] note",
			[]SnippetPart{{Text: "["}, {Text: "synthetic", Match: true}, {Text: "] note"}}},
		{"two matches", markOpen + "a" + markClose + " and " + markOpen + "b" + markClose, "a and b",
			[]SnippetPart{{Text: "a", Match: true}, {Text: " and "}, {Text: "b", Match: true}}},
		{"unclosed marker", "x " + markOpen + "y", "x y", []SnippetPart{{Text: "x "}, {Text: "y", Match: true}}},
		{"empty", "", "", []SnippetPart{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain, parts := splitSnippet(tt.in)
			if plain != tt.plain || !reflect.DeepEqual(parts, tt.parts) {
				t.Fatalf("got %q %+v", plain, parts)
			}
		})
	}
}
