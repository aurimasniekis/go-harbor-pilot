package acceptance_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// fakeQuota serves a JSON quota document whose remaining value the test
// controls.
func fakeQuota(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var remaining atomic.Int64
	remaining.Store(80)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reset := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
		_, _ = w.Write([]byte(`{"window":{"remaining":` + itoa(remaining.Load()) + `,"limit":100,"reset":"` + reset + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &remaining
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

type board struct {
	Monitors []struct {
		ID      string `json:"id"`
		Health  string `json:"health"`
		Buckets []struct {
			Key               string   `json:"key"`
			RemainingFraction *float64 `json:"remaining_fraction"`
			Exhausted         bool     `json:"exhausted"`
		} `json:"buckets"`
	} `json:"monitors"`
	Health []struct {
		AccountID string   `json:"account_id"`
		Available bool     `json:"available"`
		Reason    string   `json:"reason"`
		Remaining *float64 `json:"remaining"`
	} `json:"account_health"`
}

func TestQuotaMonitorDrivesRouting(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	qsrv, remaining := fakeQuota(t)
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("sub-1", prov, apptest.AccountOpts{Billing: "subscription"})
	a2 := e.Account("sub-2", prov, apptest.AccountOpts{Billing: "subscription"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))

	// Invalid adapter options are rejected at configuration time.
	if code := e.Admin.Do("POST", "/api/v1/quota-monitors", map[string]any{"name": "bad", "spec": map[string]any{"adapter": "generic_http",
		"account_id": a1, "options": map[string]any{"url": "ftp://x"}, "enabled": true}}, nil); code != 422 {
		t.Fatalf("invalid options accepted: %d", code)
	}
	var mon struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/quota-monitors", map[string]any{"name": "sub-1-quota", "spec": map[string]any{
		"adapter": "generic_http", "account_id": a1, "enabled": true, "poll_interval_secs": 3600,
		"options": map[string]any{"url": qsrv.URL, "auth_type": "none", "allow_private_network": true, "buckets": []any{map[string]any{
			"key": "five_hour", "label": "5-hour window", "kind": "fixed_window", "unit": "requests", "global": true,
			"remaining_path": "window.remaining", "limit_path": "window.limit", "reset_at_path": "window.reset"}}}}}, &mon)

	poll := func() board {
		e.Admin.MustDo(200, "POST", "/api/v1/quota/monitors/"+mon.ID+"/refresh", nil, nil)
		var b board
		e.Eventually(func() bool {
			e.Admin.MustDo(200, "GET", "/api/v1/quota/board", nil, &b)
			return len(b.Monitors) == 1 && len(b.Monitors[0].Buckets) == 1 && b.Monitors[0].Buckets[0].RemainingFraction != nil &&
				*b.Monitors[0].Buckets[0].RemainingFraction == float64(remaining.Load())/100
		}, "monitor observation")
		return b
	}
	b := poll()
	if b.Monitors[0].Health != "healthy" {
		t.Fatalf("monitor health %s", b.Monitors[0].Health)
	}

	remaining.Store(0)
	b = poll()
	if !b.Monitors[0].Buckets[0].Exhausted {
		t.Fatal("bucket not marked exhausted")
	}
	for _, h := range b.Health {
		if h.AccountID == a1 && (h.Available || !strings.Contains(h.Reason, "5-hour window")) {
			t.Fatalf("account health %+v", h)
		}
	}
	r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
	_ = apptest.ReadAll(r)
	if r.StatusCode != 200 {
		t.Fatalf("request failed: %d", r.StatusCode)
	}
	e.Flush()
	if acc := e.QueryString(`SELECT final_account_id FROM requests ORDER BY id DESC LIMIT 1`); acc != a2 {
		t.Fatalf("routed to %s, want the account with quota left", acc)
	}
	if n := e.QueryInt(`SELECT count(*) FROM attempts WHERE account_id = ?`, a1); n != 0 {
		t.Fatal("exhausted account was attempted")
	}
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.quota.window_exhausted.v1'`) == 1
	}, "exhaustion event")

	remaining.Store(40)
	poll()
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.quota.window_recovered.v1'`) == 1
	}, "recovery event")
	if n := e.QueryInt(`SELECT count(*) FROM quota_observations`); n != 3 {
		t.Fatalf("history rows %d", n)
	}
	var fc struct {
		Unavailable *string `json:"unavailable_reason"`
		Confidence  string  `json:"confidence"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/quota/buckets/"+mon.ID+"/forecast?bucket=five_hour", nil, &fc)
	if fc.Confidence == "" {
		t.Fatal("forecast missing confidence")
	}
}

func TestUnofficialAdapterNeedsAcknowledgement(t *testing.T) {
	e := apptest.New(t)
	spec := map[string]any{"adapter": "claude_subscription", "enabled": true, "options": map[string]any{"token_source": "env"}}
	var adapters struct {
		Items []struct {
			ID               string `json:"id"`
			RequiresTermsAck bool   `json:"requires_terms_ack"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/quota/adapters", nil, &adapters)
	id := ""
	for _, a := range adapters.Items {
		if a.RequiresTermsAck {
			id = a.ID
			break
		}
	}
	if id == "" {
		t.Fatal("no unofficial adapter listed")
	}
	spec["adapter"] = id
	var errBody struct {
		Error struct {
			Details struct {
				Fields []struct {
					Location string `json:"location"`
				} `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	if code := e.Admin.Do("POST", "/api/v1/quota-monitors", map[string]any{"name": "sub", "spec": spec}, &errBody); code != 422 {
		t.Fatalf("unacknowledged unofficial adapter accepted: %d", code)
	}
	found := false
	for _, d := range errBody.Error.Details.Fields {
		found = found || d.Location == "body.spec.acknowledge_terms_risk"
	}
	if !found {
		t.Fatalf("missing acknowledgement error: %+v", errBody)
	}
}

func TestBudgetBlocksMeteredSpend(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	price := [2]string{"3", "15"}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price})
	acc := e.Account("api", prov, apptest.AccountOpts{Billing: "metered_api"})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	bind := e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{})
	tok := e.Token(bind)
	e.Admin.MustDo(201, "POST", "/api/v1/budgets", map[string]any{"name": "laptop-daily", "spec": map[string]any{"scope": "binding", "scope_id": bind,
		"period": "day", "limit": "0.001", "currency": "USD", "enforcement": "block", "enabled": true}}, nil)

	up.Push(apptest.Reply{Text: "ok", InputTok: 100, OutputTok: 50}) // 0.0003 + 0.00075 USD
	r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
	if b := apptest.ReadAll(r); r.StatusCode != 200 {
		t.Fatalf("first request: %d %s", r.StatusCode, b)
	}
	e.Flush()
	r = e.Gateway("POST", "/v1/messages", tok, msg("x"))
	body := apptest.ReadAll(r)
	if r.StatusCode < 400 || !strings.Contains(body, "budget") {
		t.Fatalf("over-budget request: %d %s", r.StatusCode, body)
	}
	if up.Count() != 1 {
		t.Fatalf("upstream contacted %d times", up.Count())
	}
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.budget.threshold_exceeded.v1'`) == 1
	}, "budget exceeded event")
	var st struct {
		Items []struct {
			State string `json:"state"`
			Spent int64  `json:"spent_e10"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/budgets/status", nil, &st)
	if len(st.Items) != 1 || st.Items[0].State != "exceeded" || st.Items[0].Spent != 10_500_000 {
		t.Fatalf("budget status %+v", st)
	}
	var sum struct {
		Groups []struct {
			Key   string `json:"key"`
			Costs []struct {
				Kind   string `json:"kind"`
				Amount string `json:"amount"`
			} `json:"costs"`
		} `json:"groups"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?group_by=binding", nil, &sum)
	if len(sum.Groups) != 1 || sum.Groups[0].Key != bind || len(sum.Groups[0].Costs) != 1 || sum.Groups[0].Costs[0].Kind != "estimated_api_charge" {
		t.Fatalf("usage summary %+v", sum)
	}
}

func TestResponseHeadersFeedQuotaBoard(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("api", prov, apptest.AccountOpts{Billing: "metered_api"})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	up.Push(apptest.Reply{Text: "ok", Headers: map[string]string{
		"anthropic-ratelimit-requests-limit": "50", "anthropic-ratelimit-requests-remaining": "45",
		"anthropic-ratelimit-requests-reset": time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)}})
	r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
	if b := apptest.ReadAll(r); r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	var b struct {
		Observed []struct {
			AccountID string `json:"account_id"`
			Buckets   []struct {
				Key               string   `json:"key"`
				Source            string   `json:"source"`
				RemainingFraction *float64 `json:"remaining_fraction"`
			} `json:"buckets"`
		} `json:"observed_accounts"`
		Health []struct {
			AccountID string   `json:"account_id"`
			Remaining *float64 `json:"remaining"`
		} `json:"account_health"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/quota/board", nil, &b)
	if len(b.Observed) != 1 || b.Observed[0].AccountID != acc || len(b.Observed[0].Buckets) != 1 ||
		b.Observed[0].Buckets[0].Key != "rl:requests:model:m" || b.Observed[0].Buckets[0].Source != "response_headers" ||
		*b.Observed[0].Buckets[0].RemainingFraction != 0.9 {
		t.Fatalf("observed %+v", b.Observed)
	}
	if len(b.Health) != 1 || b.Health[0].Remaining == nil || *b.Health[0].Remaining != 0.9 {
		t.Fatalf("health %+v", b.Health)
	}
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM quota_buckets WHERE owner_key = ? AND source = 'response_headers'`, acc) == 1
	}, "header observation persisted")
}

func TestQuotaLoginFlowGuards(t *testing.T) {
	e := apptest.New(t)
	var mon struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/quota-monitors", map[string]any{"name": "claude-sub", "spec": map[string]any{
		"adapter": "claude_subscription", "enabled": true, "acknowledge_terms_risk": true, "options": map[string]any{"token_source": "login"}}}, &mon)
	var logins struct {
		Items []struct {
			MonitorID string `json:"monitor_id"`
			SignedIn  bool   `json:"signed_in"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/quota/logins", nil, &logins)
	if len(logins.Items) != 1 || logins.Items[0].MonitorID != mon.ID || logins.Items[0].SignedIn {
		t.Fatalf("logins %+v", logins)
	}
	var st struct {
		AuthorizeURL string `json:"authorize_url"`
		State        string `json:"state"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/quota/logins/"+mon.ID+"/start", map[string]any{}, &st)
	if !strings.Contains(st.AuthorizeURL, "code_challenge_method=S256") || st.State == "" {
		t.Fatalf("authorize url %q", st.AuthorizeURL)
	}
	if code := e.Admin.Do("POST", "/api/v1/quota/logins/"+mon.ID+"/complete", map[string]any{"state": "forged", "code": "x#forged"}, nil); code != 422 {
		t.Fatalf("forged state accepted: %d", code)
	}
	if n := e.QueryInt(`SELECT count(*) FROM quota_oauth_tokens`); n != 0 {
		t.Fatal("token stored after failed login")
	}
	var other struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/quota-monitors", map[string]any{"name": "plain", "spec": map[string]any{"adapter": "generic_http", "enabled": false,
		"options": map[string]any{"url": "https://quota.example.test/v1", "auth_type": "none", "buckets": []any{map[string]any{"key": "k", "unit": "requests", "remaining_path": "r"}}}}}, &other)
	if code := e.Admin.Do("POST", "/api/v1/quota/logins/"+other.ID+"/start", map[string]any{}, nil); code != 404 {
		t.Fatalf("non-login monitor: %d", code)
	}
}
