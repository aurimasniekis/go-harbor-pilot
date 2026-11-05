package authapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
)

func TestValidationErrorsBecomeFieldErrors(t *testing.T) {
	joined := errors.Join(
		errors.New("issuer must be an https URL (http only for private-network issuers)"),
		errors.New(`invalid role mapping "ops" -> "superuser"`),
	)
	err := mapErr(joined)
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("not an API error: %v", err)
	}
	details, _ := ae.ErrorBody.Error.Details.(map[string]any)
	fields, _ := details["fields"].([]api.FieldError)
	if len(fields) != 2 {
		t.Fatalf("fields = %+v", fields)
	}
	tests := []struct {
		i        int
		location string
		contains string
	}{
		{0, "body.issuer", "https URL"},
		{1, "body.role_mappings", `"ops" -> "superuser"`},
	}
	for _, tt := range tests {
		f := fields[tt.i]
		if f.Location != tt.location || !strings.Contains(f.Message, tt.contains) {
			t.Errorf("field %d = %+v, want %s containing %q", tt.i, f, tt.location, tt.contains)
		}
	}
	if ae.ErrorBody.Error.Message != "validation failed" {
		t.Errorf("message = %q", ae.ErrorBody.Error.Message)
	}
}

func TestUnmatchedValidationKeepsMessage(t *testing.T) {
	err := mapErr(errors.New("something else is invalid"))
	var ae *api.Error
	if !errors.As(err, &ae) || ae.ErrorBody.Error.Message != "something else is invalid" || ae.ErrorBody.Error.Details != nil {
		t.Fatalf("got %+v", err)
	}
}

func TestTestMessageExplainsDiscoveryFailures(t *testing.T) {
	err := errors.New(`oidc discovery for corp failed: oidc: failed to decode provider discovery object: invalid character 'o'`)
	got := testMessage(err, "https://id.example.test/")
	want := "Couldn't read https://id.example.test/.well-known/openid-configuration. Check the issuer URL, and that this server can reach it."
	if got != want {
		t.Fatalf("got %q", got)
	}
	if got := testMessage(errors.New("boom"), ""); got != "single sign-on failed: boom" {
		t.Fatalf("other errors: %q", got)
	}
}
