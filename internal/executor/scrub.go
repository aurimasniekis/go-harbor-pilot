package executor

import (
	"regexp"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

// keyLike matches strings shaped like well-known API keys and bearer
// tokens. Upstream error messages sometimes echo the rejected key; these
// patterns catch keys other than the one the attempt used.
var keyLike = regexp.MustCompile(`(?i)\b(sk-[a-z0-9_\-]{8,}|sk_[a-z0-9_]{8,}|hpk_[a-z0-9_]{8,}|xai-[a-z0-9_\-]{8,}|gh[pousr]_[a-z0-9]{16,}|AIza[0-9a-z_\-]{20,})` +
	`|(bearer\s+)[a-z0-9._\-]{16,}`)

// Redacted replaces secret material in text.
const Redacted = "[redacted]"

// ScrubText removes the attempt's credential and key-like strings from
// text that came from an upstream or a CLI.
func ScrubText(s string, cred credentials.Secret) string {
	if v := cred.Reveal(); len(v) >= 6 {
		s = strings.ReplaceAll(s, v, Redacted)
	}
	return keyLike.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(strings.ToLower(m), "bearer") {
			return m[:strings.IndexAny(m, " \t")+1] + Redacted
		}
		return Redacted
	})
}

// ScrubError scrubs an error's upstream-provided text in place.
func ScrubError(e *ir.Error, cred credentials.Secret) {
	if e == nil {
		return
	}
	e.Message = ScrubText(e.Message, cred)
	e.UpstreamType = ScrubText(e.UpstreamType, cred)
}
