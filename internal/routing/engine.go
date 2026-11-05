package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// Engine limits keep evaluation bounded.
const (
	MaxRules       = 100
	MaxTables      = 50
	MaxTableRows   = 200
	MaxTableInputs = 10
	MaxExprNodes   = 500
	MaxExprLen     = 2000
)

// RuleSpec is one ordered rule.
type RuleSpec struct {
	Name       string  `json:"name"`
	Stage      string  `json:"stage" enum:"request,candidate"`
	When       string  `json:"when" doc:"Boolean Expr over the stage environment."`
	Action     string  `json:"action" enum:"block,require,set_intent,exclude,boost" doc:"request: block|require|set_intent; candidate: exclude|boost"`
	Capability string  `json:"capability,omitempty"`
	Intent     string  `json:"intent,omitempty"`
	Boost      float64 `json:"boost,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Enabled    bool    `json:"enabled"`
}

// TableRow is one decision-table row. Conditions align with the table's
// inputs; "-" or "" matches anything, otherwise the cell is an Expr over
// `value` (e.g. `value == "codex"`, `value > 0.2`, `value in ["a","b"]`).
type TableRow struct {
	Conditions []string `json:"conditions"`
	Action     string   `json:"action" enum:"block,require,set_intent,exclude,boost"`
	Capability string   `json:"capability,omitempty"`
	Intent     string   `json:"intent,omitempty"`
	Boost      float64  `json:"boost,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// TableSpec is a decision table with an explicit hit policy.
type TableSpec struct {
	Name      string     `json:"name"`
	Stage     string     `json:"stage" enum:"request,candidate"`
	HitPolicy string     `json:"hit_policy" enum:"first,unique,collect" doc:"first: first matching row wins; unique: at most one row may match (more is a configuration error); collect: every matching row applies."`
	Inputs    []string   `json:"inputs" doc:"Expr expressions producing each column's value."`
	Rows      []TableRow `json:"rows"`
	Enabled   bool       `json:"enabled"`
}

// EngineSpec is the routing-engine section of a plan document.
type EngineSpec struct {
	Rules  []RuleSpec  `json:"rules"`
	Tables []TableSpec `json:"tables"`
	// Score is an optional float Expr over the candidate environment;
	// higher ranks earlier (after hard restrictions, before pool order).
	Score string `json:"score"`
}

// ParseEngine decodes a plan's engine section strictly.
func ParseEngine(raw json.RawMessage) (*EngineSpec, error) {
	e := &EngineSpec{}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return e, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(e); err != nil {
		return nil, err
	}
	return e, nil
}

// ---------- environments ----------

// RequestEnv describes the request (trusted context and observable facts;
// never prompt content).
type RequestEnv struct {
	Model             string   `expr:"model"`
	Protocol          string   `expr:"protocol"`
	Harness           string   `expr:"harness"`
	HarnessConfidence string   `expr:"harness_confidence"`
	Stream            bool     `expr:"stream"`
	Tools             int      `expr:"tools"`
	ToolNames         []string `expr:"tool_names"`
	Images            bool     `expr:"images"`
	InputChars        int      `expr:"input_chars"`
	Required          []string `expr:"required"`
	Intent            string   `expr:"intent"`
	IntentSource      string   `expr:"intent_source"`
	SessionConfidence string   `expr:"session_confidence"`
}

// BindingEnv describes the client binding.
type BindingEnv struct {
	Name      string            `expr:"name"`
	Harness   string            `expr:"expected_harness"`
	Refs      map[string]string `expr:"refs"`
	Project   string            `expr:"project"`
	Workspace string            `expr:"workspace"`
}

// TimeEnv is request time (UTC). The evaluation clock is part of the
// recorded inputs so replays are deterministic.
type TimeEnv struct {
	Hour    int    `expr:"hour"`
	Weekday string `expr:"weekday"`
}

// HealthEnv describes account health. Remaining and ResetInMinutes are nil
// when unknown: comparing them without a known(...) guard is an evaluation
// error, which makes the rule not apply (and is shown in the explanation).
type HealthEnv struct {
	Available      bool `expr:"available"`
	Known          bool `expr:"known"`
	Stale          bool `expr:"stale"`
	Remaining      any  `expr:"remaining"`
	ResetInMinutes any  `expr:"reset_in_minutes"`
}

// CandidateEnv describes one candidate.
type CandidateEnv struct {
	Provider struct {
		Name           string `expr:"name"`
		Type           string `expr:"type"`
		Classification string `expr:"classification"`
	} `expr:"provider"`
	Account struct {
		Name    string            `expr:"name"`
		Billing string            `expr:"billing"`
		Labels  map[string]string `expr:"labels"`
	} `expr:"account"`
	Model struct {
		ID            string   `expr:"id"`
		Capabilities  []string `expr:"capabilities"`
		ContextWindow int      `expr:"context_window"`
		MaxOutput     int      `expr:"max_output"`
	} `expr:"model"`
	Health   HealthEnv `expr:"health"`
	Native   bool      `expr:"native"`
	Upstream string    `expr:"upstream"`
	Priority int       `expr:"priority"`
}

// Env is the full evaluation environment.
type Env struct {
	Request   RequestEnv   `expr:"request"`
	Binding   BindingEnv   `expr:"binding"`
	Time      TimeEnv      `expr:"time"`
	Candidate CandidateEnv `expr:"candidate"`
	Value     any          `expr:"value"`
}

var exprFuncs = []expr.Option{
	expr.Function("known", func(p ...any) (any, error) { return p[0] != nil, nil }, new(func(any) bool)),
	expr.DisableBuiltin("now"),
	expr.MaxNodes(MaxExprNodes),
}

func compileExpr(code string, kind string) (*vm.Program, error) {
	if len(code) > MaxExprLen {
		return nil, fmt.Errorf("expression longer than %d characters", MaxExprLen)
	}
	opts := append([]expr.Option{expr.Env(Env{})}, exprFuncs...)
	switch kind {
	case "bool":
		opts = append(opts, expr.AsBool())
	case "float":
		opts = append(opts, expr.AsFloat64())
	}
	return expr.Compile(code, opts...)
}

// ---------- compiled engine ----------

type compiledRule struct {
	spec RuleSpec
	prog *vm.Program
}

type compiledTable struct {
	spec   TableSpec
	inputs []*vm.Program
	cells  [][]*vm.Program // nil = match anything
}

// Compiled is a plan's compiled engine (attached to the snapshot).
type Compiled struct {
	Rules  []compiledRule
	Tables []compiledTable
	Score  *vm.Program
	Empty  bool
}

// FieldError mirrors controlplane.FieldError.
type FieldError = cp.FieldError

var requestActions = []string{"block", "require", "set_intent"}
var candidateActions = []string{"exclude", "boost"}

// CompileEngine validates and compiles an engine section.
func CompileEngine(raw json.RawMessage) (*Compiled, []FieldError) {
	var errs []FieldError
	fail := func(loc, msg string) {
		errs = append(errs, FieldError{Location: "body.document.engine." + loc, Message: msg})
	}
	spec, err := ParseEngine(raw)
	if err != nil {
		return nil, []FieldError{{Location: "body.document.engine", Message: "invalid engine section: " + err.Error()}}
	}
	c := &Compiled{Empty: len(spec.Rules) == 0 && len(spec.Tables) == 0 && spec.Score == ""}
	if len(spec.Rules) > MaxRules {
		fail("rules", fmt.Sprintf("at most %d rules", MaxRules))
	}
	if len(spec.Tables) > MaxTables {
		fail("tables", fmt.Sprintf("at most %d tables", MaxTables))
	}
	checkAction := func(loc, stage, action, capability, intent string) {
		switch stage {
		case "request":
			if !slices.Contains(requestActions, action) {
				fail(loc+".action", "request-stage actions are block, require, set_intent")
			}
		case "candidate":
			if !slices.Contains(candidateActions, action) {
				fail(loc+".action", "candidate-stage actions are exclude, boost")
			}
		default:
			fail(loc+".stage", "stage must be request or candidate")
		}
		if action == "require" && !slices.Contains(cp.Capabilities, capability) {
			fail(loc+".capability", "unknown capability "+capability)
		}
		if action == "set_intent" && (intent == "" || len(intent) > 64) {
			fail(loc+".intent", "intent must be 1-64 characters")
		}
	}
	names := map[string]bool{}
	for i, r := range spec.Rules {
		loc := fmt.Sprintf("rules[%d]", i)
		if r.Name == "" || names[r.Name] {
			fail(loc+".name", "rule names must be unique and non-empty")
		}
		names[r.Name] = true
		checkAction(loc, r.Stage, r.Action, r.Capability, r.Intent)
		if r.Stage == "request" && strings.Contains(r.When, "candidate.") {
			fail(loc+".when", "request-stage rules cannot reference candidate.*")
		}
		p, err := compileExpr(r.When, "bool")
		if err != nil {
			fail(loc+".when", err.Error())
			continue
		}
		if r.Enabled {
			c.Rules = append(c.Rules, compiledRule{spec: r, prog: p})
		}
	}
	for i, t := range spec.Tables {
		loc := fmt.Sprintf("tables[%d]", i)
		if t.Name == "" || names[t.Name] {
			fail(loc+".name", "table names must be unique and non-empty")
		}
		names[t.Name] = true
		if !slices.Contains([]string{"first", "unique", "collect"}, t.HitPolicy) {
			fail(loc+".hit_policy", "hit policy must be first, unique or collect")
		}
		if len(t.Inputs) == 0 || len(t.Inputs) > MaxTableInputs {
			fail(loc+".inputs", fmt.Sprintf("tables need 1-%d inputs", MaxTableInputs))
		}
		if len(t.Rows) > MaxTableRows {
			fail(loc+".rows", fmt.Sprintf("at most %d rows", MaxTableRows))
		}
		ct := compiledTable{spec: t}
		for j, in := range t.Inputs {
			if t.Stage == "request" && strings.Contains(in, "candidate.") {
				fail(fmt.Sprintf("%s.inputs[%d]", loc, j), "request-stage tables cannot reference candidate.*")
			}
			p, err := compileExpr(in, "any")
			if err != nil {
				fail(fmt.Sprintf("%s.inputs[%d]", loc, j), err.Error())
				continue
			}
			ct.inputs = append(ct.inputs, p)
		}
		seen := map[string]int{}
		for j, row := range t.Rows {
			rloc := fmt.Sprintf("%s.rows[%d]", loc, j)
			if len(row.Conditions) != len(t.Inputs) {
				fail(rloc+".conditions", fmt.Sprintf("expected %d conditions, one per input", len(t.Inputs)))
				continue
			}
			checkAction(rloc, t.Stage, row.Action, row.Capability, row.Intent)
			key := strings.Join(row.Conditions, "\x00")
			if prev, dup := seen[key]; dup && t.HitPolicy != "collect" {
				fail(rloc+".conditions", fmt.Sprintf("identical to row %d; with hit policy %s it can never apply", prev, t.HitPolicy))
			}
			seen[key] = j
			var cells []*vm.Program
			for k, cond := range row.Conditions {
				cond = strings.TrimSpace(cond)
				if cond == "" || cond == "-" {
					cells = append(cells, nil)
					continue
				}
				p, err := compileExpr(cond, "bool")
				if err != nil {
					fail(fmt.Sprintf("%s.conditions[%d]", rloc, k), err.Error())
					cells = append(cells, nil)
					continue
				}
				cells = append(cells, p)
			}
			ct.cells = append(ct.cells, cells)
		}
		if t.Enabled {
			c.Tables = append(c.Tables, ct)
		}
	}
	if spec.Score != "" {
		p, err := compileExpr(spec.Score, "float")
		if err != nil {
			fail("score", err.Error())
		} else {
			c.Score = p
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return c, nil
}

func init() {
	cp.PlanProbe = ProbePlan
	cp.PlanCompiler = func(s *cp.Snapshot, p *cp.PlanView) (any, error) {
		c, errs := CompileEngine(p.Published.Document.Engine)
		if len(errs) > 0 {
			return nil, &cp.ValidationError{Errors: errs}
		}
		return c, nil
	}
}

// ---------- evaluation ----------

// Trace records one rule/table evaluation for the explanation.
type Trace struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // rule | table
	Stage     string `json:"stage"`
	Matched   bool   `json:"matched"`
	Effect    string `json:"effect,omitempty"`
	Error     string `json:"error,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Row       *int   `json:"row,omitempty"`
}

// effect is one action to apply.
type effect struct {
	action, capability, intent, reason string
	boost                              float64
	source                             string
}

func runBool(p *vm.Program, env *Env) (bool, error) {
	out, err := expr.Run(p, env)
	if err != nil {
		return false, err
	}
	b, _ := out.(bool)
	return b, nil
}

// evaluate runs every rule and table of a stage and returns the effects.
func (c *Compiled) evaluate(stage string, env *Env, traces *[]Trace, cand *Candidate) []effect {
	var out []effect
	add := func(t Trace) {
		if cand != nil {
			t.AccountID, t.Model = cand.AccountID, cand.Model
		}
		if len(*traces) < 500 {
			*traces = append(*traces, t)
		}
	}
	for _, r := range c.Rules {
		if r.spec.Stage != stage {
			continue
		}
		ok, err := runBool(r.prog, env)
		t := Trace{Name: r.spec.Name, Kind: "rule", Stage: stage, Matched: ok}
		if err != nil {
			t.Error = err.Error()
			add(t)
			continue
		}
		if ok {
			e := effect{action: r.spec.Action, capability: r.spec.Capability, intent: r.spec.Intent, reason: r.spec.Reason, boost: r.spec.Boost, source: "rule:" + r.spec.Name}
			t.Effect = describe(e)
			out = append(out, e)
		}
		add(t)
	}
	for _, t := range c.Tables {
		if t.spec.Stage != stage {
			continue
		}
		values := make([]any, len(t.inputs))
		var inErr error
		for i, p := range t.inputs {
			v, err := expr.Run(p, env)
			if err != nil {
				inErr = err
				break
			}
			values[i] = v
		}
		tr := Trace{Name: t.spec.Name, Kind: "table", Stage: stage}
		if inErr != nil {
			tr.Error = inErr.Error()
			add(tr)
			continue
		}
		var matched []int
		for ri, cells := range t.cells {
			all := true
			for ci, cell := range cells {
				if cell == nil {
					continue
				}
				cellEnv := *env
				cellEnv.Value = values[ci]
				ok, err := runBool(cell, &cellEnv)
				if err != nil || !ok {
					all = false
					break
				}
			}
			if all {
				matched = append(matched, ri)
				if t.spec.HitPolicy == "first" {
					break
				}
			}
		}
		if t.spec.HitPolicy == "unique" && len(matched) > 1 {
			tr.Error = fmt.Sprintf("hit policy unique but rows %v matched; table ignored", matched)
			add(tr)
			continue
		}
		for _, ri := range matched {
			row := t.spec.Rows[ri]
			e := effect{action: row.Action, capability: row.Capability, intent: row.Intent, reason: row.Reason, boost: row.Boost, source: fmt.Sprintf("table:%s#%d", t.spec.Name, ri)}
			r := ri
			add(Trace{Name: t.spec.Name, Kind: "table", Stage: stage, Matched: true, Effect: describe(e), Row: &r})
			out = append(out, e)
		}
		if len(matched) == 0 {
			add(tr)
		}
	}
	return out
}

func describe(e effect) string {
	switch e.action {
	case "block":
		if e.reason == "" {
			return "block"
		}
		return "block: " + e.reason
	case "require":
		return "require " + e.capability
	case "set_intent":
		return "intent=" + e.intent
	case "exclude":
		return "exclude: " + e.reason
	case "boost":
		return fmt.Sprintf("boost %+g", e.boost)
	}
	return e.action
}

// Score evaluates the score expression for a candidate.
func (c *Compiled) score(env *Env) (float64, error) {
	if c.Score == nil {
		return 0, nil
	}
	out, err := expr.Run(c.Score, env)
	if err != nil {
		return 0, err
	}
	f, _ := out.(float64)
	return f, nil
}

// Variables documents the evaluation environment for the UI and API.
func Variables() []Variable {
	return []Variable{
		{"request.model", "string", "request", "Model name the client asked for."},
		{"request.protocol", "string", "request", "Inbound protocol (anthropic_messages, openai_chat, openai_responses, gemini)."},
		{"request.harness", "string", "request", "Detected harness (heuristic): claude_code, codex, opencode, pi, generic."},
		{"request.harness_confidence", "string", "request", "medium (user agent/header match) or low."},
		{"request.stream", "bool", "request", "Streaming requested."},
		{"request.tools", "int", "request", "Number of tool definitions offered."},
		{"request.tool_names", "[]string", "request", "Names of tools offered (definitions only, no content)."},
		{"request.images", "bool", "request", "Request contains images."},
		{"request.input_chars", "int", "request", "Approximate size of text input in characters (size only, no content)."},
		{"request.required", "[]string", "request", "Capabilities required so far."},
		{"request.intent", "string", "request", "Trusted declared or rule-derived intent ('' if none)."},
		{"request.intent_source", "string", "request", "declared | rule | classifier | ''."},
		{"request.session_confidence", "string", "request", "high | medium | unknown."},
		{"binding.name", "string", "request", "Client binding name."},
		{"binding.expected_harness", "string", "request", "Harness the binding expects."},
		{"binding.refs", "map[string]string", "request", "Trusted external references (credential bound refs and binding refs)."},
		{"binding.project", "string", "request", "Project name ('' if none)."},
		{"binding.workspace", "string", "request", "Workspace name ('' if none)."},
		{"time.hour", "int", "request", "Hour of day (UTC) of the request."},
		{"time.weekday", "string", "request", "Weekday (UTC), e.g. Monday."},
		{"candidate.provider.name", "string", "candidate", "Provider name."},
		{"candidate.provider.type", "string", "candidate", "Provider type."},
		{"candidate.provider.classification", "string", "candidate", "official | partial | unofficial | local."},
		{"candidate.account.name", "string", "candidate", "Account name."},
		{"candidate.account.billing", "string", "candidate", "metered_api | subscription | free | local."},
		{"candidate.account.labels", "map[string]string", "candidate", "Account labels."},
		{"candidate.model.id", "string", "candidate", "Upstream model id."},
		{"candidate.model.capabilities", "[]string", "candidate", "Declared capabilities (empty when undeclared)."},
		{"candidate.model.context_window", "int", "candidate", "Declared context window (0 = unknown)."},
		{"candidate.model.max_output", "int", "candidate", "Declared max output tokens (0 = unknown)."},
		{"candidate.health.available", "bool", "candidate", "Account currently eligible (cooldowns, exhaustion)."},
		{"candidate.health.known", "bool", "candidate", "Quota data exists for this account."},
		{"candidate.health.stale", "bool", "candidate", "Quota data is older than its freshness threshold."},
		{"candidate.health.remaining", "float|nil", "candidate", "Fraction of the tightest window left (0..1); nil when unknown — guard with known()."},
		{"candidate.health.reset_in_minutes", "float|nil", "candidate", "Minutes until the tightest window resets; nil when unknown."},
		{"candidate.native", "bool", "candidate", "Upstream speaks the client's protocol (no translation)."},
		{"candidate.upstream", "string", "candidate", "Upstream protocol."},
		{"candidate.priority", "int", "candidate", "Pool member priority."},
		{"value", "any", "table", "In decision-table cells: the column's input value."},
	}
}

// Variable documents one environment variable.
type Variable struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Stage       string `json:"stage"`
	Description string `json:"description"`
}

// ValidateExpr compiles a standalone expression ("bool" or "float").
func ValidateExpr(code, kind string) error {
	_, err := compileExpr(code, kind)
	return err
}
