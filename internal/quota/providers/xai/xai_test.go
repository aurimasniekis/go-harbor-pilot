package xai

import (
	"context"
	"encoding/json"
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

const testKey = "xai-mgmt-SECRET-test-key"

var now = time.Date(2026, 10, 17, 11, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "xai", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fakeServer struct {
	t      *testing.T
	status map[string]int
	header map[string]string
	mu     sync.Mutex
	usage  map[string]any
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	for k, v := range f.header {
		w.Header().Set(k, v)
	}
	if code, ok := f.status[r.URL.Path]; ok {
		w.WriteHeader(code)
		return
	}
	const team = "/v1/billing/teams/team-0000-test"
	switch r.URL.Path {
	case "/auth/management-keys/validation":
		_, _ = w.Write(fixture(f.t, "validation.json"))
	case team + "/prepaid/balance":
		_, _ = w.Write(fixture(f.t, "prepaid_balance.json"))
	case team + "/postpaid/spending-limits":
		_, _ = w.Write(fixture(f.t, "spending_limits.json"))
	case team + "/postpaid/invoice/preview":
		_, _ = w.Write(fixture(f.t, "invoice_preview.json"))
	case team + "/usage":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.usage = body
		f.mu.Unlock()
		_, _ = w.Write(fixture(f.t, "usage.json"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newServer(t *testing.T, status map[string]int, header ...map[string]string) (*httptest.Server, *fakeServer) {
	f := &fakeServer{t: t, status: status}
	if len(header) > 0 {
		f.header = header[0]
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv, f
}

func cfg(base string, opts map[string]any, key string) providers.AccountConfig {
	o := map[string]any{"base_url": base}
	for k, v := range opts {
		o[k] = v
	}
	return providers.AccountConfig{
		Account:     domain.Account{ID: "xai-team", ProviderID: ProviderID},
		Options:     o,
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"management_key": credentials.NewSecret(key)}),
	}
}

func adapter() providers.Adapter {
	return New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{})})
}

func bucket(t *testing.T, res *providers.FetchResult, key string) domain.BucketObservation {
	t.Helper()
	for _, b := range res.Buckets {
		if b.Spec.Key == key {
			return b
		}
	}
	t.Fatalf("bucket %q missing in %+v", key, res.Buckets)
	return domain.BucketObservation{}
}

func mustAmount(t *testing.T, s string) money.Amount {
	t.Helper()
	a, err := money.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFetch(t *testing.T) {
	srv, fs := newServer(t, nil)
	a := adapter()
	c := cfg(srv.URL, nil, testKey)
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	bal := bucket(t, res, "balance:prepaid")
	if bal.RemainingAmount == nil || *bal.RemainingAmount != mustAmount(t, "37.66") || bal.Spec.Kind != domain.KindBalance {
		t.Fatalf("balance = %+v", bal)
	}
	bud := bucket(t, res, "budget:postpaid")
	if *bud.LimitAmount != mustAmount(t, "200") || *bud.UsedAmount != mustAmount(t, "45.5") || *bud.RemainingAmount != mustAmount(t, "154.5") {
		t.Fatalf("budget = %+v", bud)
	}
	if !strings.Contains(bud.Spec.Label, "2026-10") {
		t.Fatalf("label = %q", bud.Spec.Label)
	}
	if res.MaskedIdentifier == nil || strings.Contains(*res.MaskedIdentifier, "team-0000-test") {
		t.Fatalf("identifier not masked: %v", res.MaskedIdentifier)
	}

	totals := map[time.Time]money.Amount{}
	models := 0
	for _, u := range res.Usage {
		if u.ActualCost != nil || u.EstimatedCost == nil || u.CurrencyVerified || u.Granularity != domain.GranHour {
			t.Fatalf("usage row cost semantics wrong: %+v", u)
		}
		switch u.Breakdown {
		case "total":
			totals[u.IntervalStart] = u.EstimatedCost.Amount
		case "model":
			models++
		}
	}
	if models != 4 {
		t.Fatalf("model rows = %d", models)
	}
	if got := totals[time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)]; got != mustAmount(t, "1.25") {
		t.Fatalf("10:00 total = %s", got)
	}
	if got := totals[time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)]; got != mustAmount(t, "0.75973725") {
		t.Fatalf("09:00 total = %s", got)
	}
	for _, u := range res.Usage {
		if u.IntervalStart.Hour() == 11 && !u.Partial {
			t.Fatal("current hour must be partial")
		}
	}

	ar := fs.usage["analyticsRequest"].(map[string]any)
	tr := ar["timeRange"].(map[string]any)
	if ar["timeUnit"] != "TIME_UNIT_HOUR" || tr["startTime"] != "2026-10-15 11:00:00" || tr["endTime"] != "2026-10-17 12:00:00" {
		t.Fatalf("usage request = %+v", ar)
	}
}

func TestFetchExplicitTeamSkipsDiscovery(t *testing.T) {
	srv, _ := newServer(t, map[string]int{"/auth/management-keys/validation": http.StatusInternalServerError})
	res, err := adapter().Fetch(context.Background(), providers.FetchRequest{
		Config: cfg(srv.URL, map[string]any{"team_id": "team-0000-test", "usage": false}, testKey), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Usage) != 0 || len(res.Buckets) != 2 {
		t.Fatalf("res = %+v", res)
	}
}

func TestOptionalEndpointsDegrade(t *testing.T) {
	const team = "/v1/billing/teams/team-0000-test"
	srv, _ := newServer(t, map[string]int{
		team + "/postpaid/spending-limits": http.StatusForbidden,
		team + "/postpaid/invoice/preview": http.StatusNotFound,
		team + "/usage":                    http.StatusForbidden,
	})
	res, err := adapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, testKey), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 1 || len(res.Notes) != 3 {
		t.Fatalf("buckets=%+v notes=%v", res.Buckets, res.Notes)
	}
}

func TestAllForbiddenSurfacesPermissionError(t *testing.T) {
	const team = "/v1/billing/teams/team-0000-test"
	srv, _ := newServer(t, map[string]int{
		team + "/prepaid/balance":          http.StatusForbidden,
		team + "/postpaid/spending-limits": http.StatusForbidden,
		team + "/postpaid/invoice/preview": http.StatusForbidden,
		team + "/usage":                    http.StatusForbidden,
	})
	_, err := adapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, testKey), Now: now})
	if pe := providers.AsError(err); pe == nil || pe.Kind != domain.ErrPermission {
		t.Fatalf("err = %v", err)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		status    map[string]int
		header    map[string]string
		wantKind  domain.ErrorKind
		wantRetry time.Duration
	}{
		{name: "wrong key is an authentication error", key: "xai-mgmt-WRONG-key", wantKind: domain.ErrAuthentication},
		{name: "rate limited honours Retry-After", key: testKey,
			status: map[string]int{"/v1/billing/teams/team-0000-test/prepaid/balance": http.StatusTooManyRequests},
			header: map[string]string{"Retry-After": "90"}, wantKind: domain.ErrRateLimited, wantRetry: 90 * time.Second},
		{name: "server error is unavailable", key: testKey,
			status: map[string]int{"/auth/management-keys/validation": http.StatusBadGateway}, wantKind: domain.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newServer(t, tt.status, tt.header)
			_, err := adapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, tt.key), Now: now})
			var pe *providers.Error
			if !errors.As(err, &pe) || pe.Kind != tt.wantKind || pe.RetryAfter != tt.wantRetry {
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
		{"missing credential", providers.AccountConfig{Options: map[string]any{}}},
		{"unknown option", cfg("https://management-api.x.ai", map[string]any{"bogus": 1}, testKey)},
		{"plain http base", cfg("http://example.com", nil, testKey)},
		{"lookback out of range", cfg("https://management-api.x.ai", map[string]any{"usage_lookback_hours": 500}, testKey)},
		{"team id with path", cfg("https://management-api.x.ai", map[string]any{"team_id": "../x"}, testKey)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := adapter().Validate(tt.c); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
