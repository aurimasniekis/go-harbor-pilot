package githubcopilot

import (
	"context"
	"encoding/json"
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

const testToken = "ghp_synthetic_copilot_token"

var now = time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "githubcopilot", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type srvState struct {
	mu      sync.Mutex
	queries []string
	paths   []string
}

func server(t *testing.T, status int, st *srvState) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != APIVersion {
			t.Errorf("missing GitHub headers: accept=%q version=%q", r.Header.Get("Accept"), r.Header.Get("X-GitHub-Api-Version"))
		}
		if st != nil {
			st.mu.Lock()
			st.queries = append(st.queries, r.URL.RawQuery)
			st.paths = append(st.paths, r.URL.Path)
			st.mu.Unlock()
		}
		if status != 0 {
			if status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "60")
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"Must have admin rights to Repository."}`))
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("day") == "":
			_, _ = w.Write(fixture(t, "usage_month.json"))
		case q.Get("day") == "16":
			_, _ = w.Write(fixture(t, "usage_day.json"))
		default:
			_, _ = w.Write(fixture(t, "usage_empty.json"))
		}
	}))
}

func cfg(base string, opts map[string]any, token string) providers.AccountConfig {
	o := map[string]any{"base_url": base}
	for k, v := range opts {
		o[k] = v
	}
	return providers.AccountConfig{
		Account:     domain.Account{ID: "copilot-org", ProviderID: ProviderID},
		Options:     o,
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret(token)}),
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

func TestCopilotFetchOrg(t *testing.T) {
	st := &srvState{}
	srv := server(t, 0, st)
	defer srv.Close()
	c := cfg(srv.URL, map[string]any{"org": "synthetic-org", "lookback_days": 2}, testToken)
	a := newAdapter()
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.paths) != 3 || st.paths[0] != "/organizations/synthetic-org/settings/billing/premium_request/usage" {
		t.Fatalf("paths = %v", st.paths)
	}
	if st.queries[0] != "month=10&year=2026" || st.queries[1] != "day=16&month=10&year=2026" {
		t.Fatalf("queries = %v", st.queries)
	}
	if len(res.Buckets) != 1 || *res.Buckets[0].UsedAmount != amount(t, "20.1234") {
		t.Fatalf("month spend = %+v", res.Buckets)
	}
	if *res.MaskedIdentifier != "org:synthetic-org" {
		t.Fatalf("identifier = %q", *res.MaskedIdentifier)
	}
	// Day 16 has data (total + 2 models); day 17 is empty (unknown, no rows).
	if len(res.Usage) != 3 {
		t.Fatalf("usage rows = %+v", res.Usage)
	}
	total := res.Usage[0]
	if total.Breakdown != "total" || *total.Requests != 131 || total.ActualCost.Amount != amount(t, "0.8") || total.Partial {
		t.Fatalf("total = %+v", total)
	}
	if !total.IntervalStart.Equal(time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)) || total.Granularity != domain.GranDay {
		t.Fatalf("interval = %v %s", total.IntervalStart, total.Granularity)
	}
	b := res.Usage[2]
	if b.Dimensions.RequestedModel != "synthetic-model-b" || *b.Requests != 11 || b.ActualCost.Amount != 0 {
		t.Fatalf("model b = %+v", b)
	}
	out, _ := json.Marshal(res)
	if strings.Contains(string(out), testToken) {
		t.Fatal("token leaked into result")
	}
}

func TestCopilotUserEndpoint(t *testing.T) {
	st := &srvState{}
	srv := server(t, 0, st)
	defer srv.Close()
	_, err := newAdapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, map[string]any{"user": "octo-cat", "lookback_days": 1}, testToken), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if st.paths[0] != "/users/octo-cat/settings/billing/premium_request/usage" {
		t.Fatalf("path = %s", st.paths[0])
	}
}

func TestCopilotErrors(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		status    int
		want      domain.ErrorKind
		wantRetry time.Duration
		wantMsg   string
	}{
		{name: "bad token", token: "wrong", want: domain.ErrAuthentication},
		{name: "not admin", token: testToken, status: http.StatusForbidden, want: domain.ErrPermission, wantMsg: "billing manager"},
		{name: "not found", token: testToken, status: http.StatusNotFound, want: domain.ErrUnsupported, wantMsg: "not available"},
		{name: "rate limited", token: testToken, status: http.StatusTooManyRequests, want: domain.ErrRateLimited, wantRetry: time.Minute},
		{name: "server error", token: testToken, status: http.StatusServiceUnavailable, want: domain.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := server(t, tt.status, nil)
			defer srv.Close()
			_, err := newAdapter().Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, map[string]any{"org": "synthetic-org"}, tt.token), Now: now})
			pe := providers.AsError(err)
			if pe == nil || pe.Kind != tt.want || pe.RetryAfter != tt.wantRetry {
				t.Fatalf("err = %v, want %s", err, tt.want)
			}
			if tt.wantMsg != "" && !strings.Contains(pe.Message, tt.wantMsg) {
				t.Fatalf("message = %q", pe.Message)
			}
			if strings.Contains(pe.Error(), testToken) || strings.Contains(pe.Error(), "admin rights") {
				t.Fatalf("error leaks token or body: %v", pe)
			}
		})
	}
}

func TestCopilotValidate(t *testing.T) {
	a := New(providers.Deps{})
	for _, o := range []map[string]any{
		{},
		{"org": "a", "user": "b"},
		{"org": "bad/org"},
		{"org": "x", "lookback_days": 40},
		{"org": "x", "base_url": "http://api.github.com"},
		{"org": "x", "unknown": 1},
	} {
		if err := a.Validate(cfg("", o, testToken)); err == nil {
			t.Fatalf("expected validation error for %v", o)
		}
	}
	if err := a.Validate(cfg("", map[string]any{"org": "x"}, "")); err == nil {
		t.Fatal("empty token must fail")
	}
	if c := a.Info().Capabilities[domain.CapQuotaRemaining]; c.Status != domain.CapUnsupported || c.Note == "" {
		t.Fatalf("quota remaining capability = %+v", c)
	}
}
