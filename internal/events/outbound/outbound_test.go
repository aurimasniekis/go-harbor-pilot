package outbound

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A destination the network policy refuses is a permanent failure: it must
// not be retried like a transient connection error.
func TestPostBlockedDestinationIsNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	c := New()
	tests := []struct {
		name         string
		allowPrivate bool
		wantOK       bool
		retryable    bool
		errContains  string
	}{
		{"loopback without allow_private_network", false, false, false, "blocked"},
		{"loopback with allow_private_network", true, true, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := c.Post(context.Background(), srv.URL, []byte(`{}`), nil, tt.allowPrivate, 2*time.Second)
			if r.OK != tt.wantOK || r.Retryable != tt.retryable || !strings.Contains(r.Err, tt.errContains) {
				t.Fatalf("result %+v", r)
			}
		})
	}
}
