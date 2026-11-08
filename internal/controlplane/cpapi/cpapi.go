// Package cpapi exposes managed configuration on the control-plane API:
// typed CRUD for every resource kind, routing plan versions, gateway
// credentials, configuration revisions, the effective configuration and
// bundle export/import.
package cpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// Resource is the API representation of a managed object of spec type S.
type Resource[S any] struct {
	ID             string                   `json:"id"`
	Kind           string                   `json:"kind"`
	Name           string                   `json:"name"`
	Spec           S                        `json:"spec"`
	Revision       int64                    `json:"revision" doc:"Use as If-Match (\"r<revision>\") for optimistic concurrency."`
	ManagedBy      string                   `json:"managed_by" enum:"ui,api,terraform,cli,import,demo"`
	DriftState     string                   `json:"drift_state" enum:"in_sync,drifted" doc:"drifted when a Terraform-managed object was changed outside Terraform."`
	DriftedAt      *time.Time               `json:"drifted_at,omitempty"`
	DriftedBy      string                   `json:"drifted_by,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
	CreatedBy      string                   `json:"created_by"`
	UpdatedBy      string                   `json:"updated_by"`
	ConfigRevision int64                    `json:"config_revision"`
	Secrets        map[string]cp.SecretInfo `json:"secrets,omitempty" doc:"Write-only secret presence; values are never returned."`
}

// WriteBody is the create/replace request body.
type WriteBody[S any] struct {
	Name    string             `json:"name" minLength:"1" maxLength:"255"`
	Spec    S                  `json:"spec"`
	Secrets map[string]*string `json:"secrets,omitempty" doc:"Write-only secret values. Omit a key to keep it; empty string clears it."`
}

func toDTO[S any](o *cp.Object) (Resource[S], error) {
	spec, err := cp.Decode[S](o.Spec)
	return Resource[S]{ID: o.ID, Kind: o.Kind, Name: o.Name, Spec: spec, Revision: o.Revision, ManagedBy: o.ManagedBy,
		DriftState: o.DriftState, DriftedAt: o.DriftedAt, DriftedBy: o.DriftedBy, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
		CreatedBy: o.CreatedBy, UpdatedBy: o.UpdatedBy, ConfigRevision: o.ConfigRevision, Secrets: o.Secrets}, err
}

// ManagedByHeader is set by clients to declare the change source.
type ManagedByHeader struct {
	ManagedBy string `header:"X-HP-Managed-By" enum:"ui,api,terraform,cli,import," required:"false" doc:"Change source; Terraform sends terraform."`
}

func managedBy(ctx context.Context, h string) string {
	if h != "" && cp.ValidManagedBy(h) {
		return h
	}
	if auth.FromContext(ctx).Kind == auth.KindService {
		return cp.ManagedAPI
	}
	return cp.ManagedUI
}

// mapErr converts service errors into API errors.
func mapErr(err error) error {
	var ve *cp.ValidationError
	var re *cp.RevisionError
	var ref *cp.ReferencedError
	var pe *cp.PublishedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ve):
		fields := make([]api.FieldError, 0, len(ve.Errors))
		for _, e := range ve.Errors {
			fields = append(fields, api.FieldError{Location: e.Location, Message: e.Message})
		}
		return api.Invalid(ve.Error(), fields...)
	case errors.As(err, &re):
		return api.PreconditionFailed(api.ETag(re.Current))
	case errors.As(err, &pe):
		return api.PreconditionFailed(`"` + pe.Current + `"`)
	case errors.As(err, &ref):
		return api.Conflict(ref.Error()).WithDetails(map[string]any{"referenced_by": ref.By})
	case errors.Is(err, cp.ErrNotFound):
		return api.NotFound("object")
	case errors.Is(err, cp.ErrNameTaken):
		return api.Conflict(err.Error())
	case errors.Is(err, cp.ErrCredentialInvalid):
		return api.Errorf(http.StatusForbidden, "forbidden", err.Error())
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return err
}

// RegisterKind adds list/get/create/replace/delete/history for one kind.
func RegisterKind[S any](a *api.API, svc func() *cp.Service, def *cp.Def[S]) {
	base := "/" + def.Plural
	tags := []string{def.Title + "s"}
	type one struct {
		ETag string `header:"ETag"`
		Body Resource[S]
	}
	type listIn struct {
		api.PageParams
	}
	type listOut struct{ Body api.Page[Resource[S]] }
	api.Register(a, api.Op{ID: "list-" + def.Plural, Method: http.MethodGet, Path: base, Summary: "List " + def.Title + "s",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *listIn) (*listOut, error) {
		after, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		objs, err := svc().List(ctx, def, after, lim+1)
		if err != nil {
			return nil, err
		}
		items := make([]Resource[S], 0, len(objs))
		for _, o := range objs {
			d, err := toDTO[S](o)
			if err != nil {
				return nil, err
			}
			items = append(items, d)
		}
		return &listOut{Body: api.Paginate(items, lim, func(r Resource[S]) string { return r.ID })}, nil
	})

	type idIn struct {
		ID string `path:"id" maxLength:"255" doc:"Object id or name."`
	}
	api.Register(a, api.Op{ID: "get-" + def.Name, Method: http.MethodGet, Path: base + "/{id}", Summary: "Get a " + def.Title,
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *idIn) (*one, error) {
		o, err := svc().Get(ctx, def, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		d, err := toDTO[S](o)
		return &one{ETag: api.ETag(o.Revision), Body: d}, err
	})

	perm := auth.PermConfigWrite
	if len(def.Secrets) > 0 {
		perm = auth.PermSecretsWrite
	}
	type createIn struct {
		ManagedByHeader
		Body WriteBody[S]
	}
	api.Register(a, api.Op{ID: "create-" + def.Name, Method: http.MethodPost, Path: base, Summary: "Create a " + def.Title,
		Tags: tags, Permission: perm, Status: http.StatusCreated}, func(ctx context.Context, in *createIn) (*one, error) {
		raw, _ := json.Marshal(in.Body.Spec)
		o, err := svc().Create(ctx, def, in.Body.Name, raw, in.Body.Secrets, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy)})
		if err != nil {
			return nil, mapErr(err)
		}
		d, err := toDTO[S](o)
		return &one{ETag: api.ETag(o.Revision), Body: d}, err
	})

	type replaceIn struct {
		ManagedByHeader
		ID      string `path:"id" maxLength:"255"`
		IfMatch string `header:"If-Match" doc:"Current ETag; required by Terraform, recommended for all clients."`
		Body    WriteBody[S]
	}
	api.Register(a, api.Op{ID: "replace-" + def.Name, Method: http.MethodPut, Path: base + "/{id}", Summary: "Replace a " + def.Title,
		Tags: tags, Permission: perm}, func(ctx context.Context, in *replaceIn) (*one, error) {
		raw, _ := json.Marshal(in.Body.Spec)
		o, err := svc().Replace(ctx, def, in.ID, in.Body.Name, raw, in.Body.Secrets, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy), IfMatch: in.IfMatch})
		if err != nil {
			return nil, mapErr(err)
		}
		d, err := toDTO[S](o)
		return &one{ETag: api.ETag(o.Revision), Body: d}, err
	})

	type deleteIn struct {
		ManagedByHeader
		ID      string `path:"id" maxLength:"255"`
		IfMatch string `header:"If-Match"`
	}
	api.Register(a, api.Op{ID: "delete-" + def.Name, Method: http.MethodDelete, Path: base + "/{id}", Summary: "Delete a " + def.Title,
		Tags: tags, Permission: auth.PermConfigWrite, Status: http.StatusNoContent}, func(ctx context.Context, in *deleteIn) (*struct{}, error) {
		return nil, mapErr(svc().Delete(ctx, def, in.ID, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy), IfMatch: in.IfMatch}))
	})

	type histIn struct {
		ID    string `path:"id" maxLength:"255"`
		Limit int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	}
	type histOut struct{ Body HistoryList }
	api.Register(a, api.Op{ID: "history-" + def.Name, Method: http.MethodGet, Path: base + "/{id}/history", Summary: "Revision history of a " + def.Title,
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *histIn) (*histOut, error) {
		o, err := svc().Get(ctx, def, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		h, err := svc().History(ctx, o.ID, max(in.Limit, 1))
		if err != nil {
			return nil, err
		}
		out := &histOut{}
		out.Body.Items = h
		if out.Body.Items == nil {
			out.Body.Items = []cp.HistoryEntry{}
		}
		return out, nil
	})
}

// Register adds every control-plane operation. svc is resolved lazily so
// the OpenAPI document can be rendered without a live service.
func Register(a *api.API, svc func() *cp.Service, publicOrigin func() string) {
	RegisterKind(a, svc, cp.WorkspaceDef)
	RegisterKind(a, svc, cp.ProjectDef)
	RegisterKind(a, svc, cp.ProviderDef)
	RegisterKind(a, svc, cp.AccountDef)
	RegisterKind(a, svc, cp.EntitlementDef)
	RegisterKind(a, svc, cp.PoolDef)
	RegisterKind(a, svc, cp.AliasDef)
	RegisterKind(a, svc, cp.PlanDef)
	RegisterKind(a, svc, cp.BindingDef)
	RegisterKind(a, svc, cp.QuotaMonitorDef)
	RegisterKind(a, svc, cp.BudgetDef)
	registerBudgetReset(a, svc)
	RegisterKind(a, svc, cp.PrivacyPolicyDef)
	RegisterKind(a, svc, cp.TracePolicyDef)
	RegisterKind(a, svc, cp.DestinationDef)
	RegisterKind(a, svc, cp.ChannelDef)
	RegisterKind(a, svc, cp.RuleDef)
	registerVersions(a, svc)
	registerCredentials(a, svc, publicOrigin)
	registerConfig(a, svc)
}

// registerBudgetReset adds POST /budgets/{id}/reset ("Start over" for a
// one-time budget, D-044).
func registerBudgetReset(a *api.API, svc func() *cp.Service) {
	type one struct {
		ETag string `header:"ETag"`
		Body Resource[cp.BudgetSpec]
	}
	type resetIn struct {
		ManagedByHeader
		ID      string `path:"id" maxLength:"255" doc:"Budget id or name."`
		IfMatch string `header:"If-Match" doc:"Current ETag; recommended for all clients."`
	}
	api.Register(a, api.Op{ID: "reset-budget", Method: http.MethodPost, Path: "/budgets/{id}/reset", Summary: "Start a one-time budget over",
		Description: "Sets spec.starts_at to the server's current time, so spend counts again from now and threshold events can fire again. " +
			"Earlier spend stays in usage reports. Only one-time budgets (period once) can be reset; a budget whose ends_at has passed " +
			"must be extended or cleared first. On a Terraform-managed budget this shows as drift.",
		Tags: []string{cp.BudgetDef.Title + "s"}, Permission: auth.PermConfigWrite}, func(ctx context.Context, in *resetIn) (*one, error) {
		o, err := svc().ResetBudget(ctx, in.ID, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy), IfMatch: in.IfMatch})
		if err != nil {
			return nil, mapErr(err)
		}
		d, err := toDTO[cp.BudgetSpec](o)
		return &one{ETag: api.ETag(o.Revision), Body: d}, err
	})
}

// HistoryList is a list of resource revisions.
type HistoryList struct {
	Items []cp.HistoryEntry `json:"items"`
}
