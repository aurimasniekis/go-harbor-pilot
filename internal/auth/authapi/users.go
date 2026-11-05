package authapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

type userOut struct {
	ETag string `header:"ETag"`
	Body UserDTO
}

type idPath struct {
	ID string `path:"id" maxLength:"64"`
}

type createUserIn struct {
	Body struct {
		Email       string `json:"email" maxLength:"254"`
		DisplayName string `json:"display_name,omitempty" maxLength:"120"`
		Role        string `json:"role" enum:"admin,operator,viewer"`
		Invite      bool   `json:"invite" doc:"Return a one-time invitation link (valid 72h) for the user to set a password."`
		SSOProvider string `json:"sso_provider,omitempty" required:"false" maxLength:"64" doc:"Pre-provision for single sign-on: the first verified sign-in from this provider (id or slug; pre-provisioned mode) with the account's email links it. Without this, SSO never links an existing account by email."`
	}
}

type createUserOut struct {
	ETag string `header:"ETag"`
	Body struct {
		User UserDTO `json:"user"`
		// InviteURL is returned exactly once.
		InviteURL string     `json:"invite_url,omitempty"`
		InviteExp *time.Time `json:"invite_expires_at,omitempty"`
	}
}

type patchUserIn struct {
	ID      string `path:"id" maxLength:"64"`
	IfMatch string `header:"If-Match"`
	Body    struct {
		DisplayName *string `json:"display_name,omitempty" maxLength:"120"`
		Role        *string `json:"role,omitempty" enum:"admin,operator,viewer"`
		Disabled    *bool   `json:"disabled,omitempty"`
		SSOProvider *string `json:"sso_provider,omitempty" required:"false" maxLength:"64" doc:"Mark the account for linking on the next verified sign-in from this provider (id or slug); empty clears the mark."`
	}
}

type grantsIn struct {
	ID      string `path:"id" maxLength:"64"`
	IfMatch string `header:"If-Match"`
	Body    struct {
		Grants []string `json:"grants" enum:"traces.content.read,traces.content.unredacted,traces.export"`
	}
}

type resetOut struct {
	Body struct {
		ResetURL  string    `json:"reset_url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
}

type passwordIn struct {
	Body struct {
		CurrentPassword string `json:"current_password" maxLength:"4096"`
		NewPassword     string `json:"new_password" maxLength:"4096"`
	}
}

func (h *Handlers) registerUsers(a *api.API) {
	tags := []string{"Users"}
	type listOut struct{ Body api.Page[UserDTO] }
	type listIn struct{ api.PageParams }
	api.Register(a, api.Op{ID: "list-users", Method: http.MethodGet, Path: "/users", Summary: "List users", Tags: tags,
		Permission: auth.PermUsersManage}, func(ctx context.Context, in *listIn) (*listOut, error) {
		after, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		us, err := h.Auth.ListUsers(ctx, after, lim+1)
		if err != nil {
			return nil, err
		}
		dtos := make([]UserDTO, 0, len(us))
		for _, u := range us {
			dtos = append(dtos, userDTO(u))
		}
		return &listOut{Body: api.Paginate(dtos, lim, func(u UserDTO) string { return u.ID })}, nil
	})

	api.Register(a, api.Op{ID: "get-user", Method: http.MethodGet, Path: "/users/{id}", Summary: "Get a user", Tags: tags,
		Permission: auth.PermUsersManage}, func(ctx context.Context, in *idPath) (*userOut, error) {
		u, err := h.Auth.GetUser(ctx, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		return &userOut{ETag: api.ETag(u.Revision), Body: userDTO(u)}, nil
	})

	api.Register(a, api.Op{ID: "create-user", Method: http.MethodPost, Path: "/users", Summary: "Create or invite a user", Tags: tags,
		Permission: auth.PermUsersManage, Status: http.StatusCreated}, func(ctx context.Context, in *createUserIn) (*createUserOut, error) {
		p := auth.FromContext(ctx)
		var u *auth.User
		err := h.tx(ctx, func(tx *sql.Tx) error {
			var err error
			u, err = h.Auth.CreateUser(ctx, tx, auth.NewUser{Email: in.Body.Email, DisplayName: in.Body.DisplayName, Role: auth.Role(in.Body.Role)})
			if err != nil {
				return err
			}
			sso, err := h.ssoProviderID(ctx, in.Body.SSOProvider)
			if err != nil {
				return err
			}
			if sso != "" {
				if err := h.Auth.SetSSOLink(ctx, tx, u.ID, sso); err != nil {
					return mapErr(err)
				}
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "user.create", TargetKind: "user", TargetID: u.ID,
				Details: map[string]any{"role": in.Body.Role, "invite": in.Body.Invite, "sso_provider": sso}})
		})
		if err != nil {
			return nil, err
		}
		out := &createUserOut{ETag: api.ETag(u.Revision)}
		if in.Body.Invite {
			tok, exp, err := h.Auth.IssueOneTime(ctx, auth.PurposeInvite, u.ID, u.Email, u.Role, p.ID)
			if err != nil {
				return nil, err
			}
			out.Body.InviteURL = h.Auth.PublicOrigin() + "/accept?purpose=invite&token=" + tok
			out.Body.InviteExp = &exp
		}
		full, err := h.Auth.GetUser(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		out.Body.User = userDTO(full)
		return out, nil
	})

	api.Register(a, api.Op{ID: "update-user", Method: http.MethodPatch, Path: "/users/{id}", Summary: "Update a user", Tags: tags,
		Permission: auth.PermUsersManage}, func(ctx context.Context, in *patchUserIn) (*userOut, error) {
		cur, err := h.Auth.GetUser(ctx, in.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		if err := api.CheckIfMatch(in.IfMatch, cur.Revision, false); err != nil {
			return nil, err
		}
		patch := auth.UserPatch{DisplayName: in.Body.DisplayName, Disabled: in.Body.Disabled}
		if in.Body.Role != nil {
			r := auth.Role(*in.Body.Role)
			patch.Role = &r
		}
		if err := h.tx(ctx, func(tx *sql.Tx) error {
			if err := h.Auth.UpdateUser(ctx, tx, in.ID, patch); err != nil {
				return err
			}
			var sso any
			if in.Body.SSOProvider != nil {
				id, err := h.ssoProviderID(ctx, *in.Body.SSOProvider)
				if err != nil {
					return err
				}
				if err := h.Auth.SetSSOLink(ctx, tx, in.ID, id); err != nil {
					return mapErr(err)
				}
				sso = id
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "user.update", TargetKind: "user", TargetID: in.ID,
				Details: map[string]any{"role": in.Body.Role, "disabled": in.Body.Disabled, "sso_provider": sso}})
		}); err != nil {
			return nil, err
		}
		u, err := h.Auth.GetUser(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &userOut{ETag: api.ETag(u.Revision), Body: userDTO(u)}, nil
	})

	api.Register(a, api.Op{ID: "delete-user", Method: http.MethodDelete, Path: "/users/{id}", Summary: "Delete a user", Tags: tags,
		Permission: auth.PermUsersManage, Status: http.StatusNoContent}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		if in.ID == auth.FromContext(ctx).ID {
			return nil, api.Conflict("you cannot delete your own account")
		}
		return nil, h.tx(ctx, func(tx *sql.Tx) error {
			if err := h.Auth.DeleteUser(ctx, tx, in.ID); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "user.delete", TargetKind: "user", TargetID: in.ID})
		})
	})

	api.Register(a, api.Op{ID: "set-user-grants", Method: http.MethodPut, Path: "/users/{id}/grants",
		Summary: "Replace a user's explicit trace-content grants", Tags: tags, Permission: auth.PermUsersManage},
		func(ctx context.Context, in *grantsIn) (*userOut, error) {
			cur, err := h.Auth.GetUser(ctx, in.ID)
			if err != nil {
				return nil, mapErr(err)
			}
			if err := api.CheckIfMatch(in.IfMatch, cur.Revision, false); err != nil {
				return nil, err
			}
			grants := make([]auth.Permission, 0, len(in.Body.Grants))
			for _, g := range in.Body.Grants {
				grants = append(grants, auth.Permission(g))
			}
			if err := h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.SetGrants(ctx, tx, in.ID, grants, auth.FromContext(ctx).ID); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "user.grants.set", TargetKind: "user", TargetID: in.ID,
					Details: map[string]any{"grants": in.Body.Grants}})
			}); err != nil {
				return nil, err
			}
			u, err := h.Auth.GetUser(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &userOut{ETag: api.ETag(u.Revision), Body: userDTO(u)}, nil
		})

	api.Register(a, api.Op{ID: "issue-password-reset", Method: http.MethodPost, Path: "/users/{id}/password-reset",
		Summary: "Issue a one-time password reset link (valid 1h)", Tags: tags, Permission: auth.PermUsersManage},
		func(ctx context.Context, in *idPath) (*resetOut, error) {
			u, err := h.Auth.GetUser(ctx, in.ID)
			if err != nil {
				return nil, mapErr(err)
			}
			tok, exp, err := h.Auth.IssueOneTime(ctx, auth.PurposeReset, u.ID, u.Email, "", auth.FromContext(ctx).ID)
			if err != nil {
				return nil, err
			}
			_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "user.password_reset.issue", TargetKind: "user", TargetID: u.ID})
			out := &resetOut{}
			out.Body.ResetURL = h.Auth.PublicOrigin() + "/accept?purpose=reset&token=" + tok
			out.Body.ExpiresAt = exp
			return out, nil
		})

	api.Register(a, api.Op{ID: "revoke-user-sessions", Method: http.MethodPost, Path: "/users/{id}/revoke-sessions",
		Summary: "Revoke all sessions of a user", Tags: tags, Permission: auth.PermUsersManage, Status: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.RevokeUserSessions(ctx, tx, in.ID, "revoked by administrator"); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "user.sessions.revoke", TargetKind: "user", TargetID: in.ID})
			})
		})

	api.Register(a, api.Op{ID: "change-my-password", Method: http.MethodPost, Path: "/session/password",
		Summary: "Change my password (revokes my other sessions)", Tags: []string{"Session"}, Permission: auth.PermSession, Status: http.StatusNoContent},
		func(ctx context.Context, in *passwordIn) (*struct{}, error) {
			p := auth.FromContext(ctx)
			if p.Kind != auth.KindUser {
				return nil, api.Forbidden("only users have passwords")
			}
			u, err := h.Auth.GetUser(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			if u.HasPassword {
				if err := h.Auth.CheckPassword(ctx, u.ID, in.Body.CurrentPassword); err != nil {
					return nil, api.Errorf(http.StatusUnauthorized, "invalid_credentials", "current password is incorrect").
						WithDetails(map[string]any{"fields": []api.FieldError{{Location: "body.current_password", Message: "Current password is incorrect."}}})
				}
				if in.Body.NewPassword == in.Body.CurrentPassword {
					return nil, api.Invalid("the new password is the same as the current one",
						api.FieldError{Location: "body.new_password", Message: "Choose a password different from the current one."})
				}
			} else if !p.RecentlyAuthenticated(h.Auth.Now()) {
				// A first password adds a sign-in path; require proof from the
				// identity provider, not just a (possibly hijacked) session.
				return nil, reauthRequired()
			}
			if _, err := auth.NormalizePassword(in.Body.NewPassword); err != nil {
				return nil, api.Invalid(err.Error(), api.FieldError{Location: "body.new_password", Message: err.Error()})
			}
			if why := auth.WeakPasswordReason(in.Body.NewPassword); why != "" {
				return nil, api.Invalid("the new password is too easy to guess", api.FieldError{Location: "body.new_password", Message: why})
			}
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.SetPassword(ctx, tx, p.ID, in.Body.NewPassword, p.SessionID); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "user.password.change", TargetKind: "user", TargetID: p.ID})
			})
		})
}

// ssoProviderID resolves an SSO provider id or slug ("" stays "").
func (h *Handlers) ssoProviderID(ctx context.Context, idOrSlug string) (string, error) {
	if idOrSlug == "" {
		return "", nil
	}
	p, err := h.Auth.GetOIDCProvider(ctx, idOrSlug)
	if err != nil {
		return "", api.Invalid("unknown SSO provider", api.FieldError{Location: "body.sso_provider", Message: "no SSO provider with this id or slug"})
	}
	if p.Provisioning != "preprovisioned" {
		return "", api.Invalid("SSO provider does not use pre-provisioned accounts",
			api.FieldError{Location: "body.sso_provider", Message: "only providers in pre-provisioned mode link existing accounts"})
	}
	return p.ID, nil
}

// reauthRequired is the API error for step-up.
func reauthRequired() error {
	return api.Errorf(http.StatusForbidden, "reauth_required", auth.ErrReauthRequired.Error())
}

// ReauthRequest confirms the signed-in user's password.
type ReauthRequest struct {
	Password string `json:"password" maxLength:"4096"`
}

// ReauthResponse reports the refreshed sign-in time.
type ReauthResponse struct {
	AuthAt time.Time `json:"auth_at"`
}

func (h *Handlers) registerReauth(a *api.API) {
	type in struct{ Body ReauthRequest }
	type out struct{ Body ReauthResponse }
	api.Register(a, api.Op{ID: "reauthenticate", Method: http.MethodPost, Path: "/session/reauth",
		Summary: "Confirm my password to unlock sensitive actions for 10 minutes", Tags: []string{"Session"}, Permission: auth.PermSession},
		func(ctx context.Context, i *in) (*out, error) {
			p := auth.FromContext(ctx)
			if err := h.Auth.Reauthenticate(ctx, p, i.Body.Password); err != nil {
				var te *auth.ThrottledError
				switch {
				case errors.As(err, &te):
					return nil, api.Errorf(http.StatusTooManyRequests, "throttled", "too many attempts; try again later")
				case errors.Is(err, auth.ErrInvalidCredentials):
					return nil, api.Errorf(http.StatusUnauthorized, "invalid_credentials", "password is incorrect")
				case errors.Is(err, auth.ErrReauthRequired):
					return nil, api.Forbidden("only interactive sessions can re-authenticate")
				default:
					return nil, api.Errorf(http.StatusConflict, "sso_session", err.Error())
				}
			}
			_ = audit.Record(ctx, h.DB.W, audit.Entry{Action: "session.reauth", TargetKind: "user", TargetID: p.ID})
			o := &out{}
			o.Body.AuthAt = h.Auth.Now()
			return o, nil
		})
}
