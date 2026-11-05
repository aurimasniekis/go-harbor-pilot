package cpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// ---------- plan versions ----------

func registerVersions(a *api.API, svc func() *cp.Service) {
	tags := []string{"Routing plans"}
	base := "/routing-plans/{id}/versions"
	type planIn struct {
		ID string `path:"id" maxLength:"255"`
	}
	type listOut struct{ Body VersionList }
	plan := func(ctx context.Context, id string) (*cp.Object, error) {
		o, err := svc().Get(ctx, cp.PlanDef, id)
		return o, mapErr(err)
	}
	api.Register(a, api.Op{ID: "list-plan-versions", Method: http.MethodGet, Path: base, Summary: "List versions of a routing plan",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *planIn) (*listOut, error) {
		p, err := plan(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		vs, err := svc().ListVersions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Items = vs
		if out.Body.Items == nil {
			out.Body.Items = []*cp.Version{}
		}
		return out, nil
	})
	type verIn struct {
		ID        string `path:"id" maxLength:"255"`
		VersionID string `path:"version_id" maxLength:"64"`
	}
	type verOut struct{ Body *cp.Version }
	api.Register(a, api.Op{ID: "get-plan-version", Method: http.MethodGet, Path: base + "/{version_id}", Summary: "Get a routing plan version",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *verIn) (*verOut, error) {
		p, err := plan(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		v, err := svc().GetVersion(ctx, p.ID, in.VersionID)
		return &verOut{Body: v}, mapErr(err)
	})
	type createDraftIn struct {
		ManagedByHeader
		ID   string `path:"id" maxLength:"255"`
		Body DraftRequest
	}
	api.Register(a, api.Op{ID: "create-plan-draft", Method: http.MethodPost, Path: base, Summary: "Create a draft version",
		Tags: tags, Permission: auth.PermConfigWrite, Status: http.StatusCreated}, func(ctx context.Context, in *createDraftIn) (*verOut, error) {
		p, err := plan(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(in.Body.Document)
		v, err := svc().SaveDraft(ctx, p.ID, "", raw, in.Body.Note, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy)})
		return &verOut{Body: v}, mapErr(err)
	})
	type updateDraftIn struct {
		ManagedByHeader
		ID        string `path:"id" maxLength:"255"`
		VersionID string `path:"version_id" maxLength:"64"`
		Body      DraftRequest
	}
	api.Register(a, api.Op{ID: "replace-plan-draft", Method: http.MethodPut, Path: base + "/{version_id}", Summary: "Replace a draft version",
		Tags: tags, Permission: auth.PermConfigWrite}, func(ctx context.Context, in *updateDraftIn) (*verOut, error) {
		p, err := plan(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(in.Body.Document)
		v, err := svc().SaveDraft(ctx, p.ID, in.VersionID, raw, in.Body.Note, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy)})
		return &verOut{Body: v}, mapErr(err)
	})
	api.Register(a, api.Op{ID: "discard-plan-draft", Method: http.MethodDelete, Path: base + "/{version_id}", Summary: "Discard a draft version",
		Tags: tags, Permission: auth.PermConfigWrite, Status: http.StatusNoContent}, func(ctx context.Context, in *verIn) (*struct{}, error) {
		p, err := plan(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return nil, mapErr(svc().DiscardDraft(ctx, p.ID, in.VersionID, cp.Mutation{ManagedBy: managedBy(ctx, "")}))
	})
	type validateOut struct{ Body *cp.ValidationReport }
	api.Register(a, api.Op{ID: "validate-plan-version", Method: http.MethodPost, Path: base + "/{version_id}/validate",
		Summary: "Validate a version against the current configuration", Tags: tags, Permission: auth.PermConfigRead},
		func(ctx context.Context, in *verIn) (*validateOut, error) {
			p, err := plan(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			r, err := svc().ValidateVersion(ctx, p.ID, in.VersionID)
			return &validateOut{Body: r}, mapErr(err)
		})
	type publishIn struct {
		ManagedByHeader
		ID        string `path:"id" maxLength:"255"`
		VersionID string `path:"version_id" maxLength:"64"`
	}
	api.Register(a, api.Op{ID: "publish-plan-version", Method: http.MethodPost, Path: base + "/{version_id}/publish",
		Summary: "Publish a draft (the previous version is superseded)", Tags: tags, Permission: auth.PermPolicyPublish},
		func(ctx context.Context, in *publishIn) (*verOut, error) {
			p, err := plan(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			v, err := svc().Publish(ctx, p.ID, in.VersionID, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy)})
			return &verOut{Body: v}, mapErr(err)
		})
	type rollbackIn struct {
		ManagedByHeader
		ID   string `path:"id" maxLength:"255"`
		Body RollbackRequest
	}
	api.Register(a, api.Op{ID: "rollback-plan", Method: http.MethodPost, Path: "/routing-plans/{id}/rollback",
		Summary: "Publish a copy of a previous version", Tags: tags, Permission: auth.PermPolicyPublish},
		func(ctx context.Context, in *rollbackIn) (*verOut, error) {
			p, err := plan(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			v, err := svc().Rollback(ctx, p.ID, in.Body.VersionID, cp.Mutation{ManagedBy: managedBy(ctx, in.ManagedBy)})
			return &verOut{Body: v}, mapErr(err)
		})
}

// ---------- gateway credentials ----------

func registerCredentials(a *api.API, svc func() *cp.Service, origin func() string) {
	tags := []string{"Gateway credentials"}
	base := "/client-bindings/{id}/credentials"
	type bindingIn struct {
		ID       string `path:"id" maxLength:"255"`
		Children bool   `query:"children" doc:"Include short-lived child credentials."`
	}
	type listOut struct{ Body CredentialList }
	binding := func(ctx context.Context, id string) (*cp.Object, error) {
		o, err := svc().Get(ctx, cp.BindingDef, id)
		return o, mapErr(err)
	}
	api.Register(a, api.Op{ID: "list-gateway-credentials", Method: http.MethodGet, Path: base, Summary: "List gateway credentials of a client binding",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *bindingIn) (*listOut, error) {
		b, err := binding(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		cs, err := svc().ListCredentials(ctx, b.ID, in.Children)
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Items = cs
		if out.Body.Items == nil {
			out.Body.Items = []*cp.Credential{}
		}
		return out, nil
	})
	type issueIn struct {
		ManagedByHeader
		ID   string `path:"id" maxLength:"255"`
		Body IssueCredentialRequest
	}
	type issueOut struct{ Body IssuedCredential }
	api.Register(a, api.Op{ID: "issue-gateway-credential", Method: http.MethodPost, Path: base, Summary: "Issue a gateway credential (token returned once)",
		Tags: tags, Permission: auth.PermTokensManage, Status: http.StatusCreated}, func(ctx context.Context, in *issueIn) (*issueOut, error) {
		b, err := binding(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		c, tok, err := svc().IssueCredential(ctx, b.ID, cp.CredentialInput{Name: in.Body.Name, Scopes: in.Body.Scopes,
			TTL: time.Duration(in.Body.TTLSeconds) * time.Second, BoundRefs: in.Body.BoundRefs, ManagedBy: managedBy(ctx, in.ManagedBy)})
		if err != nil {
			return nil, mapErr(err)
		}
		out := &issueOut{}
		out.Body.Credential, out.Body.Token, out.Body.BaseURLs = c, tok, BaseURLs(origin())
		return out, nil
	})
	type revokeIn struct {
		ID           string `path:"id" maxLength:"255"`
		CredentialID string `path:"credential_id" maxLength:"64"`
	}
	api.Register(a, api.Op{ID: "revoke-gateway-credential", Method: http.MethodDelete, Path: base + "/{credential_id}",
		Summary: "Revoke a gateway credential and its children", Tags: tags, Permission: auth.PermTokensManage, Status: http.StatusNoContent},
		func(ctx context.Context, in *revokeIn) (*struct{}, error) {
			b, err := binding(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return nil, mapErr(svc().RevokeCredential(ctx, b.ID, in.CredentialID, "revoked via API"))
		})
}

// BaseURLs lists gateway base URLs by client protocol.
func BaseURLs(origin string) map[string]string {
	return map[string]string{
		"anthropic":          origin,
		"openai":             origin + "/v1",
		"gemini":             origin,
		"anthropic_explicit": origin + "/anthropic",
		"openai_explicit":    origin + "/openai/v1",
	}
}

// ---------- configuration ----------

// RevisionDTO is a configuration revision.
type RevisionDTO struct {
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	ActorID   string    `json:"actor_id"`
	ActorKind string    `json:"actor_kind"`
	Source    string    `json:"source"`
	Summary   string    `json:"summary"`
}

// EffectiveDTO summarizes the active compiled configuration.
type EffectiveDTO struct {
	ConfigRevision int64            `json:"config_revision"`
	CompiledAt     time.Time        `json:"compiled_at"`
	Counts         map[string]int   `json:"counts"`
	Plans          []EffectivePlan  `json:"plans"`
	Aliases        []EffectiveAlias `json:"aliases"`
	Drifted        []DriftedObject  `json:"drifted"`
}

// EffectivePlan is a plan and its published version.
type EffectivePlan struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	PublishedVersion int64  `json:"published_version" doc:"0 when no version is published."`
	VersionID        string `json:"version_id,omitempty"`
	Bindings         int    `json:"bindings"`
}

// EffectiveAlias shows alias resolution.
type EffectiveAlias struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Chain  []string `json:"chain"`
	PoolID string   `json:"pool_id"`
}

// DriftedObject is a Terraform-managed object edited elsewhere.
type DriftedObject struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func registerConfig(a *api.API, svc func() *cp.Service) {
	tags := []string{"Configuration"}
	type revIn struct {
		api.PageParams
	}
	type revOut struct{ Body api.Page[RevisionDTO] }
	api.Register(a, api.Op{ID: "list-config-revisions", Method: http.MethodGet, Path: "/config/revisions", Summary: "List configuration revisions (newest first)",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *revIn) (*revOut, error) {
		before, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		q := `SELECT revision, created_at, actor_id, actor_kind, source, summary FROM config_revisions`
		args := []any{}
		if before != "" {
			q += ` WHERE revision < CAST(? AS INTEGER)`
			args = append(args, before)
		}
		q += ` ORDER BY revision DESC LIMIT ?`
		args = append(args, lim+1)
		rows, err := svc().DB().R.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var items []RevisionDTO
		for rows.Next() {
			var r RevisionDTO
			var at int64
			if err := rows.Scan(&r.Revision, &at, &r.ActorID, &r.ActorKind, &r.Source, &r.Summary); err != nil {
				return nil, err
			}
			r.CreatedAt = time.UnixMilli(at).UTC()
			items = append(items, r)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return &revOut{Body: api.Paginate(items, lim, func(r RevisionDTO) string { return itoa(r.Revision) })}, nil
	})

	type effOut struct{ Body EffectiveDTO }
	api.Register(a, api.Op{ID: "get-effective-config", Method: http.MethodGet, Path: "/config/effective", Summary: "The active compiled configuration",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*effOut, error) {
		s := svc().Snapshot()
		d := EffectiveDTO{ConfigRevision: s.ConfigRevision, CompiledAt: s.CompiledAt, Plans: []EffectivePlan{}, Aliases: []EffectiveAlias{}, Drifted: []DriftedObject{},
			Counts: map[string]int{"workspaces": len(s.Workspaces), "projects": len(s.Projects), "providers": len(s.Providers), "provider_accounts": len(s.Accounts),
				"subscription_entitlements": len(s.Entitlements), "pools": len(s.Pools), "model_aliases": len(s.Aliases), "routing_plans": len(s.Plans), "client_bindings": len(s.Bindings),
				"quota_monitors": len(s.Monitors), "budgets": len(s.Budgets), "privacy_policies": len(s.Privacy), "trace_policies": len(s.TracePols),
				"event_destinations": len(s.Destinations), "notification_channels": len(s.Channels), "notification_rules": len(s.Rules)}}
		bindingsPerPlan := map[string]int{}
		for _, b := range s.Bindings {
			bindingsPerPlan[b.Spec.PlanID]++
		}
		for _, p := range s.Plans {
			ep := EffectivePlan{ID: p.ID, Name: p.Name, Bindings: bindingsPerPlan[p.ID]}
			if p.Published != nil {
				ep.PublishedVersion, ep.VersionID = p.Published.Version, p.Published.ID
			}
			d.Plans = append(d.Plans, ep)
		}
		sort.Slice(d.Plans, func(i, j int) bool { return d.Plans[i].Name < d.Plans[j].Name })
		for _, al := range s.Aliases {
			names := []string{}
			for _, c := range s.AliasChain[al.ID] {
				names = append(names, s.Aliases[c].Name)
			}
			d.Aliases = append(d.Aliases, EffectiveAlias{ID: al.ID, Name: al.Name, Chain: names, PoolID: s.AliasPool[al.ID]})
		}
		sort.Slice(d.Aliases, func(i, j int) bool { return d.Aliases[i].Name < d.Aliases[j].Name })
		rows, err := svc().DB().R.QueryContext(ctx, `SELECT id, kind, name FROM resources WHERE drift_state = 'drifted' ORDER BY kind, name`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var x DriftedObject
			if err := rows.Scan(&x.ID, &x.Kind, &x.Name); err != nil {
				return nil, err
			}
			d.Drifted = append(d.Drifted, x)
		}
		return &effOut{Body: d}, rows.Err()
	})

	type exportIn struct {
		Kinds []string `query:"kind" doc:"Limit to these kinds (repeatable)."`
	}
	type exportOut struct{ Body *cp.Bundle }
	api.Register(a, api.Op{ID: "export-config", Method: http.MethodGet, Path: "/config/export", Summary: "Export managed configuration as a portable bundle (no secrets)",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *exportIn) (*exportOut, error) {
		b, err := svc().Export(ctx, in.Kinds)
		return &exportOut{Body: b}, mapErr(err)
	})
	type importIn struct {
		ManagedByHeader
		Body ImportRequest
	}
	type importOut struct{ Body *cp.ImportResult }
	api.Register(a, api.Op{ID: "import-config", Method: http.MethodPost, Path: "/config/import", Summary: "Import a bundle atomically (supports dry run)",
		Tags: tags, Permission: auth.PermPolicyPublish, MaxBodyBytes: 16 << 20}, func(ctx context.Context, in *importIn) (*importOut, error) {
		mb := managedBy(ctx, in.ManagedBy)
		if mb == cp.ManagedUI || mb == cp.ManagedAPI {
			mb = cp.ManagedImport
		}
		r, err := svc().Import(ctx, &in.Body.Bundle, cp.ImportOptions{Modes: in.Body.Modes, DryRun: in.Body.DryRun,
			PublishPlans: in.Body.PublishPlans, AllowTerraform: in.Body.AllowTerraform, ManagedBy: mb})
		return &importOut{Body: r}, mapErr(err)
	})
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// VersionList lists plan versions.
type VersionList struct {
	Items []*cp.Version `json:"items"`
}

// DraftRequest creates or replaces a draft version.
type DraftRequest struct {
	Document cp.PlanDocument `json:"document"`
	Note     string          `json:"note" maxLength:"500"`
}

// RollbackRequest selects the version to restore.
type RollbackRequest struct {
	VersionID string `json:"version_id" doc:"A previously published version to restore."`
}

// CredentialList lists gateway credentials.
type CredentialList struct {
	Items []*cp.Credential `json:"items"`
}

// IssueCredentialRequest describes a credential to issue.
type IssueCredentialRequest struct {
	Name       string            `json:"name" minLength:"1" maxLength:"80"`
	Scopes     []string          `json:"scopes,omitempty" doc:"Default: gateway:invoke, gateway:models."`
	TTLSeconds int64             `json:"ttl_seconds,omitempty" minimum:"0" maximum:"63072000" doc:"0 = no expiry."`
	BoundRefs  map[string]string `json:"bound_refs,omitempty" doc:"Trusted references bound to this credential."`
}

// IssuedCredential is returned once when a credential is issued.
type IssuedCredential struct {
	Credential *cp.Credential    `json:"credential"`
	Token      string            `json:"token" doc:"The bearer token (hpk_…). Returned exactly once."`
	BaseURLs   map[string]string `json:"base_urls" doc:"Base URLs per protocol for client configuration."`
}

// ImportRequest imports a bundle.
type ImportRequest struct {
	Bundle         cp.Bundle         `json:"bundle"`
	Modes          map[string]string `json:"modes,omitempty" doc:"Per kind: merge (default) or replace (also delete objects absent from the bundle)."`
	DryRun         bool              `json:"dry_run"`
	PublishPlans   bool              `json:"publish_plans" doc:"Publish the bundle's plan documents when they differ."`
	AllowTerraform bool              `json:"allow_terraform" doc:"Overwrite Terraform-managed objects (they will show as drifted)."`
}
