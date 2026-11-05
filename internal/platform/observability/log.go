// Package observability provides structured JSON logging with secret
// redaction, request correlation and Prometheus metrics.
package observability

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// Redacted replaces secret-looking values.
const Redacted = "[REDACTED]"

var sensitiveKeys = []string{
	"token", "secret", "password", "passwd", "api_key", "apikey", "authorization",
	"cookie", "credential", "private_key", "access_key", "refresh",
}

// secretPatterns match common credential formats that might slip into log
// strings (error messages, URLs).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`sk-(?:ant-|proj-|or-)?[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`(?i)bot[0-9]{5,}:[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)=([^&\s"']+)`),
	regexp.MustCompile(`(?:user|team)-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`xai-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`hp[a-z]_[A-Za-z0-9]{6,}_[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`HPSEC_[A-Z2-7]{26}`),
}

// RedactString masks secret-looking substrings.
func RedactString(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			if i := strings.IndexByte(m, '='); i > 0 && re == secretPatterns[3] {
				return m[:i+1] + Redacted
			}
			return Redacted
		})
	}
	return s
}

func isSensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// redactAttr is a slog ReplaceAttr function.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
		return slog.Time(slog.TimeKey, a.Value.Time().UTC())
	}
	if isSensitiveKey(a.Key) && a.Value.Kind() != slog.KindGroup {
		// Keys such as "token_count" are not secrets; allow numeric values.
		switch a.Value.Kind() {
		case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration:
			return a
		}
		return slog.String(a.Key, Redacted)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, RedactString(a.Value.String()))
	}
	if a.Value.Kind() == slog.KindAny {
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, RedactString(err.Error()))
		}
	}
	return a
}

// NewLogger creates a JSON logger with stable field names and redaction.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr})
	return slog.New(&contextHandler{Handler: h}).With("service", "harbor-pilot")
}

type ctxKey int

const requestIDKey ctxKey = 1

// WithRequestID stores a request ID on the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the context request ID.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// contextHandler adds request_id from the context to every record.
type contextHandler struct {
	slog.Handler
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}

// ParseLevel parses debug|info|warn|error.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
