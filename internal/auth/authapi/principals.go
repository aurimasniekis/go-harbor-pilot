package authapi

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

// ServicePrincipalDTO is the public representation.
type ServicePrincipalDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	Disabled    bool      `json:"disabled"`
	ManagedBy   string    `json:"managed_by"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Revision    int64     `json:"revision"`
}

// APITokenDTO is token metadata; plaintext is only returned on issue.
type APITokenDTO struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix" doc:"Non-secret token prefix for identification (hpa_<prefix>_…)."`
	Name       string     `json:"name"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `json:"last_used_ip,omitempty"`
}

func spDTO(sp *auth.ServicePrincipal) ServicePrincipalDTO {
	d := ServicePrincipalDTO{ID: sp.ID, Name: sp.Name, Description: sp.Description, Permissions: []string{}, Disabled: sp.Disabled,
		ManagedBy: sp.ManagedBy, CreatedBy: sp.CreatedBy, CreatedAt: sp.CreatedAt, UpdatedAt: sp.UpdatedAt, Revision: sp.Revision}
	for _, p := range sp.Permissions {
		d.Permissions = append(d.Permissions, string(p))
	}
	return d
}

func tokenDTO(t auth.APIToken) APITokenDTO {
	return APITokenDTO{ID: t.ID, Prefix: t.Prefix, Name: t.Name, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, LastUsedIP: t.LastUsedIP}
}

type spBody struct {
	Name        string   `json:"name" pattern:"^[a-z0-9][a-z0-9._-]{0,62}$"`
	Description string   `json:"description,omitempty" maxLength:"500"`
	Permissions []string `json:"permissions" maxItems:"64"`
	Disabled    bool     `json:"disabled,omitempty"`
}

type spOut struct {
	ETag string `header:"ETag"`
	Body ServicePrincipalDTO
}

func perms(in []string) []auth.Permission {
	out := make([]auth.Permission, 0, len(in))
	for _, p := range in {
		out = append(out, auth.Permission(p))
	}
	return out
}

func managedBy(ctx context.Context, header string) string {
	switch header {
	case "terraform", "cli", "import", "api":
		return header
	}
	if auth.FromContext(ctx).Kind == auth.KindService {
		return "api"
	}
	return "ui"
}

func (h *Handlers) registerPrincipals(a *api.API) {
	tags := []string{"Service principals"}
	type listIn struct{ api.PageParams }
	type listOut struct{ Body api.Page[ServicePrincipalDTO] }
	api.Register(a, api.Op{ID: "list-service-principals", Method: http.MethodGet, Path: "/service-principals", Summary: "List service principals",
		Tags: tags, Permission: auth.PermUsersManage}, func(ctx context.Context, in *listIn) (*listOut, error) {
		after, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		sps, err := h.Auth.ListServicePrincipals(ctx, after, lim+1)
		if err != nil {
			return nil, err
		}
		d := make([]ServicePrincipalDTO, 0, len(sps))
		for _, sp := range sps {
			d = append(d, spDTO(sp))
		}
		return &listOut{Body: api.Paginate(d, lim, func(x ServicePrincipalDTO) string { return x.ID })}, nil
	})

	api.Register(a, api.Op{ID: "get-service-principal", Method: http.MethodGet, Path: "/service-principals/{id}", Summary: "Get a service principal",
		Tags: tags, Permission: auth.PermUsersManage}, func(ctx context.Context, in *idPath) (*spOut, error) {
		sp, err := h.Auth.GetServicePrincipal(ctx, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		return &spOut{ETag: api.ETag(sp.Revision), Body: spDTO(sp)}, nil
	})

	type createIn struct {
		ManagedBy string `header:"X-HP-Managed-By" enum:"ui,api,terraform,cli,import" required:"false"`
		Body      spBody
	}
	api.Register(a, api.Op{ID: "create-service-principal", Method: http.MethodPost, Path: "/service-principals", Summary: "Create a service principal",
		Tags: tags, Permission: auth.PermUsersManage, Status: http.StatusCreated}, func(ctx context.Context, in *createIn) (*spOut, error) {
		var sp *auth.ServicePrincipal
		err := h.tx(ctx, func(tx *sql.Tx) error {
			var err error
			sp, err = h.Auth.CreateServicePrincipal(ctx, tx, auth.SPInput{Name: in.Body.Name, Description: in.Body.Description,
				Permissions: perms(in.Body.Permissions), ManagedBy: managedBy(ctx, in.ManagedBy)}, auth.FromContext(ctx).ID)
			if err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "service_principal.create", TargetKind: "service_principal", TargetID: sp.ID,
				Details: map[string]any{"permissions": in.Body.Permissions}})
		})
		if err != nil {
			return nil, err
		}
		return &spOut{ETag: api.ETag(sp.Revision), Body: spDTO(sp)}, nil
	})

	type replaceIn struct {
		ID        string `path:"id" maxLength:"64"`
		IfMatch   string `header:"If-Match"`
		ManagedBy string `header:"X-HP-Managed-By" enum:"ui,api,terraform,cli,import" required:"false"`
		Body      spBody
	}
	api.Register(a, api.Op{ID: "replace-service-principal", Method: http.MethodPut, Path: "/service-principals/{id}", Summary: "Replace a service principal",
		Tags: tags, Permission: auth.PermUsersManage}, func(ctx context.Context, in *replaceIn) (*spOut, error) {
		cur, err := h.Auth.GetServicePrincipal(ctx, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		if err := api.CheckIfMatch(in.IfMatch, cur.Revision, false); err != nil {
			return nil, err
		}
		if in.Body.Name != cur.Name {
			return nil, api.Invalid("name is immutable", api.FieldError{Location: "body.name", Message: "cannot be changed"})
		}
		var sp *auth.ServicePrincipal
		err = h.tx(ctx, func(tx *sql.Tx) error {
			var err error
			sp, err = h.Auth.UpdateServicePrincipal(ctx, tx, in.ID, auth.SPInput{Description: in.Body.Description,
				Permissions: perms(in.Body.Permissions), Disabled: in.Body.Disabled, ManagedBy: managedBy(ctx, in.ManagedBy)})
			if err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "service_principal.update", TargetKind: "service_principal", TargetID: in.ID,
				Details: map[string]any{"permissions": in.Body.Permissions, "disabled": in.Body.Disabled}})
		})
		if err != nil {
			return nil, err
		}
		return &spOut{ETag: api.ETag(sp.Revision), Body: spDTO(sp)}, nil
	})

	api.Register(a, api.Op{ID: "delete-service-principal", Method: http.MethodDelete, Path: "/service-principals/{id}",
		Summary: "Delete a service principal and its tokens", Tags: tags, Permission: auth.PermUsersManage, Status: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if auth.FromContext(ctx).ID == in.ID {
				return nil, api.Conflict("a service principal cannot delete itself")
			}
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.DeleteServicePrincipal(ctx, tx, in.ID); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "service_principal.delete", TargetKind: "service_principal", TargetID: in.ID})
			})
		})

	type tokensOut struct {
		Body struct {
			Items []APITokenDTO `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-api-tokens", Method: http.MethodGet, Path: "/service-principals/{id}/tokens", Summary: "List API tokens",
		Tags: tags, Permission: auth.PermUsersManage}, func(ctx context.Context, in *idPath) (*tokensOut, error) {
		ts, err := h.Auth.ListAPITokens(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		out := &tokensOut{}
		out.Body.Items = []APITokenDTO{}
		for _, t := range ts {
			out.Body.Items = append(out.Body.Items, tokenDTO(t))
		}
		return out, nil
	})

	type issueIn struct {
		ID   string `path:"id" maxLength:"64"`
		Body struct {
			Name       string `json:"name" minLength:"1" maxLength:"80"`
			TTLSeconds int64  `json:"ttl_seconds,omitempty" minimum:"0" maximum:"63072000" doc:"0 = no expiry."`
		}
	}
	type issueOut struct {
		Body struct {
			Token APITokenDTO `json:"token"`
			// Plaintext is shown exactly once.
			Plaintext string `json:"plaintext" doc:"The bearer token. Shown exactly once; store it securely."`
		}
	}
	api.Register(a, api.Op{ID: "issue-api-token", Method: http.MethodPost, Path: "/service-principals/{id}/tokens",
		Summary: "Issue an API token (plaintext returned once)", Tags: tags, Permission: auth.PermUsersManage, Status: http.StatusCreated},
		func(ctx context.Context, in *issueIn) (*issueOut, error) {
			var t *auth.APIToken
			var plain string
			err := h.tx(ctx, func(tx *sql.Tx) error {
				var err error
				t, plain, err = h.Auth.IssueAPIToken(ctx, tx, in.ID, in.Body.Name, time.Duration(in.Body.TTLSeconds)*time.Second, auth.FromContext(ctx).ID)
				if err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "api_token.issue", TargetKind: "service_principal", TargetID: in.ID,
					Details: map[string]any{"token_id": t.ID, "prefix": t.Prefix}})
			})
			if err != nil {
				return nil, err
			}
			out := &issueOut{}
			out.Body.Token, out.Body.Plaintext = tokenDTO(*t), plain
			return out, nil
		})

	type revokeIn struct {
		ID      string `path:"id" maxLength:"64"`
		TokenID string `path:"token_id" maxLength:"64"`
	}
	api.Register(a, api.Op{ID: "revoke-api-token", Method: http.MethodDelete, Path: "/service-principals/{id}/tokens/{token_id}",
		Summary: "Revoke an API token", Tags: tags, Permission: auth.PermUsersManage, Status: http.StatusNoContent},
		func(ctx context.Context, in *revokeIn) (*struct{}, error) {
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.RevokeAPIToken(ctx, tx, in.ID, in.TokenID); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "api_token.revoke", TargetKind: "service_principal", TargetID: in.ID,
					Details: map[string]any{"token_id": in.TokenID}})
			})
		})
}
