package nexos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "nexos", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func server(t *testing.T, status map[string]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer team-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.URL.Path
		if r.URL.Path == "/v1/usage" && r.URL.Query().Get("group_by") == "api_key" {
			key = "group"
		}
		if code, ok := status[key]; ok {
			w.WriteHeader(code)
			return
		}
		switch {
		case key == "group":
			_, _ = w.Write(fixture(t, "usage_by_key.json"))
		case r.URL.Path == "/v1/usage":
			if r.URL.Query().Get("granularity") != "hour" || r.URL.Query().Get("include") != "top_models" {
				t.Errorf("unexpected query %s", r.URL.RawQuery)
			}
			_, _ = w.Write(fixture(t, "usage_top_models.json"))
		case r.URL.Path == "/v1/companies/co-1/api_keys/with_usage":
			_, _ = w.Write(fixture(t, "api_keys_with_usage.json"))
		case r.URL.Path == "/v1/models/all":
			_, _ = w.Write(fixture(t, "models_all.json"))
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
		Account:     domain.Account{ID: "nexos-team", ProviderID: ProviderID},
		Options:     o,
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret(key)}),
	}
}

var now = time.Date(2026, 10, 17, 11, 30, 0, 0, time.UTC)

func TestNexosFetch(t *testing.T) {
	srv := server(t, nil)
	defer srv.Close()
	a := New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{})})
	c := cfg(srv.URL, map[string]any{"company_id": "co-1", "key_uuid": "11111111-1111-4111-8111-111111111111", "pricing_currency": "USD"}, "team-test-key")
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	byBreakdown := map[string][]domain.UsageAggregate{}
	for _, u := range res.Usage {
		byBreakdown[u.Breakdown] = append(byBreakdown[u.Breakdown], u)
	}
	if len(byBreakdown["total"]) != 2 || len(byBreakdown["model"]) != 2 || len(byBreakdown["api_key"]) != 1 {
		t.Fatalf("breakdown counts: total=%d model=%d key=%d", len(byBreakdown["total"]), len(byBreakdown["model"]), len(byBreakdown["api_key"]))
	}
	first := byBreakdown["total"][0]
	if first.ActualCost.Amount.String() != "0.30" || first.ActualCost.Currency != "USD" || first.CurrencyVerified {
		t.Fatalf("cost: %+v verified=%v", first.ActualCost, first.CurrencyVerified)
	}
	if first.Tokens.CacheReadIncludedInInput {
		t.Fatal("row 1 total equals exclusive sum; cache must not be marked as included")
	}
	second := byBreakdown["total"][1]
	if !second.Tokens.CacheReadIncludedInInput {
		t.Fatal("row 2 total equals input+output; cache read must be marked as included in input")
	}
	if *second.Tokens.TotalInput() != 1000 {
		t.Fatalf("total input double counted: %d", *second.Tokens.TotalInput())
	}
	if !second.Partial || first.Partial {
		t.Fatal("only the in-progress hour should be partial")
	}
	if got := byBreakdown["model"][1].Dimensions.RequestedModel; got != "__other__" {
		t.Fatalf("other bucket model = %q", got)
	}
	var budget *domain.BucketObservation
	for i := range res.Buckets {
		if res.Buckets[i].Spec.Key == "budget:key" {
			budget = &res.Buckets[i]
		}
	}
	if budget == nil || *budget.Remaining != 267.5 || budget.Spec.Unit != domain.UnitCredits {
		t.Fatalf("budget bucket: %+v", budget)
	}
	if len(res.APIKeys) != 2 || res.APIKeys[0].Name != "production-agents" || *res.APIKeys[1].Status != "budget_exceeded" {
		t.Fatalf("keys: %+v", res.APIKeys)
	}
	foundPrice := false
	for _, p := range res.Prices {
		if p.Model == "synthetic-model-a" && p.Category == "input" && p.PerMillion.String() == "3.00" {
			foundPrice = true
		}
	}
	if !foundPrice {
		t.Fatalf("per-token price not converted to per-million: %+v", res.Prices)
	}
}

func TestNexosGroupForbiddenIsNote(t *testing.T) {
	srv := server(t, map[string]int{"group": http.StatusForbidden})
	defer srv.Close()
	a := New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{})})
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, "team-test-key"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) == 0 {
		t.Fatal("expected a note about the missing key breakdown")
	}
}

func TestNexosErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		key    string
		want   domain.ErrorKind
	}{
		{name: "bad key", key: "wrong", want: domain.ErrAuthentication},
		{name: "rate limited", key: "team-test-key", status: http.StatusTooManyRequests, want: domain.ErrRateLimited},
		{name: "server error", key: "team-test-key", status: http.StatusBadGateway, want: domain.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := map[string]int{}
			if tt.status != 0 {
				st["/v1/usage"] = tt.status
			}
			srv := server(t, st)
			defer srv.Close()
			a := New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{})})
			_, err := a.Fetch(context.Background(), providers.FetchRequest{Config: cfg(srv.URL, nil, tt.key), Now: now})
			pe := providers.AsError(err)
			if pe == nil || pe.Kind != tt.want {
				t.Fatalf("err = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestNexosValidate(t *testing.T) {
	a := New(providers.Deps{})
	for _, o := range []map[string]any{
		{"base_url": "http://api.nexos.ai"},
		{"lookback_hours": 500},
		{"key_uuid": "x"},
		{"unknown": true},
	} {
		if err := a.Validate(cfg("", o, "k")); err == nil {
			t.Fatalf("expected validation error for %v", o)
		}
	}
	empty := cfg("", nil, "")
	empty.Options = nil
	if err := a.Validate(empty); err == nil {
		t.Fatal("missing api_key must fail")
	}
}
