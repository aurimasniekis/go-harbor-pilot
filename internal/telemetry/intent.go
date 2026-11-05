package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// DefaultIntents are the labels used when a plan does not list its own.
var DefaultIntents = []string{"code_generation", "debugging", "review", "refactoring", "testing", "explanation", "planning", "documentation", "chat"}

// HeuristicVersion identifies the local classifier's rules.
const HeuristicVersion = "heuristic-v1"

var intentWords = map[string]*regexp.Regexp{
	"debugging":       regexp.MustCompile(`(?i)\b(bug|error|exception|stack ?trace|panic|fails?|failing|crash|broken|fix|debug|traceback)\b`),
	"review":          regexp.MustCompile(`(?i)\b(review|code review|feedback on|critique|audit|look over)\b`),
	"refactoring":     regexp.MustCompile(`(?i)\b(refactor|clean ?up|simplify|rename|extract|restructure|dedupe)\b`),
	"testing":         regexp.MustCompile(`(?i)\b(tests?|unit test|coverage|spec|assert|e2e|integration test)\b`),
	"explanation":     regexp.MustCompile(`(?i)\b(explain|what does|how does|why does|walk me through|understand)\b`),
	"planning":        regexp.MustCompile(`(?i)\b(plan|design|architecture|approach|roadmap|trade-?offs?|proposal)\b`),
	"documentation":   regexp.MustCompile(`(?i)\b(docs?|documentation|readme|docstring|comment|changelog)\b`),
	"code_generation": regexp.MustCompile(`(?i)\b(implement|write|create|add|build|generate|scaffold)\b`),
}

// LastUserText returns the text of the last user message (tool results
// excluded). The caller bounds its length.
func LastUserText(r *ir.Request) string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role != ir.RoleUser {
			continue
		}
		var b strings.Builder
		for _, p := range m.Parts {
			if p.Kind == ir.PartText {
				b.WriteString(p.Text)
				b.WriteByte('\n')
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	return ""
}

// ClassifyHeuristic picks the label with the most keyword hits within the
// allowed labels. It runs locally; no content leaves the gateway.
func ClassifyHeuristic(text string, allowed []string) string {
	if len(allowed) == 0 {
		allowed = DefaultIntents
	}
	best, bestN := "", 0
	for _, l := range allowed {
		re := intentWords[l]
		if re == nil {
			continue
		}
		if n := len(re.FindAllStringIndex(text, 20)); n > bestN {
			best, bestN = l, n
		}
	}
	if best == "" {
		for _, l := range allowed {
			if l == "chat" {
				return "chat"
			}
		}
	}
	return best
}

// MessageFingerprints hashes each message (role, text, tool call and result
// ids) and lists tool result ids, for history deduplication.
func MessageFingerprints(r *ir.Request) ([]string, []string) {
	fps := make([]string, 0, len(r.Messages))
	var results []string
	for _, m := range r.Messages {
		h := sha256.New()
		h.Write([]byte(m.Role))
		for _, p := range m.Parts {
			h.Write([]byte{0, byte(len(p.Kind))})
			h.Write([]byte(p.Kind))
			switch {
			case p.ToolCall != nil:
				h.Write([]byte(p.ToolCall.ID + "\x00" + p.ToolCall.Name))
			case p.ToolResult != nil:
				h.Write([]byte(p.ToolResult.CallID))
				results = append(results, p.ToolResult.CallID)
			case p.Reasoning != nil:
				h.Write([]byte(p.Reasoning.Text))
			default:
				h.Write([]byte(p.Text))
			}
		}
		fps = append(fps, hex.EncodeToString(h.Sum(nil)[:12]))
	}
	if results == nil {
		results = []string{}
	}
	return fps, results
}
