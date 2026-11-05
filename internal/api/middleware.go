package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
)

var reqIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RandomID returns n random bytes hex-encoded.
func RandomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RequestID assigns or propagates X-Request-ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !reqIDPattern.MatchString(id) {
			id = RandomID(12)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(observability.WithRequestID(r.Context(), id)))
	})
}

// Recoverer converts panics into a redacted 500 envelope.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					msg := "panic"
					switch t := v.(type) {
					case error:
						msg = t.Error()
					case string:
						msg = t
					}
					log.ErrorContext(r.Context(), "http.panic", "panic", observability.RedactString(msg), "stack", string(debug.Stack()))
					WriteError(w, r, Internal())
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

// SecurityHeaders sets defensive response headers for UI and API routes.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// NoStore disables caching of API responses.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Flush supports streaming handlers behind the logger.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one structured line per request and counts it by route
// pattern and status class (never by raw path).
func AccessLog(log *slog.Logger, m *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			if m != nil {
				m.HTTPRequests.WithLabelValues(route, strconv.Itoa(status/100)+"xx").Inc()
			}
			lvl := slog.LevelInfo
			if route == "/healthz" || route == "/readyz" || route == "/metrics" {
				lvl = slog.LevelDebug
			}
			log.Log(r.Context(), lvl, "http.request",
				"method", r.Method, "route", route, "status", status,
				"duration_ms", time.Since(start).Milliseconds(), "bytes", sw.bytes)
		})
	}
}
