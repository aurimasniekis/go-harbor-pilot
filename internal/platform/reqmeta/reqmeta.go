// Package reqmeta carries per-request client metadata (client IP, user agent)
// derived with trusted-proxy rules.
package reqmeta

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Meta describes the caller of an HTTP request.
type Meta struct {
	IP        string
	UserAgent string
}

type ctxKey struct{}

// From returns request metadata from the context.
func From(ctx context.Context) Meta {
	m, _ := ctx.Value(ctxKey{}).(Meta)
	return m
}

// With stores metadata on the context.
func With(ctx context.Context, m Meta) context.Context { return context.WithValue(ctx, ctxKey{}, m) }

// Middleware derives the client IP. X-Forwarded-For is honoured only when the
// direct peer is a trusted proxy; the right-most untrusted hop wins.
func Middleware(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ua := r.UserAgent()
			if len(ua) > 256 {
				ua = ua[:256]
			}
			m := Meta{IP: ClientIP(r, trusted), UserAgent: ua}
			next.ServeHTTP(w, r.WithContext(With(r.Context(), m)))
		})
	}
}

// ClientIP computes the client address of r.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !isTrusted(peer, trusted) {
		return peer.Unmap().String()
	}
	// Proxies may append a separate header line instead of extending the
	// first one; read them all, in order, so the right-most hop is the one
	// the nearest trusted proxy added.
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !isTrusted(a, trusted) {
			return a.Unmap().String()
		}
	}
	return peer.Unmap().String()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	a = a.Unmap()
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// RateKey groups IPv6 clients by /64 for rate limiting.
func RateKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if a.Is6() && !a.Is4In6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.Unmap().String()
}
