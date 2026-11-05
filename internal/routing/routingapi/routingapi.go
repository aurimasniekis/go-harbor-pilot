// Package routingapi exposes routing tools: the typed variable reference,
// expression validation, plan simulation and replay of recorded requests.
// Simulation and replay never dispatch upstream requests.
package routingapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
)

// Deps are the API's collaborators (resolved lazily for spec rendering).
type Deps struct {
	CP          func() *cp.Service
	Health      func(accountID string) routing.Health
	UpstreamFor func(providerType string, inbound ir.Protocol) ir.Protocol
}

// HealthOverride replaces an account's health in a simulation.
type HealthOverride struct {
	Available      bool     `json:"available"`
	Remaining      *float64 `json:"remaining,omitempty" minimum:"0" maximum:"1"`
	ResetInMinutes *float64 `json:"reset_in_minutes,omitempty"`
	Stale          bool     `json:"stale,omitempty"`
	Reason         string   `json:"reason,omitempty"`
}

// SimulateRequest describes a synthetic request to route.
type SimulateRequest struct {
	VersionID         string                    `json:"version_id,omitempty" doc:"Plan version to evaluate (draft allowed); default = published."`
	BindingID         string                    `json:"binding_id,omitempty" doc:"Client binding context; default = a synthetic binding using this plan."`
	Model             string                    `json:"model" minLength:"1"`
	Protocol          string                    `json:"protocol" enum:"anthropic_messages,openai_chat,openai_responses,gemini"`
	Harness           string                    `json:"harness,omitempty"`
	Stream            bool                      `json:"stream"`
	ToolNames         []string                  `json:"tool_names,omitempty"`
	Images            bool                      `json:"images,omitempty"`
	InputChars        int                       `json:"input_chars,omitempty"`
	Required          []string                  `json:"required,omitempty" doc:"Capabilities the request needs."`
	Intent            string                    `json:"intent,omitempty"`
	SessionConfidence string                    `json:"session_confidence,omitempty"`
	At                *time.Time                `json:"at,omitempty" doc:"Evaluation time (default now)."`
	HealthMode        string                    `json:"health_mode,omitempty" enum:"current,assume_available," doc:"current (live health) or assume_available."`
	HealthOverrides   map[string]HealthOverride `json:"health_overrides,omitempty" doc:"Per-account health overrides keyed by account id."`
}

// SimulationResult wraps a simulated decision.
type SimulationResult struct {
	Mode     string            `json:"mode" doc:"simulation (synthetic input) or replay (recorded input)."`
	Decision *routing.Decision `json:"decision"`
	Error    string            `json:"error,omitempty" doc:"Why the request would be refused."`
	Kind     string            `json:"error_kind,omitempty"`
	Version  int64             `json:"version"`
}

// ReplayResult compares a recorded decision with a fresh evaluation.
type ReplayResult struct {
	Recorded json.RawMessage  `json:"recorded"`
	Replay   SimulationResult `json:"replay"`
	Changed  bool             `json:"changed" doc:"The first-choice candidate or outcome differs."`
	Notes    []string         `json:"notes"`
}

// Register adds the routing operations.
func Register(a *api.API, d Deps) {
	tags := []string{"Routing"}
	type varsOut struct {
		Body struct {
			Items []routing.Variable `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-routing-variables", Method: http.MethodGet, Path: "/routing/variables", Summary: "Typed reference of routing expression variables",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*varsOut, error) {
		out := &varsOut{}
		out.Body.Items = routing.Variables()
		return out, nil
	})

	type exprIn struct {
		Body struct {
			Expression string `json:"expression" maxLength:"2000"`
			Kind       string `json:"kind" enum:"bool,float,any"`
		}
	}
	type exprOut struct {
		Body struct {
			Valid bool   `json:"valid"`
			Error string `json:"error,omitempty"`
		}
	}
	api.Register(a, api.Op{ID: "validate-routing-expression", Method: http.MethodPost, Path: "/routing/validate-expression",
		Summary: "Compile an expression against the typed environment", Tags: tags, Permission: auth.PermConfigRead},
		func(ctx context.Context, in *exprIn) (*exprOut, error) {
			out := &exprOut{}
			if err := routing.ValidateExpr(in.Body.Expression, in.Body.Kind); err != nil {
				out.Body.Error = err.Error()
			} else {
				out.Body.Valid = true
			}
			return out, nil
		})

	type simIn struct {
		ID   string `path:"id" maxLength:"255"`
		Body SimulateRequest
	}
	type simOut struct{ Body SimulationResult }
	api.Register(a, api.Op{ID: "simulate-plan", Method: http.MethodPost, Path: "/routing-plans/{id}/simulate",
		Summary: "Route a synthetic request through a plan version (no upstream call)", Tags: tags, Permission: auth.PermConfigRead},
		func(ctx context.Context, in *simIn) (*simOut, error) {
			r, err := simulate(ctx, d, in.ID, in.Body, "simulation")
			if err != nil {
				return nil, err
			}
			return &simOut{Body: *r}, nil
		})

	type replayIn struct {
		ID   string `path:"id" maxLength:"64"`
		Body struct {
			VersionID  string `json:"version_id,omitempty" doc:"Plan version to replay against (default: currently published)."`
			HealthMode string `json:"health_mode,omitempty" enum:"current,assume_available,"`
		}
	}
	type replayOut struct{ Body ReplayResult }
	api.Register(a, api.Op{ID: "replay-request", Method: http.MethodPost, Path: "/activity/requests/{id}/replay",
		Summary: "Re-evaluate a recorded request's routing with its recorded inputs (no upstream call)", Tags: tags, Permission: auth.PermActivityRead},
		func(ctx context.Context, in *replayIn) (*replayOut, error) {
			svc := d.CP()
			var binding, model, protocol, harness, decision, caps, toolNames, sconf string
			var stream int64
			var created int64
			var planID sql.NullString
			err := svc.DB().R.QueryRowContext(ctx, `SELECT COALESCE(binding_id,''), COALESCE(requested_model,''), inbound_protocol, COALESCE(harness,''),
				decision, required_caps, tool_names, session_confidence, stream, created_at, plan_id FROM requests WHERE id = ?`, in.ID).
				Scan(&binding, &model, &protocol, &harness, &decision, &caps, &toolNames, &sconf, &stream, &created, &planID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, api.NotFound("request")
			}
			if err != nil {
				return nil, err
			}
			b := svc.Snapshot().Bindings[binding]
			if b == nil {
				return nil, api.Conflict("the request's client binding no longer exists; replay is not possible")
			}
			at := time.UnixMilli(created).UTC()
			sr := SimulateRequest{VersionID: in.Body.VersionID, BindingID: binding, Model: model, Protocol: protocol, Harness: harness,
				Stream: stream == 1, SessionConfidence: sconf, At: &at, HealthMode: in.Body.HealthMode}
			_ = json.Unmarshal([]byte(caps), &sr.Required)
			_ = json.Unmarshal([]byte(toolNames), &sr.ToolNames)
			res, err := simulate(ctx, d, b.Spec.PlanID, sr, "replay")
			if err != nil {
				return nil, err
			}
			out := &replayOut{Body: ReplayResult{Recorded: json.RawMessage(decision), Replay: *res,
				Notes: []string{"Replay re-evaluates routing with the recorded model, protocol, harness, capabilities, tool names and time.",
					"Prompt content is never stored or re-sent; no upstream request is made.",
					"Account health uses " + nonEmpty(sr.HealthMode, "current") + " state, which may differ from the original request."}}}
			var rec routing.Decision
			if json.Unmarshal([]byte(decision), &rec) == nil {
				first := func(d *routing.Decision) string {
					if d == nil || len(d.Candidates) == 0 {
						return ""
					}
					return d.Candidates[0].AccountID + "|" + d.Candidates[0].Model
				}
				out.Body.Changed = first(&rec) != first(res.Decision) || (res.Error != "") != (len(rec.Candidates) == 0)
			}
			return &replayOut{Body: out.Body}, nil
		})
}

func nonEmpty(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func simulate(ctx context.Context, d Deps, planIDOrName string, in SimulateRequest, mode string) (*SimulationResult, error) {
	svc := d.CP()
	snap := svc.Snapshot()
	plan, err := svc.Get(ctx, cp.PlanDef, planIDOrName)
	if err != nil {
		return nil, api.NotFound("routing plan")
	}
	pv := snap.Plans[plan.ID]
	if pv == nil {
		return nil, api.NotFound("routing plan")
	}
	trial := *pv
	if in.VersionID != "" || pv.Published == nil {
		vid := in.VersionID
		if vid == "" {
			return nil, api.Invalid("the plan has no published version; pass version_id to simulate a draft")
		}
		v, err := svc.GetVersion(ctx, plan.ID, vid)
		if err != nil {
			return nil, api.NotFound("plan version")
		}
		errs := cp.ValidateDocument(&v.Document)
		errs = append(errs, cp.ValidateDocumentRefs(snap, &v.Document)...)
		comp, cerrs := routing.CompileEngine(v.Document.Engine)
		errs = append(errs, cerrs...)
		if len(errs) > 0 {
			fields := make([]api.FieldError, 0, len(errs))
			for _, e := range errs {
				fields = append(fields, api.FieldError{Location: e.Location, Message: e.Message})
			}
			return nil, api.Invalid("the version does not validate", fields...)
		}
		trial.Published = &cp.VersionView{ID: v.ID, Version: v.Version, Document: v.Document, Compiled: comp}
	}
	var binding *cp.Res[cp.BindingSpec]
	if in.BindingID != "" {
		binding = snap.Bindings[in.BindingID]
		if binding == nil {
			return nil, api.NotFound("client binding")
		}
	} else {
		binding = &cp.Res[cp.BindingSpec]{ID: "simulation", Name: "simulation", Spec: cp.BindingSpec{PlanID: plan.ID, Enabled: true}}
	}
	now := time.Now().UTC()
	if in.At != nil {
		now = in.At.UTC()
	}
	health := d.Health
	if in.HealthMode == "assume_available" || health == nil {
		health = func(string) routing.Health { return routing.Health{Available: true} }
	}
	if len(in.HealthOverrides) > 0 {
		base := health
		health = func(id string) routing.Health {
			o, ok := in.HealthOverrides[id]
			if !ok {
				return base(id)
			}
			h := routing.Health{Available: o.Available, Reason: o.Reason, Remaining: o.Remaining, Stale: o.Stale}
			if o.ResetInMinutes != nil {
				t := now.Add(time.Duration(*o.ResetInMinutes * float64(time.Minute)))
				h.ResetAt = &t
			}
			if !o.Available && h.Reason == "" {
				h.Reason = "simulated unavailable"
			}
			return h
		}
	}
	required := in.Required
	if in.Stream && !contains(required, "streaming") {
		required = append(required, "streaming")
	}
	if len(in.ToolNames) > 0 && !contains(required, "tools") {
		required = append(required, "tools")
	}
	if in.Images && !contains(required, "vision") {
		required = append(required, "vision")
	}
	intentSrc := ""
	if in.Intent != "" {
		intentSrc = "declared"
	}
	refs := map[string]string{}
	for k, v := range binding.Spec.ExternalRefs {
		refs[k] = v
	}
	project := binding.Spec.ProjectID
	ws := ""
	if p := snap.Projects[project]; p != nil {
		ws = p.Spec.WorkspaceID
	}
	dec, derr := routing.Decide(routing.Input{Snapshot: snap, Binding: binding, Plan: &trial, RequestedModel: in.Model, Required: cp.SortedUnique(required),
		Inbound: ir.Protocol(in.Protocol), RequestSeed: "simulation", Now: now, HealthOf: health, UpstreamFor: d.UpstreamFor,
		Harness: nonEmpty(in.Harness, "generic"), HarnessConfidence: "simulated", Stream: in.Stream, ToolNames: in.ToolNames, Images: in.Images,
		InputChars: in.InputChars, Intent: in.Intent, IntentSource: intentSrc, SessionConfidence: nonEmpty(in.SessionConfidence, "unknown"),
		Refs: refs, ProjectID: project, WorkspaceID: ws})
	res := &SimulationResult{Mode: mode, Decision: dec}
	if trial.Published != nil {
		res.Version = trial.Published.Version
	}
	if dec != nil {
		dec.Mode = mode
	}
	if derr != nil {
		res.Error = derr.Error()
		var re *routing.Error
		if errors.As(derr, &re) {
			res.Kind = string(re.Kind)
		}
	}
	return res, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
