package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Errors surfaced to callers. Messages are safe to show to users and never
// reveal whether an account exists.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrThrottled          = errors.New("too many failed attempts; try again later")
	ErrSessionInvalid     = errors.New("session expired or revoked")
	ErrPasswordLoginOff   = errors.New("password sign-in is disabled; use single sign-on")
	ErrTokenInvalid       = errors.New("token is invalid, expired or already used")
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrLastAdmin          = errors.New("cannot remove or demote the last active administrator")
)

// Options configure the service.
type Options struct {
	DB            *store.DB
	Keys          *store.ServerKeys
	Keyring       *crypto.Keyring
	Clock         clock.Clock
	Log           *slog.Logger
	SecureCookies bool
	PublicOrigin  string // scheme://host used for Origin checks and OIDC redirects
}

// Service implements authentication and user/service-principal management.
type Service struct {
	o             Options
	db            *store.DB
	clock         clock.Clock
	sessionPepper []byte
	tokenPepper   []byte
}

// New creates the service and loads (or creates) its server keys.
func New(ctx context.Context, o Options) (*Service, error) {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	sp, err := o.Keys.Get(ctx, store.KeySessionPepper)
	if err != nil {
		return nil, fmt.Errorf("session pepper: %w", err)
	}
	tp, err := o.Keys.Get(ctx, store.KeyTokenPepper)
	if err != nil {
		return nil, fmt.Errorf("token pepper: %w", err)
	}
	return &Service{o: o, db: o.DB, clock: o.Clock, sessionPepper: sp, tokenPepper: tp}, nil
}

// TokenPepper returns the HMAC key used for bearer-token verifiers (shared
// with gateway credentials).
func (s *Service) TokenPepper() []byte { return s.tokenPepper }

// Cookie names depend on whether Secure cookies are possible.
func (s *Service) SessionCookieName() string {
	if s.o.SecureCookies {
		return "__Host-hp_session"
	}
	return "hp_session"
}

// FlowCookieName is the OIDC browser-binding cookie.
func (s *Service) FlowCookieName() string {
	if s.o.SecureCookies {
		return "__Host-hp_oidc"
	}
	return "hp_oidc"
}

// SecureCookies reports whether cookies carry the Secure attribute.
func (s *Service) SecureCookies() bool { return s.o.SecureCookies }

// PublicOrigin returns the configured public origin.
func (s *Service) PublicOrigin() string { return s.o.PublicOrigin }

func (s *Service) nowMS() int64 { return s.clock.Now().UnixMilli() }

// ---------- settings ----------

// Settings are the mutable authentication settings.
type Settings struct {
	PasswordLogin   bool
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	Revision        int64
	UpdatedAt       time.Time
}

// GetSettings loads the authentication settings.
func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	var st Settings
	var pl int64
	var idle, abs, upd int64
	err := s.db.R.QueryRowContext(ctx, `SELECT password_login, idle_timeout_s, absolute_timeout_s, updated_at, revision FROM auth_settings WHERE id = 1`).
		Scan(&pl, &idle, &abs, &upd, &st.Revision)
	st.PasswordLogin = pl == 1
	st.IdleTimeout = time.Duration(idle) * time.Second
	st.AbsoluteTimeout = time.Duration(abs) * time.Second
	st.UpdatedAt = time.UnixMilli(upd).UTC()
	return st, err
}

// UpdateSettings stores new settings (caller checks revision).
func (s *Service) UpdateSettings(ctx context.Context, tx *sql.Tx, st Settings) (int64, error) {
	if st.IdleTimeout < 5*time.Minute || st.IdleTimeout > 24*time.Hour {
		return 0, fmt.Errorf("idle timeout must be between 5m and 24h")
	}
	if st.AbsoluteTimeout < st.IdleTimeout || st.AbsoluteTimeout > 30*24*time.Hour {
		return 0, fmt.Errorf("absolute timeout must be between the idle timeout and 30 days")
	}
	pl := 0
	if st.PasswordLogin {
		pl = 1
	}
	var rev int64
	err := tx.QueryRowContext(ctx, `UPDATE auth_settings SET password_login = ?, idle_timeout_s = ?, absolute_timeout_s = ?,
		updated_at = ?, revision = revision + 1 WHERE id = 1 RETURNING revision`,
		pl, int64(st.IdleTimeout/time.Second), int64(st.AbsoluteTimeout/time.Second), s.nowMS()).Scan(&rev)
	return rev, err
}

// ---------- sessions ----------

// Now returns the service clock's current time.
func (s *Service) Now() time.Time { return s.clock.Now() }

// SessionInfo describes a validated web session.
type SessionInfo struct {
	DisplayID       string
	UserID          string
	AuthMethod      string
	AuthProvider    string
	Limited         bool
	AuthAt          time.Time
	CreatedAt       time.Time
	LastSeenAt      time.Time
	IdleExpiresAt   time.Time
	AbsoluteExpires time.Time
	IP              string
	UserAgent       string
	csrfSecret      []byte
}

// CreateSession starts a session for userID and returns the cookie token.
func (s *Service) CreateSession(ctx context.Context, ex store.Execer, userID, method, provider string, limited bool) (string, *SessionInfo, error) {
	st, err := s.GetSettings(ctx)
	if err != nil {
		return "", nil, err
	}
	now := s.clock.Now()
	tok := RandomString(32)
	info := &SessionInfo{
		DisplayID: ids.NewAt(ids.Session, now), UserID: userID, AuthMethod: method, AuthProvider: provider,
		Limited: limited, AuthAt: now, CreatedAt: now, LastSeenAt: now,
		IdleExpiresAt: now.Add(st.IdleTimeout), AbsoluteExpires: now.Add(st.AbsoluteTimeout),
		csrfSecret: crypto.RandomBytes(32),
	}
	if limited {
		// Setup and recovery sessions are short-lived regardless of settings.
		info.AbsoluteExpires = now.Add(time.Hour)
		info.IdleExpiresAt = now.Add(30 * time.Minute)
	}
	m := reqmeta.From(ctx)
	info.IP, info.UserAgent = m.IP, m.UserAgent
	lim := 0
	if limited {
		lim = 1
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO web_sessions(id_hash, display_id, user_id, csrf_secret, auth_method, auth_provider,
		limited, auth_at, created_at, last_seen_at, idle_expires_at, absolute_expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		HashOpaque(s.sessionPepper, "session", tok), info.DisplayID, userID, info.csrfSecret, method, nullStr(provider),
		lim, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), info.IdleExpiresAt.UnixMilli(), info.AbsoluteExpires.UnixMilli(),
		nullStr(info.IP), nullStr(info.UserAgent))
	if err != nil {
		return "", nil, err
	}
	return tok, info, nil
}

// ValidateSession resolves a cookie token. When touch is true the idle
// deadline is extended (at most once per minute to bound writes).
func (s *Service) ValidateSession(ctx context.Context, tok string, touch bool) (*Principal, *SessionInfo, error) {
	if tok == "" || len(tok) > 128 {
		return nil, nil, ErrSessionInvalid
	}
	h := HashOpaque(s.sessionPepper, "session", tok)
	var info SessionInfo
	var provider, ip, ua sql.NullString
	var limited int64
	var authAt, created, seen, idle, abs int64
	var revoked sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `SELECT display_id, user_id, csrf_secret, auth_method, auth_provider, limited, auth_at,
		created_at, last_seen_at, idle_expires_at, absolute_expires_at, ip, user_agent, revoked_at FROM web_sessions WHERE id_hash = ?`, h).
		Scan(&info.DisplayID, &info.UserID, &info.csrfSecret, &info.AuthMethod, &provider, &limited, &authAt, &created, &seen,
			&idle, &abs, &ip, &ua, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	now := s.clock.Now()
	if revoked.Valid || now.UnixMilli() >= idle || now.UnixMilli() >= abs {
		return nil, nil, ErrSessionInvalid
	}
	info.AuthProvider, info.IP, info.UserAgent = provider.String, ip.String, ua.String
	info.Limited = limited == 1
	info.AuthAt, info.CreatedAt, info.LastSeenAt = time.UnixMilli(authAt), time.UnixMilli(created), time.UnixMilli(seen)
	info.IdleExpiresAt, info.AbsoluteExpires = time.UnixMilli(idle), time.UnixMilli(abs)

	u, err := s.GetUser(ctx, info.UserID)
	if err != nil || u.Disabled {
		return nil, nil, ErrSessionInvalid
	}
	// Roles sourced from SSO only apply to SSO sessions; a local password
	// session of an SSO-provisioned user gets the viewer role at most.
	role := u.Role
	if u.RoleSource != "local" && info.AuthMethod != "oidc" && info.AuthMethod != "breakglass" {
		role = RoleViewer
	}
	p := NewPrincipal(KindUser, u.ID, u.DisplayName, role, u.Grants)
	p.AuthMethod = info.AuthMethod
	p.AuthAt = info.AuthAt
	p.SessionID = info.DisplayID
	p.Limited = info.Limited
	if info.Limited {
		p.Perms = map[Permission]bool{}
	}

	if touch && now.Sub(info.LastSeenAt) > time.Minute {
		st, err := s.GetSettings(ctx)
		if err == nil {
			newIdle := now.Add(st.IdleTimeout)
			if info.Limited {
				newIdle = now.Add(30 * time.Minute)
			}
			if newIdle.After(info.AbsoluteExpires) {
				newIdle = info.AbsoluteExpires
			}
			if _, err := s.db.W.ExecContext(ctx, `UPDATE web_sessions SET last_seen_at = ?, idle_expires_at = ? WHERE id_hash = ?`,
				now.UnixMilli(), newIdle.UnixMilli(), h); err == nil {
				info.LastSeenAt, info.IdleExpiresAt = now, newIdle
			}
		}
	}
	return p, &info, nil
}

// CSRFToken returns the CSRF token bound to a session.
func (s *Service) CSRFToken(info *SessionInfo) string {
	m := hmac.New(sha256.New, info.csrfSecret)
	m.Write([]byte("harbor-pilot/csrf/v1|" + info.DisplayID))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// CheckCSRF compares a presented CSRF token in constant time.
func (s *Service) CheckCSRF(info *SessionInfo, presented string) bool {
	if presented == "" {
		return false
	}
	return hmac.Equal([]byte(s.CSRFToken(info)), []byte(presented))
}

// RevokeSessionToken revokes the session identified by its cookie token.
func (s *Service) RevokeSessionToken(ctx context.Context, tok, reason string) error {
	_, err := s.db.W.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = ?, revoke_reason = ? WHERE id_hash = ? AND revoked_at IS NULL`,
		s.nowMS(), reason, HashOpaque(s.sessionPepper, "session", tok))
	return err
}

// RevokeSession revokes a session by display id. When userID is non-empty
// the session must belong to that user.
func (s *Service) RevokeSession(ctx context.Context, displayID, userID, reason string) error {
	q := `UPDATE web_sessions SET revoked_at = ?, revoke_reason = ? WHERE display_id = ? AND revoked_at IS NULL`
	args := []any{s.nowMS(), reason, displayID}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := s.db.W.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeUserSessions revokes every active session of a user.
func (s *Service) RevokeUserSessions(ctx context.Context, ex store.Execer, userID, reason string) error {
	_, err := ex.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = ?, revoke_reason = ? WHERE user_id = ? AND revoked_at IS NULL`,
		s.nowMS(), reason, userID)
	return err
}

// ListSessions lists active sessions of a user.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]SessionInfo, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT display_id, auth_method, auth_provider, limited, auth_at, created_at, last_seen_at,
		idle_expires_at, absolute_expires_at, ip, user_agent FROM web_sessions
		WHERE user_id = ? AND revoked_at IS NULL AND idle_expires_at > ? AND absolute_expires_at > ? ORDER BY created_at DESC LIMIT 100`,
		userID, s.nowMS(), s.nowMS())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var i SessionInfo
		var prov, ip, ua sql.NullString
		var lim, authAt, created, seen, idle, abs int64
		if err := rows.Scan(&i.DisplayID, &i.AuthMethod, &prov, &lim, &authAt, &created, &seen, &idle, &abs, &ip, &ua); err != nil {
			return nil, err
		}
		i.UserID = userID
		i.AuthProvider, i.IP, i.UserAgent, i.Limited = prov.String, ip.String, ua.String, lim == 1
		i.AuthAt, i.CreatedAt, i.LastSeenAt = time.UnixMilli(authAt), time.UnixMilli(created), time.UnixMilli(seen)
		i.IdleExpiresAt, i.AbsoluteExpires = time.UnixMilli(idle), time.UnixMilli(abs)
		out = append(out, i)
	}
	return out, rows.Err()
}

// PurgeExpired deletes sessions, OIDC state and one-time tokens past expiry.
func (s *Service) PurgeExpired(ctx context.Context) error {
	now := s.nowMS()
	week := now - 7*24*time.Hour.Milliseconds()
	for _, q := range []string{
		`DELETE FROM web_sessions WHERE absolute_expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)`,
		`DELETE FROM oidc_auth_state WHERE expires_at < ? OR created_at < ?`,
		`DELETE FROM one_time_tokens WHERE expires_at < ? OR (used_at IS NOT NULL AND used_at < ?)`,
	} {
		if _, err := s.db.W.ExecContext(ctx, q, now, week); err != nil {
			return err
		}
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
