package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
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
