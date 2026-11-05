package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Changing my password rejects reusing the current one and trivially
// repetitive passwords, and points at the field that is wrong.
func TestChangePasswordRules(t *testing.T) {
	e := apptest.New(t)
	tests := []struct {
		name, current, next string
		want                int
		field               string
	}{
		{"wrong current password", "not-my-password", "a-new-strong-password", 401, "body.current_password"},
		{"same as current", "correct-horse-battery", "correct-horse-battery", 422, "body.new_password"},
		{"one repeated character", "correct-horse-battery", "aaaaaaaaaaaa", 422, "body.new_password"},
		{"too short", "correct-horse-battery", "short", 422, "body.new_password"},
		{"accepted", "correct-horse-battery", "another-long-passphrase", 204, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out json.RawMessage
			code := e.Admin.Do("POST", "/api/v1/session/password", map[string]string{"current_password": tt.current, "new_password": tt.next}, &out)
			if code != tt.want {
				t.Fatalf("status %d (%s), want %d", code, out, tt.want)
			}
			if tt.field != "" && !strings.Contains(string(out), tt.field) {
				t.Fatalf("error does not name %s: %s", tt.field, out)
			}
		})
	}
}

// Users change their own display name; the change is validated and audited.
func TestUpdateMyProfile(t *testing.T) {
	e := apptest.New(t)
	tests := []struct {
		name string
		body map[string]any
		code int
		want string
	}{
		{"new name", map[string]any{"display_name": "  Ada Lovelace  "}, 200, "Ada Lovelace"},
		{"blank", map[string]any{"display_name": "   "}, 422, ""},
		{"too long", map[string]any{"display_name": strings.Repeat("x", 121)}, 422, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out struct {
				DisplayName string `json:"display_name"`
			}
			if code := e.Admin.Do("PATCH", "/api/v1/session/profile", tt.body, &out); code != tt.code {
				t.Fatalf("status %d, want %d", code, tt.code)
			}
			if tt.want != "" && out.DisplayName != tt.want {
				t.Fatalf("display name %q, want %q", out.DisplayName, tt.want)
			}
		})
	}
	var s struct {
		User struct {
			DisplayName string `json:"display_name"`
		} `json:"user"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/session", nil, &s)
	if s.User.DisplayName != "Ada Lovelace" {
		t.Fatalf("session shows %q", s.User.DisplayName)
	}
	if n := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'user.profile.update'`); n != 1 {
		t.Fatalf("audit entries %d, want 1", n)
	}
}
