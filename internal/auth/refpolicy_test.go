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
