package auth

import (
	"strings"
	"testing"
)

// OIDC client secret references follow the reference policy.
func TestOIDCClientSecretRefPolicy(t *testing.T) {
	base := func(ref string) *OIDCInput {
		return &OIDCInput{Slug: "sso", DisplayName: "SSO", Issuer: "https://idp.example.test", ClientID: "hp", ClientSecretRef: ref,
			Provisioning: "jit", DefaultRole: RoleViewer, AllowedDomains: []string{"example.test"}}
	}
	tests := []struct {
		ref string
		ok  bool
	}{
		{"env:HP_SECRET_OIDC", true},
		{"env:HP_MASTER_KEY", false},
		{"env:PATH", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			err := base(tt.ref).validate()
			if tt.ok && err != nil {
				t.Fatalf("allowed reference rejected: %v", err)
			}
			if !tt.ok && (err == nil || !strings.Contains(err.Error(), "client_secret_ref")) {
				t.Fatalf("disallowed reference accepted: %v", err)
			}
		})
	}
}

// SafeReturnTo keeps post-login redirects on this origin.
func TestSafeReturnTo(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/activity?x=1", "/activity?x=1"},
		{"/settings/users#top", "/settings/users#top"},
		{"", "/"},
		{"https://evil.example/", "/"},
		{"//evil.example", "/"},
		{"/\\evil.example", "/"},
		{"/\t/evil.example", "/"},
		{"/\n/evil.example", "/"},
		{"/\r/evil.example", "/"},
		{"/%2F%2Fevil.example", "/"},
		{"\\\\evil.example", "/"},
		{"/a\x00b", "/"},
		{"/" + strings.Repeat("a", 600), "/"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := SafeReturnTo(tt.in); got != tt.want {
				t.Fatalf("SafeReturnTo(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
