package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
)

type sessionRow struct {
	id, binding, keyHash, source, confidence, harness string
	at                                                time.Time
}

// SessionHeaders are harness headers that carry a conversation id. They are
// client-supplied hints: used for grouping only when the binding trusts
// session hints, always recorded as claims.
var SessionHeaders = []string{"X-HP-Session", "X-Claude-Code-Session-Id", "Session_id", "Session-Id", "X-Session-Id", "X-Conversation-Id"}

// SessionResult is the resolved session identity of a request.
type SessionResult struct {
	ID         string
	Key        string // affinity key (never persisted raw)
	Source     string // bound_ref | harness_header
	Confidence string // high | medium | unknown
	Claim      string // the raw header claim (recorded as untrusted)
	ClaimFrom  string
}

// Sessions resolves and records sessions. Sessions are never inferred from
// the credential alone: without a trusted reference or a trusted harness
// hint the session is "unknown".
type Sessions struct {
	w     *Writer
	key   []byte
	mu    sync.Mutex
	cache map[string]string // binding|hash -> ses id
}

// NewSessions creates a resolver. key is a server HMAC key.
func NewSessions(w *Writer, key []byte) *Sessions {
	return &Sessions{w: w, key: key, cache: map[string]string{}}
}

func (s *Sessions) hash(binding, k string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("harbor-pilot/session/v1|" + binding + "|" + k))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// Resolve determines the session of a request.
func (s *Sessions) Resolve(ctx context.Context, bindingID string, boundRefs map[string]string, h http.Header, trustHints bool, harness string, now time.Time) SessionResult {
	r := SessionResult{Confidence: "unknown"}
	for _, name := range SessionHeaders {
		if v := strings.TrimSpace(h.Get(name)); v != "" && len(v) <= 200 {
			r.Claim, r.ClaimFrom = v, strings.ToLower(name)
			break
		}
	}
	var key, source, conf string
	switch {
	case boundRefs["session"] != "":
		key, source, conf = "ref:"+boundRefs["session"], "bound_ref", "high"
	case r.Claim != "" && trustHints:
		key, source, conf = "hint:"+r.Claim, "harness_header", "medium"
	default:
		return r
	}
	hash := s.hash(bindingID, key)
	ck := bindingID + "|" + hash
	s.mu.Lock()
	id, ok := s.cache[ck]
	if !ok {
		var existing string
		if err := s.w.db.R.QueryRowContext(ctx, `SELECT id FROM sessions WHERE binding_id = ? AND key_hash = ?`, bindingID, hash).Scan(&existing); err == nil {
			id = existing
		} else {
			id = ids.NewAt(ids.ConvSession, now)
		}
		if len(s.cache) > 100000 {
			s.cache = map[string]string{}
		}
		s.cache[ck] = id
	}
	s.mu.Unlock()
	s.w.enqueue(item{ses: &sessionRow{id: id, binding: bindingID, keyHash: hash, source: source, confidence: conf, harness: harness, at: now}})
	r.ID, r.Key, r.Source, r.Confidence = id, hash, source, conf
	return r
}

// Harness is a heuristic identification of the calling client. It is
// telemetry, not proof of which executable sent the request.
type Harness struct {
	Name       string // claude_code | codex | opencode | pi | generic
	Version    string
	Confidence string // medium (user agent / header match) | low
	Evidence   string
}

var (
	reClaude   = regexp.MustCompile(`(?i)\bclaude-cli/([0-9][0-9A-Za-z.\-+]*)`)
	reCodex    = regexp.MustCompile(`(?i)\bcodex[_-]cli[_-]rs/([0-9][0-9A-Za-z.\-+]*)`)
	reOpenCode = regexp.MustCompile(`(?i)\bopencode/([0-9][0-9A-Za-z.\-+]*)`)
	rePi       = regexp.MustCompile(`(?i)\bpi(?:-coding-agent)?/([0-9][0-9A-Za-z.\-+]*)`)
)

// DetectHarness inspects request headers.
func DetectHarness(h http.Header) Harness {
	ua := h.Get("User-Agent")
	match := func(re *regexp.Regexp, name string) (Harness, bool) {
		if m := re.FindStringSubmatch(ua); m != nil {
			return Harness{Name: name, Version: m[1], Confidence: "medium", Evidence: "user-agent"}, true
		}
		return Harness{}, false
	}
	if hh, ok := match(reClaude, "claude_code"); ok {
		return hh
	}
	if hh, ok := match(reCodex, "codex"); ok {
		return hh
	}
	if strings.EqualFold(h.Get("Originator"), "codex_cli_rs") {
		return Harness{Name: "codex", Confidence: "medium", Evidence: "originator header"}
	}
	if hh, ok := match(reOpenCode, "opencode"); ok {
		return hh
	}
	if hh, ok := match(rePi, "pi"); ok {
		return hh
	}
	if h.Get("X-Claude-Code-Session-Id") != "" {
		return Harness{Name: "claude_code", Confidence: "low", Evidence: "session header"}
	}
	return Harness{Name: "generic", Confidence: "low", Evidence: "no known signature"}
}
