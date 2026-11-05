package cursor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

const testKey = "key_synthetic_cursor_admin"

var now = time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "cursor", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type recorded struct {
	path string
	body map[string]any
}

func server(t *testing.T, status map[string]int, calls *[]recorded) *httptest.Server {
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testKey+":"))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected %s %s content-type %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls != nil {
			*calls = append(*calls, recorded{r.URL.Path, body})
		}
		if code, ok := status[r.URL.Path]; ok {
			if code == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "42")
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"denied for alpha@example.invalid"}`))
			return
		}
		page := fmt.Sprint(body["page"])
		switch r.URL.Path {
		case "/teams/spend":
			_, _ = w.Write(fixture(t, "spend_page"+page+".json"))
		case "/teams/daily-usage-data":
			_, _ = w.Write(fixture(t, "daily_page"+page+".json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func cfg(base string, opts map[string]any, key string) providers.AccountConfig {
	o := map[string]any{"base_url": base}
	for k, v := range opts {
		o[k] = v
	}
	return providers.AccountConfig{
		Account:     domain.Account{ID: "cursor-team", ProviderID: ProviderID},
		Options:     o,
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret(key)}),
	}
}

func newAdapter() *Adapter {
	return New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{})})
}

func amount(t *testing.T, s string) money.Amount {
	t.Helper()
	a, err := money.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCursorFetch(t *testing.T) {
	var calls []recorded
	srv := server(t, nil, &calls)
	defer srv.Close()
	a := newAdapter()
	c := cfg(srv.URL, nil, testKey)
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}

	// Pagination: two spend pages and two daily pages.
	if len(calls) != 4 {
		t.Fatalf("calls = %+v", calls)
	}
	daily := calls[2].body
	if daily["startDate"] != float64(time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC).UnixMilli()) || daily["endDate"] != float64(now.UnixMilli()) {
		t.Fatalf("daily range = %v..%v", daily["startDate"], daily["endDate"])
	}

	buckets := map[string]domain.BucketObservation{}
	for _, b := range res.Buckets {
		buckets[b.Spec.Key] = b
	}
	if b := buckets["cycle_spend"]; b.UsedAmount == nil || *b.UsedAmount != amount(t, "72.5024") || b.Spec.Currency != "USD" {
		t.Fatalf("cycle spend = %+v", b)
	}
	if b := buckets["cycle_on_demand_spend"]; b.UsedAmount == nil || *b.UsedAmount != amount(t, "22.5049") {
		t.Fatalf("on-demand spend = %+v", b)
	}
	var memberBuckets []domain.BucketObservation
	for k, b := range buckets {
		if strings.HasPrefix(k, "member_budget:") {
			memberBuckets = append(memberBuckets, b)
		}
	}
	if len(memberBuckets) != 2 {
		t.Fatalf("member budgets = %d, want 2 (members without limits are skipped)", len(memberBuckets))
	}
	for _, b := range memberBuckets {
		if *b.LimitAmount == amount(t, "50") && *b.RemainingAmount != amount(t, "37.495") {
			t.Fatalf("alpha remaining = %v", b.RemainingAmount.String())
		}
		if *b.LimitAmount == amount(t, "25") && *b.UsedAmount != amount(t, "9.9999") {
			t.Fatalf("gamma used = %v (effective limit should win over monthly)", b.UsedAmount.String())
		}
	}

	var totals, users []domain.UsageAggregate
	for _, u := range res.Usage {
		switch u.Breakdown {
		case "total":
			totals = append(totals, u)
		case "user":
			users = append(users, u)
		}
	}
	if len(totals) != 2 {
		t.Fatalf("totals = %+v", totals)
	}
	if *totals[0].Requests != 32 || totals[0].Partial || totals[0].Granularity != domain.GranDay {
		t.Fatalf("2026-10-16 = %+v", totals[0])
	}
	if *totals[1].Requests != 7 || !totals[1].Partial {
		t.Fatalf("2026-10-17 = %+v", totals[1])
	}
	if len(users) != 4 {
		t.Fatalf("user rows = %d", len(users))
	}

	// No identities leak into the result.
	out, _ := json.Marshal(res)
	for _, bad := range []string{"example.invalid", "Synthetic", "user_synthetic", testKey} {
		if strings.Contains(string(out), bad) {
			t.Fatalf("result leaks %q", bad)
		}
	}
	for _, u := range users {
		if !strings.HasPrefix(u.Dimensions.User, "m_") {
			t.Fatalf("user dimension not hashed: %q", u.Dimensions.User)
		}
	}
}

func TestCursorMemberBudgetsDisabled(t *testing.T) {
	srv := server(t, nil, nil)
	defer srv.Close()
	res, err := newAdapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, map[string]any{"member_budgets": false}, testKey), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 2 {
		t.Fatalf("buckets = %d, want team spend only", len(res.Buckets))
	}
}

func TestCursorErrors(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		status    map[string]int
		want      domain.ErrorKind
		wantRetry time.Duration
	}{
		{name: "bad key", key: "wrong", want: domain.ErrAuthentication},
		{name: "not admin", key: testKey, status: map[string]int{"/teams/spend": http.StatusForbidden}, want: domain.ErrPermission},
		{name: "rate limited", key: testKey, status: map[string]int{"/teams/daily-usage-data": http.StatusTooManyRequests},
			want: domain.ErrRateLimited, wantRetry: 42 * time.Second},
		{name: "server error", key: testKey, status: map[string]int{"/teams/spend": http.StatusBadGateway}, want: domain.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := server(t, tt.status, nil)
			defer srv.Close()
			_, err := newAdapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, tt.key), Now: now})
			pe := providers.AsError(err)
			if pe == nil || pe.Kind != tt.want || pe.RetryAfter != tt.wantRetry {
				t.Fatalf("err = %v (retry %v), want %s (retry %v)", err, pe.RetryAfter, tt.want, tt.wantRetry)
			}
			for _, bad := range []string{testKey, "example.invalid", srv.URL} {
				if strings.Contains(pe.Error(), bad) {
					t.Fatalf("error leaks %q: %v", bad, pe)
				}
			}
		})
	}
}

func TestCursorValidate(t *testing.T) {
	a := New(providers.Deps{})
	for _, o := range []map[string]any{
		{"base_url": "http://api.cursor.com"},
		{"lookback_days": 31},
		{"lookback_days": -1},
		{"unknown": true},
	} {
		if err := a.Validate(cfg("", o, testKey)); err == nil {
			t.Fatalf("expected validation error for %v", o)
		}
	}
	noKey := cfg("", nil, "")
	noKey.Options = nil
	noKey.Credentials = credentials.NewBundle(nil)
	if err := a.Validate(noKey); err == nil {
		t.Fatal("missing api_key must fail")
	}
	if a.Info().DefaultPollInterval < time.Hour {
		t.Fatal("poll interval must respect the 20 req/min limit (≥1h)")
	}
	if a.Info().Capabilities[domain.CapQuotaRemaining].Status != domain.CapUnsupported {
		t.Fatal("quota remaining must be unsupported")
	}
}
