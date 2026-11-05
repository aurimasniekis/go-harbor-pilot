package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type sessionCtxKey struct{}

// SessionFromContext returns the validated web session, if any.
func SessionFromContext(ctx context.Context) *SessionInfo {
	s, _ := ctx.Value(sessionCtxKey{}).(*SessionInfo)
	return s
}

// Middleware authenticates control-plane requests. Bearer hpa_ tokens take
// precedence and never fall back to cookies. Cookie sessions on unsafe
// methods must pass the Origin/Sec-Fetch-Site check and present a valid
// X-CSRF-Token; the outcome is recorded as Principal.CSRFChecked and
// enforced per operation.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if authz := r.Header.Get("Authorization"); authz != "" {
			tok, ok := strings.CutPrefix(authz, "Bearer ")
			if !ok {
				next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, Anonymous)))
				return
			}
			p, err := s.AuthenticateAPIToken(ctx, strings.TrimSpace(tok))
			if err != nil {
				p = Anonymous
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
			return
		}
		c, err := r.Cookie(s.SessionCookieName())
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, Anonymous)))
			return
		}
		// Long-lived streams validate without extending the idle deadline.
		touch := !strings.HasSuffix(r.URL.Path, "/events/stream")
		p, info, err := s.ValidateSession(ctx, c.Value, touch)
		if err != nil {
			http.SetCookie(w, s.ClearSessionCookie())
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, Anonymous)))
			return
		}
		if isUnsafe(r.Method) {
			p.CSRFChecked = s.sameOrigin(r) && s.CheckCSRF(info, r.Header.Get("X-CSRF-Token"))
		}
		ctx = context.WithValue(WithPrincipal(ctx, p), sessionCtxKey{}, info)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isUnsafe(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// sameOrigin requires Origin to equal the public origin, or (when Origin is
// absent) Sec-Fetch-Site: same-origin.
func (s *Service) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return strings.EqualFold(o, s.o.PublicOrigin)
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// SessionCookie builds the session cookie.
func (s *Service) SessionCookie(tok string, info *SessionInfo) *http.Cookie {
	// MaxAge is computed from the service clock (which may be frozen in demo
	// mode) rather than an absolute Expires, so it is always relative to now.
	return &http.Cookie{ //nolint:gosec // Secure is set whenever the public URL is https
		Name: s.SessionCookieName(), Value: tok, Path: "/", HttpOnly: true, Secure: s.o.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: max(int(info.AbsoluteExpires.Sub(s.clock.Now())/time.Second), 1),
	}
}

// ClearSessionCookie expires the session cookie.
func (s *Service) ClearSessionCookie() *http.Cookie {
	return &http.Cookie{Name: s.SessionCookieName(), Value: "", //nolint:gosec // Secure is set whenever the public URL is https
		Path: "/", HttpOnly: true, Secure: s.o.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: -1}
}
