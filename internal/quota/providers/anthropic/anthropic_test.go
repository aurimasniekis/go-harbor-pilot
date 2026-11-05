package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

var now = time.Date(2026, 10, 17, 11, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "anthropic", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type memStore struct {
	mu sync.Mutex
	m  map[domain.AccountID]providers.OAuthToken
}

func (s *memStore) Get(id domain.AccountID) (providers.OAuthToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[id]
	if !ok {
		return t, providers.ErrNoToken
	}
	return t, nil
}
func (s *memStore) Put(id domain.AccountID, t providers.OAuthToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = t
	return nil
}
func (s *memStore) Delete(id domain.AccountID) error { delete(s.m, id); return nil }

func subCfg(opts map[string]any, creds map[string]string) providers.AccountConfig {
	b := map[string]credentials.Secret{}
	for k, v := range creds {
		b[k] = credentials.NewSecret(v)
	}
	return providers.AccountConfig{Account: domain.Account{ID: "claude-me", ProviderID: SubscriptionID}, Options: opts, Credentials: credentials.NewBundle(b)}
}

func TestSubscriptionFetchFromMountedFile(t *testing.T) {
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
		_, _ = w.Write(fixture(t, "oauth_usage.json"))
	}))
	defer srv.Close()
	root, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "testdata", "quota", "anthropic"))
	files := &providers.FileReader{}
	files.SetRoots([]string{root})
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{}), Files: files}
	a := NewSubscription(deps, oauthflow.New(deps.HTTP, func() time.Time { return now }))
	c := subCfg(map[string]any{"token_source": "file", "credentials_file": filepath.Join(root, "claude_credentials.json"), "usage_url": srv.URL}, nil)
	if err := a.Validate(c); err != nil {
		t.Fatal(err)
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer synthetic-access-token-not-real" || gotBeta != DefaultOAuthBeta {
		t.Fatalf("headers: %q %q", gotAuth, gotBeta)
	}
	b := map[string]domain.BucketObservation{}
	for _, o := range res.Buckets {
		b[o.Spec.Key] = o
		if o.Spec.Official {
			t.Fatalf("bucket %s from an undocumented endpoint must not be marked official", o.Spec.Key)
		}
	}
	if *b["five_hour"].Remaining != 62.5 || b["five_hour"].ResetAt.Hour() != 15 || !b["five_hour"].Spec.Global {
		t.Fatalf("five hour: %+v", b["five_hour"])
	}
	if opus := b["seven_day:model:opus"]; opus.Remaining != nil || opus.Spec.Global {
		t.Fatal("null utilization must remain unknown and model buckets must not be global")
	}
	if s := b["seven_day:model:synthetic"]; s.Remaining == nil || *s.Remaining != 88 {
		t.Fatalf("weekly scoped limit missing: %+v", s)
	}
	if e := b["extra_usage"]; e.Used == nil || *e.Used != 2.4 || e.Spec.Unit != domain.UnitPercent {
		t.Fatalf("extra usage: %+v", e)
	}
}

func TestSubscriptionExpiredFileIsAuthError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	_ = os.WriteFile(p, []byte(`{"claudeAiOauth":{"accessToken":"x","expiresAt":1000}}`), 0o600)
	files := &providers.FileReader{}
	files.SetRoots([]string{dir})
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{}), Files: files}
	a := NewSubscription(deps, oauthflow.New(deps.HTTP, func() time.Time { return now }))
	_, err := a.Fetch(context.Background(), providers.FetchRequest{Config: subCfg(map[string]any{"credentials_file": p, "usage_url": "https://127.0.0.1:1"}, nil), Now: now})
	pe := providers.AsError(err)
	if pe.Kind != domain.ErrAuthentication || !strings.Contains(pe.Message, "never modifies") {
		t.Fatalf("err = %v", err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"expiresAt":1000`) {
		t.Fatal("mounted file was modified")
	}
}

func TestSubscriptionLoginRefreshesAndRotates(t *testing.T) {
	var refreshes int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":28800}`))
	}))
	defer tokenSrv.Close()
	usageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(fixture(t, "oauth_usage.json"))
	}))
	defer usageSrv.Close()
	store := &memStore{m: map[domain.AccountID]providers.OAuthToken{"claude-me": {
		AccessToken: credentials.NewSecret("old"), RefreshToken: credentials.NewSecret("old-refresh"), ExpiresAt: now.Add(-time.Minute),
	}}}
	holder := &providers.TokenHolder{}
	holder.Set(store)
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{}), Tokens: holder}
	a := NewSubscription(deps, oauthflow.New(deps.HTTP, func() time.Time { return now }))
	c := subCfg(map[string]any{"token_source": "login", "usage_url": usageSrv.URL, "token_url": tokenSrv.URL}, nil)
	for range 2 {
		if _, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	if refreshes != 1 {
		t.Fatalf("refreshed %d times, want 1 (second poll must reuse the stored token)", refreshes)
	}
	got, _ := store.Get("claude-me")
	if got.RefreshToken.Reveal() != "new-refresh" {
		t.Fatal("rotated refresh token not persisted")
	}
}

func TestSubscriptionLoginRequiresStore(t *testing.T) {
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{}), Tokens: &providers.TokenHolder{}}
	a := NewSubscription(deps, oauthflow.New(deps.HTTP, func() time.Time { return now }))
	_, err := a.Fetch(context.Background(), providers.FetchRequest{Config: subCfg(map[string]any{"token_source": "login"}, nil), Now: now})
	if pe := providers.AsError(err); pe.Kind != domain.ErrConfiguration {
		t.Fatalf("err = %v", err)
	}
}

func TestAdminFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-ant-admin01-synthetic" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/v1/organizations/cost_report":
			_, _ = w.Write(fixture(t, "cost_report.json"))
		case r.URL.Path == "/v1/organizations/usage_report/messages" && q.Get("bucket_width") == "1h":
			if strings.Join(q["group_by[]"], ",") != "model,api_key_id" {
				t.Errorf("group_by = %v", q["group_by[]"])
			}
			_, _ = w.Write(fixture(t, "usage_report_1h.json"))
		case r.URL.Path == "/v1/organizations/usage_report/messages" && q.Get("page") == "page_2":
			_, _ = w.Write(fixture(t, "usage_report_1d_p2.json"))
		case r.URL.Path == "/v1/organizations/usage_report/messages":
			_, _ = w.Write(fixture(t, "usage_report_1d.json"))
		case r.URL.Path == "/v1/organizations/api_keys":
			_, _ = w.Write(fixture(t, "api_keys.json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a := NewAdmin(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{})})
	c := providers.AccountConfig{
		Account: domain.Account{ID: "org", ProviderID: AdminID}, Options: map[string]any{"base_url": srv.URL},
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret("sk-ant-admin01-synthetic")}),
	}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var hourTotal, dayTotal *domain.UsageAggregate
	var modelSum, keySum int64
	for i, u := range res.Usage {
		switch {
		case u.Granularity == domain.GranHour && u.Breakdown == "total":
			hourTotal = &res.Usage[i]
		case u.Granularity == domain.GranDay && u.Breakdown == "total":
			dayTotal = &res.Usage[i]
		case u.Granularity == domain.GranHour && u.Breakdown == "model":
			modelSum += *u.Tokens.Input
		case u.Granularity == domain.GranHour && u.Breakdown == "api_key":
			keySum += *u.Tokens.Input
		}
	}
	if hourTotal == nil || *hourTotal.Tokens.Input != 1350 || *hourTotal.Tokens.CacheRead != 4000 || *hourTotal.Tokens.CacheWrite != 200 {
		t.Fatalf("hour total: %+v", hourTotal)
	}
	if hourTotal.Requests != nil {
		t.Fatal("Anthropic usage reports have no request counts; must be unknown")
	}
	if modelSum != 1350 || keySum != 1350 {
		t.Fatalf("breakdowns must partition the same usage: model=%d key=%d", modelSum, keySum)
	}
	if hourTotal.ActualCost != nil {
		t.Fatal("hourly rows have no provider cost")
	}
	// 123.45 + 0.55 + 1000 cents = USD 11.24
	if dayTotal == nil || dayTotal.ActualCost == nil || dayTotal.ActualCost.Amount.String() != "11.24" {
		t.Fatalf("daily cost: %+v", dayTotal)
	}
	if len(res.APIKeys) != 1 || res.APIKeys[0].Hint != "sk-ant-api03-SYN...TH01" || res.APIKeys[0].Name != "ci-agents" {
		t.Fatalf("keys: %+v", res.APIKeys)
	}
}
