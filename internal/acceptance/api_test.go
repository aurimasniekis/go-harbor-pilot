package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
)

// Validation errors never echo submitted values: they may be secrets.
func TestValidationErrorsDoNotEchoValues(t *testing.T) {
	e := apptest.New(t)
	prov := e.Provider("p", "anthropic", "https://api.example.test")
	const marker = "sk-SECRET-MARKER-0042"
	tests := []struct {
		name string
		path string
		body map[string]any
	}{
		{"secret of the wrong type", "/api/v1/provider-accounts", map[string]any{"name": "a", "spec": map[string]any{"provider_id": prov, "billing": "metered_api",
			"enabled": true}, "secrets": map[string]any{"credential": []string{marker}}}},
		{"secret-looking enum value", "/api/v1/provider-accounts", map[string]any{"name": "b", "spec": map[string]any{"provider_id": prov, "billing": marker,
			"enabled": true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body json.RawMessage
			if code := e.Admin.Do("POST", tt.path, tt.body, &body); code != 422 {
				t.Fatalf("status %d: %s", code, body)
			}
			if strings.Contains(string(body), marker) {
				t.Fatalf("error body echoes the submitted value: %s", body)
			}
		})
	}
}

// Optional spec fields may be omitted; an omitted fallback gets the default
// steps while an explicit empty list means no fallback.
func TestOptionalFieldsAndFallbackDefault(t *testing.T) {
	e := apptest.New(t)
	e.Admin.MustDo(201, "POST", "/api/v1/workspaces", map[string]any{"name": "minimal", "spec": map[string]any{}}, nil)
	prov := e.Provider("p", "anthropic", "https://api.example.test", apptest.Model{ID: "m"})
	acc := e.Account("a", prov, apptest.AccountOpts{Secret: "sk-test"})
	alias := e.Alias("coding/default", e.Pool("pool", "ordered", apptest.Member{Account: acc, Model: "m"}))
	var plan struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans", map[string]any{"name": "minimal", "spec": map[string]any{}}, &plan)
	tests := []struct {
		name string
		doc  map[string]any
		want []string
	}{
		{"omitted fallback", map[string]any{"default_alias_id": alias}, []string{"alternate_account", "alternate_model", "stop"}},
		{"explicit empty steps", map[string]any{"default_alias_id": alias, "fallback": map[string]any{"steps": []string{}}}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v struct {
				Document struct {
					Fallback struct {
						Steps []string `json:"steps"`
					} `json:"fallback"`
				} `json:"document"`
			}
			e.Admin.MustDo(201, "POST", "/api/v1/routing-plans/"+plan.ID+"/versions", map[string]any{"document": tt.doc}, &v)
			if strings.Join(v.Document.Fallback.Steps, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("steps %v, want %v", v.Document.Fallback.Steps, tt.want)
			}
		})
	}
}

// Objects used by a published plan cannot be deleted (409 with the plan);
// publish and rollback honor If-Match on the published version; and one
// gateway credential can be read by id.
func TestPlanReferencesPreconditionsAndCredentials(t *testing.T) {
	e := apptest.New(t)
	prov := e.Provider("p", "anthropic", "https://api.example.test", apptest.Model{ID: "m"})
	acc := e.Account("a", prov, apptest.AccountOpts{Secret: "sk-test"})
	alias := e.Alias("coding/default", e.Pool("pool", "ordered", apptest.Member{Account: acc, Model: "m"}))
	planID := e.Plan("team", apptest.PlanOpts{DefaultAlias: alias})

	var conflict struct {
		Error struct {
			Details struct {
				ReferencedBy []string `json:"referenced_by"`
			} `json:"details"`
		} `json:"error"`
	}
	if code := e.Admin.Do("DELETE", "/api/v1/model-aliases/"+alias, nil, &conflict); code != 409 {
		t.Fatalf("delete alias used by a published plan: %d", code)
	}
	if strings.Join(conflict.Error.Details.ReferencedBy, ",") != "routing_plan team" {
		t.Fatalf("referenced_by %v", conflict.Error.Details.ReferencedBy)
	}

	type version struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	var list struct {
		Items []version `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/routing-plans/"+planID+"/versions", nil, &list)
	var published string
	for _, v := range list.Items {
		if v.Status == "published" {
			published = v.ID
		}
	}
	var draft version
	e.Admin.MustDo(201, "POST", "/api/v1/routing-plans/"+planID+"/versions", map[string]any{"document": map[string]any{"default_alias_id": alias}}, &draft)
	pub := "/api/v1/routing-plans/" + planID + "/versions/" + draft.ID + "/publish"
	if code := e.Admin.Do("POST", pub, nil, nil, "If-Match", `"none"`); code != 412 {
		t.Fatalf("publish with a stale precondition: %d", code)
	}
	e.Admin.MustDo(200, "POST", pub, nil, nil, "If-Match", `"`+published+`"`)
	before := e.QueryInt(`SELECT count(*) FROM policy_versions`)
	if code := e.Admin.Do("POST", "/api/v1/routing-plans/"+planID+"/rollback", map[string]any{"version_id": published}, nil, "If-Match", `"`+published+`"`); code != 412 {
		t.Fatalf("rollback with a stale precondition: %d", code)
	}
	if after := e.QueryInt(`SELECT count(*) FROM policy_versions`); after != before {
		t.Fatalf("a refused rollback left a draft (%d -> %d versions)", before, after)
	}
	e.Admin.MustDo(200, "POST", "/api/v1/routing-plans/"+planID+"/rollback", map[string]any{"version_id": published}, nil, "If-Match", `"`+draft.ID+`"`)

	b1 := e.Binding("one", planID, apptest.BindingOpts{})
	b2 := e.Binding("two", planID, apptest.BindingOpts{})
	var issued struct {
		Credential struct {
			ID string `json:"id"`
		} `json:"credential"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/client-bindings/"+b1+"/credentials", map[string]any{"name": "ci"}, &issued)
	var got struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/client-bindings/"+b1+"/credentials/"+issued.Credential.ID, nil, &got)
	if got.ID != issued.Credential.ID || got.Token != "" {
		t.Fatalf("credential %+v", got)
	}
	if code := e.Admin.Do("GET", "/api/v1/client-bindings/"+b2+"/credentials/"+issued.Credential.ID, nil, nil); code != 404 {
		t.Fatalf("credential read through another binding: %d", code)
	}
	e.Admin.MustDo(204, "DELETE", "/api/v1/client-bindings/"+b1+"/credentials/"+issued.Credential.ID, nil, nil, "X-HP-Managed-By", "terraform")
	if src := e.QueryString(`SELECT json_extract(details, '$.source') FROM audit_log WHERE action = 'gateway_credential.revoke'`); src != "terraform" {
		t.Fatalf("revoke source %q", src)
	}
}

type attention struct {
	Items []struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Title    string `json:"title"`
		Link     string `json:"link"`
	} `json:"items"`
}

func (a attention) has(prefix string) bool {
	for _, it := range a.Items {
		if strings.HasPrefix(it.ID, prefix) {
			return true
		}
	}
	return false
}

// The attention list surfaces real problems, most severe first, and clears
// them when they are resolved.
func TestAttentionList(t *testing.T) {
	t.Run("demo conditions", func(t *testing.T) {
		e := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.Demo.Enabled = true }))
		var got attention
		e.Eventually(func() bool {
			got = attention{}
			e.Admin.MustDo(200, "GET", "/api/v1/attention", nil, &got)
			return got.has("quota-auth:") && got.has("quota-used:")
		}, "the demo's failing credential and used-up weekly limit")
		if got.Items[0].Severity != "critical" {
			t.Fatalf("not sorted by severity: %+v", got.Items)
		}
		if got.has("system:") {
			t.Fatalf("demo mode reports setup gaps: %+v", got.Items)
		}
	})
	t.Run("setup gaps and failed deliveries", func(t *testing.T) {
		e := apptest.New(t)
		var got attention
		e.Admin.MustDo(200, "GET", "/api/v1/attention", nil, &got)
		if !got.has("system:no-backup") || got.has("system:plain-http") {
			t.Fatalf("items %+v (loopback http is not flagged; a missing backup is)", got.Items)
		}
		e.Admin.MustDo(200, "POST", "/api/v1/system/backups", map[string]any{}, nil)
		if _, err := e.App.DB.W.Exec(`INSERT INTO notifications(id, channel_id, kind, severity, title, body, status, attempts, next_attempt_at, created_at)
			VALUES ('nfy_x', 'nch_x', 'alert', 'warning', 't', 'b', 'dead', 7, 0, 0), ('nfy_y', 'nch_x', 'test', 'info', 't', 'b', 'dead', 1, 0, 0)`); err != nil {
			t.Fatal(err)
		}
		got = attention{}
		e.Admin.MustDo(200, "GET", "/api/v1/attention", nil, &got)
		if got.has("system:no-backup") || !got.has("delivery:notifications") {
			t.Fatalf("after backup and a failed alert: %+v", got.Items)
		}
		for _, it := range got.Items {
			if it.ID == "delivery:notifications" && it.Title != "1 notifications could not be sent" {
				t.Fatalf("test sends must not count: %q", it.Title)
			}
		}
	})
}

// The request list filters by time, fallbacks and search and reports the
// total; usage series split by a dimension.
func TestRequestFiltersAndGroupedSeries(t *testing.T) {
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	price := [2]string{"3", "15"}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price})
	a1 := e.Account("a1", prov, apptest.AccountOpts{})
	a2 := e.Account("a2", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias})
	tokA := e.Token(e.Binding("laptop", plan, apptest.BindingOpts{}))
	tokB := e.Token(e.Binding("ci", plan, apptest.BindingOpts{}))
	send := func(tok, model string) {
		r := e.Gateway("POST", "/v1/messages", tok, map[string]any{"model": model, "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "x"}}})
		if b := apptest.ReadAll(r); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
	up.Push(apptest.Reply{Status: 429, RetryAfter: 5})
	send(tokA, "claude-opus") // needs a fallback
	send(tokA, "claude-sonnet")
	send(tokB, "gpt-x")
	e.Flush()

	type page struct {
		Items []struct {
			ID             string `json:"id"`
			RequestedModel string `json:"requested_model"`
			Attempts       int    `json:"attempts"`
		} `json:"items"`
		Total      int64  `json:"total"`
		NextCursor string `json:"next_cursor"`
	}
	tests := []struct {
		name  string
		query string
		want  int64
	}{
		{"all", "", 3},
		{"fallbacks", "fallbacks=true", 1},
		{"model prefix", "q=claude", 2},
		{"underscore is literal", "q=claude_", 0},
		{"future range", "from=2999-01-01T00:00:00Z", 0},
		{"past range", "to=2000-01-01T00:00:00Z", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p page
			e.Admin.MustDo(200, "GET", "/api/v1/activity/requests?limit=1&"+tt.query, nil, &p)
			if p.Total != tt.want || int64(len(p.Items)) != min(tt.want, 1) || (p.NextCursor != "") != (tt.want > 1) {
				t.Fatalf("total %d items %d next %q, want total %d", p.Total, len(p.Items), p.NextCursor, tt.want)
			}
		})
	}
	var all page
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests", nil, &all)
	var byID page
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests?q="+all.Items[0].ID, nil, &byID)
	if byID.Total != 1 || byID.Items[0].ID != all.Items[0].ID {
		t.Fatalf("search by id %+v", byID)
	}

	var ser struct {
		Points []struct {
			Attempts int64 `json:"attempts"`
		} `json:"points"`
		Groups []struct {
			Key    string `json:"key"`
			Points []struct {
				Attempts int64 `json:"attempts"`
			} `json:"points"`
		} `json:"groups"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/usage/series?step=day&by=binding&top=1", nil, &ser)
	sum := func(pts []struct {
		Attempts int64 `json:"attempts"`
	}) (n int64) {
		for _, p := range pts {
			n += p.Attempts
		}
		return n
	}
	if len(ser.Groups) != 2 || ser.Groups[1].Key != "other" {
		t.Fatalf("groups %+v", ser.Groups)
	}
	if total := sum(ser.Points); total == 0 || sum(ser.Groups[0].Points)+sum(ser.Groups[1].Points) != total {
		t.Fatalf("groups do not add up to the total: %+v vs %+v", ser.Groups, ser.Points)
	}
	e.Admin.MustDo(200, "GET", "/api/v1/usage/series?step=week", nil, &ser)
	if len(ser.Points) == 0 {
		t.Fatal("weekly series is empty")
	}
}
