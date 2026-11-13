package gateway

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
)

func unifiedOnly(h http.Header) map[string]string {
	out := map[string]string{}
	for k := range h {
		out[http.CanonicalHeaderKey(k)] = h.Get(k)
	}
	return out
}

func sameHeaders(t *testing.T, got http.Header, want map[string]string) {
	t.Helper()
	g := unifiedOnly(got)
	if len(g) != len(want) {
		t.Fatalf("headers\n got %v\nwant %v", g, want)
	}
	for k, v := range want {
		if g[http.CanonicalHeaderKey(k)] != v {
			t.Fatalf("%s = %q, want %q (all: %v)", k, g[http.CanonicalHeaderKey(k)], v, g)
		}
	}
}

func TestSeatUsageHeaders(t *testing.T) {
	r5 := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	r7 := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	s5, s7 := strconv.FormatInt(r5.Unix(), 10), strconv.FormatInt(r7.Unix(), 10)
	p := "anthropic-ratelimit-unified-"
	tests := []struct {
		name string
		ws   []SeatWindow
		want map[string]string
	}{
		{"nothing known", nil, map[string]string{}},
		{"both allowed, claim is the fuller window", []SeatWindow{{"five_hour", 0.123, &r5, false}, {"seven_day", 0.42, &r7, false}}, map[string]string{
			p + "status": "allowed", p + "representative-claim": "seven_day", p + "reset": s7,
			p + "5h-status": "allowed", p + "5h-utilization": "0.12", p + "5h-reset": s5,
			p + "7d-status": "allowed", p + "7d-utilization": "0.42", p + "7d-reset": s7,
		}},
		{"warning window wins over a fuller-but-equal status", []SeatWindow{{"five_hour", 0.87, &r5, false}, {"seven_day", 0.42, &r7, false}}, map[string]string{
			p + "status": "allowed_warning", p + "representative-claim": "five_hour", p + "reset": s5,
			p + "5h-status": "allowed_warning", p + "5h-utilization": "0.87", p + "5h-reset": s5, p + "5h-surpassed-threshold": "0.75",
			p + "7d-status": "allowed", p + "7d-utilization": "0.42", p + "7d-reset": s7,
		}},
		{"second threshold, never 1 while allowed", []SeatWindow{{"seven_day", 0.999, &r7, false}}, map[string]string{
			p + "status": "allowed_warning", p + "representative-claim": "seven_day", p + "reset": s7,
			p + "7d-status": "allowed_warning", p + "7d-utilization": "0.99", p + "7d-reset": s7, p + "7d-surpassed-threshold": "0.95",
		}},
		{"exhausted window is rejected and representative", []SeatWindow{{"five_hour", 0.6, &r5, false}, {"seven_day", 1, &r7, true}}, map[string]string{
			p + "status": "rejected", p + "representative-claim": "seven_day", p + "reset": s7,
			p + "5h-status": "allowed", p + "5h-utilization": "0.6", p + "5h-reset": s5,
			p + "7d-status": "rejected", p + "7d-utilization": "1", p + "7d-reset": s7, p + "7d-surpassed-threshold": "1",
		}},
		{"unknown reset and unknown window", []SeatWindow{{"five_hour", 0.3, nil, false}, {"seven_day_opus", 0.9, &r7, false}}, map[string]string{
			p + "status": "allowed", p + "representative-claim": "five_hour",
			p + "5h-status": "allowed", p + "5h-utilization": "0.3",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			seatUsageHeaders(h, tt.ws)
			sameHeaders(t, h, tt.want)
		})
	}
}

func TestBudgetUsageHeaders(t *testing.T) {
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	se := strconv.FormatInt(end.Unix(), 10)
	p := "anthropic-ratelimit-unified-"
	usage := func(spent, reserved, limit string, period string, end *time.Time) *accounting.BudgetUsage {
		s, _ := money.ParseDecimal(spent)
		r, _ := money.ParseDecimal(reserved)
		l, _ := money.ParseDecimal(limit)
		u := &accounting.BudgetUsage{Spent: s, Reserved: r, Limit: l, Period: period, PeriodEnd: end}
		if l > 0 {
			u.Fraction = float64(s+r) / float64(l)
		}
		return u
	}
	tests := []struct {
		name string
		u    *accounting.BudgetUsage
		want map[string]string
	}{
		{"no budget", nil, map[string]string{}},
		{"zero limit", usage("0", "0", "0", "month", &end), map[string]string{}},
		{"monthly, low", usage("10", "2", "100", "month", &end), map[string]string{
			p + "status": "allowed", p + "representative-claim": "overage", p + "overage-status": "allowed",
			p + "overage-utilization": "0.12", p + "reset": se, p + "overage-reset": se, p + "overage-period": "monthly",
		}},
		{"daily, past the first notice", usage("8", "0.2", "10", "day", &end), map[string]string{
			p + "status": "allowed_warning", p + "representative-claim": "overage", p + "overage-status": "allowed_warning",
			p + "overage-utilization": "0.82", p + "overage-surpassed-threshold": "0.75",
			p + "reset": se, p + "overage-reset": se, p + "overage-period": "daily",
		}},
		{"over a warn-only limit stays below 1", usage("12", "0", "10", "week", &end), map[string]string{
			p + "status": "allowed_warning", p + "representative-claim": "overage", p + "overage-status": "allowed_warning",
			p + "overage-utilization": "0.99", p + "overage-surpassed-threshold": "0.95",
			p + "reset": se, p + "overage-reset": se, p + "overage-period": "weekly",
		}},
		{"one-time budget without an end", usage("1", "0", "4", cp.PeriodOnce, nil), map[string]string{
			p + "status": "allowed", p + "representative-claim": "overage", p + "overage-status": "allowed", p + "overage-utilization": "0.25",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			budgetUsageHeaders(h, tt.u)
			sameHeaders(t, h, tt.want)
		})
	}
}

func TestSeatAccount(t *testing.T) {
	for _, tt := range []struct {
		typ, billing string
		want         bool
	}{
		{cp.ProviderClaudeCode, cp.BillingSubscription, true},
		{cp.ProviderAnthropic, cp.BillingSubscription, true},
		{cp.ProviderAnthropic, cp.BillingMetered, false},
		{cp.ProviderCodex, cp.BillingSubscription, false},
		{cp.ProviderOpenAICompatible, cp.BillingMetered, false},
	} {
		if got := seatAccount(tt.typ, tt.billing); got != tt.want {
			t.Errorf("seatAccount(%s, %s) = %v", tt.typ, tt.billing, got)
		}
	}
}
