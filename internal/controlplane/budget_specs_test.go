package controlplane

import (
	"encoding/json"
	"testing"
	"time"
)

func budgetSpec(over map[string]any) map[string]any {
	s := map[string]any{"scope": "global", "scope_id": "", "period": "month", "limit": "10", "currency": "usd", "enabled": true}
	for k, v := range over {
		s[k] = v
	}
	return s
}

// One-time budgets: starts_at and ends_at only with period once, both RFC
// 3339 with ends_at after starts_at, and no timezone (D-044).
func TestBudgetSpecValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		over map[string]any
		loc  string // "" = valid
	}{
		{"day", map[string]any{"period": "day"}, ""},
		{"week", map[string]any{"period": "week"}, ""},
		{"month with timezone", map[string]any{"timezone": "Europe/Vilnius"}, ""},
		{"once", map[string]any{"period": "once"}, ""},
		{"once with window", map[string]any{"period": "once", "starts_at": "2026-11-01T00:00:00Z", "ends_at": "2027-03-31T23:59:59Z"}, ""},
		{"once open start, set end", map[string]any{"period": "once", "ends_at": "2027-01-01T00:00:00Z"}, ""},
		{"unknown period", map[string]any{"period": "year"}, "body.spec.period"},
		{"empty period", map[string]any{"period": ""}, "body.spec.period"},
		{"starts_at on a recurring budget", map[string]any{"starts_at": "2026-11-01T00:00:00Z"}, "body.spec.starts_at"},
		{"ends_at on a recurring budget", map[string]any{"period": "day", "ends_at": "2026-11-01T00:00:00Z"}, "body.spec.ends_at"},
		{"timezone on once", map[string]any{"period": "once", "timezone": "UTC"}, "body.spec.timezone"},
		{"bad starts_at", map[string]any{"period": "once", "starts_at": "2026-11-01"}, "body.spec.starts_at"},
		{"bad ends_at", map[string]any{"period": "once", "ends_at": "tomorrow"}, "body.spec.ends_at"},
		{"ends_at equal to starts_at", map[string]any{"period": "once", "starts_at": "2026-11-01T00:00:00Z", "ends_at": "2026-11-01T02:00:00+02:00"}, "body.spec.ends_at"},
		{"ends_at before starts_at", map[string]any{"period": "once", "starts_at": "2026-11-02T00:00:00Z", "ends_at": "2026-11-01T00:00:00Z"}, "body.spec.ends_at"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(budgetSpec(tt.over))
			v := NewValidator("", nil)
			BudgetDef.Canonicalize(raw, v)
			if tt.loc == "" {
				if !v.OK() {
					t.Fatalf("unexpected errors %+v", v.Errors)
				}
				return
			}
			if len(v.Errors) != 1 || v.Errors[0].Location != tt.loc {
				t.Fatalf("errors %+v, want one at %s", v.Errors, tt.loc)
			}
		})
	}
}

// starts_at and ends_at are stored in UTC, truncated to the second.
func TestBudgetSpecNormalizesTimes(t *testing.T) {
	raw, _ := json.Marshal(budgetSpec(map[string]any{"period": "once", "starts_at": "2026-11-08T12:12:44.987+02:00", "ends_at": "2027-01-01T00:00:00Z"}))
	v := NewValidator("", nil)
	canon, _ := BudgetDef.Canonicalize(raw, v)
	if !v.OK() {
		t.Fatal(v.Errors)
	}
	s, err := Decode[BudgetSpec](canon)
	if err != nil {
		t.Fatal(err)
	}
	if s.StartsAt != "2026-11-08T10:12:44Z" || s.EndsAt != "2027-01-01T00:00:00Z" {
		t.Fatalf("normalized %q %q", s.StartsAt, s.EndsAt)
	}
	// A recurring budget stored before one-time budgets decodes unchanged.
	old, err := Decode[BudgetSpec](json.RawMessage(`{"scope":"global","scope_id":"","period":"month","limit":"10","currency":"USD","enabled":true}`))
	if err != nil || old.StartsAt != "" || old.EndsAt != "" || old.Period != "month" {
		t.Fatalf("old spec %+v %v", old, err)
	}
}

func TestBudgetWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 30, 0, 0, time.UTC)
	created := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	for _, tt := range []struct {
		name       string
		spec       BudgetSpec
		start, end time.Time
		open       bool
	}{
		{"day", BudgetSpec{Period: "day"}, day(2026, 11, 18), day(2026, 11, 19), false},
		{"week starts Monday", BudgetSpec{Period: "week"}, day(2026, 11, 16), day(2026, 11, 23), false},
		{"month", BudgetSpec{Period: "month"}, day(2026, 11, 1), day(2026, 12, 1), false},
		{"once from creation, open", BudgetSpec{Period: "once"}, created, time.Time{}, true},
		{"once from starts_at", BudgetSpec{Period: "once", StartsAt: "2025-06-01T00:00:00Z"}, day(2025, 6, 1), time.Time{}, true},
		{"once with an end", BudgetSpec{Period: "once", EndsAt: "2027-01-01T00:00:00Z"}, created, day(2027, 1, 1), false},
		{"once ignores the recurring timezone", BudgetSpec{Period: "once", StartsAt: "2026-11-20T00:00:00Z"}, day(2026, 11, 20), time.Time{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start, end, open := tt.spec.Window(now, created)
			if !start.Equal(tt.start) || !end.Equal(tt.end) || open != tt.open {
				t.Fatalf("window %v %v %v, want %v %v %v", start, end, open, tt.start, tt.end, tt.open)
			}
		})
	}
}
