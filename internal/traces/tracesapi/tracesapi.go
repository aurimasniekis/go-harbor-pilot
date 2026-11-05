// Package tracesapi exposes short-lived traces. Content access, export and
// deletion are audited; unredacted content needs its own permission.
package tracesapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

// Deps are the API's dependencies.
type Deps struct {
	Traces   func() *traces.Service
	Main     func() *store.DB // audit log
	Snapshot func() *cp.Snapshot
}

func notFound(err error) error {
	if errors.Is(err, traces.ErrNotFound) || errors.Is(err, traces.ErrErased) {
		return api.Errorf(http.StatusNotFound, "not_found", "trace not found or expired")
	}
	return err
}

func record(ctx context.Context, d Deps, action, id, outcome string, details map[string]any) {
	_ = audit.Record(ctx, d.Main().W, audit.Entry{Action: action, TargetKind: "trace", TargetID: id, Outcome: outcome, Details: details})
}

// Register mounts the endpoints.
func Register(a *api.API, d Deps) {
	tags := []string{"Traces"}
	type listIn struct {
		RequestID string `query:"request_id" maxLength:"64"`
		SessionID string `query:"session_id" maxLength:"64"`
		BindingID string `query:"binding_id" maxLength:"64"`
		Limit     int    `query:"limit" minimum:"0" maximum:"500"`
	}
	type listOut struct {
		Body struct {
			Items     []traces.Meta `json:"items"`
			Dropped   int64         `json:"dropped" doc:"Captures dropped because the writer was behind (since start)."`
			OverQuota int64         `json:"over_quota" doc:"Captures skipped by per-plan quotas (since start)."`
		}
	}
	api.Register(a, api.Op{ID: "list-traces", Method: http.MethodGet, Path: "/traces", Summary: "List live traces (metadata only)",
		Tags: tags, Permission: auth.PermTraceMetadataRead}, func(ctx context.Context, in *listIn) (*listOut, error) {
		items, err := d.Traces().List(ctx, traces.Filter{RequestID: in.RequestID, SessionID: in.SessionID, BindingID: in.BindingID, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Items, out.Body.Dropped, out.Body.OverQuota = items, d.Traces().Dropped(), d.Traces().OverQuota()
		return out, nil
	})

	type getIn struct {
		ID string `path:"id"`
	}
	type getOut struct{ Body *traces.Content }
	load := func(ctx context.Context, id, action string) (*traces.Content, error) {
		c, err := d.Traces().Get(ctx, id)
		if err != nil {
			record(ctx, d, action, id, audit.Failed, map[string]any{"reason": "not found or expired"})
			return nil, notFound(err)
		}
		if c.Mode == cp.TraceUnredacted && !auth.FromContext(ctx).Can(auth.PermTraceContentUnredacted) {
			record(ctx, d, action, id, audit.Denied, map[string]any{"mode": c.Mode})
			return nil, api.Errorf(http.StatusForbidden, "forbidden", "unredacted trace content requires the traces.content.unredacted permission")
		}
		return c, nil
	}
	api.Register(a, api.Op{ID: "get-trace", Method: http.MethodGet, Path: "/traces/{id}", Summary: "Read trace content",
		Tags: tags, Permission: auth.PermTraceContentRead}, func(ctx context.Context, in *getIn) (*getOut, error) {
		c, err := load(ctx, in.ID, "trace.read")
		if err != nil {
			return nil, err
		}
		record(ctx, d, "trace.read", in.ID, audit.Success, map[string]any{"mode": c.Mode})
		return &getOut{Body: c}, nil
	})

	type expOut struct {
		ContentType        string `header:"Content-Type"`
		ContentDisposition string `header:"Content-Disposition"`
		Body               []byte
	}
	api.Register(a, api.Op{ID: "export-trace", Method: http.MethodGet, Path: "/traces/{id}/export", Summary: "Download trace content",
		Description: "Allowed only when the trace policy permits export. Exports are generated on demand and never cached.",
		Tags:        tags, Permission: auth.PermTraceExport}, func(ctx context.Context, in *getIn) (*expOut, error) {
		c, err := load(ctx, in.ID, "trace.export")
		if err != nil {
			return nil, err
		}
		pol := d.Snapshot().TracePols[c.PolicyID]
		if pol == nil || !pol.Spec.AllowExport {
			record(ctx, d, "trace.export", in.ID, audit.Denied, map[string]any{"reason": "export not permitted by policy"})
			return nil, api.Errorf(http.StatusForbidden, "forbidden", "the trace policy does not permit export")
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		record(ctx, d, "trace.export", in.ID, audit.Success, map[string]any{"mode": c.Mode, "bytes": len(b)})
		return &expOut{ContentType: "application/json", ContentDisposition: `attachment; filename="` + c.ID + `.json"`, Body: b}, nil
	})

	type searchIn struct {
		Q     string `query:"q" required:"true" maxLength:"200"`
		Limit int    `query:"limit" minimum:"0" maximum:"100"`
	}
	type searchOut struct {
		Body struct {
			Items []traces.Hit `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "search-traces", Method: http.MethodGet, Path: "/traces/search", Summary: "Search redacted trace text",
		Tags: tags, Permission: auth.PermTraceContentRead}, func(ctx context.Context, in *searchIn) (*searchOut, error) {
		hits, err := d.Traces().Search(ctx, in.Q, in.Limit)
		if err != nil {
			return nil, err
		}
		record(ctx, d, "trace.search", "", audit.Success, map[string]any{"results": len(hits)})
		out := &searchOut{}
		out.Body.Items = hits
		return out, nil
	})

	type delIn struct {
		ID string `path:"id"`
	}
	api.Register(a, api.Op{ID: "delete-trace", Method: http.MethodDelete, Path: "/traces/{id}", Summary: "Delete a trace",
		Tags: tags, Permission: auth.PermTraceDelete, Status: http.StatusNoContent}, func(ctx context.Context, in *delIn) (*struct{}, error) {
		n, err := d.Traces().Delete(ctx, in.ID, "", "")
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, api.NotFound("trace")
		}
		record(ctx, d, "trace.delete", in.ID, audit.Success, nil)
		return nil, nil
	})
	type bulkIn struct {
		RequestID string `query:"request_id" maxLength:"64"`
		SessionID string `query:"session_id" maxLength:"64"`
	}
	type bulkOut struct {
		Body struct {
			Deleted int64 `json:"deleted"`
		}
	}
	api.Register(a, api.Op{ID: "delete-traces", Method: http.MethodDelete, Path: "/traces", Summary: "Delete traces of a request or session",
		Tags: tags, Permission: auth.PermTraceDelete}, func(ctx context.Context, in *bulkIn) (*bulkOut, error) {
		if in.RequestID == "" && in.SessionID == "" {
			return nil, api.Invalid("request_id or session_id is required")
		}
		n, err := d.Traces().Delete(ctx, "", in.RequestID, in.SessionID)
		if err != nil {
			return nil, err
		}
		record(ctx, d, "trace.delete", "", audit.Success, map[string]any{"request_id": in.RequestID, "session_id": in.SessionID, "deleted": n})
		out := &bulkOut{}
		out.Body.Deleted = n
		return out, nil
	})
}
