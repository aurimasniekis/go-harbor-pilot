package acceptance_test

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
)

func userMsg(text string) map[string]any {
	return map[string]any{"model": "x", "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": text}}}
}

// Scenario N: a SpaceMan run (run-scoped child token with bound refs) and a
// standalone client share a gateway without identity or accounting leakage.
func TestScenarioN_SpaceManAndStandaloneClientCoexist(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("a", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias})
	spaceBinding := e.Binding("spaceman-runs", plan, apptest.BindingOpts{Harness: "claude_code", ExternalRefs: map[string]string{"spaceman.space": "spc_alpha"}})
	parent := e.Token(spaceBinding, "gateway:invoke", "gateway:models", "tokens:mint")
	laptop := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))

	mint := func(run string) (string, string) {
		r := e.Gateway("POST", "/v1/harbor/credentials", parent, map[string]any{"name": "run " + run, "scopes": []string{"gateway:invoke"}, "ttl_seconds": 3600,
			"bound_refs": map[string]string{"spaceman.space": "spc_alpha", "spaceman.run": run, "session": run}})
		var out struct {
			Token      string `json:"token"`
			Credential struct {
				ID string `json:"id"`
			} `json:"credential"`
		}
		body := apptest.ReadAll(r)
		if r.StatusCode != 201 || json.Unmarshal([]byte(body), &out) != nil || out.Token == "" {
			t.Fatalf("mint %s: %d %s", run, r.StatusCode, body)
		}
		return out.Token, out.Credential.ID
	}
	run1, run1ID := mint("run_1")
	run2, _ := mint("run_2")

	// A child token cannot widen its scopes or mint further tokens.
	if r := e.Gateway("POST", "/v1/harbor/credentials", run1, map[string]any{"name": "x"}); r.StatusCode < 400 {
		t.Fatalf("child minted a credential: %d", r.StatusCode)
	}
	// The standalone client tries to claim a SpaceMan run through headers:
	// recorded as an untrusted claim, never as attribution.
	for _, tok := range []string{run1, run1, run2, laptop} {
		r := e.Gateway("POST", "/v1/messages", tok, userMsg("hello"), "X-HP-Session", "run_1", "X-HP-External-Ref", "spaceman.run=run_1")
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
	e.Flush()
	type row struct{ binding, session, refs, runRef string }
	rows := map[string]row{}
	q, _ := e.App.DB.R.Query(`SELECT r.credential_id, r.binding_id, COALESCE(r.session_id,''), r.trusted_refs, COALESCE(l.run_ref,'')
		FROM requests r JOIN ledger l ON l.request_id = r.id ORDER BY r.id`)
	defer q.Close()
	n := 0
	for q.Next() {
		var cred string
		var x row
		_ = q.Scan(&cred, &x.binding, &x.session, &x.refs, &x.runRef)
		rows[cred+"#"+string(rune('0'+n))] = x
		n++
		switch {
		case strings.Contains(x.refs, "run_1"):
			if x.binding != spaceBinding || x.runRef != "run_1" {
				t.Fatalf("run_1 attribution %+v", x)
			}
		case strings.Contains(x.refs, "run_2"):
			if x.binding != spaceBinding || x.runRef != "run_2" {
				t.Fatalf("run_2 attribution %+v", x)
			}
		default:
			if x.binding == spaceBinding || x.runRef != "" || x.session != "" {
				t.Fatalf("standalone request leaked SpaceMan identity: %+v", x)
			}
		}
	}
	if n != 4 {
		t.Fatalf("rows %d", n)
	}
	// Run sessions stay separate; the standalone request has no session.
	if s := e.QueryInt(`SELECT COUNT(DISTINCT session_id) FROM requests WHERE session_id IS NOT NULL`); s != 2 {
		t.Fatalf("sessions %d", s)
	}
	if c := e.QueryString(`SELECT claims FROM requests WHERE binding_id <> ?`, spaceBinding); !strings.Contains(c, "run_1") {
		t.Fatalf("standalone claim not recorded as untrusted: %s", c)
	}
	// History dedupe: run_1's second identical request repeats its history.
	if rep := e.QueryInt(`SELECT SUM(repeated_messages) FROM requests WHERE trusted_refs LIKE '%run_1%'`); rep != 1 {
		t.Fatalf("repeated messages %d", rep)
	}
	if u := e.QueryInt(`SELECT COUNT(*) FROM requests WHERE occurrence = 'uncertain'`); u != 1 {
		t.Fatalf("uncertain occurrences %d", u)
	}
	// Usage statistics split by client.
	var st struct {
		Groups []struct {
			Key      string `json:"key"`
			Requests int    `json:"requests"`
		} `json:"groups"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/stats?by=client", nil, &st)
	if len(st.Groups) != 2 || st.Groups[0].Key != spaceBinding || st.Groups[0].Requests != 3 {
		t.Fatalf("stats %+v", st)
	}
	// The run owner revokes the run token when the run ends.
	if r := e.Gateway("DELETE", "/v1/harbor/credentials/"+run1ID, laptop, nil); r.StatusCode != 404 {
		t.Fatalf("another client revoked a run token: %d", r.StatusCode)
	}
	if r := e.Gateway("DELETE", "/v1/harbor/credentials/"+run1ID, parent, nil); r.StatusCode != 204 {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	if r := e.Gateway("POST", "/v1/messages", run1, userMsg("again")); r.StatusCode != 401 {
		t.Fatalf("revoked run token still works: %d", r.StatusCode)
	}
}

func TestIntentClassifierAndPrecedence(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("a", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	var pl struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans", map[string]any{"name": "intents", "spec": map[string]any{"display_name": "i", "description": ""}}, &pl)
	doc := map[string]any{"schema_version": 1, "model_mappings": []any{}, "default_alias_id": alias, "intent": map[string]any{"mode": "heuristic"},
		"restrictions": map[string]any{"allowed_provider_ids": []string{}, "denied_provider_ids": []string{}, "allowed_billing": []string{}, "allowed_classifications": []string{}, "required_capabilities": []string{}},
		"fallback":     map[string]any{"steps": []string{"alternate_account", "stop"}, "max_attempts": 2, "allow_paid_fallback": false, "wait_max_seconds": 0},
		"engine": map[string]any{"rules": []any{map[string]any{"name": "review-rule", "stage": "request", "when": "request.input_chars > 5000",
			"action": "set_intent", "intent": "review", "enabled": true}}}}
	var draft struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans/"+pl.ID+"/versions", map[string]any{"note": "", "document": doc}, &draft)
	e.Admin.MustDo(200, "POST", "/api/v1/routing-plans/"+pl.ID+"/versions/"+draft.ID+"/publish", nil, nil)
	tok := e.Token(e.Binding("b", pl.ID, apptest.BindingOpts{}))
	for _, text := range []string{"There is a panic and a stack trace when the tests run, please fix this bug", strings.Repeat("x ", 3000) + " fix the bug"} {
		_ = apptest.ReadAll(e.Gateway("POST", "/v1/messages", tok, userMsg(text)))
	}
	e.Flush()
	q, _ := e.App.DB.R.Query(`SELECT intent, intent_source FROM requests ORDER BY id`)
	defer q.Close()
	var got []string
	for q.Next() {
		var i, s string
		_ = q.Scan(&i, &s)
		got = append(got, i+"/"+s)
	}
	if strings.Join(got, ",") != "debugging/classifier,review/rule" {
		t.Fatalf("intents %v (rules must outrank the classifier)", got)
	}
	var st struct {
		Groups []struct {
			Key string `json:"key"`
		} `json:"groups"`
		Totals struct {
			ClassifierRuns int `json:"classifier_runs"`
		} `json:"totals"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/stats?by=intent", nil, &st)
	if len(st.Groups) != 2 || st.Totals.ClassifierRuns != 2 {
		t.Fatalf("stats %+v", st)
	}
	// List rows carry the intent and where it came from.
	var list struct {
		Items []struct {
			Intent       string         `json:"intent"`
			IntentSource string         `json:"intent_source"`
			IntentDetail map[string]any `json:"intent_detail"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests?limit=10", nil, &list)
	sources := map[string]int{}
	for _, it := range list.Items {
		if it.Intent == "" {
			t.Fatalf("list row without intent: %+v", list.Items)
		}
		sources[it.IntentSource]++
		if it.IntentSource == "classifier" && it.IntentDetail["version"] == nil {
			t.Fatalf("classifier row without detail: %+v", it)
		}
	}
	if sources["rule"] == 0 || sources["classifier"] == 0 {
		t.Fatalf("intent sources in list %v", sources)
	}
}

func TestModelIntentClassifierSeesMaskedContent(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("a", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	var pp struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": "mask", "spec": map[string]any{"required": true}}, &pp)
	var pl struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans", map[string]any{"name": "intents", "spec": map[string]any{"display_name": "i", "description": ""}}, &pl)
	doc := map[string]any{"schema_version": 1, "model_mappings": []any{}, "default_alias_id": alias,
		"intent":       map[string]any{"mode": "model", "alias_id": alias, "send_content": true, "labels": []string{"testing", "debugging"}},
		"privacy":      map[string]any{"policy_id": pp.ID},
		"restrictions": map[string]any{"allowed_provider_ids": []string{}, "denied_provider_ids": []string{}, "allowed_billing": []string{}, "allowed_classifications": []string{}, "required_capabilities": []string{}},
		"fallback":     map[string]any{"steps": []string{"alternate_account", "stop"}, "max_attempts": 2, "allow_paid_fallback": false, "wait_max_seconds": 0}}
	var draft struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans/"+pl.ID+"/versions", map[string]any{"note": "", "document": doc}, &draft)
	e.Admin.MustDo(200, "POST", "/api/v1/routing-plans/"+pl.ID+"/versions/"+draft.ID+"/publish", nil, nil)
	tok := e.Token(e.Binding("b", pl.ID, apptest.BindingOpts{}))
	up.Push(apptest.Reply{Text: "Testing.", InputTok: 30, OutputTok: 2}, apptest.Reply{Text: "done"})
	_ = apptest.ReadAll(e.Gateway("POST", "/v1/messages", tok, userMsg("write tests for token "+fakeSecret)))
	if up.Count() != 2 {
		t.Fatalf("upstream calls %d", up.Count())
	}
	first := up.Requests[0].Raw
	if strings.Contains(first, fakeSecret) || !strings.Contains(first, "HPSEC_") {
		t.Fatalf("classifier received unmasked content: %s", first)
	}
	e.Flush()
	if got := e.QueryString(`SELECT intent || '/' || intent_source FROM requests`); got != "testing/classifier" {
		t.Fatalf("intent %s", got)
	}
	if n := e.QueryInt(`SELECT COUNT(*) FROM ledger WHERE idempotency_key LIKE 'classifier:%' AND input_tokens = 30`); n != 1 {
		t.Fatal("classifier usage not accounted separately")
	}
}

type refUsageGroup struct {
	Key      string `json:"key"`
	Attempts int64  `json:"attempts"`
	Tokens   struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
	} `json:"tokens"`
	Costs []struct {
		Kind   string `json:"kind"`
		Amount string `json:"amount"`
	} `json:"costs"`
	Allocated []struct {
		Kind   string `json:"kind"`
		Amount string `json:"amount"`
	} `json:"allocated"`
}

type refUsageSummary struct {
	GroupBy string `json:"group_by"`
	Filters *struct {
		Refs      map[string]string `json:"refs"`
		BindingID string            `json:"binding_id"`
	} `json:"filters"`
	Groups     []refUsageGroup `json:"groups"`
	NextCursor string          `json:"next_cursor"`
	Totals     []struct {
		Kind   string `json:"kind"`
		Amount string `json:"amount"`
	} `json:"totals"`
	Fees []struct {
		Prorated string `json:"prorated"`
	} `json:"subscription_fees"`
}

func decimal(t *testing.T, s string) float64 {
	t.Helper()
	a, err := money.ParseDecimal(s)
	if err != nil {
		t.Fatalf("amount %q: %v", s, err)
	}
	return float64(a) / 1e10
}

// D-041: two SpaceMan runs under one binding and one project get separate
// usage totals through the trusted-ref filter and group-by; header claims
// never count.
func TestSpaceManPerRunUsageByTrustedRef(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &[2]string{"1", "2"}})
	acc := e.Account("a", prov, apptest.AccountOpts{Billing: "subscription"})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias})
	var ws, proj struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/workspaces", map[string]any{"name": "w", "spec": map[string]any{}}, &ws)
	e.Admin.MustDo(201, "POST", "/api/v1/projects", map[string]any{"name": "proj", "spec": map[string]any{"workspace_id": ws.ID}}, &proj)
	e.Admin.MustDo(201, "POST", "/api/v1/subscription-entitlements", map[string]any{"name": "sub", "spec": map[string]any{"account_id": acc,
		"plan_name": "Team", "fee": "30", "currency": "USD", "period": "monthly", "starts_on": "2020-01-01"}}, nil)
	binding := e.Binding("spaceman-runs", plan, apptest.BindingOpts{ProjectID: proj.ID, ExternalRefs: map[string]string{"spaceman.fleet": "fleet_1", "spaceman.note": ""}})
	parent := e.Token(binding, "gateway:invoke", "gateway:models", "tokens:mint")
	laptop := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))

	mint := func(run string) string {
		r := e.Gateway("POST", "/v1/harbor/credentials", parent, map[string]any{"name": "run " + run, "scopes": []string{"gateway:invoke"}, "ttl_seconds": 3600,
			"bound_refs": map[string]string{"spaceman.space": "spc_alpha", "spaceman.run": run, "spaceman.instance": "ins_" + run, "session": run,
				"project_id": proj.ID}})
		var out struct {
			Token string `json:"token"`
		}
		body := apptest.ReadAll(r)
		if r.StatusCode != 201 || json.Unmarshal([]byte(body), &out) != nil || out.Token == "" {
			t.Fatalf("mint %s: %d %s", run, r.StatusCode, body)
		}
		return out.Token
	}
	runA, runB := mint("run_a"), mint("run_b")
	up.Push(apptest.Reply{Text: "a1", InputTok: 100, OutputTok: 10}, apptest.Reply{Text: "a2", InputTok: 200, OutputTok: 20},
		apptest.Reply{Text: "b1", InputTok: 1000, OutputTok: 100}, apptest.Reply{Text: "l1", InputTok: 50, OutputTok: 5})
	// The standalone client claims run_a through headers: never attribution.
	for _, c := range []struct{ tok, prompt string }{{runA, "a1"}, {runA, "a2"}, {runB, "b1"}, {laptop, "l1"}} {
		r := e.Gateway("POST", "/v1/messages", c.tok, userMsg(c.prompt), "X-HP-Session", "run_a", "X-HP-External-Ref", "spaceman.run=run_a")
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%s: %d %s", c.prompt, r.StatusCode, b)
		}
	}
	e.Flush()

	cost := func(g refUsageGroup) float64 {
		for _, c := range g.Costs {
			if c.Kind == "list_equivalent" {
				return decimal(t, c.Amount)
			}
		}
		t.Fatalf("group %q has no list_equivalent cost: %+v", g.Key, g.Costs)
		return 0
	}

	// Per-run totals inside the space.
	var s refUsageSummary
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?ref=spaceman.space:spc_alpha&group_by=ref:spaceman.run", nil, &s)
	if s.GroupBy != "ref:spaceman.run" || s.Filters == nil || s.Filters.Refs["spaceman.space"] != "spc_alpha" || len(s.Groups) != 2 {
		t.Fatalf("per-run summary %+v", s)
	}
	a, b := s.Groups[0], s.Groups[1]
	if a.Key != "run_a" || a.Attempts != 2 || a.Tokens.Input != 300 || a.Tokens.Output != 30 {
		t.Fatalf("run_a %+v", a)
	}
	if b.Key != "run_b" || b.Attempts != 1 || b.Tokens.Input != 1000 || b.Tokens.Output != 100 {
		t.Fatalf("run_b %+v", b)
	}
	if ca, cb := cost(a), cost(b); math.Abs(ca-0.00036) > 1e-9 || math.Abs(cb-0.0012) > 1e-9 {
		t.Fatalf("run costs %v %v", ca, cb)
	}
	var spaceTotal float64
	for _, x := range s.Totals {
		if x.Kind == "list_equivalent" {
			spaceTotal = decimal(t, x.Amount)
		}
	}
	if math.Abs(spaceTotal-(cost(a)+cost(b))) > 1e-9 {
		t.Fatalf("space total %v != run sum", spaceTotal)
	}
	// Each run's allocation is its share of the account's tokens (the
	// laptop's included in the denominator), not the whole fee.
	if len(s.Fees) != 1 || len(a.Allocated) != 1 || len(b.Allocated) != 1 {
		t.Fatalf("fees %+v allocations %+v %+v", s.Fees, a.Allocated, b.Allocated)
	}
	pr := decimal(t, s.Fees[0].Prorated)
	accountTokens := float64(330 + 1100 + 55)
	if got, want := decimal(t, a.Allocated[0].Amount), pr*330/accountTokens; math.Abs(got-want) > 1e-6 {
		t.Fatalf("run_a allocation %v, want %v", got, want)
	}
	if got, want := decimal(t, b.Allocated[0].Amount), pr*1100/accountTokens; math.Abs(got-want) > 1e-6 {
		t.Fatalf("run_b allocation %v, want %v", got, want)
	}

	// The split comes from the ref, not the project: one project group.
	var byProject refUsageSummary
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?ref=spaceman.space:spc_alpha&group_by=project", nil, &byProject)
	if len(byProject.Groups) != 1 || byProject.Groups[0].Key != proj.ID || byProject.Groups[0].Attempts != 3 {
		t.Fatalf("by project %+v", byProject.Groups)
	}
	// One run's total by model; binding_id combines with ref.
	var one refUsageSummary
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?ref=spaceman.run:run_a&binding_id="+binding+"&group_by=model", nil, &one)
	if len(one.Groups) != 1 || one.Groups[0].Key != "m" || one.Groups[0].Attempts != 2 || one.Groups[0].Tokens.Input != 300 ||
		one.Filters == nil || one.Filters.BindingID != binding {
		t.Fatalf("run_a by model %+v", one)
	}
	var none refUsageSummary
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?ref=spaceman.run:run_a&ref=spaceman.run:run_b", nil, &none)
	if len(none.Groups) != 0 {
		t.Fatalf("contradicting refs matched %+v", none.Groups)
	}
	// Rows without the key form the "" group; groups page by key with
	// whole-range totals on every page.
	var p1, p2 refUsageSummary
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?group_by=ref:spaceman.run&limit=2", nil, &p1)
	if len(p1.Groups) != 2 || p1.Groups[0].Key != "" || p1.Groups[0].Attempts != 1 || p1.Groups[1].Key != "run_a" || p1.NextCursor == "" {
		t.Fatalf("page 1 %+v", p1)
	}
	e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?group_by=ref:spaceman.run&limit=2&cursor="+url.QueryEscape(p1.NextCursor), nil, &p2)
	if len(p2.Groups) != 1 || p2.Groups[0].Key != "run_b" || p2.NextCursor != "" {
		t.Fatalf("page 2 %+v", p2)
	}
	if !reflect.DeepEqual(p1.Totals, p2.Totals) || len(p1.Totals) == 0 {
		t.Fatalf("totals differ across pages: %+v %+v", p1.Totals, p2.Totals)
	}
	// A group cursor only pages the query that produced it.
	for name, q := range map[string]string{
		"group_by": "group_by=ref:spaceman.instance&limit=2",
		"filter":   "group_by=ref:spaceman.run&limit=2&ref=spaceman.space:spc_alpha",
		"binding":  "group_by=ref:spaceman.run&limit=2&binding_id=" + binding,
		"range":    "group_by=ref:spaceman.run&limit=2&from=" + url.QueryEscape(time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339)),
	} {
		code, body, _ := e.Admin.GetRaw("/api/v1/usage/summary?" + q + "&cursor=" + url.QueryEscape(p1.NextCursor))
		if code != 422 || !strings.Contains(string(body), "query.cursor") {
			t.Fatalf("cursor reused with another %s: %d %s", name, code, body)
		}
	}
	// The binding's empty ref value is not indexed, so it doesn't join the
	// "" group of rows without the key: here that is every row.
	if n := e.QueryInt(`SELECT (SELECT count(*) FROM ledger_refs WHERE key = 'spaceman.note') + (SELECT count(*) FROM request_refs WHERE key = 'spaceman.note')`); n != 0 {
		t.Fatalf("empty ref values indexed: %d rows", n)
	}
	if n := e.QueryInt(`SELECT count(*) FROM request_refs WHERE key = 'spaceman.fleet'`); n != 3 {
		t.Fatalf("fleet ref rows %d, want 3", n)
	}

	// Activity: run_a's requests, paged.
	var reqs struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
		Total      int64  `json:"total"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests?ref=spaceman.run:run_a&limit=1", nil, &reqs)
	if reqs.Total != 2 || len(reqs.Items) != 1 || reqs.NextCursor == "" {
		t.Fatalf("run_a requests %+v", reqs)
	}
	first, cursor := reqs.Items[0].ID, reqs.NextCursor
	reqs.Items, reqs.NextCursor, reqs.Total = nil, "", 0
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests?ref=spaceman.run:run_a&limit=1&cursor="+url.QueryEscape(cursor), nil, &reqs)
	if reqs.Total != 2 || len(reqs.Items) != 1 || reqs.Items[0].ID == first || reqs.NextCursor != "" {
		t.Fatalf("run_a requests page 2 %+v", reqs)
	}
	// The external session value finds the internal session id.
	var sess struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/sessions?ref=session:run_a", nil, &sess)
	want := e.QueryString(`SELECT session_id FROM requests WHERE trusted_refs LIKE '%"run_a"%' LIMIT 1`)
	if len(sess.Items) != 1 || sess.Items[0].ID != want || !strings.HasPrefix(want, "ses_") {
		t.Fatalf("sessions for run_a %+v (want %s)", sess.Items, want)
	}
}

func TestTrustedRefFilterValidation(t *testing.T) {
	e := apptest.New(t)
	long := strings.Repeat("v", 257)
	for _, tt := range []struct {
		name, path, loc string
	}{
		{"no colon", "/api/v1/usage/summary?ref=nocolon", "query.ref"},
		{"empty key", "/api/v1/usage/summary?ref=:v", "query.ref"},
		{"bad key", "/api/v1/usage/summary?ref=Bad.Key:v", "query.ref"},
		{"empty value", "/api/v1/activity/requests?ref=spaceman.run:", "query.ref"},
		{"long value", "/api/v1/activity/requests?ref=spaceman.run:" + long, "query.ref"},
		{"too many", "/api/v1/activity/sessions?ref=a:1&ref=b:2&ref=c:3&ref=d:4&ref=e:5", "query.ref"},
		{"empty group key", "/api/v1/usage/summary?group_by=ref:", "query.group_by"},
		{"bad group key", "/api/v1/usage/summary?group_by=ref:BAD", "query.group_by"},
		{"unknown group", "/api/v1/usage/summary?group_by=refx", "query.group_by"},
		{"bad cursor", "/api/v1/usage/summary?group_by=ref:spaceman.run&cursor=" + api.EncodeCursor("x"), "query.cursor"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, body, _ := e.Admin.GetRaw(tt.path)
			if code != 422 || !strings.Contains(string(body), `"code":"validation_failed"`) || !strings.Contains(string(body), tt.loc) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	// Values may contain colons and commas.
	for _, path := range []string{"/api/v1/usage/summary?ref=" + url.QueryEscape("git.repo:https://x.example/a,b") + "&group_by=ref:git.repo",
		"/api/v1/activity/requests?ref=" + url.QueryEscape("git.repo:https://x.example/a,b")} {
		if code, body, _ := e.Admin.GetRaw(path); code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
	}
}

// The demo's spaceman-runs client supports the orchestrator flow: each run
// token binds its own spaceman.space and a demo project, while the fleet
// reference configured on the client cannot be overridden.
func TestDemoSpaceManRunsBindingAcceptsPerRunRefs(t *testing.T) {
	e := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.Demo.Enabled = true }))
	ctx := context.Background()
	binding, err := e.App.CP.Get(ctx, cp.BindingDef, "spaceman-runs")
	if err != nil {
		t.Fatal(err)
	}
	data, err := e.App.CP.Get(ctx, cp.ProjectDef, "data-pipeline")
	if err != nil {
		t.Fatal(err)
	}
	parent := e.Token(binding.ID, "gateway:invoke", "gateway:models", "tokens:mint")
	tests := []struct {
		name string
		refs map[string]string
		want int
	}{
		{"per-run space and project", map[string]string{"spaceman.space": "spc_run_a", "spaceman.run": "run_a", "project_id": data.ID}, 201},
		{"another space", map[string]string{"spaceman.space": "spc_run_b", "spaceman.run": "run_b"}, 201},
		{"same fleet", map[string]string{"spaceman.fleet": "fleet_synthetic", "spaceman.run": "run_c"}, 201},
		{"overrides the fleet", map[string]string{"spaceman.fleet": "fleet_other", "spaceman.run": "run_d"}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.Gateway("POST", "/v1/harbor/credentials", parent, map[string]any{"name": "run", "scopes": []string{"gateway:invoke"}, "ttl_seconds": 600,
				"bound_refs": tt.refs})
			body := apptest.ReadAll(r)
			if r.StatusCode != tt.want {
				t.Fatalf("mint: %d %s", r.StatusCode, body)
			}
			if tt.want != 201 {
				return
			}
			var out struct {
				Credential struct {
					ParentID  string            `json:"parent_id"`
					CreatedBy string            `json:"created_by"`
					BoundRefs map[string]string `json:"bound_refs"`
				} `json:"credential"`
			}
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
			for k, v := range tt.refs {
				if out.Credential.BoundRefs[k] != v {
					t.Errorf("bound_refs[%s] = %q, want %q", k, out.Credential.BoundRefs[k], v)
				}
			}
			if out.Credential.CreatedBy == "anonymous" || out.Credential.CreatedBy != out.Credential.ParentID {
				t.Errorf("created_by = %q, want the parent %q", out.Credential.CreatedBy, out.Credential.ParentID)
			}
		})
	}
}
