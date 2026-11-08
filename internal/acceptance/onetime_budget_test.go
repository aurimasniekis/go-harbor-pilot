package acceptance_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
)

// budgetRig is a metered account priced at 1 USD per million input tokens
// and 10000 USD per million output tokens, so a reply of n million input
// tokens costs n USD and a request's reservation estimate is about 0.16 USD
// (16 max output tokens).
type budgetRig struct {
	e         *apptest.Env
	clk       *clock.Fake
	up        *apptest.Upstream
	plan      string
	ws        string
	proj      string
	other     string
	bind      string
	otherBind string
}

func newBudgetRig(t *testing.T, start time.Time) *budgetRig {
	t.Helper()
	clk := clock.NewFake(start)
	e := apptest.New(t, apptest.WithClock(clk))
	up := apptest.NewUpstream(t, "anthropic")
	price := [2]string{"1", "10000"}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price})
	acc := e.Account("api", prov, apptest.AccountOpts{Billing: "metered_api"})
	r := &budgetRig{e: e, clk: clk, up: up}
	r.plan = e.Plan("default", apptest.PlanOpts{DefaultAlias: e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))})
	r.ws = r.create("/api/v1/workspaces", "ws", map[string]any{})
	r.proj = r.create("/api/v1/projects", "checkout", map[string]any{"workspace_id": r.ws})
	r.other = r.create("/api/v1/projects", "billing", map[string]any{"workspace_id": r.ws})
	r.bind = e.Binding("laptop", r.plan, apptest.BindingOpts{ProjectID: r.proj})
	r.otherBind = e.Binding("ci", r.plan, apptest.BindingOpts{ProjectID: r.other})
	return r
}

func (r *budgetRig) create(path, name string, spec map[string]any) string {
	r.e.T.Helper()
	var out struct {
		ID string `json:"id"`
	}
	r.e.Admin.MustDo(201, "POST", path, map[string]any{"name": name, "spec": spec}, &out)
	return out.ID
}

func (r *budgetRig) budget(name string, over map[string]any) string {
	r.e.T.Helper()
	spec := map[string]any{"scope": "project", "scope_id": r.proj, "period": "once", "limit": "10", "currency": "USD", "enforcement": "block",
		"warn_at": 0.8, "enabled": true}
	for k, v := range over {
		spec[k] = v
	}
	return r.create("/api/v1/budgets", name, spec)
}

// advance moves the clock and signs in again (sessions time out).
func (r *budgetRig) advance(to time.Time) {
	r.clk.Set(to)
	r.e.Admin = r.e.SignIn()
}

// request sends one message as binding and answers it with millions of
// input tokens; maxTokens sets the reservation estimate (0.01 USD each).
func (r *budgetRig) request(bind string, millions float64, maxTokens int) (int, string) {
	r.e.T.Helper()
	r.up.Push(apptest.Reply{Text: "ok", InputTok: int64(millions * 1e6), OutputTok: 0})
	body := map[string]any{"model": "", "max_tokens": maxTokens, "messages": []any{map[string]any{"role": "user", "content": "x"}}}
	resp := r.e.Gateway("POST", "/v1/messages", r.e.Token(bind), body)
	out := apptest.ReadAll(resp)
	r.e.Flush()
	return resp.StatusCode, out
}

// refused sends a request that a budget must refuse before it reaches the
// upstream (no reply is queued) and returns the response body.
func (r *budgetRig) refused(bind string, maxTokens int) string {
	r.e.T.Helper()
	served := r.up.Count()
	body := map[string]any{"model": "", "max_tokens": maxTokens, "messages": []any{map[string]any{"role": "user", "content": "x"}}}
	resp := r.e.Gateway("POST", "/v1/messages", r.e.Token(bind), body)
	out := apptest.ReadAll(resp)
	r.e.Flush()
	if resp.StatusCode != 429 || r.up.Count() != served || !strings.Contains(out, "exhausted") {
		r.e.T.Fatalf("request not refused by a budget: %d %s", resp.StatusCode, out)
	}
	return out
}

func (r *budgetRig) spend(bind string, millions float64) {
	r.e.T.Helper()
	if code, body := r.request(bind, millions, 16); code != 200 {
		r.e.T.Fatalf("spend %v: %d %s", millions, code, body)
	}
}

type statusItem struct {
	BudgetID    string  `json:"budget_id"`
	Name        string  `json:"name"`
	Period      string  `json:"period"`
	PeriodStart string  `json:"period_start"`
	PeriodEnd   *string `json:"period_end"`
	Spent       int64   `json:"spent_e10"`
	Remaining   int64   `json:"remaining_e10"`
	Projected   *int64  `json:"projected_spend_e10"`
	State       string  `json:"state"`
}

func (r *budgetRig) status(id string) statusItem {
	r.e.T.Helper()
	var st struct {
		Items []statusItem `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/budgets/status", nil, &st)
	for _, it := range st.Items {
		if it.BudgetID == id {
			return it
		}
	}
	r.e.T.Fatalf("budget %s not in status", id)
	return statusItem{}
}

func (r *budgetRig) events(action string) int64 {
	return r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = ?`, "harborpilot.budget.threshold_"+action+".v1")
}

func (r *budgetRig) mint(bind string) (int, string) {
	r.e.T.Helper()
	parent := r.e.Token(bind, "gateway:invoke", "tokens:mint")
	resp := r.e.Gateway("POST", "/v1/harbor/credentials", parent, map[string]any{"name": "run", "scopes": []string{"gateway:invoke"}, "check_budget": true})
	return resp.StatusCode, apptest.ReadAll(resp)
}

func (r *budgetRig) etag(id string) string {
	r.e.T.Helper()
	var o struct {
		Revision int64 `json:"revision"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/budgets/"+id, nil, &o)
	return fmt.Sprintf(`"r%d"`, o.Revision)
}

const e10 = 10_000_000_000

// A one-time budget counts spend across calendar months and never resets:
// once used up it stays used up, and its threshold events fire once (D-044).
func TestOneTimeBudgetNeverResets(t *testing.T) {
	r := newBudgetRig(t, time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC))
	id := r.budget("trial-credit", nil)
	control := r.budget("ci-monthly", map[string]any{"period": "month", "scope_id": r.other})

	r.spend(r.bind, 4)
	r.spend(r.otherBind, 1)
	r.advance(time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC))
	if c := r.status(control); c.Spent != 0 {
		t.Fatalf("control budget in February: %+v", c)
	}
	r.spend(r.bind, 4)
	r.advance(time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC))
	r.spend(r.bind, 1.9)

	st := r.status(id)
	if st.Period != "once" || st.Spent != 99*e10/10 || st.State != "warning" || st.PeriodEnd != nil || st.Projected != nil ||
		st.Remaining != e10/10 || st.PeriodStart != "2026-01-15T10:00:00Z" {
		t.Fatalf("status after 9.90: %+v", st)
	}
	// The estimate (0.16) would take it past 10: refused.
	if body := r.refused(r.bind, 16); !strings.Contains(body, "in total") {
		t.Fatalf("over-budget request: %s", body)
	}
	r.e.Eventually(func() bool { return r.events("exceeded") == 1 }, "exceeded event")
	if n := r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.access.request_blocked.v1'`); n != 1 {
		t.Fatalf("blocked events %d", n)
	}
	// A small request fits and uses up the rest.
	if code, body := r.request(r.bind, 0.1, 1); code != 200 {
		t.Fatalf("last request: %d %s", code, body)
	}
	for _, month := range []time.Time{time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)} {
		r.advance(month)
		r.refused(r.bind, 1)
		if code, body := r.mint(r.bind); code != 429 || !strings.Contains(body, "budget_exceeded") {
			t.Fatalf("%s: mint %d %s", month.Month(), code, body)
		}
		if st := r.status(id); st.State != "exceeded" || st.Spent != 10*e10 || st.Remaining != 0 {
			t.Fatalf("%s: status %+v", month.Month(), st)
		}
		if c := r.status(control); c.Spent != 0 || c.State != "ok" {
			t.Fatalf("%s: control %+v", month.Month(), c)
		}
	}
	if w, x := r.events("warning"), r.events("exceeded"); w != 1 || x != 1 {
		t.Fatalf("events: %d warning, %d exceeded; want 1 each", w, x)
	}
}

// Raising the limit unblocks at once and re-arms threshold events; a reset
// starts counting again from now (D-044).
func TestOneTimeBudgetTopUpAndReset(t *testing.T) {
	r := newBudgetRig(t, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	id := r.budget("trial-credit", nil)
	r.spend(r.bind, 10)
	r.refused(r.bind, 1)
	r.e.Eventually(func() bool { return r.events("exceeded") == 1 }, "first exceeded event")

	stale := `"r999"`
	spec := map[string]any{"scope": "project", "scope_id": r.proj, "period": "once", "limit": "20", "currency": "USD", "enforcement": "block", "warn_at": 0.8, "enabled": true}
	r.e.Admin.MustDo(412, "PUT", "/api/v1/budgets/"+id, map[string]any{"name": "trial-credit", "spec": spec}, nil, "If-Match", stale)
	r.e.Admin.MustDo(200, "PUT", "/api/v1/budgets/"+id, map[string]any{"name": "trial-credit", "spec": spec}, nil, "If-Match", r.etag(id))
	r.spend(r.bind, 6) // reserves 10.16 of 20: no warning
	if n := r.events("warning"); n != 0 {
		t.Fatalf("warning events at 10.16 of 20: %d", n)
	}
	r.spend(r.bind, 4) // reserves 16.16 of 20: warning
	if n := r.events("warning"); n != 1 {
		t.Fatalf("warning events after crossing 16: %d", n)
	}
	r.refused(r.bind, 1)
	r.e.Eventually(func() bool { return r.events("exceeded") == 2 }, "re-armed exceeded event")

	// Start over.
	r.advance(time.Date(2026, 3, 9, 8, 30, 15, 0, time.UTC))
	r.e.Admin.MustDo(412, "POST", "/api/v1/budgets/"+id+"/reset", nil, nil, "If-Match", stale)
	revs := r.e.QueryInt(`SELECT count(*) FROM resource_history WHERE resource_id = ?`, id)
	cfgEvents := r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.config.budget_updated.v1'`)
	var reset struct {
		Spec struct {
			StartsAt string `json:"starts_at"`
		} `json:"spec"`
	}
	code := r.e.Admin.Do("POST", "/api/v1/budgets/"+id+"/reset", nil, &reset, "If-Match", r.etag(id))
	if code != 200 || reset.Spec.StartsAt != "2026-03-09T08:30:15Z" {
		t.Fatalf("reset: %d %+v", code, reset)
	}
	if st := r.status(id); st.Spent != 0 || st.State != "ok" || st.PeriodStart != "2026-03-09T08:30:15Z" {
		t.Fatalf("status after reset: %+v", st)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM resource_history WHERE resource_id = ?`, id); n != revs+1 {
		t.Fatalf("history entries %d, want %d", n, revs+1)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM config_revisions WHERE summary = 'reset budget trial-credit'`); n != 1 {
		t.Fatalf("config revisions with the reset summary: %d", n)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'budget.update' AND target_id = ?`, id); n < 1 {
		t.Fatalf("audit entries %d", n)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.config.budget_updated.v1'`); n != cfgEvents+1 {
		t.Fatalf("config events %d, want %d", n, cfgEvents+1)
	}
	// Earlier spend is still in usage reports.
	var sum struct {
		Groups []struct {
			Costs []struct {
				Amount string `json:"amount"`
			} `json:"costs"`
		} `json:"groups"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/usage/summary?group_by=project&from=2026-03-01T00:00:00Z", nil, &sum)
	if len(sum.Groups) != 1 || len(sum.Groups[0].Costs) != 1 || !strings.HasPrefix(sum.Groups[0].Costs[0].Amount, "20") {
		t.Fatalf("usage summary after reset %+v", sum)
	}
	// The new window warns again.
	w := r.events("warning")
	r.spend(r.bind, 16)
	r.spend(r.bind, 0)
	if r.events("warning") != w+1 {
		t.Fatalf("warning did not fire in the new window")
	}

	monthly := r.budget("monthly", map[string]any{"period": "month"})
	var errBody struct {
		Error struct {
			Details struct {
				Fields []struct {
					Location string `json:"location"`
				} `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	if code := r.e.Admin.Do("POST", "/api/v1/budgets/"+monthly+"/reset", nil, &errBody); code != 422 ||
		len(errBody.Error.Details.Fields) != 1 || errBody.Error.Details.Fields[0].Location != "body.spec.period" {
		t.Fatalf("reset of a monthly budget: %d %+v", code, errBody)
	}
	ended := r.budget("ended", map[string]any{"starts_at": "2026-01-01T00:00:00Z", "ends_at": "2026-02-01T00:00:00Z"})
	if code := r.e.Admin.Do("POST", "/api/v1/budgets/"+ended+"/reset", nil, nil); code != 422 {
		t.Fatalf("reset of an ended budget: %d", code)
	}
	r.e.Admin.MustDo(404, "POST", "/api/v1/budgets/bdg_missing/reset", nil, nil)
}

// The window: a backdated start counts earlier spend, a future start makes
// the budget scheduled and an end in the past expired; neither blocks a
// request or a mint (D-044).
func TestOneTimeBudgetWindow(t *testing.T) {
	r := newBudgetRig(t, time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))
	r.spend(r.bind, 5)
	r.advance(time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC))

	backdated := r.budget("backdated", map[string]any{"starts_at": "2026-05-01T00:00:00Z", "enforcement": "warn", "limit": "100"})
	fromNow := r.budget("from-now", map[string]any{"enforcement": "warn", "limit": "100"})
	if st := r.status(backdated); st.Spent != 5*e10 || st.State != "ok" {
		t.Fatalf("backdated %+v", st)
	}
	if st := r.status(fromNow); st.Spent != 0 {
		t.Fatalf("from creation %+v", st)
	}

	scheduled := r.budget("scheduled", map[string]any{"starts_at": "2026-06-01T00:00:00+02:00", "limit": "0.01"})
	if st := r.status(scheduled); st.State != "scheduled" || st.PeriodStart != "2026-05-31T22:00:00Z" {
		t.Fatalf("scheduled %+v", st)
	}
	if code, body := r.request(r.bind, 1, 16); code != 200 {
		t.Fatalf("scheduled budget blocked a request: %d %s", code, body)
	}
	if code, body := r.mint(r.bind); code != 201 {
		t.Fatalf("scheduled budget blocked a mint: %d %s", code, body)
	}
	r.e.Admin.MustDo(204, "DELETE", "/api/v1/budgets/"+scheduled, nil, nil)

	events := r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type LIKE 'harborpilot.budget.threshold_%'`)
	expired := r.budget("expired", map[string]any{"starts_at": "2026-05-01T00:00:00Z", "ends_at": "2026-05-15T00:00:00Z", "limit": "1"})
	st := r.status(expired)
	if st.State != "expired" || st.Spent != 5*e10 || st.PeriodEnd == nil || *st.PeriodEnd != "2026-05-15T00:00:00Z" || st.Projected != nil {
		t.Fatalf("expired %+v", st)
	}
	if code, body := r.request(r.bind, 1, 16); code != 200 {
		t.Fatalf("expired budget blocked a request: %d %s", code, body)
	}
	if code, body := r.mint(r.bind); code != 201 {
		t.Fatalf("expired budget blocked a mint: %d %s", code, body)
	}
	if st := r.status(expired); st.Spent != 5*e10 {
		t.Fatalf("spend after ends_at counted: %+v", st)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type LIKE 'harborpilot.budget.threshold_%'`); n != events {
		t.Fatalf("expired budget emitted events: %d -> %d", events, n)
	}
	// Extending ends_at revives it: it now counts all 7 USD against 1.
	r.e.Admin.MustDo(200, "PUT", "/api/v1/budgets/"+expired, map[string]any{"name": "expired", "spec": map[string]any{"scope": "project", "scope_id": r.proj,
		"period": "once", "starts_at": "2026-05-01T00:00:00Z", "ends_at": "2026-06-01T00:00:00Z", "limit": "1", "currency": "USD", "enforcement": "block", "enabled": true}}, nil, "If-Match", r.etag(expired))
	if st := r.status(expired); st.State != "exceeded" || st.Spent != 7*e10 || st.Projected == nil {
		t.Fatalf("revived %+v", st)
	}
	r.refused(r.bind, 1)
	if code, _ := r.mint(r.bind); code != 429 {
		t.Fatalf("revived budget did not block a mint: %d", code)
	}
	// A time zone is rejected on a one-time budget.
	var errBody struct {
		Error struct {
			Details struct {
				Fields []struct {
					Location string `json:"location"`
				} `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	code := r.e.Admin.Do("POST", "/api/v1/budgets", map[string]any{"name": "tz", "spec": map[string]any{"scope": "global", "scope_id": "", "period": "once",
		"limit": "1", "currency": "USD", "timezone": "Europe/Vilnius", "enabled": true}}, &errBody)
	if code != 422 || len(errBody.Error.Details.Fields) != 1 || errBody.Error.Details.Fields[0].Location != "body.spec.timezone" {
		t.Fatalf("timezone on once: %d %+v", code, errBody)
	}
}

// GET /budgets/status?project_id= returns the budgets covering a project
// (and binding) by the request path's rule, whatever their state;
// scope+scope_id is an exact match.
func TestBudgetStatusFilters(t *testing.T) {
	r := newBudgetRig(t, time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))
	otherWS := r.create("/api/v1/workspaces", "elsewhere", map[string]any{})
	acc := r.e.QueryString(`SELECT id FROM resources WHERE kind = 'provider_account'`)
	mk := func(name, scope, scopeID string, over map[string]any) {
		o := map[string]any{"scope": scope, "scope_id": scopeID, "period": "month"}
		for k, v := range over {
			o[k] = v
		}
		r.budget(name, o)
	}
	mk("global", "global", "", nil)
	mk("ws", "workspace", r.ws, nil)
	mk("ws-other", "workspace", otherWS, nil)
	mk("proj", "project", r.proj, nil)
	mk("proj-ended", "project", r.proj, map[string]any{"period": "once", "starts_at": "2026-01-01T00:00:00Z", "ends_at": "2026-02-01T00:00:00Z"})
	mk("proj-off", "project", r.proj, map[string]any{"enabled": false})
	mk("proj-other", "project", r.other, nil)
	mk("bind", "binding", r.bind, nil)
	mk("bind-other", "binding", r.otherBind, nil)
	mk("acc", "account", acc, nil)

	get := func(q string) (int, []statusItem) {
		var st struct {
			Items []statusItem `json:"items"`
		}
		code := r.e.Admin.Do("GET", "/api/v1/budgets/status"+q, nil, &st)
		return code, st.Items
	}
	for _, tt := range []struct {
		name, query string
		code        int
		want        string
	}{
		{"everything", "", 200, "acc bind bind-other global proj proj-ended proj-off proj-other ws ws-other"},
		{"project", "?project_id=" + r.proj, 200, "global proj proj-ended proj-off ws"},
		{"project and binding", "?project_id=" + r.proj + "&binding_id=" + r.bind, 200, "bind global proj proj-ended proj-off ws"},
		{"other project", "?project_id=" + r.other, 200, "global proj-other ws"},
		{"exact account", "?scope=account&scope_id=" + acc, 200, "acc"},
		{"exact workspace", "?scope=workspace&scope_id=" + r.ws, 200, "ws"},
		{"exact global", "?scope=global", 200, "global"},
		{"both kinds of filter", "?project_id=" + r.proj + "&scope=global", 400, ""},
		{"binding without project", "?binding_id=" + r.bind, 400, ""},
		{"scope_id without scope", "?scope_id=" + r.ws, 400, ""},
		{"global with an id", "?scope=global&scope_id=" + r.ws, 400, ""},
		{"unknown scope", "?scope=team&scope_id=x", 422, ""},
		{"unknown project", "?project_id=prj_missing", 404, ""},
		{"unknown binding", "?project_id=" + r.proj + "&binding_id=cbd_missing", 404, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, items := get(tt.query)
			if code != tt.code {
				t.Fatalf("status %d, want %d", code, tt.code)
			}
			if code != 200 {
				return
			}
			names := []string{}
			for _, it := range items {
				names = append(names, it.Name)
				if it.Name == "proj-ended" && it.State != "expired" {
					t.Fatalf("proj-ended state %s", it.State)
				}
				if it.Name == "proj-off" && it.State != "disabled" {
					t.Fatalf("proj-off state %s", it.State)
				}
			}
			if got := strings.Join(names, " "); got != tt.want {
				t.Fatalf("budgets %q, want %q", got, tt.want)
			}
		})
	}
}

// GET /system answers a service-principal token with config.read and shows
// no data directory, key ids or secrets.
func TestSystemInfo(t *testing.T) {
	e := apptest.New(t)
	var sp struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/service-principals", map[string]any{"name": "spaceman", "permissions": []string{"config.read"}}, &sp)
	var tok struct {
		Plaintext string `json:"plaintext"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/service-principals/"+sp.ID+"/tokens", map[string]any{"name": "t"}, &tok)
	get := func(bearer string) (int, string) {
		req, _ := http.NewRequest("GET", e.Srv.URL+"/api/v1/system", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, apptest.ReadAll(resp)
	}
	code, body := get(tok.Plaintext)
	if code != 200 {
		t.Fatalf("service principal: %d %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out["version"] != "test" || out["started_at"] == "" || out["schema_version"] == nil {
		t.Fatalf("system %s", body)
	}
	if _, ok := out["commit"]; !ok {
		t.Fatalf("no commit field: %s", body)
	}
	if v, _ := out["schema_version"].(float64); int64(v) != e.QueryInt(`SELECT MAX(version) FROM schema_migrations`) {
		t.Fatalf("schema_version %v", out["schema_version"])
	}
	for _, leak := range []string{e.DataDir, e.App.Keyring.CurrentID(), "data_dir", "master_key"} {
		if strings.Contains(body, leak) {
			t.Fatalf("system info shows %q: %s", leak, body)
		}
	}
	if code, _ := get(""); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	e.Admin.MustDo(200, "GET", "/api/v1/system", nil, nil)
}
