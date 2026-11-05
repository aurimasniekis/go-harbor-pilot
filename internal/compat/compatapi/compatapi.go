// Package compatapi exposes compatibility breakers, active degradation and
// Protocol Watch.
package compatapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/compat"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Deps are the API's dependencies.
type Deps struct {
	Compat   func() *compat.Manager
	Watch    func() *compat.Watch
	Snapshot func() *cp.Snapshot
	Main     func() *store.DB
	// Probe runs an authorized live canary.
	Probe func(ctx context.Context, providerID, path string) (*ir.Error, error)
}

// Degradation describes a route currently served in a degraded mode.
type Degradation struct {
	Breaker compat.Breaker `json:"breaker"`
	Plans   []PlanEffect   `json:"plans"`
}

// PlanEffect is what a plan does on an open breaker.
type PlanEffect struct {
	PlanID string   `json:"plan_id"`
	Name   string   `json:"name"`
	Action string   `json:"action" enum:"fail,passthrough"`
	Losses []string `json:"losses" doc:"Features lost while degraded (pre-authorized by the plan)."`
}

// Register mounts the endpoints.
func Register(a *api.API, d Deps) {
	tags := []string{"Compatibility"}
	type brOut struct {
		Body struct {
			Items []compat.Breaker `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-breakers", Method: http.MethodGet, Path: "/compat/breakers", Summary: "Compatibility breakers",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*brOut, error) {
		out := &brOut{}
		out.Body.Items = d.Compat().List()
		return out, nil
	})

	type ovIn struct {
		Body struct {
			ProviderID string `json:"provider_id"`
			Path       string `json:"path" doc:"<inbound>><upstream>, e.g. anthropic_messages>anthropic_messages."`
			Override   string `json:"override" enum:"auto,force_open,force_closed"`
			Reason     string `json:"reason" maxLength:"500"`
		}
	}
	type ovOut struct{ Body compat.Breaker }
	api.Register(a, api.Op{ID: "override-breaker", Method: http.MethodPost, Path: "/compat/breakers/override", Summary: "Force a breaker open or closed",
		Tags: tags, Permission: auth.PermCompatManage}, func(ctx context.Context, in *ovIn) (*ovOut, error) {
		if d.Snapshot().Providers[in.Body.ProviderID] == nil || !strings.Contains(in.Body.Path, ">") {
			return nil, api.Invalid("unknown provider or invalid path")
		}
		b, err := d.Compat().SetOverride(ctx, in.Body.ProviderID, in.Body.Path, in.Body.Override, in.Body.Reason)
		if err != nil {
			return nil, err
		}
		_ = audit.Record(ctx, d.Main().W, audit.Entry{Action: "compat.breaker.override", TargetKind: "breaker", TargetID: b.Key,
			Details: map[string]any{"override": in.Body.Override, "reason": in.Body.Reason}})
		return &ovOut{Body: b}, nil
	})

	type probeIn struct {
		Body struct {
			ProviderID string `json:"provider_id"`
			Path       string `json:"path"`
		}
	}
	type probeOut struct {
		Body struct {
			Breaker compat.Breaker `json:"breaker"`
			Result  string         `json:"result" enum:"succeeded,failed"`
			Error   string         `json:"error,omitempty"`
		}
	}
	api.Register(a, api.Op{ID: "probe-breaker", Method: http.MethodPost, Path: "/compat/breakers/probe", Summary: "Run a live canary on an open breaker",
		Description: "Sends one minimal synthetic request through the normal (inspected) path using an account of the provider. This is an explicitly authorized live canary; it consumes a few tokens and never replays client content.",
		Tags:        tags, Permission: auth.PermCompatManage}, func(ctx context.Context, in *probeIn) (*probeOut, error) {
		if !d.Compat().BeginProbe(in.Body.ProviderID, in.Body.Path) {
			return nil, api.Errorf(http.StatusConflict, "conflict", "the breaker is closed or a probe is already running")
		}
		perr, err := d.Probe(ctx, in.Body.ProviderID, in.Body.Path)
		if err != nil {
			d.Compat().ProbeResult(in.Body.ProviderID, in.Body.Path, &ir.Error{Kind: ir.ErrInternal, Message: err.Error()})
			return nil, api.Invalid(err.Error())
		}
		b := d.Compat().ProbeResult(in.Body.ProviderID, in.Body.Path, perr)
		_ = audit.Record(ctx, d.Main().W, audit.Entry{Action: "compat.breaker.probe", TargetKind: "breaker", TargetID: b.Key,
			Details: map[string]any{"succeeded": perr == nil}})
		out := &probeOut{}
		out.Body.Breaker, out.Body.Result = b, "succeeded"
		if perr != nil {
			out.Body.Result, out.Body.Error = "failed", perr.Message
		}
		return out, nil
	})

	type degOut struct {
		Body struct {
			Items []Degradation `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-degradations", Method: http.MethodGet, Path: "/compat/degradations", Summary: "Routes currently degraded and the features lost",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*degOut, error) {
		snap := d.Snapshot()
		out := &degOut{}
		out.Body.Items = []Degradation{}
		for _, b := range d.Compat().List() {
			if b.Effective() == compat.Closed {
				continue
			}
			dg := Degradation{Breaker: b, Plans: []PlanEffect{}}
			for _, p := range snap.Plans {
				if p.Published == nil {
					continue
				}
				c := p.Published.Document.Compatibility
				eff := PlanEffect{PlanID: p.ID, Name: p.Name, Action: c.OnBreakerOpen, Losses: []string{}}
				if c.OnBreakerOpen == "passthrough" {
					eff.Losses = append(eff.Losses, "detailed_usage", "tool_statistics")
					if p.Published.Document.Privacy.PolicyID != "" && slices.Contains(c.AllowedLosses, "masking") {
						eff.Losses = append(eff.Losses, "masking")
					}
					if p.Published.Document.Privacy.TracePolicyID != "" {
						eff.Losses = append(eff.Losses, "traces")
					}
				}
				dg.Plans = append(dg.Plans, eff)
			}
			out.Body.Items = append(out.Body.Items, dg)
		}
		return out, nil
	})

	wtags := []string{"Protocol Watch"}
	type chIn struct {
		Limit int `query:"limit" minimum:"0" maximum:"500"`
	}
	type chOut struct {
		Body struct {
			Items     []compat.Change `json:"items"`
			Processed int64           `json:"processed"`
			Dropped   int64           `json:"dropped" doc:"Signals dropped under pressure; coverage is reduced when non-zero."`
		}
	}
	api.Register(a, api.Op{ID: "list-protocol-changes", Method: http.MethodGet, Path: "/protocol-watch/changes", Summary: "Observed protocol changes",
		Description: "An observed compatibility changelog, not an authoritative provider release history. Absence is not proof of removal.",
		Tags:        wtags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *chIn) (*chOut, error) {
		items, err := d.Watch().Changes(ctx, in.Limit)
		if err != nil {
			return nil, err
		}
		out := &chOut{}
		out.Body.Items = items
		out.Body.Processed, out.Body.Dropped = d.Watch().Coverage()
		return out, nil
	})

	type wiIn struct {
		Status string `query:"status" enum:"new,acknowledged,investigating,fix_available,verified,resolved,ignored," required:"false"`
	}
	type wiOut struct {
		Body struct {
			Items []compat.WorkItem `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-work-items", Method: http.MethodGet, Path: "/protocol-watch/work-items", Summary: "Protocol work items",
		Tags: wtags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *wiIn) (*wiOut, error) {
		items, err := d.Watch().WorkItems(ctx, in.Status)
		if err != nil {
			return nil, err
		}
		out := &wiOut{}
		out.Body.Items = items
		return out, nil
	})
	type oneIn struct {
		ID string `path:"id"`
	}
	type oneOut struct {
		ETag string `header:"ETag"`
		Body compat.WorkItem
	}
	api.Register(a, api.Op{ID: "get-work-item", Method: http.MethodGet, Path: "/protocol-watch/work-items/{id}", Summary: "Get a work item",
		Tags: wtags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *oneIn) (*oneOut, error) {
		wi, err := d.Watch().GetWorkItem(ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, api.NotFound("work item")
		}
		if err != nil {
			return nil, err
		}
		return &oneOut{ETag: api.ETag(wi.Revision), Body: wi}, nil
	})
	type updIn struct {
		ID      string `path:"id"`
		IfMatch string `header:"If-Match"`
		Body    compat.WorkUpdate
	}
	api.Register(a, api.Op{ID: "update-work-item", Method: http.MethodPatch, Path: "/protocol-watch/work-items/{id}", Summary: "Triage a work item",
		Tags: wtags, Permission: auth.PermCompatManage}, func(ctx context.Context, in *updIn) (*oneOut, error) {
		var rev int64
		if v := strings.Trim(strings.TrimPrefix(in.IfMatch, "W/"), `"`); v != "" {
			rev, _ = strconv.ParseInt(strings.TrimPrefix(v, "r"), 10, 64)
		}
		wi, err := d.Watch().UpdateWorkItem(ctx, in.ID, rev, in.Body)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, api.NotFound("work item")
		case errors.Is(err, compat.ErrStale):
			return nil, api.Errorf(http.StatusPreconditionFailed, "precondition_failed", err.Error())
		case err != nil:
			return nil, err
		}
		_ = audit.Record(ctx, d.Main().W, audit.Entry{Action: "protocol_watch.work_item.update", TargetKind: "work_item", TargetID: in.ID})
		return &oneOut{ETag: api.ETag(wi.Revision), Body: wi}, nil
	})
	api.Register(a, api.Op{ID: "generate-work-item-fixture", Method: http.MethodPost, Path: "/protocol-watch/work-items/{id}/fixture",
		Summary: "Generate a synthetic fixture from the structural diff", Description: "Uses placeholder values only; mark it reviewed before use.",
		Tags: wtags, Permission: auth.PermCompatManage}, func(ctx context.Context, in *oneIn) (*oneOut, error) {
		wi, err := d.Watch().GenerateFixture(ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, api.NotFound("work item")
		}
		if err != nil {
			return nil, err
		}
		return &oneOut{ETag: api.ETag(wi.Revision), Body: wi}, nil
	})
}
