package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// User is a human operator account.
type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        Role
	RoleSource  string
	HasPassword bool
	Disabled    bool
	Grants      []Permission
	Identities  []Identity
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
	Revision    int64
}

// Identity is a linked external (OIDC) identity.
type Identity struct {
	ProviderID string
	Issuer     string
	Subject    string
	LinkedAt   time.Time
	LastSeenAt *time.Time
}

const userCols = `id, email, display_name, role, role_source, password_hash IS NOT NULL, disabled_at IS NOT NULL,
	created_at, updated_at, last_login_at, revision`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	var u User
	var role string
	var hasPw, disabled int64
	var created, updated int64
	var last sql.NullInt64
	if err := sc.Scan(&u.ID, &u.Email, &u.DisplayName, &role, &u.RoleSource, &hasPw, &disabled, &created, &updated, &last, &u.Revision); err != nil {
		return nil, err
	}
	u.Role = Role(role)
	u.HasPassword, u.Disabled = hasPw == 1, disabled == 1
	u.CreatedAt, u.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	if last.Valid {
		t := time.UnixMilli(last.Int64).UTC()
		u.LastLoginAt = &t
	}
	return &u, nil
}

// GetUser loads a user with grants and identities.
func (s *Service) GetUser(ctx context.Context, id string) (*User, error) {
	u, err := scanUser(s.db.R.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadUserExtras(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Service) userByEmail(ctx context.Context, q store.Execer, email string) (*User, string, error) {
	var hash sql.NullString
	row := q.QueryRowContext(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE email = ?`, strings.TrimSpace(email))
	var u User
	var role string
	var hasPw, disabled, created, updated int64
	var last sql.NullInt64
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &role, &u.RoleSource, &hasPw, &disabled, &created, &updated, &last, &u.Revision, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	u.Role, u.HasPassword, u.Disabled = Role(role), hasPw == 1, disabled == 1
	return &u, hash.String, nil
}

func (s *Service) loadUserExtras(ctx context.Context, u *User) error {
	grants, err := store.Query(ctx, s.db.R, func(r *sql.Rows) (Permission, error) {
		var p string
		err := r.Scan(&p)
		return Permission(p), err
	}, `SELECT permission FROM user_grants WHERE user_id = ? ORDER BY permission`, u.ID)
	if err != nil {
		return err
	}
	u.Grants = grants
	idents, err := store.Query(ctx, s.db.R, func(r *sql.Rows) (Identity, error) {
		var id Identity
		var linked int64
		var seen sql.NullInt64
		if err := r.Scan(&id.ProviderID, &id.Issuer, &id.Subject, &linked, &seen); err != nil {
			return id, err
		}
		id.LinkedAt = time.UnixMilli(linked).UTC()
		if seen.Valid {
			t := time.UnixMilli(seen.Int64).UTC()
			id.LastSeenAt = &t
		}
		return id, nil
	}, `SELECT provider_id, issuer, subject, linked_at, last_seen_at FROM user_identities WHERE user_id = ?`, u.ID)
	u.Identities = idents
	return err
}

// ListUsers returns users ordered by id after cursor.
func (s *Service) ListUsers(ctx context.Context, after string, limit int) ([]*User, error) {
	out, err := store.Query(ctx, s.db.R, func(r *sql.Rows) (*User, error) { return scanUser(r) }, `SELECT `+userCols+` FROM users WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	for _, u := range out {
		if err := s.loadUserExtras(ctx, u); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CountUsers returns the number of users.
func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// NewUser describes a user to create.
type NewUser struct {
	Email       string
	DisplayName string
	Role        Role
	Password    string // optional; empty = SSO or invite only
	RoleSource  string
}

// ValidateEmail normalizes and validates an email address.
func ValidateEmail(e string) (string, error) {
	e = strings.TrimSpace(e)
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || len(e) > 254 {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(e), nil
}

// CreateUser inserts a user in tx.
func (s *Service) CreateUser(ctx context.Context, tx *sql.Tx, nu NewUser) (*User, error) {
	email, err := ValidateEmail(nu.Email)
	if err != nil {
		return nil, err
	}
	if !ValidRole(nu.Role) {
		return nil, errors.New("invalid role")
	}
	name := strings.TrimSpace(nu.DisplayName)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	if len(name) > 120 {
		return nil, errors.New("display name too long")
	}
	var hash any
	if nu.Password != "" {
		h, err := HashPassword(nu.Password)
		if err != nil {
			return nil, err
		}
		hash = h
	}
	src := nu.RoleSource
	if src == "" {
		src = "local"
	}
	now := s.nowMS()
	id := ids.New(ids.User)
	var pwAt any
	if hash != nil {
		pwAt = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO users(id, email, display_name, role, role_source, password_hash, password_set_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, email, name, string(nu.Role), src, hash, pwAt, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &User{ID: id, Email: email, DisplayName: name, Role: nu.Role, RoleSource: src, HasPassword: hash != nil,
		CreatedAt: time.UnixMilli(now).UTC(), UpdatedAt: time.UnixMilli(now).UTC(), Revision: 1}, nil
}

// UserPatch lists optional changes.
type UserPatch struct {
	DisplayName *string
	Role        *Role
	Disabled    *bool
}

// UpdateUser applies a patch in tx, refusing to remove the last admin.
func (s *Service) UpdateUser(ctx context.Context, tx *sql.Tx, id string, p UserPatch) error {
	cur, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if p.DisplayName != nil {
		n := strings.TrimSpace(*p.DisplayName)
		if n == "" || len(n) > 120 {
			return errors.New("display name must be 1-120 characters")
		}
		cur.DisplayName = n
	}
	demote := false
	if p.Role != nil {
		if !ValidRole(*p.Role) {
			return errors.New("invalid role")
		}
		demote = cur.Role == RoleAdmin && *p.Role != RoleAdmin
		cur.Role = *p.Role
		cur.RoleSource = "local"
	}
	if p.Disabled != nil {
		demote = demote || (*p.Disabled && !cur.Disabled && cur.Role == RoleAdmin)
		cur.Disabled = *p.Disabled
	}
	if demote {
		if err := s.ensureOtherAdmin(ctx, tx, id); err != nil {
			return err
		}
	}
	var disabledAt any
	if cur.Disabled {
		disabledAt = s.nowMS()
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET display_name = ?, role = ?, role_source = ?,
		disabled_at = CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(disabled_at, ?) END,
		updated_at = ?, revision = revision + 1 WHERE id = ?`,
		cur.DisplayName, string(cur.Role), cur.RoleSource, disabledAt, disabledAt, s.nowMS(), id)
	if err != nil {
		return err
	}
	if cur.Disabled {
		return s.RevokeUserSessions(ctx, tx, id, "user disabled")
	}
	return nil
}

func (s *Service) ensureOtherAdmin(ctx context.Context, tx *sql.Tx, exceptID string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL AND id <> ?`, exceptID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// DeleteUser removes a user (not the last admin).
func (s *Service) DeleteUser(ctx context.Context, tx *sql.Tx, id string) error {
	var role string
	err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role == string(RoleAdmin) {
		if err := s.ensureOtherAdmin(ctx, tx, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

// SetPassword sets or clears (empty) a user's local password and revokes
// other sessions.
func (s *Service) SetPassword(ctx context.Context, tx *sql.Tx, id, password, keepSession string) error {
	var hash any
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return err
		}
		hash = h
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_set_at = ?, updated_at = ?, revision = revision + 1 WHERE id = ?`,
		hash, s.nowMS(), s.nowMS(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = ?, revoke_reason = 'password changed'
		WHERE user_id = ? AND revoked_at IS NULL AND display_id <> ?`, s.nowMS(), id, keepSession)
	return err
}

// SetGrants replaces a user's explicit grants (content permissions only).
func (s *Service) SetGrants(ctx context.Context, tx *sql.Tx, id string, grants []Permission, by string) error {
	for _, g := range grants {
		ok := false
		for _, c := range ContentPermissions {
			if g == c {
				ok = true
			}
		}
		if !ok {
			return errors.New("only trace content permissions can be granted explicitly: " + string(g))
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_grants WHERE user_id = ?`, id); err != nil {
		return err
	}
	for _, g := range grants {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_grants(user_id, permission, granted_by, granted_at) VALUES (?, ?, ?, ?)`,
			id, string(g), by, s.nowMS()); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = ?, revision = revision + 1 WHERE id = ?`, s.nowMS(), id)
	return err
}
