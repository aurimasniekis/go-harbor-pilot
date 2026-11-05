package acceptance_test

import (
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Validating a draft runs sample requests through it and warns when its
// rules would block all or most traffic, naming the rule or table row.
func TestValidationWarnsWhenAVersionBlocksTraffic(t *testing.T) {
	e := apptest.New(t)
	prov := e.Provider("p", "anthropic", "https://api.example.test", apptest.Model{ID: "m"})
	acc := e.Account("a", prov, apptest.AccountOpts{Secret: "sk-test"})
	alias := e.Alias("coding/default", e.Pool("pool", "ordered", apptest.Member{Account: acc, Model: "m"}))
	plan := e.Plan("team", apptest.PlanOpts{DefaultAlias: alias, Mappings: map[string]string{"claude-*": alias}})
	table := func(cells []string, action string) map[string]any {
		return map[string]any{"name": "table-1", "stage": "request", "hit_policy": "first", "inputs": []string{"request.harness"}, "enabled": true,
			"rows": []any{map[string]any{"conditions": cells, "action": action}}}
	}
	rule := func(when string) map[string]any {
		return map[string]any{"name": "no-agents", "stage": "request", "when": when, "action": "block", "enabled": true}
	}
	tests := []struct {
		name   string
		engine map[string]any
		want   string // substring of a warning; "" = no traffic warning
	}{
		{"match-all table row that blocks", map[string]any{"tables": []any{table([]string{"-"}, "block")}}, "blocks every request"},
		{"table that blocks one tool", map[string]any{"tables": []any{table([]string{`value == "codex"`}, "block")}}, ""},
		{"rule that blocks three of five tools", map[string]any{"rules": []any{rule(`request.harness in ["claude_code", "codex", "opencode"]`)}}, "blocks most requests"},
		{"harmless intent table", map[string]any{"tables": []any{table([]string{"-"}, "set_intent")}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := map[string]any{"default_alias_id": alias, "model_mappings": []any{map[string]any{"match": "claude-*", "alias_id": alias}}, "engine": tt.engine}
			if rows, ok := tt.engine["tables"].([]any); ok {
				r := rows[0].(map[string]any)["rows"].([]any)[0].(map[string]any)
				if r["action"] == "set_intent" {
					r["intent"] = "coding"
				}
			}
			var v struct {
				ID string `json:"id"`
			}
			e.Admin.MustDo(201, "POST", "/api/v1/routing-plans/"+plan+"/versions", map[string]any{"document": doc}, &v)
			var rep struct {
				Valid    bool `json:"valid"`
				Warnings []struct {
					Message string `json:"message"`
				} `json:"warnings"`
			}
			e.Admin.MustDo(200, "POST", "/api/v1/routing-plans/"+plan+"/versions/"+v.ID+"/validate", nil, &rep)
			var got []string
			for _, w := range rep.Warnings {
				if strings.Contains(w.Message, "blocks") {
					got = append(got, w.Message)
				}
			}
			switch {
			case tt.want == "" && len(got) > 0:
				t.Fatalf("unexpected traffic warning: %v", got)
			case tt.want != "" && (len(got) != 1 || !strings.Contains(got[0], tt.want)):
				t.Fatalf("warnings %v, want one containing %q", got, tt.want)
			case tt.want != "" && !strings.Contains(got[0], "table-1 row 1") && !strings.Contains(got[0], "rule no-agents"):
				t.Fatalf("warning does not name the source: %q", got[0])
			}
			if !rep.Valid {
				t.Fatalf("a traffic warning must not make the version invalid: %+v", rep)
			}
			e.Admin.MustDo(204, "DELETE", "/api/v1/routing-plans/"+plan+"/versions/"+v.ID, nil, nil)
		})
	}
}
