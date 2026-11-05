package together

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

const testKey = "tgp-SECRET-test-key"

var now = time.Date(2026, 10, 17, 11, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "together", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fakeServer struct {
	t       *testing.T
	status  int
	header  map[string]string
	loop    bool // always return a next cursor
	mu      sync.Mutex
	queries []string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.RawQuery)
	f.mu.Unlock()
	for k, v := range f.header {
		w.Header().Set(k, v)
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	if r.URL.Path != "/v1/billing/usage" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	if q.Get("granularity") != "hour" {
		f.t.Errorf("unexpected query %s", r.URL.RawQuery)
	}
	switch {
	case f.loop:
		_, _ = w.Write([]byte(`{"currency":"USD","data":[],"next_cursor":"c-` + q.Get("after") + `x"}`))
	case q.Get("month") != "2026-10" && q.Get("month") != "2026-09":
		f.t.Errorf("unexpected month %s", q.Get("month"))
	case q.Get("month") == "2026-09":
		_, _ = w.Write([]byte(`{"currency":"USD","data":[],"next_cursor":null}`))
	case q.Get("after") == "":
		_, _ = w.Write(fixture(f.t, "usage_p1.json"))
	case q.Get("after") == "cursor-page-2":
		_, _ = w.Write(fixture(f.t, "usage_p2.json"))
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func newServer(t *testing.T, f *fakeServer) *httptest.Server {
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv
}

func cfg(base string, opts map[string]any, key string) providers.AccountConfig {
	o := map[string]any{"base_url": base}
	for k, v := range opts {
		o[k] = v
	}
	return providers.AccountConfig{
		Account:     domain.Account{ID: "together-org", ProviderID: ProviderID},
		Options:     o,
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret(key)}),
	}
}

func adapter() providers.Adapter {
	return New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})})
}

func amt(t *testing.T, s string) money.Amount {
	t.Helper()
	a, err := money.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFetchPaginatesAndAggregates(t *testing.T) {
	fs := &fakeServer{}
	srv := newServer(t, fs)
	a := adapter()
	c := cfg(srv.URL, map[string]any{"organization_id": "org-test-0001"}, testKey)
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.queries) != 2 || !strings.Contains(fs.queries[0], "organization_id=org-test-0001") {
		t.Fatalf("queries = %v", fs.queries)
	}
	h := func(hour int) time.Time { return time.Date(2026, 10, 17, hour, 0, 0, 0, time.UTC) }
	type k struct {
		start     time.Time
		breakdown string
		dim       string
	}
	got := map[k]domain.UsageAggregate{}
	for _, u := range res.Usage {
		if u.ActualCost == nil || u.ActualCost.Currency != "USD" || !u.CurrencyVerified || u.Granularity != domain.GranHour {
			t.Fatalf("row = %+v", u)
		}
		got[k{u.IntervalStart, u.Breakdown, u.Dimensions.RequestedModel + u.Dimensions.APIKeyID}] = u
	}
	checks := []struct {
		key  k
		want string
	}{
		{k{h(9), "total", ""}, "0.132"},
		{k{h(9), "model", "meta-llama/Llama-3.3-70B-Instruct-Turbo"}, "0.132"},
		{k{h(9), "api_key", "key-test-0001"}, "0.1056"},
		{k{h(9), "api_key", "key-test-0002"}, "0.0264"},
		{k{h(10), "total", ""}, "3.36"},
		{k{h(10), "api_key", otherKey}, "3.36"},
		{k{h(10), "model", "Dedicated Endpoint GPU hours"}, "3.36"},
		{k{h(11), "model", "Qwen/Qwen2.5-7B-Instruct-Turbo"}, "0.015"},
	}
	for _, c := range checks {
		u, ok := got[c.key]
		if !ok {
			t.Fatalf("missing row %+v", c.key)
		}
		if u.ActualCost.Amount != amt(t, c.want) {
			t.Fatalf("%+v = %s, want %s", c.key, u.ActualCost.Amount, c.want)
		}
	}
	if !got[k{h(11), "total", ""}].Partial || got[k{h(10), "total", ""}].Partial {
		t.Fatal("partial flag wrong")
	}
	if len(res.APIKeys) != 2 || res.APIKeys[0].ProviderKeyID != "key-test-0001" || res.APIKeys[0].Project != "proj-test-a" {
		t.Fatalf("keys = %+v", res.APIKeys)
	}
}

func TestPreviousMonthEarlyInMonth(t *testing.T) {
	fs := &fakeServer{}
	srv := newServer(t, fs)
	early := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	if _, err := adapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, testKey), Now: early}); err != nil {
		t.Fatal(err)
	}
	if len(fs.queries) != 3 || !strings.Contains(fs.queries[0], "month=2026-09") {
		t.Fatalf("queries = %v", fs.queries)
	}
}

func TestPaginationIsBounded(t *testing.T) {
	fs := &fakeServer{loop: true}
	srv := newServer(t, fs)
	res, err := adapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, map[string]any{"max_pages": 3}, testKey), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.queries) != 3 || len(res.Notes) != 1 {
		t.Fatalf("queries=%d notes=%v", len(fs.queries), res.Notes)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		status    int
		header    map[string]string
		wantKind  domain.ErrorKind
		wantRetry time.Duration
		wantMsg   string
	}{
		{name: "wrong key is an authentication error", key: "tgp-WRONG-key", wantKind: domain.ErrAuthentication},
		{name: "404 is the beta unsupported state", key: testKey, status: http.StatusNotFound,
			wantKind: domain.ErrUnsupported, wantMsg: "beta"},
		{name: "rate limited honours Retry-After", key: testKey, status: http.StatusTooManyRequests,
			header: map[string]string{"Retry-After": "30"}, wantKind: domain.ErrRateLimited, wantRetry: 30 * time.Second},
		{name: "organization mismatch is a permission error", key: testKey, status: http.StatusForbidden, wantKind: domain.ErrPermission},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, &fakeServer{status: tt.status, header: tt.header})
			_, err := adapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, tt.key), Now: now})
			var pe *providers.Error
			if !errors.As(err, &pe) || pe.Kind != tt.wantKind || pe.RetryAfter != tt.wantRetry || !strings.Contains(pe.Message, tt.wantMsg) {
				t.Fatalf("err = %#v", err)
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "WRONG") {
				t.Fatalf("secret leaked: %v", err)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		c    providers.AccountConfig
	}{
		{"missing credential", providers.AccountConfig{}},
		{"unknown option", cfg("https://api.together.ai", map[string]any{"bogus": true}, testKey)},
		{"plain http base", cfg("http://example.com", nil, testKey)},
		{"max pages out of range", cfg("https://api.together.ai", map[string]any{"max_pages": 99}, testKey)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := adapter().Validate(tt.c); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
