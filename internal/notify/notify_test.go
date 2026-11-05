package notify

import (
	"strings"
	"testing"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

func TestQuietHours(t *testing.T) {
	ch := cp.ChannelSpec{Timezone: "Europe/Vilnius", QuietHours: cp.QuietHours{Enabled: true, Start: "22:00", End: "07:00"}}
	tests := []struct {
		utc   string
		quiet bool
	}{
		{"2026-11-05T19:59:00Z", false}, // 21:59 local
		{"2026-11-05T20:00:00Z", true},  // 22:00 local
		{"2026-11-06T03:00:00Z", true},  // 05:00 local
		{"2026-11-06T05:00:00Z", false}, // 07:00 local
	}
	for _, tt := range tests {
		now, _ := time.Parse(time.RFC3339, tt.utc)
		if got := inQuietHours(ch, now); got != tt.quiet {
			t.Errorf("%s: quiet=%v", tt.utc, got)
		}
	}
}

func TestRenderIsBoundedAndTemplated(t *testing.T) {
	env := envelope{Name: "budget.threshold.exceeded", Severity: "critical", Data: map[string]any{"budget": "ci", "spent": "12.50", "big": strings.Repeat("x", 10000)}}
	title, body := render("", env, "alert", 2)
	if title != "Budget threshold exceeded" || len(body) > maxBody+3 || !strings.Contains(body, "+2 similar") {
		t.Fatalf("%q / %d", title, len(body))
	}
	_, body = render(`{{.Data.budget | upper}} spent {{.Data.spent}} {{.Data.missing}}`, env, "alert", 0)
	if body != "CI spent 12.50 " {
		t.Fatalf("%q", body)
	}
	if _, err := parseTemplate("{{.Data.x"); err == nil {
		t.Fatal("broken template accepted")
	}
}

func TestEventFilter(t *testing.T) {
	f := cp.EventFilter{Events: []string{"quota.*", "routing.account.switched"}, MinSeverity: "warning", Subjects: map[string]string{"provider_id": "prv_1"}}
	s := map[string]string{"provider_id": "prv_1"}
	tests := []struct {
		name, sev string
		subj      map[string]string
		want      bool
	}{
		{"quota.window.exhausted", "warning", s, true},
		{"quota.window.exhausted", "info", s, false},
		{"routing.account.switched", "critical", s, true},
		{"routing.fallback.selected", "warning", s, false},
		{"quota.window.exhausted", "warning", map[string]string{"provider_id": "prv_2"}, false},
	}
	for _, tt := range tests {
		if got := f.Matches(tt.name, tt.sev, tt.subj); got != tt.want {
			t.Errorf("%s/%s: %v", tt.name, tt.sev, got)
		}
	}
}
