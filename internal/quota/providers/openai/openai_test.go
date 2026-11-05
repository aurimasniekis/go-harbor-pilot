package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/oauthflow"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

var now = time.Date(2026, 10, 17, 11, 30, 0, 0, time.UTC)

const usageHour = `{"object":"page","data":[{"object":"bucket","start_time":1792231200,"end_time":1792234800,"results":[
 {"object":"organization.usage.completions.result","input_tokens":1000,"input_cached_tokens":400,"output_tokens":200,"num_model_requests":5,"project_id":null,"user_id":null,"api_key_id":"key_SYNTH1","model":"gpt-synthetic","batch":null},
 {"object":"organization.usage.completions.result","input_tokens":100,"input_cached_tokens":0,"output_tokens":50,"num_model_requests":1,"project_id":null,"user_id":null,"api_key_id":"key_SYNTH2","model":"gpt-synthetic","batch":null}]}],
 "has_more":false,"next_page":null}`

const usageDay = `{"object":"page","data":[{"object":"bucket","start_time":1792195200,"end_time":1792281600,"results":[
 {"input_tokens":1100,"input_cached_tokens":400,"output_tokens":250,"num_model_requests":6,"api_key_id":"key_SYNTH1","model":"gpt-synthetic"}]}],"has_more":false,"next_page":null}`

const costs = `{"object":"page","data":[{"object":"bucket","start_time":1792195200,"end_time":1792281600,"results":[
 {"object":"organization.costs.result","amount":{"value":0.06,"currency":"usd"},"line_item":null,"project_id":null},
 {"object":"organization.costs.result","amount":{"value":1.1,"currency":"usd"},"line_item":null,"project_id":null}]}],"has_more":false,"next_page":null}`

func TestAdminFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-admin-synthetic" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/v1/organization/costs":
			fmt.Fprint(w, costs)
		case r.URL.Path == "/v1/organization/usage/completions" && r.URL.Query().Get("bucket_width") == "1h":
			if strings.Join(r.URL.Query()["group_by"], ",") != "model,api_key_id" {
				t.Errorf("group_by = %v", r.URL.Query()["group_by"])
			}
			fmt.Fprint(w, usageHour)
		case r.URL.Path == "/v1/organization/usage/completions":
			fmt.Fprint(w, usageDay)
		case r.URL.Path == "/v1/organization/projects":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"proj_1","name":"Agents","status":"active"}],"has_more":false}`)
		case r.URL.Path == "/v1/organization/projects/proj_1/api_keys":
			fmt.Fprint(w, `{"object":"list","data":[{"object":"organization.project.api_key","redacted_value":"sk-abc...xyz","name":"ci","created_at":1760000000,"last_used_at":1792230000,"id":"key_SYNTH1"}],"has_more":false}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a := NewAdmin(providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true})})
	c := providers.AccountConfig{Account: domain.Account{ID: "oa", ProviderID: AdminID}, Options: map[string]any{"base_url": srv.URL},
		Credentials: credentials.NewBundle(map[string]credentials.Secret{"api_key": credentials.NewSecret("sk-admin-synthetic")})}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var ht, dt *domain.UsageAggregate
	for i, u := range res.Usage {
		if u.Breakdown == "total" && u.Granularity == domain.GranHour {
			ht = &res.Usage[i]
		}
		if u.Breakdown == "total" && u.Granularity == domain.GranDay {
			dt = &res.Usage[i]
		}
	}
	if ht == nil || *ht.Requests != 6 || *ht.Tokens.Input != 1100 || *ht.Tokens.CacheRead != 400 || !ht.Tokens.CacheReadIncludedInInput {
		t.Fatalf("hour total: %+v", ht)
	}
	if *ht.Tokens.TotalInput() != 1100 || *ht.Tokens.UncachedInput() != 700 {
		t.Fatal("cached tokens must not be double counted")
	}
	if dt == nil || dt.ActualCost == nil || dt.ActualCost.Amount.String() != "1.16" || dt.ActualCost.Currency != "USD" {
		t.Fatalf("day cost: %+v", dt)
	}
	if len(res.APIKeys) != 1 || res.APIKeys[0].Hint != "sk-abc...xyz" || res.APIKeys[0].ProviderLastUsedAt == nil {
		t.Fatalf("keys: %+v", res.APIKeys)
	}
}

func fakeJWT(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(claims)) + ".sig"
}

func TestCodexFetchFromAuthJSON(t *testing.T) {
	dir := t.TempDir()
	access := fakeJWT(fmt.Sprintf(`{"exp":%d}`, now.Add(time.Hour).Unix()))
	auth := fmt.Sprintf(`{"tokens":{"id_token":%q,"access_token":%q,"refresh_token":"r","account_id":"acct-synthetic"},"last_refresh":"2026-10-17T10:00:00Z"}`,
		fakeJWT(`{}`), access)
	p := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(p, []byte(auth), 0o600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ChatGPT-Account-Id") != "acct-synthetic" || r.Header.Get("Authorization") != "Bearer "+access {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,
		  "primary_window":{"used_percent":42,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1792240000},
		  "secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_after_seconds":86400,"reset_at":1792800000}},
		  "credits":{"has_credits":true,"unlimited":false,"balance":"12.5"},
		  "additional_rate_limits":[{"limit_name":"Synthetic model","metered_feature":"synthetic_feature","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_after_seconds":10,"reset_at":1792240000}}}]}`)
	}))
	defer srv.Close()
	files := &providers.FileReader{}
	files.SetRoots([]string{dir})
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true}), Files: files}
	a := NewCodex(deps, oauthflow.New(deps.HTTP, func() time.Time { return now }))
	c := providers.AccountConfig{Account: domain.Account{ID: "codex", ProviderID: CodexID},
		Options: map[string]any{"credentials_file": p, "usage_url": srv.URL}}
	res, err := a.Fetch(context.Background(), providers.FetchRequest{Config: c, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	b := map[string]domain.BucketObservation{}
	for _, o := range res.Buckets {
		b[o.Spec.Key] = o
	}
	if *b["five_hour"].Remaining != 58 || b["five_hour"].Spec.Window != 5*time.Hour || !b["five_hour"].Spec.Global {
		t.Fatalf("five_hour: %+v", b["five_hour"])
	}
	if *b["seven_day"].Remaining != 90 {
		t.Fatalf("seven_day: %+v", b["seven_day"])
	}
	if add := b["additional:synthetic_feature:primary"]; add.Spec.Global || add.Spec.Scope != domain.ScopeModel {
		t.Fatalf("additional limit must be model-scoped and non-global: %+v", add.Spec)
	}
	if cr := b["credits"]; cr.Spec.Unit != domain.UnitCredits || *cr.Remaining != 12.5 {
		t.Fatalf("credits: %+v", cr)
	}
}

func TestCodexExpiredAccessToken(t *testing.T) {
	dir := t.TempDir()
	access := fakeJWT(fmt.Sprintf(`{"exp":%d}`, now.Add(-time.Hour).Unix()))
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(fmt.Sprintf(`{"tokens":{"access_token":%q,"account_id":"a"}}`, access)), 0o600)
	files := &providers.FileReader{}
	files.SetRoots([]string{dir})
	deps := providers.Deps{HTTP: providers.NewHTTPClient(providers.HTTPOptions{AllowPrivate: true}), Files: files}
	a := NewCodex(deps, oauthflow.New(deps.HTTP, func() time.Time { return now }))
	_, err := a.Fetch(context.Background(), providers.FetchRequest{Config: providers.AccountConfig{Account: domain.Account{ID: "x"},
		Options: map[string]any{"credentials_file": filepath.Join(dir, "auth.json")}}, Now: now})
	if pe := providers.AsError(err); pe.Kind != domain.ErrAuthentication {
		t.Fatalf("err = %v", err)
	}
}
