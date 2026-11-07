package acceptance_test

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// A mint with check_budget is refused with 429 budget_exceeded when an
// enabled blocking budget covering the child's binding, project or workspace
// is used up (D-043). Without it, mints behave as before.
func TestMintCheckBudget(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	price := [2]string{"3", "15"}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price})
	acc := e.Account("api", prov, apptest.AccountOpts{Billing: "metered_api"})
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))})
	create := func(path string, spec map[string]any) string {
		var out struct {
			ID string `json:"id"`
		}
		e.Admin.MustDo(201, "POST", path, map[string]any{"name": "x" + strings.ReplaceAll(path, "/", "-") + "-" + e.QueryString(`SELECT lower(hex(randomblob(4)))`), "spec": spec}, &out)
		return out.ID
	}
	wsRun, wsHome := create("/api/v1/workspaces", map[string]any{}), create("/api/v1/workspaces", map[string]any{})
	proj := create("/api/v1/projects", map[string]any{"workspace_id": wsRun})
	home := create("/api/v1/projects", map[string]any{"workspace_id": wsHome})
	other := create("/api/v1/projects", map[string]any{"workspace_id": wsHome})
	runs := e.Binding("runs", plan, apptest.BindingOpts{ProjectID: home, AllowedProjects: []string{home, proj}})
	laptop := e.Binding("laptop", plan, apptest.BindingOpts{ProjectID: other})
	parent := e.Token(runs, "gateway:invoke", "tokens:mint")

	mint := func(body map[string]any) (int, string) {
		t.Helper()
		body["name"], body["scopes"] = "run", []string{"gateway:invoke"}
		r := e.Gateway("POST", "/v1/harbor/credentials", parent, body)
		return r.StatusCode, apptest.ReadAll(r)
	}
	// Spend 0.00105 USD in (runs, proj, wsRun) through a run token and in
	// (laptop, other, wsHome) through the laptop. A budget of 0.001 in either
	// scope is used up; one of 1 is not.
	code, body := mint(map[string]any{"bound_refs": map[string]string{"project_id": proj}})
	var minted struct {
		Token string `json:"token"`
	}
	if code != 201 || json.Unmarshal([]byte(body), &minted) != nil {
		t.Fatalf("mint: %d %s", code, body)
	}
	up.Push(apptest.Reply{Text: "ok", InputTok: 100, OutputTok: 50}, apptest.Reply{Text: "ok", InputTok: 100, OutputTok: 50})
	for _, tok := range []string{minted.Token, e.Token(laptop)} {
		if r := e.Gateway("POST", "/v1/messages", tok, msg("x")); r.StatusCode != 200 {
			t.Fatalf("spend: %d %s", r.StatusCode, apptest.ReadAll(r))
		}
	}
	e.Flush()

	withProject := map[string]string{"project_id": proj}
	for _, tt := range []struct {
		name   string
		budget map[string]any
		check  any // check_budget; nil leaves it out
		refs   map[string]string
		want   int
	}{
		{"default mint ignores budgets", map[string]any{"scope": "binding", "scope_id": runs}, nil, withProject, 201},
		{"check_budget false", map[string]any{"scope": "binding", "scope_id": runs}, false, withProject, 201},
		{"binding exhausted", map[string]any{"scope": "binding", "scope_id": runs}, true, withProject, 429},
		{"binding exhausted, no bound refs", map[string]any{"scope": "binding", "scope_id": runs}, true, nil, 429},
		{"binding not exhausted", map[string]any{"scope": "binding", "scope_id": runs, "limit": "1"}, true, withProject, 201},
		{"warn only", map[string]any{"scope": "binding", "scope_id": runs, "enforcement": "warn"}, true, withProject, 201},
		{"disabled", map[string]any{"scope": "binding", "scope_id": runs, "enabled": false}, true, withProject, 201},
		{"other binding", map[string]any{"scope": "binding", "scope_id": laptop}, true, withProject, 201},
		{"bound project", map[string]any{"scope": "project", "scope_id": proj}, true, withProject, 429},
		{"project not bound: the binding's own applies", map[string]any{"scope": "project", "scope_id": proj}, true, nil, 201},
		{"other project", map[string]any{"scope": "project", "scope_id": other}, true, withProject, 201},
		{"bound project's workspace", map[string]any{"scope": "workspace", "scope_id": wsRun}, true, withProject, 429},
		{"binding project's workspace", map[string]any{"scope": "workspace", "scope_id": wsHome}, true, nil, 429},
		{"workspace not in scope", map[string]any{"scope": "workspace", "scope_id": wsHome}, true, withProject, 201},
		{"global", map[string]any{"scope": "global", "scope_id": ""}, true, nil, 429},
		{"account budgets are not checked", map[string]any{"scope": "account", "scope_id": acc}, true, withProject, 201},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := map[string]any{"period": "day", "limit": "0.001", "currency": "USD", "enforcement": "block", "enabled": true}
			for k, v := range tt.budget {
				spec[k] = v
			}
			id := create("/api/v1/budgets", spec)
			defer e.Admin.MustDo(204, "DELETE", "/api/v1/budgets/"+id, nil, nil)
			issued := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'gateway_credential.issue'`)
			events := e.QueryInt(`SELECT count(*) FROM event_outbox`)
			req := map[string]any{}
			if tt.refs != nil {
				req["bound_refs"] = tt.refs
			}
			if tt.check != nil {
				req["check_budget"] = tt.check
			}
			code, body := mint(req)
			if code != tt.want {
				t.Fatalf("mint: %d %s", code, body)
			}
			if code != 429 {
				return
			}
			var out struct {
				Error struct {
					Code     string `json:"code"`
					Message  string `json:"message"`
					BudgetID string `json:"budget_id"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(body), &out); err != nil || out.Error.Code != "budget_exceeded" || out.Error.BudgetID != id ||
				!strings.Contains(out.Error.Message, id) || !strings.Contains(out.Error.Message, "exhausted") {
				t.Fatalf("refusal body %s", body)
			}
			// A refused mint writes nothing: no credential, audit entry or event.
			if n := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'gateway_credential.issue'`); n != issued {
				t.Fatalf("audit entries %d, want %d", n, issued)
			}
			if n := e.QueryInt(`SELECT count(*) FROM event_outbox`); n != events {
				t.Fatalf("events %d, want %d", n, events)
			}
		})
	}
	// Validation still comes first: an invalid mint is a 400, not a 429.
	id := create("/api/v1/budgets", map[string]any{"scope": "global", "scope_id": "", "period": "day", "limit": "0.001", "currency": "USD", "enabled": true})
	defer e.Admin.MustDo(204, "DELETE", "/api/v1/budgets/"+id, nil, nil)
	if code, body := mint(map[string]any{"check_budget": true, "bound_refs": map[string]string{"project_id": other}}); code != 400 {
		t.Fatalf("invalid mint with check_budget: %d %s", code, body)
	}
	if code, body := mint(map[string]any{"check_budget": "yes"}); code != 400 {
		t.Fatalf("non-boolean check_budget: %d %s", code, body)
	}
}

// A budget check that cannot read usage fails the mint with a generic 5xx
// (the gateway envelope maps internal errors to 502, as for a failed revoke).
// The cause is logged, never returned to the client.
func TestMintCheckBudgetReadFailure(t *testing.T) {
	logs := &syncBuffer{}
	e := apptest.New(t, apptest.WithLog(slog.New(slog.NewTextHandler(logs, nil))))
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("api", prov, apptest.AccountOpts{Billing: "metered_api"})
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))})
	parent := e.Token(e.Binding("runs", plan, apptest.BindingOpts{}), "gateway:invoke", "tokens:mint")
	e.Admin.MustDo(201, "POST", "/api/v1/budgets", map[string]any{"name": "global", "spec": map[string]any{"scope": "global", "scope_id": "",
		"period": "day", "limit": "1", "currency": "USD", "enforcement": "block", "enabled": true}}, nil)

	issued := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'gateway_credential.issue'`)
	// Make the budget's usage read fail.
	if _, err := e.App.DB.W.Exec(`ALTER TABLE ledger RENAME TO ledger_moved`); err != nil {
		t.Fatal(err)
	}
	r := e.Gateway("POST", "/v1/harbor/credentials", parent, map[string]any{"name": "run", "scopes": []string{"gateway:invoke"}, "check_budget": true})
	body := apptest.ReadAll(r)
	if _, err := e.App.DB.W.Exec(`ALTER TABLE ledger_moved RENAME TO ledger`); err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 502 {
		t.Fatalf("mint: %d %s", r.StatusCode, body)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Error.Message != "could not check budgets" {
		t.Fatalf("body %s", body)
	}
	for _, leak := range []string{"ledger", "no such table", "SQL", "sql"} {
		if strings.Contains(body, leak) {
			t.Fatalf("body leaks %q: %s", leak, body)
		}
	}
	if logged := logs.String(); !strings.Contains(logged, "mint.budget_check.failed") || !strings.Contains(logged, "ledger") {
		t.Fatalf("cause not logged:\n%s", logged)
	}
	if n := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'gateway_credential.issue'`); n != issued {
		t.Fatalf("a failed check issued a credential: %d audit entries, want %d", n, issued)
	}
}
