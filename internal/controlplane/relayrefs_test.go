package controlplane

import (
	"encoding/json"
	"testing"
)

// A Pulse relay's file references get exactly one message per field.
func TestRelayFileRefsReportOneProblem(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want int
	}{
		{"plain path", "/etc/pulse.creds", 1},
		{"env reference", "env:HP_SECRET_PULSE", 1},
		{"empty file reference", "file:", 1},
		{"file reference", "file:/etc/harbor/pulse.creds", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"type": "pulse", "enabled": true, "pulse_url": "tls://pulse.example.test:4222", "creds_ref": tt.ref})
			v := NewValidator("", nil)
			DestinationDef.Canonicalize(raw, v)
			n := 0
			for _, e := range v.Errors {
				if e.Location == "body.spec.creds_ref" {
					n++
				}
			}
			if n != tt.want {
				t.Fatalf("creds_ref problems = %d, want %d: %+v", n, tt.want, v.Errors)
			}
		})
	}
}
