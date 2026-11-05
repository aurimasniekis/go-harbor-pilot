package reqmeta

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"direct client ignores forwarded headers", "203.0.113.9:5000", []string{"198.51.100.1"}, "203.0.113.9"},
		{"trusted proxy, one header", "10.0.0.2:5000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"client-supplied hop is skipped", "10.0.0.2:5000", []string{"1.2.3.4, 198.51.100.7"}, "198.51.100.7"},
		// A proxy that appends a second header line: the client-controlled
		// first line must not win.
		{"proxy appends a second header line", "10.0.0.2:5000", []string{"1.2.3.4", "198.51.100.7"}, "198.51.100.7"},
		{"chain of trusted proxies", "10.0.0.2:5000", []string{"198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, trusted); got != tt.want {
				t.Fatalf("ClientIP = %s, want %s", got, tt.want)
			}
		})
	}
}
