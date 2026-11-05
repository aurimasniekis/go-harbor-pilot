package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
)

// ServicePrincipal is an automation identity.
type ServicePrincipal struct {
	ID          string
	Name        string
	Description string
	Permissions []Permission
	Disabled    bool
	ManagedBy   string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Revision    int64
}

// APIToken is token metadata (the plaintext is never stored).
type APIToken struct {
	ID          string
	Prefix      string
	PrincipalID string
	Name        string
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedBy   string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	LastUsedIP  string
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ValidName checks the slug-style names used for principals and objects.
func ValidName(n string) bool { return nameRE.MatchString(n) }

func validatePerms(perms []Permission) ([]Permission, error) {
	out := slices.Clone(perms)
	slices.Sort(out)
	out = slices.Compact(out)
	for _, p := range out {
		if !ValidPermission(p) {
			return nil, errors.New("unknown permission " + string(p))
		}
	}
	return out, nil
}

const spCols = `id, name, description, permissions, disabled_at IS NOT NULL, managed_by, created_by, created_at, updated_at, revision`

func scanSP(sc interface{ Scan(...any) error }) (*ServicePrincipal, error) {
	var sp ServicePrincipal
	var perms string
	var disabled, created, updated int64
	if err := sc.Scan(&sp.ID, &sp.Name, &sp.Description, &perms, &disabled, &sp.ManagedBy, &sp.CreatedBy, &created, &updated, &sp.Revision); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(perms), &sp.Permissions)
	sp.Disabled = disabled == 1
	sp.CreatedAt, sp.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return &sp, nil
}

// GetServicePrincipal loads one principal.
func (s *Service) GetServicePrincipal(ctx context.Context, id string) (*ServicePrincipal, error) {
	sp, err := scanSP(s.db.R.QueryRowContext(ctx, `SELECT `+spCols+` FROM service_principals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sp, err
}

// ListServicePrincipals lists principals after cursor.
func (s *Service) ListServicePrincipals(ctx context.Context, after string, limit int) ([]*ServicePrincipal, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+spCols+` FROM service_principals WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServicePrincipal
	for rows.Next() {
		sp, err := scanSP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// SPInput describes a principal create/update.
type SPInput struct {
	Name        string
	Description string
	Permissions []Permission
	Disabled    bool
	ManagedBy   string
}

// CreateServicePrincipal inserts a principal.
func (s *Service) CreateServicePrincipal(ctx context.Context, tx *sql.Tx, in SPInput, by string) (*ServicePrincipal, error) {
	if !ValidName(in.Name) {
		return nil, errors.New("name must be a lowercase slug (a-z, 0-9, . _ -), 1-63 characters")
	}
	perms, err := validatePerms(in.Permissions)
	if err != nil {
		return nil, err
	}
	pj, _ := json.Marshal(perms)
	now := s.nowMS()
	id := ids.New(ids.Principal)
	mb := in.ManagedBy
	if mb == "" {
		mb = "ui"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO service_principals(id, name, description, permissions, managed_by, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, in.Name, strings.TrimSpace(in.Description), string(pj), mb, by, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrConflict
		}
		return nil, err
	}
	return scanSP(tx.QueryRowContext(ctx, `SELECT `+spCols+` FROM service_principals WHERE id = ?`, id))
}

// UpdateServicePrincipal replaces mutable fields.
func (s *Service) UpdateServicePrincipal(ctx context.Context, tx *sql.Tx, id string, in SPInput) (*ServicePrincipal, error) {
	perms, err := validatePerms(in.Permissions)
	if err != nil {
		return nil, err
	}
	pj, _ := json.Marshal(perms)
	var disabled any
	if in.Disabled {
		disabled = s.nowMS()
	}
	mb := in.ManagedBy
	if mb == "" {
		mb = "ui"
	}
	res, err := tx.ExecContext(ctx, `UPDATE service_principals SET description = ?, permissions = ?,
		disabled_at = CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(disabled_at, ?) END, managed_by = ?,
		updated_at = ?, revision = revision + 1 WHERE id = ?`,
		strings.TrimSpace(in.Description), string(pj), disabled, disabled, mb, s.nowMS(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return scanSP(tx.QueryRowContext(ctx, `SELECT `+spCols+` FROM service_principals WHERE id = ?`, id))
}

// DeleteServicePrincipal deletes a principal and its tokens.
func (s *Service) DeleteServicePrincipal(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM service_principals WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// IssueAPIToken mints a token for a principal. The plaintext is returned once.
func (s *Service) IssueAPIToken(ctx context.Context, tx *sql.Tx, principalID, name string, ttl time.Duration, by string) (*APIToken, string, error) {
	if _, err := scanSP(tx.QueryRowContext(ctx, `SELECT `+spCols+` FROM service_principals WHERE id = ?`, principalID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return nil, "", errors.New("token name must be 1-80 characters")
	}
	if ttl < 0 || ttl > 2*365*24*time.Hour {
		return nil, "", errors.New("token lifetime must be at most two years")
	}
	g := NewToken(TokenKindAPI)
	now := s.clock.Now()
	t := &APIToken{ID: ids.NewAt(ids.Credential, now), Prefix: g.Prefix, PrincipalID: principalID, Name: name, CreatedBy: by, CreatedAt: now.UTC()}
	var exp any
	if ttl > 0 {
		e := now.Add(ttl).UTC()
		t.ExpiresAt = &e
		exp = e.UnixMilli()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(id, prefix, verifier, principal_id, name, expires_at, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, g.Prefix, Verifier(s.tokenPepper, TokenKindAPI, g.Prefix, g.Secret), principalID, name, exp, by, now.UnixMilli())
	if err != nil {
		return nil, "", err
	}
	return t, g.Plaintext, nil
}

// ListAPITokens lists a principal's tokens.
func (s *Service) ListAPITokens(ctx context.Context, principalID string) ([]APIToken, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, prefix, principal_id, name, expires_at, revoked_at, created_by, created_at, last_used_at, last_used_ip
		FROM api_tokens WHERE principal_id = ? ORDER BY created_at DESC`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var exp, rev, used sql.NullInt64
		var ip sql.NullString
		var created int64
		if err := rows.Scan(&t.ID, &t.Prefix, &t.PrincipalID, &t.Name, &exp, &rev, &t.CreatedBy, &created, &used, &ip); err != nil {
			return nil, err
		}
		t.CreatedAt = time.UnixMilli(created).UTC()
		t.ExpiresAt, t.RevokedAt, t.LastUsedAt = msPtr(exp), msPtr(rev), msPtr(used)
		t.LastUsedIP = ip.String
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken revokes a token.
func (s *Service) RevokeAPIToken(ctx context.Context, tx *sql.Tx, principalID, tokenID string) error {
	res, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND principal_id = ?`,
		s.nowMS(), tokenID, principalID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateAPIToken resolves an hpa_ bearer token to a principal.
func (s *Service) AuthenticateAPIToken(ctx context.Context, tok string) (*Principal, error) {
	kind, prefix, secret, err := ParseToken(tok)
	if err != nil || kind != TokenKindAPI {
		return nil, ErrTokenInvalid
	}
	var id, principalID string
	var verifier []byte
	var exp, rev, used sql.NullInt64
	err = s.db.R.QueryRowContext(ctx, `SELECT id, principal_id, verifier, expires_at, revoked_at, last_used_at FROM api_tokens WHERE prefix = ?`, prefix).
		Scan(&id, &principalID, &verifier, &exp, &rev, &used)
	if err != nil {
		// Spend comparable work for unknown prefixes.
		_ = VerifierEqual(Verifier(s.tokenPepper, kind, prefix, secret), make([]byte, 32))
		return nil, ErrTokenInvalid
	}
	if !VerifierEqual(Verifier(s.tokenPepper, kind, prefix, secret), verifier) {
		return nil, ErrTokenInvalid
	}
	now := s.nowMS()
	if rev.Valid || (exp.Valid && exp.Int64 <= now) {
		return nil, ErrTokenInvalid
	}
	sp, err := s.GetServicePrincipal(ctx, principalID)
	if err != nil || sp.Disabled {
		return nil, ErrTokenInvalid
	}
	if !used.Valid || now-used.Int64 > time.Minute.Milliseconds() {
		_, _ = s.db.W.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?`,
			now, nullStr(reqmeta.From(ctx).IP), id)
	}
	p := NewPrincipal(KindService, sp.ID, sp.Name, "", sp.Permissions)
	p.AuthMethod = "api_token"
	return p, nil
}

func msPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}
