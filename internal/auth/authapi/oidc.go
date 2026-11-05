package authapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

// OIDCProviderDTO is the public representation (secrets are write-only).
type OIDCProviderDTO struct {
	ID                  string             `json:"id"`
	Slug                string             `json:"slug"`
	DisplayName         string             `json:"display_name"`
	Issuer              string             `json:"issuer"`
	ClientID            string             `json:"client_id"`
	ClientSecretSet     bool               `json:"client_secret_set"`
	ClientSecretRef     string             `json:"client_secret_ref,omitempty"`
	Scopes              []string           `json:"scopes"`
	GroupsClaim         string             `json:"groups_claim"`
	RoleMappings        []auth.RoleMapping `json:"role_mappings"`
	DefaultRole         string             `json:"default_role,omitempty"`
	Provisioning        string             `json:"provisioning" enum:"preprovisioned,jit"`
	AllowedDomains      []string           `json:"allowed_domains"`
	AllowedAlgs         []string           `json:"allowed_algs"`
	AllowPrivateNetwork bool               `json:"allow_private_network"`
	Enabled             bool               `json:"enabled"`
	RedirectURI         string             `json:"redirect_uri" doc:"Register this redirect URI with the identity provider."`
	ManagedBy           string             `json:"managed_by"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	Revision            int64              `json:"revision"`
}

func (h *Handlers) oidcDTO(p *auth.OIDCProvider) OIDCProviderDTO {
	d := OIDCProviderDTO{ID: p.ID, Slug: p.Slug, DisplayName: p.DisplayName, Issuer: p.Issuer, ClientID: p.ClientID,
		ClientSecretSet: p.ClientSecretSet, ClientSecretRef: p.ClientSecretRef, Scopes: p.Scopes, GroupsClaim: p.GroupsClaim,
		RoleMappings: p.RoleMappings, DefaultRole: string(p.DefaultRole), Provisioning: p.Provisioning, AllowedDomains: p.AllowedDomains,
		AllowedAlgs: p.AllowedAlgs, AllowPrivateNetwork: p.AllowPrivateNetwork, Enabled: p.Enabled,
		RedirectURI: h.Auth.PublicOrigin() + "/api/v1/auth/oidc/callback", ManagedBy: p.ManagedBy,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Revision: p.Revision}
	if d.RoleMappings == nil {
		d.RoleMappings = []auth.RoleMapping{}
	}
	if d.AllowedDomains == nil {
		d.AllowedDomains = []string{}
	}
	return d
}

type oidcBody struct {
	Slug                string             `json:"slug" pattern:"^[a-z0-9][a-z0-9._-]{0,62}$"`
	DisplayName         string             `json:"display_name" minLength:"1" maxLength:"80"`
	Issuer              string             `json:"issuer" format:"uri" maxLength:"512"`
	ClientID            string             `json:"client_id" minLength:"1" maxLength:"256"`
	ClientSecret        *string            `json:"client_secret,omitempty" maxLength:"1024" doc:"Write-only. Omit to keep the current secret; empty string clears it."`
	ClientSecretRef     string             `json:"client_secret_ref,omitempty" maxLength:"512" doc:"Alternative to client_secret: env:NAME or file:/path."`
	Scopes              []string           `json:"scopes,omitempty" maxItems:"16"`
	GroupsClaim         string             `json:"groups_claim,omitempty" maxLength:"128" doc:"Dotted claim path, e.g. porch.groups or groups."`
	RoleMappings        []auth.RoleMapping `json:"role_mappings,omitempty" maxItems:"64"`
	DefaultRole         string             `json:"default_role,omitempty" enum:"admin,operator,viewer,"`
	Provisioning        string             `json:"provisioning" enum:"preprovisioned,jit"`
	AllowedDomains      []string           `json:"allowed_domains,omitempty" maxItems:"32"`
	AllowedAlgs         []string           `json:"allowed_algs,omitempty" maxItems:"6"`
	AllowPrivateNetwork bool               `json:"allow_private_network,omitempty"`
	Enabled             bool               `json:"enabled"`
}

func (b oidcBody) input(mb string) auth.OIDCInput {
	return auth.OIDCInput{Slug: b.Slug, DisplayName: b.DisplayName, Issuer: b.Issuer, ClientID: b.ClientID, ClientSecret: b.ClientSecret,
		ClientSecretRef: b.ClientSecretRef, Scopes: b.Scopes, GroupsClaim: b.GroupsClaim, RoleMappings: b.RoleMappings,
		DefaultRole: auth.Role(b.DefaultRole), Provisioning: b.Provisioning, AllowedDomains: b.AllowedDomains, AllowedAlgs: b.AllowedAlgs,
		AllowPrivateNetwork: b.AllowPrivateNetwork, Enabled: b.Enabled, ManagedBy: mb}
}

type oidcOut struct {
	ETag string `header:"ETag"`
	Body OIDCProviderDTO
}

type redirectOut struct {
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

// test results are kept in memory briefly for the admin who ran the test.
type testResult struct {
	userID string
	claims map[string]any
	at     time.Time
}

var (
	testMu      sync.Mutex
	testResults = map[string]testResult{}
)

func (h *Handlers) registerOIDC(a *api.API) {
	tags := []string{"Single sign-on"}
	flowTags := []string{"Session"}

	type startIn struct {
		Slug     string `path:"slug" maxLength:"64"`
		ReturnTo string `query:"return_to" maxLength:"512"`
	}
	api.Register(a, api.Op{ID: "oidc-start", Method: http.MethodGet, Path: "/auth/oidc/{slug}/start",
		Summary: "Start single sign-on (redirects to the identity provider)", Tags: flowTags, Permission: auth.PermPublic, Status: http.StatusFound},
		func(ctx context.Context, in *startIn) (*redirectOut, error) {
			st, err := h.Auth.StartOIDC(ctx, in.Slug, auth.OIDCPurposeLogin, "", in.ReturnTo)
			if err != nil {
				return &redirectOut{Location: "/login?error=" + url.QueryEscape(userMessage(err))}, nil
			}
			return &redirectOut{Location: st.AuthURL, SetCookie: []http.Cookie{h.flowCookie(st.BrowserCookie, st.ExpiresAt)}}, nil
		})

	api.Register(a, api.Op{ID: "oidc-link-start", Method: http.MethodPost, Path: "/auth/oidc/{slug}/link",
		Summary: "Link an SSO identity to my account (returns the provider URL)", Tags: flowTags, Permission: auth.PermSession},
		func(ctx context.Context, in *startIn) (*linkOut, error) {
			p := auth.FromContext(ctx)
			if p.Kind != auth.KindUser {
				return nil, api.Forbidden("only users can link identities")
			}
			st, err := h.Auth.StartOIDC(ctx, in.Slug, auth.OIDCPurposeLink, p.ID, "/account")
			if err != nil {
				return nil, api.Invalid(userMessage(err))
			}
			out := &linkOut{SetCookie: []http.Cookie{h.flowCookie(st.BrowserCookie, st.ExpiresAt)}}
			out.Body.AuthURL = st.AuthURL
			return out, nil
		})

	type callbackIn struct {
		Code             string `query:"code" maxLength:"2048"`
		State            string `query:"state" maxLength:"256"`
		Iss              string `query:"iss" maxLength:"512"`
		Error            string `query:"error" maxLength:"128"`
		ErrorDescription string `query:"error_description" maxLength:"512"`
		Flow             string `cookie:"__Host-hp_oidc"`
		FlowDev          string `cookie:"hp_oidc"`
	}
	api.Register(a, api.Op{ID: "oidc-callback", Method: http.MethodGet, Path: "/auth/oidc/callback",
		Summary: "Single sign-on callback (redirect target registered with the identity provider)", Tags: flowTags,
		Permission: auth.PermPublic, Status: http.StatusFound},
		func(ctx context.Context, in *callbackIn) (*redirectOut, error) {
			clear := h.flowCookie("", time.Unix(0, 0)) //nolint:gosec // Secure is set whenever the public URL is https
			clear.MaxAge = -1
			fail := func(msg string) (*redirectOut, error) {
				_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "auth.login", Outcome: audit.Denied, Details: map[string]any{"method": "oidc", "reason": msg}})
				return &redirectOut{Location: "/login?error=" + url.QueryEscape(msg), SetCookie: []http.Cookie{clear}}, nil
			}
			if in.Error != "" {
				return fail("identity provider returned " + in.Error)
			}
			browser := in.Flow
			if !h.Auth.SecureCookies() {
				browser = in.FlowDev
			}
			var linking *auth.Principal
			if p := auth.FromContext(ctx); p.Authenticated() {
				linking = p
			}
			res, err := h.Auth.CompleteOIDC(ctx, in.State, in.Code, in.Iss, browser, linking)
			if err != nil {
				return fail(userMessage(err))
			}
			switch res.Purpose {
			case auth.OIDCPurposeTest:
				rid := auth.RandomString(16)
				testMu.Lock()
				for k, v := range testResults {
					if time.Since(v.at) > 10*time.Minute {
						delete(testResults, k)
					}
				}
				owner := ""
				if linking != nil {
					owner = linking.ID
				}
				testResults[rid] = testResult{userID: owner, claims: res.Claims, at: time.Now()}
				testMu.Unlock()
				return &redirectOut{Location: "/settings/auth?sso_test=" + rid, SetCookie: []http.Cookie{clear}}, nil
			case auth.OIDCPurposeLink:
				_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "user.identity.link", TargetKind: "user", TargetID: res.User.ID,
					Details: map[string]any{"provider_id": res.ProviderID}})
				return &redirectOut{Location: res.ReturnTo, SetCookie: []http.Cookie{clear}}, nil
			}
			_ = audit.Record(auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, ID: res.User.ID}), h.DB.W,
				audit.Entry{Action: "auth.login", TargetKind: "session", TargetID: res.Info.DisplayID,
					Details: map[string]any{"method": "oidc", "provider_id": res.ProviderID}})
			return &redirectOut{Location: res.ReturnTo, SetCookie: []http.Cookie{clear, *h.Auth.SessionCookie(res.Session, res.Info)}}, nil
		})

	// ----- provider administration -----
	type listOut struct{ Body OIDCProviderList }
	api.Register(a, api.Op{ID: "list-oidc-providers", Method: http.MethodGet, Path: "/auth/oidc-providers", Summary: "List SSO providers",
		Tags: tags, Permission: auth.PermAuthSettings}, func(ctx context.Context, _ *struct{}) (*listOut, error) {
		ps, err := h.Auth.ListOIDCProviders(ctx)
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Items = []OIDCProviderDTO{}
		for _, p := range ps {
			out.Body.Items = append(out.Body.Items, h.oidcDTO(p))
		}
		return out, nil
	})
	api.Register(a, api.Op{ID: "get-oidc-provider", Method: http.MethodGet, Path: "/auth/oidc-providers/{id}", Summary: "Get an SSO provider",
		Tags: tags, Permission: auth.PermAuthSettings}, func(ctx context.Context, in *idPath) (*oidcOut, error) {
		p, err := h.Auth.GetOIDCProvider(ctx, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		return &oidcOut{ETag: api.ETag(p.Revision), Body: h.oidcDTO(p)}, nil
	})
	type createIn struct {
		ManagedBy string `header:"X-HP-Managed-By" enum:"ui,api,terraform,cli,import" required:"false"`
		Body      oidcBody
	}
	api.Register(a, api.Op{ID: "create-oidc-provider", Method: http.MethodPost, Path: "/auth/oidc-providers", Summary: "Create an SSO provider",
		Tags: tags, Permission: auth.PermAuthSettings, Status: http.StatusCreated}, func(ctx context.Context, in *createIn) (*oidcOut, error) {
		var p *auth.OIDCProvider
		err := h.tx(ctx, func(tx *sql.Tx) error {
			var err error
			if p, err = h.Auth.SaveOIDCProvider(ctx, tx, "", in.Body.input(managedBy(ctx, in.ManagedBy))); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "auth.oidc_provider.create", TargetKind: "oidc_provider", TargetID: p.ID,
				Details: map[string]any{"issuer": p.Issuer, "provisioning": p.Provisioning}})
		})
		if err != nil {
			return nil, err
		}
		return &oidcOut{ETag: api.ETag(p.Revision), Body: h.oidcDTO(p)}, nil
	})
	type replaceIn struct {
		ID        string `path:"id" maxLength:"64"`
		IfMatch   string `header:"If-Match"`
		ManagedBy string `header:"X-HP-Managed-By" enum:"ui,api,terraform,cli,import" required:"false"`
		Body      oidcBody
	}
	api.Register(a, api.Op{ID: "replace-oidc-provider", Method: http.MethodPut, Path: "/auth/oidc-providers/{id}", Summary: "Replace an SSO provider",
		Tags: tags, Permission: auth.PermAuthSettings}, func(ctx context.Context, in *replaceIn) (*oidcOut, error) {
		cur, err := h.Auth.GetOIDCProvider(ctx, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		if err := api.CheckIfMatch(in.IfMatch, cur.Revision, false); err != nil {
			return nil, err
		}
		var p *auth.OIDCProvider
		err = h.tx(ctx, func(tx *sql.Tx) error {
			var err error
			if p, err = h.Auth.SaveOIDCProvider(ctx, tx, cur.ID, in.Body.input(managedBy(ctx, in.ManagedBy))); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "auth.oidc_provider.update", TargetKind: "oidc_provider", TargetID: p.ID,
				Details: map[string]any{"secret_changed": in.Body.ClientSecret != nil || in.Body.ClientSecretRef != ""}})
		})
		if err != nil {
			return nil, err
		}
		return &oidcOut{ETag: api.ETag(p.Revision), Body: h.oidcDTO(p)}, nil
	})
	api.Register(a, api.Op{ID: "delete-oidc-provider", Method: http.MethodDelete, Path: "/auth/oidc-providers/{id}",
		Summary: "Delete an SSO provider (unlinks identities)", Tags: tags, Permission: auth.PermAuthSettings, Status: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.DeleteOIDCProvider(ctx, tx, in.ID); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "auth.oidc_provider.delete", TargetKind: "oidc_provider", TargetID: in.ID})
			})
		})
	api.Register(a, api.Op{ID: "test-oidc-provider", Method: http.MethodPost, Path: "/auth/oidc-providers/{id}/test",
		Summary: "Start a test sign-in that reports mapped claims without signing in", Tags: tags, Permission: auth.PermAuthSettings},
		func(ctx context.Context, in *idPath) (*linkOut, error) {
			st, err := h.Auth.StartOIDC(ctx, in.ID, auth.OIDCPurposeTest, "", "/settings/auth")
			if err != nil {
				return nil, api.Invalid(userMessage(err))
			}
			out := &linkOut{SetCookie: []http.Cookie{h.flowCookie(st.BrowserCookie, st.ExpiresAt)}}
			out.Body.AuthURL = st.AuthURL
			return out, nil
		})
	type testIn struct {
		RID string `path:"rid" maxLength:"64"`
	}
	type testOut struct {
		Body struct {
			Claims map[string]any `json:"claims"`
		}
	}
	api.Register(a, api.Op{ID: "get-oidc-test-result", Method: http.MethodGet, Path: "/auth/oidc-test-results/{rid}",
		Summary: "Fetch the outcome of a test sign-in (valid 10 minutes)", Tags: tags, Permission: auth.PermAuthSettings},
		func(ctx context.Context, in *testIn) (*testOut, error) {
			testMu.Lock()
			r, ok := testResults[in.RID]
			testMu.Unlock()
			if !ok || time.Since(r.at) > 10*time.Minute || (r.userID != "" && r.userID != auth.FromContext(ctx).ID) {
				return nil, api.NotFound("test result")
			}
			out := &testOut{}
			out.Body.Claims = r.claims
			return out, nil
		})

	type unlinkIn struct {
		ID   string `path:"id" maxLength:"64"`
		Body struct {
			Issuer  string `json:"issuer"`
			Subject string `json:"subject"`
		}
	}
	api.Register(a, api.Op{ID: "unlink-user-identity", Method: http.MethodPost, Path: "/users/{id}/unlink-identity",
		Summary: "Unlink an SSO identity (revokes SSO sessions)", Tags: []string{"Users"}, Permission: auth.PermUsersManage, Status: http.StatusNoContent},
		func(ctx context.Context, in *unlinkIn) (*struct{}, error) {
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.UnlinkIdentity(ctx, tx, in.ID, in.Body.Issuer, in.Body.Subject); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "user.identity.unlink", TargetKind: "user", TargetID: in.ID})
			})
		})
}

type linkOut struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      struct {
		AuthURL string `json:"auth_url"`
	}
}

func (h *Handlers) flowCookie(v string, exp time.Time) http.Cookie {
	return http.Cookie{ //nolint:gosec // Secure is set whenever the public URL is https
		Name: h.Auth.FlowCookieName(), Value: v, Path: "/", HttpOnly: true, Secure: h.Auth.SecureCookies(),
		SameSite: http.SameSiteLaxMode, MaxAge: max(int(time.Until(exp)/time.Second), 1)}
}

func userMessage(err error) string {
	for _, e := range []error{auth.ErrOIDCState, auth.ErrOIDCNoAccount, auth.ErrOIDCNoRole, auth.ErrOIDCDisabled,
		auth.ErrOIDCEmailDomain, auth.ErrOIDCAlreadyLinked, auth.ErrNotFound} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "single sign-on failed: " + err.Error()
}

// OIDCProviderList lists SSO providers.
type OIDCProviderList struct {
	Items []OIDCProviderDTO `json:"items"`
}
