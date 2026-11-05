package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// Adapter requests go through the outbound network policy: loopback and
// private endpoints only for monitors that opt in.
func TestHTTPClientEnforcesNetworkPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	c := NewHTTPClient(HTTPOptions{})
	tests := []struct {
		name    string
		ctx     context.Context
		blocked bool
	}{
		{"loopback without opt-in", context.Background(), true},
		{"loopback with the monitor's opt-in", WithPrivateNetwork(context.Background(), true), false},
		{"explicitly not allowed", WithPrivateNetwork(context.Background(), false), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Do(tt.ctx, Request{URL: srv.URL})
			var pe *Error
			blocked := errors.As(err, &pe) && pe.Kind == domain.ErrConfiguration && errors.Is(err, ErrDestinationBlocked)
			if blocked != tt.blocked {
				t.Fatalf("blocked=%v want %v (err %v)", blocked, tt.blocked, err)
			}
		})
	}
	t.Setenv("HTTPS_PROXY", srv.URL)
	t.Setenv("HTTP_PROXY", srv.URL)
	if _, err := c.Do(context.Background(), Request{URL: "http://127.0.0.1:1/"}); !errors.Is(err, ErrDestinationBlocked) {
		t.Fatalf("environment proxy must not bypass the policy: %v", err)
	}
}
