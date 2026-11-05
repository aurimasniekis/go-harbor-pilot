package acceptance_test

import (
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

func msg(model string) map[string]any {
	return map[string]any{"model": model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "x"}}}
}

// Scenario C: authorized account exhaustion triggers safe failover and
// exactly one switch event.
func TestScenarioC_ExhaustionFailoverOneSwitchEvent(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("sub-1", prov, apptest.AccountOpts{Billing: "subscription"})
	a2 := e.Account("sub-2", prov, apptest.AccountOpts{Billing: "subscription"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	call := func() int {
		r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
		_ = apptest.ReadAll(r)
		return r.StatusCode
	}
	if call() != 200 { // baseline on sub-1
		t.Fatal("baseline failed")
	}
	up.Push(apptest.Reply{Status: 429, RetryAfter: 600}) // sub-1 exhausted
	for range 5 {
		if c := call(); c != 200 {
			t.Fatalf("request after exhaustion failed: %d", c)
		}
	}
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.routing.account_switched.v1'`) == 1
	}, "exactly one switch event")
	payload := e.QueryString(`SELECT payload FROM event_outbox WHERE type = 'harborpilot.routing.account_switched.v1'`)
	if !strings.Contains(payload, a1) || !strings.Contains(payload, "rate_limited") {
		t.Fatalf("switch event payload %s", payload)
	}
	// The exhausted account is not retried while cooling down: only one 429.
	e.Flush()
	if n := e.QueryInt(`SELECT count(*) FROM attempts WHERE account_id = ? AND status = 'error'`, a1); n != 1 {
		t.Fatalf("exhausted account attempted %d times", n)
	}
	if n := e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.routing.fallback_selected.v1'`); n != 0 {
		t.Fatal("same-model account failover is a switch, not a model/provider fallback")
	}
}

// Scenario D: an unapproved paid fallback is blocked.
func TestScenarioD_UnapprovedPaidFallbackBlocked(t *testing.T) {
	e := apptest.New(t)
	sub := apptest.NewUpstream(t, "anthropic")
	paid := apptest.NewUpstream(t, "openai")
	pSub := e.Provider("anthropic", "anthropic", sub.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	pPaid := e.Provider("compat", "openai_compatible", paid.Srv.URL, apptest.Model{ID: "g"})
	aSub := e.Account("subscription", pSub, apptest.AccountOpts{Billing: "subscription"})
	aPaid := e.Account("metered", pPaid, apptest.AccountOpts{Billing: "metered_api"})
	alias := e.Alias("coding/default", e.Pool("mixed", "ordered", apptest.Member{Account: aSub, Model: "m"}, apptest.Member{Account: aPaid, Model: "g", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias, AllowPaidFallback: false}), apptest.BindingOpts{}))
	sub.Push(apptest.Reply{Status: 429, RetryAfter: 60})
	r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
	body := apptest.ReadAll(r)
	if r.StatusCode != 429 || !strings.Contains(body, "rate_limit_error") {
		t.Fatalf("expected the subscription's rate limit to surface: %d %s", r.StatusCode, body)
	}
	if paid.Count() != 0 {
		t.Fatal("metered upstream was contacted without authorization")
	}
	e.Flush()
	dec := e.QueryString(`SELECT decision FROM requests`)
	if !strings.Contains(dec, "paid_fallback_not_authorized") {
		t.Fatalf("decision does not explain the exclusion: %s", dec)
	}
	// With authorization the same plan falls back.
	tok2 := e.Token(e.Binding("b2", e.Plan("paid-ok", apptest.PlanOpts{DefaultAlias: alias, AllowPaidFallback: true}), apptest.BindingOpts{}))
	r = e.Gateway("POST", "/v1/messages", tok2, msg("x"))
	if b := apptest.ReadAll(r); r.StatusCode != 200 || paid.Count() != 1 {
		t.Fatalf("authorized paid fallback: %d %s (paid calls %d)", r.StatusCode, b, paid.Count())
	}
}

func TestSimulationAndReplay(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("a1", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: a1, Model: "m"}))
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias})
	tok := e.Token(e.Binding("b", plan, apptest.BindingOpts{}))
	var sim struct {
		Mode     string `json:"mode"`
		Error    string `json:"error"`
		Decision struct {
			Candidates []struct {
				AccountID string `json:"account_id"`
			} `json:"candidates"`
		} `json:"decision"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/routing-plans/"+plan+"/simulate", map[string]any{"model": "anything", "protocol": "anthropic_messages", "stream": true,
		"health_overrides": map[string]any{a1: map[string]any{"available": false, "reason": "drill"}}}, &sim)
	if sim.Mode != "simulation" || sim.Error == "" || up.Count() != 0 {
		t.Fatalf("simulation: %+v (upstream calls %d)", sim, up.Count())
	}
	r := e.Gateway("POST", "/v1/messages", tok, msg("anything"))
	reqID := r.Header.Get("X-Request-ID")
	_ = apptest.ReadAll(r)
	e.Flush()
	var rep struct {
		Changed bool `json:"changed"`
		Replay  struct {
			Mode string `json:"mode"`
		} `json:"replay"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/activity/requests/"+reqID+"/replay", map[string]any{}, &rep)
	if rep.Replay.Mode != "replay" || rep.Changed || up.Count() != 1 {
		t.Fatalf("replay: %+v calls=%d", rep, up.Count())
	}
	var vars struct {
		Items []struct{ Name string } `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/routing/variables", nil, &vars)
	if len(vars.Items) < 30 {
		t.Fatal("variable reference incomplete")
	}
	var v struct {
		Valid bool   `json:"valid"`
		Error string `json:"error"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/routing/validate-expression", map[string]any{"expression": "request.tools > 'x'", "kind": "bool"}, &v)
	if v.Valid {
		t.Fatal("type error not detected")
	}
}
