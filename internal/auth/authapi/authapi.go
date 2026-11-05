// Package authapi exposes authentication, user, service-principal, SSO,
// security-alert and audit operations on the control-plane API.
package authapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ratelimit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Handlers holds dependencies.
type Handlers struct {
	Auth  *auth.Service
	DB    *store.DB
	login *ratelimit.Limiter
	setup *ratelimit.Limiter
}

// Register adds all operations.
func Register(a *api.API, svc *auth.Service, db *store.DB) {
	h := &Handlers{Auth: svc, DB: db, login: ratelimit.New(60, 30, time.Now), setup: ratelimit.New(10, 5, time.Now)}
	h.registerSession(a)
	h.registerUsers(a)
	h.registerPrincipals(a)
	h.registerOIDC(a)
	h.registerAdmin(a)
	h.registerReauth(a)
}

// ---------- DTOs ----------

// UserDTO is the public user representation.
type UserDTO struct {
	ID          string        `json:"id"`
	Email       string        `json:"email"`
	DisplayName string        `json:"display_name"`
	Role        string        `json:"role" enum:"admin,operator,viewer"`
	RoleSource  string        `json:"role_source" doc:"local, or oidc:<provider id> when roles come from SSO groups"`
	HasPassword bool          `json:"has_password"`
	Disabled    bool          `json:"disabled"`
	Grants      []string      `json:"grants" doc:"Explicit trace-content permissions."`
	Identities  []IdentityDTO `json:"identities"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastLoginAt *time.Time    `json:"last_login_at,omitempty"`
	Revision    int64         `json:"revision"`
	// SSOLinkProvider is set while the account waits for its first sign-in
	// from that pre-provisioned SSO provider.
	SSOLinkProvider string `json:"sso_link_provider,omitempty" required:"false" doc:"SSO provider id whose first verified sign-in will link this account."`
}

// IdentityDTO describes a linked SSO identity.
type IdentityDTO struct {
	ProviderID string     `json:"provider_id"`
	Issuer     string     `json:"issuer"`
	Subject    string     `json:"subject"`
	LinkedAt   time.Time  `json:"linked_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

func userDTO(u *auth.User) UserDTO {
	d := UserDTO{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: string(u.Role), RoleSource: u.RoleSource,
		HasPassword: u.HasPassword, Disabled: u.Disabled, Grants: []string{}, Identities: []IdentityDTO{},
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, LastLoginAt: u.LastLoginAt, Revision: u.Revision, SSOLinkProvider: u.SSOLinkProvider}
	for _, g := range u.Grants {
		d.Grants = append(d.Grants, string(g))
	}
	for _, i := range u.Identities {
		d.Identities = append(d.Identities, IdentityDTO{ProviderID: i.ProviderID, Issuer: i.Issuer, Subject: i.Subject, LinkedAt: i.LinkedAt, LastSeenAt: i.LastSeenAt})
	}
	return d
}

// ---------- session ----------

// SessionDTO describes the caller.
type SessionDTO struct {
	Authenticated  bool              `json:"authenticated"`
	SetupRequired  bool              `json:"setup_required" doc:"No administrator exists yet; use the setup link printed by 'harbor-pilot admin bootstrap'."`
	PasswordLogin  bool              `json:"password_login"`
	OIDCProviders  []OIDCLoginOption `json:"oidc_providers"`
	User           *UserDTO          `json:"user,omitempty"`
	PrincipalKind  string            `json:"principal_kind,omitempty"`
	Role           string            `json:"role,omitempty"`
	Permissions    []string          `json:"permissions"`
	AuthMethod     string            `json:"auth_method,omitempty"`
	SessionID      string            `json:"session_id,omitempty"`
	Limited        bool              `json:"limited"`
	CSRFToken      string            `json:"csrf_token,omitempty"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
	SecurityAlerts int               `json:"security_alerts" doc:"Unacknowledged security alerts (admins only)."`
}

// OIDCLoginOption is shown on the sign-in page.
type OIDCLoginOption struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type sessionOut struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      SessionDTO
}

func (h *Handlers) describe(ctx context.Context, p *auth.Principal, info *auth.SessionInfo) (SessionDTO, error) {
	d := SessionDTO{Permissions: []string{}, OIDCProviders: []OIDCLoginOption{}}
	st, err := h.Auth.GetSettings(ctx)
	if err != nil {
		return d, err
	}
	d.PasswordLogin = st.PasswordLogin
	if d.SetupRequired, err = h.Auth.SetupRequired(ctx); err != nil {
		return d, err
	}
	provs, err := h.Auth.ListOIDCProviders(ctx)
	if err != nil {
		return d, err
	}
	for _, pr := range provs {
		if pr.Enabled {
			d.OIDCProviders = append(d.OIDCProviders, OIDCLoginOption{Slug: pr.Slug, DisplayName: pr.DisplayName})
		}
	}
	if !p.Authenticated() {
		return d, nil
	}
	d.Authenticated = true
	d.PrincipalKind = string(p.Kind)
	d.Role = string(p.Role)
	d.Permissions = p.PermissionList()
	d.AuthMethod = p.AuthMethod
	d.Limited = p.Limited
	if p.Kind == auth.KindUser {
		u, err := h.Auth.GetUser(ctx, p.ID)
		if err != nil {
			return d, err
		}
		ud := userDTO(u)
		d.User = &ud
	}
	if info != nil {
		d.SessionID = info.DisplayID
		d.CSRFToken = h.Auth.CSRFToken(info)
		exp := info.IdleExpiresAt
		if info.AbsoluteExpires.Before(exp) {
			exp = info.AbsoluteExpires
		}
		d.ExpiresAt = &exp
	}
	if p.Can(auth.PermSystemManage) {
		alerts, _ := h.Auth.ListSecurityAlerts(ctx, false)
		d.SecurityAlerts = len(alerts)
	}
	return d, nil
}

type loginIn struct {
	Body struct {
		Email    string `json:"email" maxLength:"254"`
		Password string `json:"password" maxLength:"4096"`
	}
}

type setupIn struct {
	Body struct {
		Token       string `json:"token" maxLength:"128"`
		Email       string `json:"email" maxLength:"254"`
		DisplayName string `json:"display_name" maxLength:"120"`
		Password    string `json:"password" maxLength:"4096"`
	}
}

type tokenIn struct {
	Body struct {
		Token string `json:"token" maxLength:"128"`
	}
}

type acceptIn struct {
	Body struct {
		Token    string `json:"token" maxLength:"128"`
		Purpose  string `json:"purpose" enum:"invite,reset"`
		Password string `json:"password" maxLength:"4096"`
	}
}

func (h *Handlers) rateCheck(ctx context.Context, l *ratelimit.Limiter) error {
	if ok, _ := l.Allow(reqmeta.RateKey(reqmeta.From(ctx).IP)); !ok {
		return api.TooMany("too many requests; slow down")
	}
	return nil
}

func (h *Handlers) signedIn(ctx context.Context, tok string, info *auth.SessionInfo) (*sessionOut, error) {
	p, info2, err := h.Auth.ValidateSession(ctx, tok, false)
	if err != nil {
		return nil, api.Internal()
	}
	_ = info
	d, err := h.describe(ctx, p, info2)
	if err != nil {
		return nil, err
	}
	return &sessionOut{SetCookie: []http.Cookie{*h.Auth.SessionCookie(tok, info2)}, Body: d}, nil
}

func (h *Handlers) registerSession(a *api.API) {
	tags := []string{"Session"}
	api.Register(a, api.Op{ID: "get-session", Method: http.MethodGet, Path: "/session", Summary: "Describe the current caller",
		Tags: tags, Permission: auth.PermPublic}, func(ctx context.Context, _ *struct{}) (*sessionOut, error) {
		d, err := h.describe(ctx, auth.FromContext(ctx), auth.SessionFromContext(ctx))
		return &sessionOut{Body: d}, err
	})

	api.Register(a, api.Op{ID: "login", Method: http.MethodPost, Path: "/session", Summary: "Sign in with email and password",
		Tags: tags, Permission: auth.PermPublic}, func(ctx context.Context, in *loginIn) (*sessionOut, error) {
		if err := h.rateCheck(ctx, h.login); err != nil {
			return nil, err
		}
		tok, info, err := h.Auth.Login(ctx, in.Body.Email, in.Body.Password)
		if err != nil {
			_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "auth.login", Outcome: audit.Denied,
				Details: map[string]any{"method": "password", "reason": err.Error()}})
			var te *auth.ThrottledError
			switch {
			case errors.As(err, &te):
				return nil, api.TooMany(err.Error())
			case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrPasswordLoginOff):
				return nil, api.Errorf(http.StatusUnauthorized, "invalid_credentials", err.Error())
			}
			return nil, err
		}
		_ = audit.Record(auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, ID: info.UserID}), h.DB.W,
			audit.Entry{Action: "auth.login", TargetKind: "session", TargetID: info.DisplayID, Details: map[string]any{"method": "password"}})
		return h.signedIn(ctx, tok, info)
	})

	api.Register(a, api.Op{ID: "logout", Method: http.MethodDelete, Path: "/session", Summary: "Sign out", Status: http.StatusOK,
		Tags: tags, Permission: auth.PermSession, AllowLimited: true}, func(ctx context.Context, _ *struct{}) (*sessionOut, error) {
		if info := auth.SessionFromContext(ctx); info != nil {
			_ = h.Auth.RevokeSession(ctx, info.DisplayID, "", "logout")
			_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "auth.logout", TargetKind: "session", TargetID: info.DisplayID})
		}
		d, err := h.describe(ctx, auth.Anonymous, nil)
		return &sessionOut{SetCookie: []http.Cookie{*h.Auth.ClearSessionCookie()}, Body: d}, err
	})

	api.Register(a, api.Op{ID: "complete-setup", Method: http.MethodPost, Path: "/auth/setup",
		Summary: "Create the first administrator with a one-time setup token", Tags: tags, Permission: auth.PermPublic},
		func(ctx context.Context, in *setupIn) (*sessionOut, error) {
			if err := h.rateCheck(ctx, h.setup); err != nil {
				return nil, err
			}
			tok, info, u, err := h.Auth.Bootstrap(ctx, in.Body.Token, in.Body.Email, in.Body.DisplayName, in.Body.Password)
			if err != nil {
				return nil, mapErr(err)
			}
			_ = audit.Record(auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, ID: u.ID}), h.DB.W,
				audit.Entry{Action: "auth.bootstrap", TargetKind: "user", TargetID: u.ID})
			return h.signedIn(ctx, tok, info)
		})

	api.Register(a, api.Op{ID: "recover", Method: http.MethodPost, Path: "/auth/recover",
		Summary: "Sign in with a break-glass recovery token", Tags: tags, Permission: auth.PermPublic},
		func(ctx context.Context, in *tokenIn) (*sessionOut, error) {
			if err := h.rateCheck(ctx, h.setup); err != nil {
				return nil, err
			}
			tok, info, err := h.Auth.Recover(ctx, in.Body.Token)
			if err != nil {
				return nil, mapErr(err)
			}
			_ = audit.Record(auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, ID: info.UserID}), h.DB.W,
				audit.Entry{Action: "auth.breakglass", TargetKind: "session", TargetID: info.DisplayID})
			return h.signedIn(ctx, tok, info)
		})

	api.Register(a, api.Op{ID: "accept-invite", Method: http.MethodPost, Path: "/auth/accept",
		Summary: "Set a password from an invitation or reset token and sign in", Tags: tags, Permission: auth.PermPublic},
		func(ctx context.Context, in *acceptIn) (*sessionOut, error) {
			if err := h.rateCheck(ctx, h.setup); err != nil {
				return nil, err
			}
			tok, info, err := h.Auth.AcceptInvite(ctx, in.Body.Purpose, in.Body.Token, in.Body.Password)
			if err != nil {
				return nil, mapErr(err)
			}
			_ = audit.Record(auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, ID: info.UserID}), h.DB.W,
				audit.Entry{Action: "auth.accept_" + in.Body.Purpose, TargetKind: "user", TargetID: info.UserID})
			return h.signedIn(ctx, tok, info)
		})

	type sessionsOut struct{ Body WebSessionList }
	api.Register(a, api.Op{ID: "list-my-sessions", Method: http.MethodGet, Path: "/session/all", Summary: "List my active sessions",
		Tags: tags, Permission: auth.PermSession}, func(ctx context.Context, _ *struct{}) (*sessionsOut, error) {
		p := auth.FromContext(ctx)
		out := &sessionsOut{}
		out.Body.Items = []WebSessionDTO{}
		if p.Kind != auth.KindUser {
			return out, nil
		}
		ss, err := h.Auth.ListSessions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, s := range ss {
			out.Body.Items = append(out.Body.Items, sessionDTO(s, p.SessionID))
		}
		return out, nil
	})

	type revokeIn struct {
		ID string `path:"id"`
	}
	api.Register(a, api.Op{ID: "revoke-my-session", Method: http.MethodDelete, Path: "/session/all/{id}", Summary: "Revoke one of my sessions",
		Tags: tags, Permission: auth.PermSession, Status: http.StatusNoContent}, func(ctx context.Context, in *revokeIn) (*struct{}, error) {
		p := auth.FromContext(ctx)
		if err := h.Auth.RevokeSession(ctx, in.ID, p.ID, "revoked by user"); err != nil {
			return nil, mapErr(err)
		}
		_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "auth.session.revoke", TargetKind: "session", TargetID: in.ID})
		return nil, nil
	})
}

// WebSessionDTO describes an active web session.
type WebSessionDTO struct {
	ID         string    `json:"id"`
	AuthMethod string    `json:"auth_method"`
	Current    bool      `json:"current"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

func sessionDTO(s auth.SessionInfo, current string) WebSessionDTO {
	exp := s.IdleExpiresAt
	if s.AbsoluteExpires.Before(exp) {
		exp = s.AbsoluteExpires
	}
	return WebSessionDTO{ID: s.DisplayID, AuthMethod: s.AuthMethod, Current: s.DisplayID == current, CreatedAt: s.CreatedAt,
		LastSeenAt: s.LastSeenAt, ExpiresAt: exp, IP: s.IP, UserAgent: s.UserAgent}
}

// mapErr converts service errors to API errors.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrNotFound):
		return api.NotFound("resource")
	case errors.Is(err, auth.ErrConflict):
		return api.Conflict("an object with this name or email already exists")
	case errors.Is(err, auth.ErrLastAdmin):
		return api.Conflict(err.Error())
	case errors.Is(err, auth.ErrTokenInvalid):
		return api.Errorf(http.StatusBadRequest, "invalid_token", err.Error())
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong), errors.Is(err, auth.ErrPasswordInvalid):
		return api.Invalid(err.Error(), api.FieldError{Location: "body.password", Message: err.Error()})
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	msg := err.Error()
	if strings.Contains(msg, "invalid") || strings.Contains(msg, "must") || strings.Contains(msg, "required") || strings.Contains(msg, "only") {
		return api.Invalid(msg)
	}
	return err
}

func (h *Handlers) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return mapErr(h.DB.Tx(ctx, fn))
}

// WebSessionList lists web sessions.
type WebSessionList struct {
	Items []WebSessionDTO `json:"items"`
}
