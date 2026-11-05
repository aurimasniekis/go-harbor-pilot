package bridge

import (
	"net/http"
	"strings"
	"testing"
)

// The session token is accepted only as a bearer header on the exact
// loopback origin; the URL itself carries no secret.
func TestMCPServerAuthentication(t *testing.T) {
	m := NewMCPServer()
	defer m.Close()
	s, err := m.NewSession([]MCPTool{{Name: "t", InputSchema: []byte(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.URL(), s.Token()) {
		t.Fatalf("URL carries the token: %s", s.URL())
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	tests := []struct {
		name   string
		url    string
		auth   string
		host   string
		status int
	}{
		{"bearer header", s.URL(), "Bearer " + s.Token(), "", 200},
		{"no token", s.URL(), "", "", 401},
		{"wrong token", s.URL(), "Bearer nope", "", 401},
		{"token in the path (old form)", s.URL() + "/" + s.Token(), "", "", 404},
		{"foreign Host header (DNS rebinding)", s.URL(), "Bearer " + s.Token(), "evil.example:80", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, tt.url, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			if tt.host != "" {
				req.Host = tt.host
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tt.status)
			}
		})
	}
}
