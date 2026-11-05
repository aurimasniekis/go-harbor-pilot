package authapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

// AuthSettingsDTO are the mutable authentication settings.
type AuthSettingsDTO struct {
	PasswordLogin          bool      `json:"password_login" doc:"When false, only SSO (and break-glass recovery) can sign in."`
	IdleTimeoutSeconds     int64     `json:"idle_timeout_seconds" minimum:"300" maximum:"86400"`
	AbsoluteTimeoutSeconds int64     `json:"absolute_timeout_seconds" minimum:"300" maximum:"2592000"`
	UpdatedAt              time.Time `json:"updated_at" readOnly:"true"`
	Revision               int64     `json:"revision" readOnly:"true"`
}

// SecurityAlertDTO is a persistent security notice.
type SecurityAlertDTO struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Message        string     `json:"message"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
}

// AuditEntryDTO is one audit record.
type AuditEntryDTO struct {
	ID         string          `json:"id"`
	At         time.Time       `json:"at"`
	ActorID    string          `json:"actor_id"`
	ActorKind  string          `json:"actor_kind"`
	Action     string          `json:"action"`
	TargetKind string          `json:"target_kind,omitempty"`
	TargetID   string          `json:"target_id,omitempty"`
	Outcome    string          `json:"outcome"`
	IP         string          `json:"ip,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	Details    json.RawMessage `json:"details"`
}

func settingsDTO(s auth.Settings) AuthSettingsDTO {
	return AuthSettingsDTO{PasswordLogin: s.PasswordLogin, IdleTimeoutSeconds: int64(s.IdleTimeout / time.Second),
		AbsoluteTimeoutSeconds: int64(s.AbsoluteTimeout / time.Second), UpdatedAt: s.UpdatedAt, Revision: s.Revision}
}

func (h *Handlers) registerAdmin(a *api.API) {
	type settingsOut struct {
		ETag string `header:"ETag"`
		Body AuthSettingsDTO
	}
	api.Register(a, api.Op{ID: "get-auth-settings", Method: http.MethodGet, Path: "/auth/settings", Summary: "Get authentication settings",
		Tags: []string{"Single sign-on"}, Permission: auth.PermAuthSettings}, func(ctx context.Context, _ *struct{}) (*settingsOut, error) {
		s, err := h.Auth.GetSettings(ctx)
		if err != nil {
			return nil, err
		}
		return &settingsOut{ETag: api.ETag(s.Revision), Body: settingsDTO(s)}, nil
	})
	type putIn struct {
		IfMatch string `header:"If-Match"`
		Body    AuthSettingsDTO
	}
	api.Register(a, api.Op{ID: "replace-auth-settings", Method: http.MethodPut, Path: "/auth/settings", Summary: "Replace authentication settings",
		Tags: []string{"Single sign-on"}, Permission: auth.PermAuthSettings}, func(ctx context.Context, in *putIn) (*settingsOut, error) {
		cur, err := h.Auth.GetSettings(ctx)
		if err != nil {
			return nil, err
		}
		if err := api.CheckIfMatch(in.IfMatch, cur.Revision, false); err != nil {
			return nil, err
		}
		if !in.Body.PasswordLogin {
			provs, err := h.Auth.ListOIDCProviders(ctx)
			if err != nil {
				return nil, err
			}
			enabled := false
			for _, p := range provs {
				enabled = enabled || p.Enabled
			}
			if !enabled {
				return nil, api.Conflict("enable at least one SSO provider before disabling password sign-in")
			}
		}
		err = h.tx(ctx, func(tx *sql.Tx) error {
			if _, err := h.Auth.UpdateSettings(ctx, tx, auth.Settings{PasswordLogin: in.Body.PasswordLogin,
				IdleTimeout: time.Duration(in.Body.IdleTimeoutSeconds) * time.Second, AbsoluteTimeout: time.Duration(in.Body.AbsoluteTimeoutSeconds) * time.Second}); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "auth.settings.update", Details: map[string]any{"password_login": in.Body.PasswordLogin,
				"idle_timeout_seconds": in.Body.IdleTimeoutSeconds, "absolute_timeout_seconds": in.Body.AbsoluteTimeoutSeconds}})
		})
		if err != nil {
			return nil, err
		}
		s, err := h.Auth.GetSettings(ctx)
		if err != nil {
			return nil, err
		}
		return &settingsOut{ETag: api.ETag(s.Revision), Body: settingsDTO(s)}, nil
	})

	type alertsIn struct {
		All bool `query:"all" doc:"Include acknowledged alerts."`
	}
	type alertsOut struct{ Body SecurityAlertList }
	api.Register(a, api.Op{ID: "list-security-alerts", Method: http.MethodGet, Path: "/security-alerts", Summary: "List security alerts",
		Tags: []string{"Audit"}, Permission: auth.PermSystemManage}, func(ctx context.Context, in *alertsIn) (*alertsOut, error) {
		as, err := h.Auth.ListSecurityAlerts(ctx, in.All)
		if err != nil {
			return nil, err
		}
		out := &alertsOut{}
		out.Body.Items = []SecurityAlertDTO{}
		for _, a := range as {
			out.Body.Items = append(out.Body.Items, SecurityAlertDTO{ID: a.ID, Kind: a.Kind, Message: a.Message, CreatedAt: a.CreatedAt,
				AcknowledgedAt: a.AcknowledgedAt, AcknowledgedBy: a.AcknowledgedBy})
		}
		return out, nil
	})
	api.Register(a, api.Op{ID: "acknowledge-security-alert", Method: http.MethodPost, Path: "/security-alerts/{id}/acknowledge",
		Summary: "Acknowledge a security alert", Tags: []string{"Audit"}, Permission: auth.PermSystemManage, Status: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			return nil, h.tx(ctx, func(tx *sql.Tx) error {
				if err := h.Auth.AcknowledgeSecurityAlert(ctx, tx, in.ID, auth.FromContext(ctx).ID); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "security_alert.acknowledge", TargetKind: "security_alert", TargetID: in.ID})
			})
		})

	type auditIn struct {
		api.PageParams
		Action     string `query:"action" maxLength:"64" doc:"Action prefix filter, e.g. auth. or trace.content"`
		TargetKind string `query:"target_kind" maxLength:"64"`
		TargetID   string `query:"target_id" maxLength:"64"`
		ActorID    string `query:"actor_id" maxLength:"64"`
		Outcome    string `query:"outcome" enum:"success,denied,error," required:"false"`
		Since      string `query:"since" doc:"RFC 3339; only entries at or after this time."`
		Search     string `query:"q" maxLength:"200" doc:"Text found in the action, actor, target or details."`
	}
	type auditOut struct{ Body api.Page[AuditEntryDTO] }
	api.Register(a, api.Op{ID: "list-audit-log", Method: http.MethodGet, Path: "/audit", Summary: "Query the audit log (newest first)",
		Tags: []string{"Audit"}, Permission: auth.PermAuditRead}, func(ctx context.Context, in *auditIn) (*auditOut, error) {
		before, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		q := `SELECT id, at, actor_id, actor_kind, action, target_kind, target_id, outcome, ip, request_id, details FROM audit_log WHERE 1=1`
		var args []any
		if before != "" {
			q += ` AND id < ?`
			args = append(args, before)
		}
		if in.Action != "" {
			q += ` AND action LIKE ? ESCAPE '\'`
			args = append(args, strings.NewReplacer("%", `\%`, "_", `\_`).Replace(in.Action)+"%")
		}
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, api.Invalid("invalid since", api.FieldError{Location: "query.since", Message: "must be RFC 3339"})
			}
			q += ` AND at >= ?`
			args = append(args, t.UnixMilli())
		}
		if s := strings.TrimSpace(in.Search); s != "" {
			like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s) + "%"
			q += ` AND (action LIKE ? ESCAPE '\' OR actor_id LIKE ? ESCAPE '\' OR COALESCE(target_id, '') LIKE ? ESCAPE '\' OR details LIKE ? ESCAPE '\')`
			args = append(args, like, like, like, like)
		}
		for col, v := range map[string]string{"target_kind": in.TargetKind, "target_id": in.TargetID, "actor_id": in.ActorID, "outcome": in.Outcome} {
			if v != "" {
				q += ` AND ` + col + ` = ?` //nolint:gosec // col comes from the static map above
				args = append(args, v)
			}
		}
		q += ` ORDER BY id DESC LIMIT ?`
		args = append(args, lim+1)
		rows, err := h.DB.R.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var items []AuditEntryDTO
		for rows.Next() {
			var e AuditEntryDTO
			var at int64
			var tk, ti, ip, rid sql.NullString
			var details string
			if err := rows.Scan(&e.ID, &at, &e.ActorID, &e.ActorKind, &e.Action, &tk, &ti, &e.Outcome, &ip, &rid, &details); err != nil {
				return nil, err
			}
			e.At = time.UnixMilli(at).UTC()
			e.TargetKind, e.TargetID, e.IP, e.RequestID = tk.String, ti.String, ip.String, rid.String
			e.Details = json.RawMessage(details)
			items = append(items, e)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return &auditOut{Body: api.Paginate(items, lim, func(e AuditEntryDTO) string { return e.ID })}, nil
	})
}

// SecurityAlertList lists security alerts.
type SecurityAlertList struct {
	Items []SecurityAlertDTO `json:"items"`
}
