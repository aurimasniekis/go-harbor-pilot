package app

import (
	"net/http/httptest"
	"testing"
)

func TestDirectLoopback(t *testing.T) {
	tests := []struct {
		name        string
		remote      string
		header      map[string]string
		behindProxy bool
		want        bool
	}{
		{"direct loopback", "127.0.0.1:4000", nil, false, true},
		{"direct ipv6 loopback", "[::1]:4000", nil, false, true},
		{"remote client", "203.0.113.5:4000", nil, false, false},
		{"same-host proxy forwarding", "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "203.0.113.5"}, false, false},
		{"same-host proxy with Forwarded", "127.0.0.1:4000", map[string]string{"Forwarded": "for=203.0.113.5"}, false, false},
		{"trusted proxies configured", "127.0.0.1:4000", nil, true, false},
		{"unparseable remote", "garbage", nil, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/metrics", nil)
			r.RemoteAddr = tt.remote
			for k, v := range tt.header {
				r.Header.Set(k, v)
			}
			if got := directLoopback(r, tt.behindProxy); got != tt.want {
				t.Fatalf("directLoopback = %v, want %v", got, tt.want)
			}
		})
	}
}
