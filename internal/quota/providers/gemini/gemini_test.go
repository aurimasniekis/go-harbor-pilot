package gemini

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

var now = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const accessToken = "ya29.test-access-token"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "gemini", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type fakeGoogle struct {
	t           *testing.T
	pub         *rsa.PublicKey
	tokenCalls  atomic.Int32
	tokenStatus int
	quotaStatus int
	monStatus   int
	fixtures    map[string]string
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/token":
		g.tokenCalls.Add(1)
		if g.tokenStatus != 0 {
			w.WriteHeader(g.tokenStatus)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
			return
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != jwtBearerGrant {
			w.WriteHeader(400)
			return
		}
		if err := g.verify(r.PostForm.Get("assertion"), "http://"+r.Host+"/token"); err != nil {
			g.t.Errorf("assertion: %v", err)
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","expires_in":3599,"token_type":"Bearer"}`))
		return
	case r.Header.Get("Authorization") != "Bearer "+accessToken:
		w.WriteHeader(401)
		return
	case strings.HasSuffix(r.URL.Path, "/services/generativelanguage.googleapis.com/quotaInfos"):
		if g.quotaStatus != 0 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(g.quotaStatus)
			return
		}
		if r.URL.Query().Get("pageToken") == "page-2" {
			_, _ = w.Write([]byte(g.fixtures["page2"]))
			return
		}
		_, _ = w.Write([]byte(g.fixtures["page1"]))
	case r.URL.Path == "/v3/projects/example-project/timeSeries":
		if g.monStatus != 0 {
			w.WriteHeader(g.monStatus)
			return
		}
		q := r.URL.Query()
		if !strings.Contains(q.Get("filter"), `resource.label.service="generativelanguage.googleapis.com"`) ||
			q.Get("aggregation.perSeriesAligner") != "ALIGN_SUM" {
			g.t.Errorf("unexpected monitoring query: %v", q)
		}
		if q.Get("aggregation.alignmentPeriod") == "60s" {
			_, _ = w.Write([]byte(g.fixtures["minute"]))
			return
		}
		// Per-day: summed since midnight Pacific (07:00 UTC in October).
		if q.Get("interval.startTime") != "2026-10-17T07:00:00Z" {
			g.t.Errorf("day interval start = %s", q.Get("interval.startTime"))
		}
		_, _ = w.Write([]byte(g.fixtures["day"]))
	default:
		w.WriteHeader(404)
	}
}

func (g *fakeGoogle) verify(jwt, aud string) error {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return errors.New("not a JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(g.pub, crypto.SHA256, sum[:], sig); err != nil {
		return err
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	if err := json.Unmarshal(cb, &c); err != nil {
		return err
	}
	if c["iss"] != "quota-reader@example-project.iam.gserviceaccount.com" || c["scope"] != Scope || c["aud"] != aud {
		return errors.New("bad claims")
	}
	if int64(c["exp"].(float64))-int64(c["iat"].(float64)) != 3600 {
		return errors.New("bad lifetime")
	}
	return nil
}

type env struct {
	g   *fakeGoogle
	srv *httptest.Server
	a   *Adapter
	cfg providers.AccountConfig
	key string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	g := &fakeGoogle{t: t, pub: &priv.PublicKey, fixtures: map[string]string{
		"page1": fixture(t, "quota_infos_page1.json"), "page2": fixture(t, "quota_infos_page2.json"),
		"minute": fixture(t, "timeseries_minute.json"), "day": fixture(t, "timeseries_day.json"),
	}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	sa, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "example-project", "private_key_id": "kid-1", "private_key": pemKey,
		"client_email": "quota-reader@example-project.iam.gserviceaccount.com", "token_uri": srv.URL + "/token",
	})
	cfg := providers.AccountConfig{
		Account:     domain.Account{ID: "gemini"},
		Options:     map[string]any{"project_id": "example-project", "quotas_base_url": srv.URL, "monitoring_base_url": srv.URL},
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"service_account": credentials.NewSecret(string(sa))}),
	}
	a := New(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})})
	return &env{g: g, srv: srv, a: a, cfg: cfg, key: pemKey}
}

func (e *env) fetch(t *testing.T) (*providers.FetchResult, error) {
	t.Helper()
	return e.a.Fetch(context.Background(), providers.FetchRequest{Config: e.cfg, Now: now})
}

func TestFetchJoinsQuotasAndUsage(t *testing.T) {
	e := newEnv(t)
	if err := e.a.Validate(e.cfg); err != nil {
		t.Fatal(err)
	}
	res, err := e.fetch(t)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]domain.BucketObservation{}
	for _, b := range res.Buckets {
		by[b.Spec.Key] = b
	}
	if len(res.Buckets) != 5 {
		t.Fatalf("got %d buckets: %v", len(res.Buckets), by)
	}
	// Per-day quotas first.
	if res.Buckets[0].Spec.Window != 24*time.Hour || res.Buckets[1].Spec.Window != 24*time.Hour {
		t.Fatalf("day quotas not first: %+v", res.Buckets[:2])
	}
	pro := by["quota:GenerateRequestsPerDayPerProjectPerModel:model=gemini-2.5-pro"]
	wantReset := time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)
	if *pro.Limit != 100 || pro.Used == nil || *pro.Used != 42 || *pro.Remaining != 58 ||
		pro.Spec.ResetSemantics != domain.ResetFixedPeriodic || pro.Spec.Period != 24*time.Hour ||
		!pro.ResetAt.Equal(wantReset) || pro.Spec.Model != "gemini-2.5-pro" || pro.Spec.Scope != domain.ScopeModel || !pro.Spec.Official {
		t.Fatalf("pro day: %+v", pro)
	}
	flash := by["quota:GenerateRequestsPerDayPerProjectPerModel:model=gemini-2.5-flash"]
	if *flash.Limit != 250 || flash.Used != nil || flash.Remaining != nil {
		t.Fatalf("flash day usage must stay unknown: %+v", flash)
	}
	rpm := by["quota:GenerateContentRequestsPerMinutePerProjectPerModel:model=gemini-2.5-pro"]
	if *rpm.Limit != 5 || *rpm.Used != 3 || rpm.Spec.Kind != domain.KindRollingWindow || rpm.Spec.ResetSemantics != domain.ResetRolling {
		t.Fatalf("pro rpm: %+v", rpm)
	}
	if b := by["quota:GenerateContentRequestsPerMinutePerProjectPerModel:model=gemini-2.5-flash"]; b.Used != nil {
		t.Fatalf("flash rpm usage must stay unknown: %+v", b)
	}
	if _, ok := by["quota:GenerateContentRequestsPerMinutePerProjectPerModel:model=gemini-legacy"]; ok {
		t.Fatal("zero limit should be skipped")
	}
	tpm := by["quota:GenerateContentInputTokensPerProjectPerMinute"]
	if tpm.Spec.Unit != domain.UnitTokens || *tpm.Used != 12000 || tpm.Spec.Scope != domain.ScopeProject {
		t.Fatalf("tpm: %+v", tpm)
	}
	if *res.MaskedIdentifier != "project: example-project" {
		t.Fatalf("identifier = %v", *res.MaskedIdentifier)
	}
	// Token is cached across fetches.
	if _, err := e.fetch(t); err != nil {
		t.Fatal(err)
	}
	if n := e.g.tokenCalls.Load(); n != 1 {
		t.Fatalf("token minted %d times", n)
	}
}

func TestMaxBuckets(t *testing.T) {
	e := newEnv(t)
	e.cfg.Options["max_buckets"] = 2
	res, err := e.fetch(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 2 || len(res.Notes) == 0 {
		t.Fatalf("buckets=%d notes=%v", len(res.Buckets), res.Notes)
	}
}

func TestMonitoringFailureKeepsLimits(t *testing.T) {
	e := newEnv(t)
	e.g.monStatus = 403
	res, err := e.fetch(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 5 || len(res.Notes) == 0 {
		t.Fatalf("buckets=%d notes=%v", len(res.Buckets), res.Notes)
	}
	for _, b := range res.Buckets {
		if b.Used != nil {
			t.Fatalf("usage must be unknown without monitoring: %+v", b)
		}
	}
}

func TestTokenRejected(t *testing.T) {
	e := newEnv(t)
	e.g.tokenStatus = 400
	_, err := e.fetch(t)
	pe := providers.AsError(err)
	if pe.Kind != domain.ErrAuthentication {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error leaks details: %v", err)
	}
}

func TestQuotaRateLimited(t *testing.T) {
	e := newEnv(t)
	e.g.quotaStatus = 429
	_, err := e.fetch(t)
	pe := providers.AsError(err)
	if pe.Kind != domain.ErrRateLimited || pe.RetryAfter != 30*time.Second {
		t.Fatalf("err = %+v", pe)
	}
}

func TestValidate(t *testing.T) {
	e := newEnv(t)
	cases := map[string]func(c *providers.AccountConfig){
		"missing project": func(c *providers.AccountConfig) { delete(c.Options, "project_id") },
		"unknown option":  func(c *providers.AccountConfig) { c.Options["bogus"] = 1 },
		"missing credential": func(c *providers.AccountConfig) {
			c.Credentials = credentials.NewBundle(nil)
		},
		"not json": func(c *providers.AccountConfig) {
			c.Credentials = credentials.NewBundle(map[string]credentials.Secret{"service_account": credentials.NewSecret("nope")})
		},
		"http token uri": func(c *providers.AccountConfig) {
			sa, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "x@y", "private_key": e.key, "token_uri": "http://evil.example/token"})
			c.Credentials = credentials.NewBundle(map[string]credentials.Secret{"service_account": credentials.NewSecret(string(sa))})
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := e.cfg
			c.Options = map[string]any{}
			for k, v := range e.cfg.Options {
				c.Options[k] = v
			}
			mut(&c)
			err := e.a.Validate(c)
			if err == nil || providers.AsError(err).Kind != domain.ErrConfiguration {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Fatal("error leaks the key")
			}
		})
	}
}

func TestRefreshDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"minute": time.Minute, "day": 24 * time.Hour, "10 seconds": 10 * time.Second, "": 0, "weird": 0} {
		if got := refreshDuration(in); got != want {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}
