package ids

import (
	"sort"
	"testing"
	"time"
)

func TestNewIsValidAndSortable(t *testing.T) {
	var got []string
	base := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		got = append(got, NewAt(Request, base.Add(time.Duration(i)*time.Millisecond)))
	}
	if !sort.StringsAreSorted(got) {
		t.Fatal("ids are not lexicographically sorted by time")
	}
	for _, id := range got {
		if !Valid(Request, id) {
			t.Fatalf("invalid id %q", id)
		}
		if Prefix(id) != Request {
			t.Fatalf("bad prefix %q", id)
		}
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		name, prefix, id string
		want             bool
	}{
		{name: "ok", prefix: Plan, id: NewAt(Plan, time.Unix(0, 0)), want: true},
		{name: "wrong prefix", prefix: Pool, id: NewAt(Plan, time.Unix(0, 0))},
		{name: "short", prefix: Plan, id: "pln_abc"},
		{name: "no separator", prefix: Plan, id: "pln01hzzzzzzzzzzzzzzzzzzzzzz"},
		{name: "bad chars", prefix: Plan, id: "pln_uuuuuuuuuuuuuuuuuuuuuuuuuu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Valid(tt.prefix, tt.id); got != tt.want {
				t.Fatalf("Valid(%q,%q)=%v", tt.prefix, tt.id, got)
			}
		})
	}
}
