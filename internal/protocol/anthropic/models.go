package anthropic

import "strings"

// noMidConversationSystem lists Messages API models that are known to reject
// a system-role entry inside messages[] (D-042). Entries are base model names:
// a dated snapshot or "-latest" alias of a listed name matches too.
//
// Source: the public Messages API documentation for mid-conversation system
// messages, which names the supporting models (the Claude 5 generation other
// than Claude Sonnet 5, plus Claude Opus 4.8) and says the feature is not
// available on Claude Sonnet 5; and the public model deprecations table, from
// which every earlier model is taken. A model missing from this list, including
// any model released later, is assumed to support the role and is passed
// through unchanged, so the upstream stays the judge.
var noMidConversationSystem = map[string]bool{
	"claude-sonnet-5":   true,
	"claude-opus-4-7":   true,
	"claude-opus-4-6":   true,
	"claude-opus-4-5":   true,
	"claude-opus-4-1":   true,
	"claude-opus-4":     true,
	"claude-sonnet-4-6": true,
	"claude-sonnet-4-5": true,
	"claude-sonnet-4":   true,
	"claude-haiku-4-5":  true,
	"claude-3-7-sonnet": true,
	"claude-3-5-sonnet": true,
	"claude-3-5-haiku":  true,
	"claude-3-opus":     true,
	"claude-3-sonnet":   true,
	"claude-3-haiku":    true,
}

// SupportsMidConversationSystem reports whether model accepts a system-role
// entry inside messages[]. It is false only for models known not to; unknown
// models count as supporting it.
func SupportsMidConversationSystem(model string) bool {
	return !noMidConversationSystem[baseModel(model)]
}

// baseModel reduces a model identifier to its base name: lower case, without
// a platform prefix ("us.anthropic."), a version suffix ("@20250929",
// "-v1:0"), a client-side annotation ("[1m]"), a "-latest" alias suffix or a
// trailing snapshot date ("-20250929").
func baseModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "anthropic."); i >= 0 {
		m = m[i+len("anthropic."):]
	}
	if i := strings.IndexAny(m, "@["); i >= 0 {
		m = m[:i]
	}
	if i := strings.LastIndex(m, "-v"); i >= 0 && strings.Contains(m[i:], ":") {
		m = m[:i]
	}
	m = strings.TrimSuffix(m, "-latest")
	if i := strings.LastIndexByte(m, '-'); i >= 0 && isDate(m[i+1:]) {
		m = m[:i]
	}
	return m
}

func isDate(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
