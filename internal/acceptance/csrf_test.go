package acceptance_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Cookie-authenticated changes need the public origin (or a loopback alias
// of a loopback public origin) and the session's CSRF token, and each
// failure has its own error code.
func TestCSRFOriginsAndCodes(t *testing.T) {
	e := apptest.New(t)
	pub, _ := url.Parse(e.App.Cfg.PublicOrigin())
	port := pub.Port()
	body := map[string]any{"name": "w" + port, "spec": map[string]any{}}
	tests := []struct {
		name     string
		hdr      []string
		wantCode int
		wantErr  string
	}{
		{"public origin", nil, 201, ""},
		{"localhost alias of a loopback public URL", []string{"Origin", "http://localhost:" + port}, 201, ""},
		{"other port", []string{"Origin", "http://127.0.0.1:1"}, 403, "origin_mismatch"},
		{"foreign origin", []string{"Origin", "https://evil.example"}, 403, "origin_mismatch"},
		{"stale token", []string{"X-CSRF-Token", "not-the-token"}, 403, "csrf_invalid"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := map[string]any{"name": body["name"].(string) + "-" + string(rune('a'+i)), "spec": body["spec"]}
			var out json.RawMessage
			code := e.Admin.Do("POST", "/api/v1/workspaces", b, &out, tt.hdr...)
			if code != tt.wantCode || (tt.wantErr != "" && !strings.Contains(string(out), `"code":"`+tt.wantErr+`"`)) {
				t.Fatalf("got %d %s, want %d %s", code, out, tt.wantCode, tt.wantErr)
			}
		})
	}
}
