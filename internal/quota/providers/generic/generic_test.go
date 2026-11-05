package generic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

func cfg(opts map[string]any) providers.AccountConfig {
	return providers.AccountConfig{
		Account:     domain.Account{ID: "g", ProviderID: ProviderID},
		Options:     opts,
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret("test-key-123")}),
	}
}

func TestGenericMapping(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"limit":"1000","used":250.5,"reset":1767225600,"items":[{"bal":"12.3456"}]},"ts":"2026-10-17T10:00:00Z"}`))
	}))
	defer srv.Close()
	a := New()
	c := cfg(map[string]any{
		"url": srv.URL, "allow_private_network": true, "source_time_path": "ts",
		"buckets": []any{
			map[string]any{"key": "monthly", "kind": "fixed_window", "unit": "credits", "window": "720h",
				"limit_path": "data.limit", "used_path": "data.used", "reset_at_path": "data.reset"},
			map[string]any{"key": "bal", "kind": "balance", "unit": "currency", "currency": "usd", "remaining_path": "data.items.0.bal"},
			map[string]any{"key": "missing", "unit": "tokens", "remaining_path": "data.nope"},
		},
	})
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key-123" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	m := res.Buckets[0]
	if *m.Limit != 1000 || *m.Used != 250.5 || *m.Remaining != 749.5 || m.ResetAt.Unix() != 1767225600 {
		t.Fatalf("unexpected bucket: %+v", m)
	}
	if res.SourceTime == nil || res.SourceTime.Hour() != 10 {
		t.Fatal("source time not parsed")
	}
	if b := res.Buckets[1]; b.RemainingAmount == nil || b.RemainingAmount.String() != "12.3456" || b.Spec.Currency != "USD" {
		t.Fatalf("currency bucket wrong: %+v", b)
	}
	if b := res.Buckets[2]; b.Remaining != nil || b.Used != nil || b.Limit != nil {
		t.Fatal("missing field must be unknown, not zero")
	}
}

func TestGenericValidation(t *testing.T) {
	bucket := []any{map[string]any{"key": "k", "unit": "percent", "remaining_path": "x"}}
	tests := []struct {
		name string
		opts map[string]any
		want string
	}{
		{name: "http without opt-in", opts: map[string]any{"url": "http://example.com", "buckets": bucket}, want: "https"},
		{name: "credentials in url", opts: map[string]any{"url": "https://u:p@example.com", "buckets": bucket}, want: "credentials"},
		{name: "file scheme", opts: map[string]any{"url": "file:///etc/passwd", "buckets": bucket}, want: "options.url"},
		{name: "unknown option", opts: map[string]any{"url": "https://example.com", "buckets": bucket, "script": "x"}, want: "unknown field"},
		{name: "no buckets", opts: map[string]any{"url": "https://example.com"}, want: "buckets"},
		{name: "bad unit", opts: map[string]any{"url": "https://example.com", "buckets": []any{map[string]any{"key": "k", "unit": "x", "used_path": "a"}}}, want: "unit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := New().Validate(cfg(tt.opts))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestGenericSSRFBlocked(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	bucket := []any{map[string]any{"key": "k", "unit": "percent", "remaining_path": "x"}}
	tests := []struct {
		name string
		url  string
		opts map[string]any
	}{
		{name: "loopback without opt-in", url: srv.URL},
		{name: "metadata even with private opt-in", url: "https://169.254.169.254/latest/meta-data", opts: map[string]any{"allow_private_network": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := map[string]any{"url": tt.url, "buckets": bucket}
			for k, v := range tt.opts {
				o[k] = v
			}
			_, err := New().Fetch(context.Background(), providers.FetchRequest{Config: cfg(o)})
			var pe *providers.Error
			if !errors.As(err, &pe) || pe.Kind != domain.ErrConfiguration || !strings.Contains(pe.Message, "SSRF") {
				t.Fatalf("expected SSRF block, got %v", err)
			}
		})
	}
}

func TestGenericRefusesRedirect(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "" {
			t.Error("credential forwarded to redirect target")
		}
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()
	c := cfg(map[string]any{"url": srv.URL, "allow_private_network": true,
		"buckets": []any{map[string]any{"key": "k", "unit": "percent", "remaining_path": "x"}}})
	if _, err := New().Fetch(context.Background(), providers.FetchRequest{Config: c}); err == nil {
		t.Fatal("redirect must be refused")
	}
	if hits != 0 {
		t.Fatal("redirect target was contacted")
	}
}
