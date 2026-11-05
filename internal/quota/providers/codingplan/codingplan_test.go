package codingplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

var now = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const secretKey = "sk-cp-SECRET-test-key"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "quota", "codingplan", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type route struct {
	status int
	body   string
	header map[string]string
}

func serve(t *testing.T, routes map[string]route) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secretKey {
			w.WriteHeader(401)
			return
		}
		rt, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		for k, v := range rt.header {
			w.Header().Set(k, v)
		}
		if rt.status != 0 {
			w.WriteHeader(rt.status)
		}
		_, _ = w.Write([]byte(rt.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cfg(base string) providers.AccountConfig {
	return providers.AccountConfig{
		Account:     domain.Account{ID: "x"},
		Options:     map[string]any{"base_url": base},
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret(secretKey)}),
	}
}

func deps() providers.Deps {
	return providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})}
}

func fetch(t *testing.T, a providers.Adapter, base string) (*providers.FetchResult, error) {
	t.Helper()
	return a.Fetch(context.Background(), providers.FetchRequest{Config: cfg(base), Now: now})
}

func byKey(res *providers.FetchResult) map[string]domain.BucketObservation {
	m := map[string]domain.BucketObservation{}
	for _, b := range res.Buckets {
		m[b.Spec.Key] = b
	}
	return m
}

func noSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), secretKey) {
		t.Fatalf("error leaks the key: %v", err)
	}
}

func TestUnofficialTier(t *testing.T) {
	for _, a := range []providers.Adapter{NewZai(deps()), NewMiniMax(deps())} {
		info := a.Info()
		if info.Tier != domain.TierUnofficial {
			t.Fatalf("%s tier = %s; config-load opt-in gate depends on TierUnofficial", info.ID, info.Tier)
		}
		if !strings.Contains(info.Capabilities[domain.CapQuotaRemaining].Note, "Community-observed") {
			t.Fatalf("%s lacks community-observed note", info.ID)
		}
	}
}

func TestZai(t *testing.T) {
	srv := serve(t, map[string]route{zaiQuotaPath: {body: fixture(t, "zai_quota.json")}})
	res, err := fetch(t, NewZai(deps()), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(res)
	if len(res.Buckets) != 3 {
		t.Fatalf("buckets: %v", m)
	}
	five := m["five_hour"]
	if five.Spec.Window != 5*time.Hour || *five.Used != 25 || *five.Remaining != 75 || !five.Spec.Global ||
		five.Spec.ResetSemantics != domain.ResetProviderTimestamp || !five.ResetAt.Equal(now.Add(2*time.Hour)) || five.Spec.Official {
		t.Fatalf("five_hour: %+v", five)
	}
	week := m["seven_day"]
	if week.Spec.Window != 7*24*time.Hour || *week.Used != 61 || !week.ResetAt.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("seven_day (string percentage/reset): %+v", week)
	}
	mcp := m["mcp_tools:720h0m0s"]
	if mcp.Spec.Unit != domain.UnitRequests || *mcp.Limit != 1000 || *mcp.Used != 120 || *mcp.Remaining != 880 || mcp.Spec.Global || mcp.ResetAt != nil {
		t.Fatalf("mcp: %+v", mcp)
	}
	if *res.MaskedIdentifier != "plan: pro" {
		t.Fatalf("identifier = %s", *res.MaskedIdentifier)
	}
}

func TestZaiErrors(t *testing.T) {
	tests := []struct {
		name  string
		route route
		kind  domain.ErrorKind
		retry time.Duration
	}{
		{"HTTP 200 with body code 401", route{body: fixture(t, "zai_auth_error.json")}, domain.ErrAuthentication, 0},
		{"HTTP 401", route{status: 401, body: `{}`}, domain.ErrAuthentication, 0},
		{"HTTP 429 Retry-After", route{status: 429, body: `{}`, header: map[string]string{"Retry-After": "90"}}, domain.ErrRateLimited, 90 * time.Second},
		{"no data", route{body: `{"code":200,"success":true}`}, domain.ErrInvalidResponse, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serve(t, map[string]route{zaiQuotaPath: tt.route})
			_, err := fetch(t, NewZai(deps()), srv.URL)
			pe := providers.AsError(err)
			if pe == nil || pe.Kind != tt.kind || pe.RetryAfter != tt.retry {
				t.Fatalf("err = %+v", pe)
			}
			noSecret(t, err)
			if strings.Contains(err.Error(), "token expired") {
				t.Fatalf("raw provider message leaked: %v", err)
			}
		})
	}
}

func TestMiniMaxTokenPlanNestedStrings(t *testing.T) {
	srv := serve(t, map[string]route{miniMaxTokenPlan: {body: fixture(t, "minimax_token_plan.json")}})
	res, err := fetch(t, NewMiniMax(deps()), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(res)
	five, week := m["five_hour"], m["seven_day"]
	if five.Remaining == nil || *five.Remaining != 96 || *five.Used != 4 || five.Spec.Scope != domain.ScopeAccount ||
		!five.ResetAt.Equal(now.Add(2*time.Hour)) || !five.Spec.Global {
		t.Fatalf("five_hour: %+v", five)
	}
	if week.Remaining == nil || *week.Remaining != 99 || !week.ResetAt.Equal(now.Add(5*24*time.Hour)) {
		t.Fatalf("seven_day: %+v", week)
	}
	if *res.MaskedIdentifier != "plan: Token Plan Plus" {
		t.Fatalf("identifier = %s", *res.MaskedIdentifier)
	}
}

func TestMiniMaxFallbackTopLevelInts(t *testing.T) {
	// token_plan path is absent (404) → coding_plan path, top-level envelope.
	srv := serve(t, map[string]route{miniMaxCodingPlan: {body: fixture(t, "minimax_coding_plan.json")}})
	res, err := fetch(t, NewMiniMax(deps()), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(res)
	m2 := m["five_hour:model:MiniMax-M2"]
	if m2.Remaining == nil || *m2.Remaining != 80 || m2.Spec.Model != "MiniMax-M2" || m2.Spec.Global {
		t.Fatalf("M2 interval: %+v (keys %v)", m2, m)
	}
	if w := m["seven_day:model:MiniMax-M2"]; w.Remaining == nil || *w.Remaining != 60 {
		t.Fatalf("M2 weekly: %+v", w)
	}
	// Exhausted status without a percentage: remaining 0 (provider-reported).
	vl := m["five_hour:model:MiniMax-VL"]
	if vl.Remaining == nil || *vl.Remaining != 0 {
		t.Fatalf("VL exhausted: %+v", vl)
	}
	// Raw counts (1200 "usage" of 1500) are never used as values.
	if m2.Limit == nil || *m2.Limit != 100 || m2.Spec.Unit != domain.UnitPercent {
		t.Fatalf("expected percent normalization: %+v", m2)
	}
}

func TestMiniMaxMissingPercentStaysUnknown(t *testing.T) {
	body := `{"base_resp":{"status_code":0},"model_remains":[{"model_name":"general","start_time":"1792224000000","end_time":"1792242000000","current_interval_total_count":1500,"current_interval_usage_count":1200,"current_interval_status":1}]}`
	srv := serve(t, map[string]route{miniMaxTokenPlan: {body: body}})
	res, err := fetch(t, NewMiniMax(deps()), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := res.Buckets[0]
	if b.Remaining != nil || b.Used != nil || len(res.Notes) == 0 {
		t.Fatalf("ambiguous counts must leave usage unknown: %+v notes=%v", b, res.Notes)
	}
	if b.Spec.Key != "five_hour" {
		t.Fatalf("string timestamps not parsed: %+v", b.Spec)
	}
}

func TestMiniMaxErrors(t *testing.T) {
	tests := []struct {
		name   string
		routes map[string]route
		kind   domain.ErrorKind
	}{
		{"base_resp auth code", map[string]route{miniMaxTokenPlan: {body: fixture(t, "minimax_auth_error.json")}}, domain.ErrAuthentication},
		{"not subscribed", map[string]route{miniMaxTokenPlan: {body: fixture(t, "minimax_not_subscribed.json")}}, domain.ErrUnsupported},
		{"429", map[string]route{miniMaxTokenPlan: {status: 429, body: `{}`, header: map[string]string{"Retry-After": "5"}}}, domain.ErrRateLimited},
		{"both paths missing", map[string]route{}, domain.ErrUnsupported},
		{"other status code", map[string]route{miniMaxTokenPlan: {body: `{"base_resp":{"status_code":"2013","status_msg":"invalid params ` + secretKey + `"}}`}}, domain.ErrInvalidResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serve(t, tt.routes)
			_, err := fetch(t, NewMiniMax(deps()), srv.URL)
			pe := providers.AsError(err)
			if pe == nil || pe.Kind != tt.kind {
				t.Fatalf("err = %+v", pe)
			}
			noSecret(t, err)
			if strings.Contains(err.Error(), "login fail") || strings.Contains(err.Error(), "invalid params") {
				t.Fatalf("raw provider message leaked: %v", err)
			}
		})
	}
}

func TestValidateOptions(t *testing.T) {
	for _, a := range []providers.Adapter{NewZai(deps()), NewMiniMax(deps())} {
		c := cfg("")
		c.Options = map[string]any{"region": "china"}
		if err := a.Validate(c); err != nil {
			t.Fatalf("%s china: %v", a.Info().ID, err)
		}
		c.Options = map[string]any{"region": "mars"}
		if err := a.Validate(c); err == nil {
			t.Fatalf("%s accepted bad region", a.Info().ID)
		}
		c.Options = map[string]any{"base_url": "http://evil.example"}
		if err := a.Validate(c); err == nil {
			t.Fatalf("%s accepted non-https base_url", a.Info().ID)
		}
		c = cfg("")
		c.Options = nil
		c.Credentials = credentials.NewBundle(nil)
		if err := a.Validate(c); err == nil {
			t.Fatalf("%s accepted missing api_key", a.Info().ID)
		}
	}
}

func TestFlexNum(t *testing.T) {
	cases := map[string]*float64{`12`: providers.Float(12), `"96"`: providers.Float(96), `"7%"`: providers.Float(7), `null`: nil, `""`: nil, `"abc"`: nil}
	for in, want := range cases {
		var f flexNum
		if err := f.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatal(err)
		}
		if (want == nil) != (f.v == nil) || (want != nil && *want != *f.v) {
			t.Errorf("%s → %v, want %v", in, f.v, want)
		}
	}
}
