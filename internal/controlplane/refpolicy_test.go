package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
)

// Secret references are checked against the reference policy when saved.
func TestSecretRefsValidatedOnWrite(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		spec map[string]any
		loc  string
	}{
		{"account master key", AccountDef, map[string]any{"provider_id": "prv_x", "billing": "metered_api", "enabled": true, "credential_ref": "env:HP_MASTER_KEY"}, "spec.credential_ref"},
		{"account arbitrary env", AccountDef, map[string]any{"provider_id": "prv_x", "billing": "metered_api", "enabled": true, "credential_ref": "env:HOME"}, "spec.credential_ref"},
		{"quota monitor env", QuotaMonitorDef, map[string]any{"adapter": "x", "enabled": true, "api_key_ref": "env:AWS_SECRET_ACCESS_KEY"}, "spec.api_key_ref"},
		{"privacy values env", PrivacyPolicyDef, map[string]any{"required": true, "secret_values_ref": "env:GITHUB_TOKEN"}, "spec.secret_values_ref"},
		{"channel secret env", ChannelDef, map[string]any{"type": "telegram", "enabled": true, "telegram_chat_id": "1", "secret_ref": "env:HP_MASTER_KEY_FILE"}, "spec.secret_ref"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.spec)
			v := NewValidator("", nil)
			tt.kind.Canonicalize(raw, v)
			found := false
			for _, e := range v.Errors {
				if e.Location == "body."+tt.loc && strings.Contains(e.Message, "HP_SECRET_") {
					found = true
				}
			}
			if !found {
				t.Fatalf("no reference-policy error at %s: %+v", tt.loc, v.Errors)
			}
		})
	}
	ok := map[string]any{"provider_id": "prv_x", "billing": "metered_api", "enabled": true, "credential_ref": "env:HP_SECRET_ANTHROPIC"}
	raw, _ := json.Marshal(ok)
	v := NewValidator("", nil)
	AccountDef.Canonicalize(raw, v)
	for _, e := range v.Errors {
		if e.Location == "body.spec.credential_ref" {
			t.Fatalf("an allowed reference was rejected: %+v", e)
		}
	}
}
