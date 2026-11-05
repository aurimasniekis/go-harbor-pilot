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
