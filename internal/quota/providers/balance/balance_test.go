package balance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

var now = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func cfg(base string, creds map[string]string) providers.AccountConfig {
	b := map[string]credentials.Secret{}
	for k, v := range creds {
		b[k] = credentials.NewSecret(v)
	}
	return providers.AccountConfig{Account: domain.Account{ID: "x"}, Options: map[string]any{"base_url": base}, Credentials: credentials.NewBundle(b)}
}

func serve(t *testing.T, routes map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok || r.Header.Get("Authorization") == "" {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, body)
	}))
}

func TestDeepSeek(t *testing.T) {
	srv := serve(t, map[string]string{"/user/balance": `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.05","granted_balance":"10.00","topped_up_balance":"100.05"}]}`})
	defer srv.Close()
	res, err := NewDeepSeek(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})}).Fetch(context.Background(),
		providers.FetchRequest{Config: cfg(srv.URL, map[string]string{"api_key": "k"}), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	b := res.Buckets[0]
	if b.Spec.Currency != "CNY" || b.RemainingAmount.String() != "110.05" || b.Limit != nil || b.Spec.Unit != domain.UnitCurrency {
		t.Fatalf("bucket: %+v", b)
	}
}

func TestMoonshot(t *testing.T) {
	srv := serve(t, map[string]string{"/v1/users/me/balance": `{"code":0,"data":{"available_balance":49.58894,"voucher_balance":46.58893,"cash_balance":-3.00001},"status":true}`})
	defer srv.Close()
	res, err := NewMoonshot(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})}).Fetch(context.Background(),
		providers.FetchRequest{Config: cfg(srv.URL, map[string]string{"api_key": "k"}), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 3 || res.Buckets[0].RemainingAmount.String() != "49.58894" || res.Buckets[2].RemainingAmount.String() != "-3.00001" {
		t.Fatalf("buckets: %+v", res.Buckets)
	}
}

func TestOpenRouter(t *testing.T) {
	srv := serve(t, map[string]string{
		"/api/v1/key":     `{"data":{"label":"sk-or-v1-abc...xyz","limit":20,"limit_remaining":12.5,"limit_reset":"monthly","usage":40,"usage_daily":1.25,"usage_weekly":4,"usage_monthly":7.5,"is_free_tier":false}}`,
		"/api/v1/credits": `{"data":{"total_credits":100,"total_usage":40}}`,
	})
	defer srv.Close()
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})}
	res, err := NewOpenRouter(deps, oauthflow.New(deps.HTTP, time.Now)).Fetch(context.Background(),
		providers.FetchRequest{Config: cfg(srv.URL, map[string]string{"api_key": "k", "management_key": "m"}), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]domain.BucketObservation{}
	for _, b := range res.Buckets {
		m[b.Spec.Key] = b
	}
	if *m["key_limit"].Remaining != 12.5 || *m["key_limit"].Used != 7.5 {
		t.Fatalf("key limit: %+v", m["key_limit"])
	}
	if *m["credits"].Remaining != 60 {
		t.Fatalf("credits: %+v", m["credits"])
	}
	if *m["usage:daily"].Used != 1.25 || m["usage:daily"].Remaining != nil {
		t.Fatal("usage bucket must report used only")
	}
}
