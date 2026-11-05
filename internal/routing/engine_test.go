package routing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// fixture builds a snapshot with two providers, three accounts and a plan.
func fixture(engine string, fallback cp.FallbackPolicy) (*cp.Snapshot, Input) {
	s := &cp.Snapshot{Providers: map[string]*cp.Res[cp.ProviderSpec]{}, Accounts: map[string]*cp.Res[cp.AccountSpec]{}, Pools: map[string]*cp.Res[cp.PoolSpec]{},
		Aliases: map[string]*cp.Res[cp.AliasSpec]{}, AliasByName: map[string]*cp.Res[cp.AliasSpec]{}, Plans: map[string]*cp.PlanView{},
		Bindings: map[string]*cp.Res[cp.BindingSpec]{}, Projects: map[string]*cp.Res[cp.ProjectSpec]{}, Workspaces: map[string]*cp.Res[cp.WorkspaceSpec]{},
		AliasPool: map[string]string{}, AliasChain: map[string][]string{}}
	s.Providers["prv_a"] = &cp.Res[cp.ProviderSpec]{ID: "prv_a", Name: "anthropic", Spec: cp.ProviderSpec{Type: cp.ProviderAnthropic, Classification: "official",
		Models: []cp.ModelSpec{{ID: "sonnet", Capabilities: []string{"streaming", "tools", "reasoning"}}}}}
	s.Providers["prv_l"] = &cp.Res[cp.ProviderSpec]{ID: "prv_l", Name: "local", Spec: cp.ProviderSpec{Type: cp.ProviderOpenAICompatible, Classification: "local",
		Models: []cp.ModelSpec{{ID: "coder", Capabilities: []string{"streaming", "tools"}}}}}
	acc := func(id, prov, billing string) {
		s.Accounts[id] = &cp.Res[cp.AccountSpec]{ID: id, Name: id, Spec: cp.AccountSpec{ProviderID: prov, Billing: billing, Enabled: true, Labels: map[string]string{"team": "core"}}}
	}
	acc("acc_sub", "prv_a", cp.BillingSubscription)
	acc("acc_api", "prv_a", cp.BillingMetered)
	acc("acc_local", "prv_l", cp.BillingLocal)
	s.Pools["pol_1"] = &cp.Res[cp.PoolSpec]{ID: "pol_1", Name: "main", Spec: cp.PoolSpec{Strategy: "ordered", Members: []cp.PoolMember{
		{AccountID: "acc_sub", Model: "sonnet", Priority: 0}, {AccountID: "acc_api", Model: "sonnet", Priority: 1}, {AccountID: "acc_local", Model: "coder", Priority: 2}}}}
	s.Aliases["als_1"] = &cp.Res[cp.AliasSpec]{ID: "als_1", Name: "coding/default", Spec: cp.AliasSpec{PoolID: "pol_1"}}
	s.AliasByName["coding/default"] = s.Aliases["als_1"]
	s.AliasPool["als_1"], s.AliasChain["als_1"] = "pol_1", []string{"als_1"}
	doc := cp.PlanDocument{DefaultAliasID: "als_1", Fallback: fallback, Engine: json.RawMessage(engine)}
	cp.NormalizeDocument(&doc)
	comp, errs := CompileEngine(doc.Engine)
	if len(errs) > 0 {
		panic(errs[0].Message)
	}
	plan := &cp.PlanView{Res: cp.Res[cp.PlanSpec]{ID: "pln_1", Name: "p"}, Published: &cp.VersionView{ID: "pvr_1", Version: 1, Document: doc, Compiled: comp}}
	s.Plans["pln_1"] = plan
	b := &cp.Res[cp.BindingSpec]{ID: "cbn_1", Name: "laptop", Spec: cp.BindingSpec{PlanID: "pln_1", Enabled: true}}
	return s, Input{Snapshot: s, Binding: b, Plan: plan, RequestedModel: "claude-x", Inbound: ir.AnthropicMessages, Now: time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC),
		Harness: "claude_code", Stream: true, RequestSeed: "r1",
		UpstreamFor: func(t string, _ ir.Protocol) ir.Protocol {
			if t == cp.ProviderAnthropic {
				return ir.AnthropicMessages
			}
			return ir.OpenAIChat
		}}
}

func ids(d *Decision) string {
	var out []string
	for _, c := range d.Candidates {
		out = append(out, c.AccountID)
	}
	return strings.Join(out, ",")
}

var allowAll = cp.FallbackPolicy{Steps: []string{cp.StepAlternateAccount, cp.StepAlternateModel, cp.StepAlternateProvider, cp.StepStop}, MaxAttempts: 3, AllowPaidFallback: true}

func TestEngineRules(t *testing.T) {
	tests := []struct {
		name     string
		engine   string
		fallback cp.FallbackPolicy
		mutate   func(in *Input)
		want     string
		wantErr  string
		check    func(t *testing.T, d *Decision)
	}{
		{name: "no engine keeps pool order", engine: `{}`, fallback: allowAll, want: "acc_sub,acc_api,acc_local"},
		{name: "paid fallback excluded unless allowed", engine: `{}`, fallback: cp.FallbackPolicy{Steps: []string{cp.StepAlternateAccount, cp.StepStop}, MaxAttempts: 3},
			want: "acc_sub,acc_local", check: func(t *testing.T, d *Decision) {
				if !strings.Contains(fmtExcluded(d), "acc_api:paid_fallback_not_authorized") {
					t.Fatalf("excluded %s", fmtExcluded(d))
				}
			}},
		{name: "request block", engine: `{"rules":[{"name":"no-codex","stage":"request","when":"request.harness == 'codex'","action":"block","reason":"codex not allowed","enabled":true}]}`,
			fallback: allowAll, mutate: func(in *Input) { in.Harness = "codex" }, wantErr: "codex not allowed"},
		{name: "require capability filters candidates", engine: `{"rules":[{"name":"deep","stage":"request","when":"request.model contains 'deep'","action":"require","capability":"reasoning","enabled":true}]}`,
			fallback: allowAll, mutate: func(in *Input) { in.RequestedModel = "coding/deep-x" }, want: "acc_sub,acc_api"},
		{name: "rule-derived intent", engine: `{"rules":[{"name":"review","stage":"request","when":"request.tools == 0 && !request.stream","action":"set_intent","intent":"review","enabled":true}]}`,
			fallback: allowAll, mutate: func(in *Input) { in.Stream = false }, want: "acc_sub,acc_api,acc_local",
			check: func(t *testing.T, d *Decision) {
				if d.Intent != "review" || d.IntentSource != "rule" {
					t.Fatalf("intent %q/%q", d.Intent, d.IntentSource)
				}
			}},
		{name: "candidate exclude by label", engine: `{"rules":[{"name":"no-local-at-night","stage":"candidate","when":"candidate.provider.classification == 'local' && time.hour >= 13","action":"exclude","reason":"local box off","enabled":true}]}`,
			fallback: allowAll, want: "acc_sub,acc_api"},
		{name: "boost reorders but never re-admits", engine: `{"rules":[
			{"name":"prefer-local","stage":"candidate","when":"candidate.account.billing == 'local'","action":"boost","boost":100,"enabled":true},
			{"name":"drop-api","stage":"candidate","when":"candidate.account.billing == 'metered_api'","action":"exclude","enabled":true},
			{"name":"boost-api","stage":"candidate","when":"candidate.account.billing == 'metered_api'","action":"boost","boost":1000,"enabled":true}]}`,
			fallback: allowAll, want: "acc_local,acc_sub"},
		{name: "unknown remaining errors instead of matching", engine: `{"rules":[{"name":"low-quota","stage":"candidate","when":"candidate.health.remaining < 0.1","action":"exclude","enabled":true}]}`,
			fallback: allowAll, want: "acc_sub,acc_api,acc_local", check: func(t *testing.T, d *Decision) {
				errs := 0
				for _, tr := range d.Rules {
					if tr.Error != "" {
						errs++
					}
				}
				if errs != 3 {
					t.Fatalf("expected an evaluation error per candidate, traces %+v", d.Rules)
				}
			}},
		{name: "known guard handles unknown", engine: `{"rules":[{"name":"low-quota","stage":"candidate","when":"known(candidate.health.remaining) && candidate.health.remaining < 0.1","action":"exclude","enabled":true}]}`,
			fallback: allowAll, mutate: func(in *Input) {
				low := 0.05
				in.HealthOf = func(id string) Health {
					if id == "acc_sub" {
						return Health{Available: true, Remaining: &low}
					}
					return Health{Available: true}
				}
			}, want: "acc_api,acc_local"},
		{name: "score expression ranks", engine: `{"score":"candidate.native ? 0.0 : 10.0"}`, fallback: allowAll, want: "acc_local,acc_sub,acc_api"},
		{name: "decision table first hit", engine: `{"tables":[{"name":"by-harness","stage":"candidate","hit_policy":"first","enabled":true,
			"inputs":["request.harness","candidate.account.billing"],
			"rows":[{"conditions":["value == 'claude_code'","value == 'subscription'"],"action":"boost","boost":-50},
			        {"conditions":["-","value == 'local'"],"action":"boost","boost":5}]}]}`, fallback: allowAll, want: "acc_local,acc_api,acc_sub"},
		{name: "unique table with overlapping rows is ignored", engine: `{"tables":[{"name":"u","stage":"request","hit_policy":"unique","enabled":true,
			"inputs":["request.harness"],"rows":[{"conditions":["value == 'claude_code'"],"action":"block","reason":"a"},{"conditions":["value != ''"],"action":"block","reason":"b"}]}]}`,
			fallback: allowAll, want: "acc_sub,acc_api,acc_local", check: func(t *testing.T, d *Decision) {
				if !strings.Contains(d.Rules[0].Error, "hit policy unique") {
					t.Fatalf("traces %+v", d.Rules)
				}
			}},
		{name: "health exclusion precedes scores", engine: `{"rules":[{"name":"b","stage":"candidate","when":"true","action":"boost","boost":1000,"enabled":true}]}`,
			fallback: allowAll, mutate: func(in *Input) {
				in.HealthOf = func(id string) Health {
					if id == "acc_sub" {
						return Health{Available: false, Reason: "exhausted"}
					}
					return Health{Available: true}
				}
			}, want: "acc_api,acc_local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, in := fixture(tt.engine, tt.fallback)
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			d, err := Decide(in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err=%v want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(d); got != tt.want {
				t.Fatalf("order %s want %s (excluded %s)", got, tt.want, fmtExcluded(d))
			}
			if tt.check != nil {
				tt.check(t, d)
			}
		})
	}
}

func fmtExcluded(d *Decision) string {
	var out []string
	for _, e := range d.Excluded {
		out = append(out, e.AccountID+":"+e.Reason)
	}
	return strings.Join(out, ",")
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		engine, want string
	}{
		{`{"rules":[{"name":"x","stage":"request","when":"request.nope == 1","action":"block","enabled":true}]}`, "has no field nope"},
		{`{"rules":[{"name":"x","stage":"request","when":"candidate.native","action":"block","enabled":true}]}`, "cannot reference candidate"},
		{`{"rules":[{"name":"x","stage":"request","when":"1 + 1","action":"block","enabled":true}]}`, "expected bool"},
		{`{"rules":[{"name":"x","stage":"candidate","when":"true","action":"block","enabled":true}]}`, "candidate-stage actions"},
		{`{"rules":[{"name":"x","stage":"request","when":"now() > now()","action":"block","enabled":true}]}`, "now"},
		{`{"tables":[{"name":"t","stage":"request","hit_policy":"first","enabled":true,"inputs":["request.harness"],"rows":[{"conditions":["-"],"action":"block"},{"conditions":["-"],"action":"block"}]}]}`, "can never apply"},
		{`{"bogus":true}`, "invalid engine section"},
	}
	for _, tt := range tests {
		_, errs := CompileEngine(json.RawMessage(tt.engine))
		joined := ""
		for _, e := range errs {
			joined += e.Message + ";"
		}
		if !strings.Contains(joined, tt.want) {
			t.Errorf("engine %s: got %q want %q", tt.engine, joined, tt.want)
		}
	}
}

func TestTransitions(t *testing.T) {
	a := Candidate{AccountID: "a1", ProviderID: "p1", Model: "m1"}
	tests := []struct {
		name  string
		to    Candidate
		steps []string
		want  string
		ok    bool
	}{
		{"same model other account", Candidate{AccountID: "a2", ProviderID: "p1", Model: "m1"}, []string{"alternate_account", "stop"}, "alternate_account", true},
		{"other model same provider", Candidate{AccountID: "a1", ProviderID: "p1", Model: "m2"}, []string{"alternate_account", "stop"}, "alternate_model", false},
		{"other provider", Candidate{AccountID: "a3", ProviderID: "p2", Model: "m1"}, []string{"alternate_account", "alternate_model", "alternate_provider"}, "alternate_provider", true},
		{"stop first", Candidate{AccountID: "a2", ProviderID: "p1", Model: "m1"}, []string{"stop", "alternate_account"}, "alternate_account", false},
	}
	for _, tt := range tests {
		step, ok := Transition(cp.FallbackPolicy{Steps: tt.steps}, a, tt.to)
		if step != tt.want || ok != tt.ok {
			t.Errorf("%s: %s %v", tt.name, step, ok)
		}
	}
}
