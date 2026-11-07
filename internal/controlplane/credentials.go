package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Gateway credential scopes.
const (
	ScopeInvoke = "gateway:invoke" // call model endpoints
	ScopeModels = "gateway:models" // list models
	ScopeUsage  = "usage:read"     // read own usage/attribution
	ScopeMint   = "tokens:mint"    // mint short-lived child credentials
)

// CredentialScopes lists valid scopes.
var CredentialScopes = []string{ScopeInvoke, ScopeModels, ScopeUsage, ScopeMint}

// ErrCredentialInvalid is returned for any authentication failure.
var ErrCredentialInvalid = errors.New("invalid, expired or revoked gateway credential")

// Credential is gateway credential metadata (never the token).
type Credential struct {
	ID         string            `json:"id"`
	Prefix     string            `json:"prefix" doc:"Non-secret prefix (hpk_<prefix>_…)."`
	BindingID  string            `json:"binding_id"`
	Name       string            `json:"name"`
	Scopes     []string          `json:"scopes"`
	ParentID   string            `json:"parent_id,omitempty"`
	BoundRefs  map[string]string `json:"bound_refs" doc:"Trusted external references bound at mint time (e.g. spaceman.run)."`
	ManagedBy  string            `json:"managed_by"`
	ExpiresAt  *time.Time        `json:"expires_at,omitempty"`
	RevokedAt  *time.Time        `json:"revoked_at,omitempty"`
	CreatedBy  string            `json:"created_by"`
	CreatedAt  time.Time         `json:"created_at"`
	LastUsedAt *time.Time        `json:"last_used_at,omitempty"`
	LastUsedIP string            `json:"last_used_ip,omitempty"`
}

// CredentialInput describes a credential to issue.
type CredentialInput struct {
	Name      string
	Scopes    []string
	TTL       time.Duration
	BoundRefs map[string]string
	ManagedBy string
	// BudgetCheck, when set, runs on a child mint once the request is valid
	// and before anything is written, with the scope the child will spend
	// in. A non-nil error refuses the mint (D-043).
	BudgetCheck func(ctx context.Context, scope ChildScope) error
}

// ChildScope is what a child credential will spend in before routing picks
// an account: its parent's binding, the project its requests are attributed
// to, and that project's workspace.
type ChildScope struct {
	BindingID, ProjectID, WorkspaceID string
}

const credCols = `id, prefix, binding_id, name, scopes, parent_id, bound_refs, managed_by, expires_at, revoked_at, created_by, created_at, last_used_at, last_used_ip` //nolint:gosec // column list, not a credential

func scanCred(r interface{ Scan(...any) error }) (*Credential, error) {
	var c Credential
	var scopes, refs string
	var parent, ip sql.NullString
	var exp, rev, used sql.NullInt64
	var created int64
	if err := r.Scan(&c.ID, &c.Prefix, &c.BindingID, &c.Name, &scopes, &parent, &refs, &c.ManagedBy, &exp, &rev, &c.CreatedBy, &created, &used, &ip); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopes), &c.Scopes)
	_ = json.Unmarshal([]byte(refs), &c.BoundRefs)
	c.ParentID, c.LastUsedIP = parent.String, ip.String
	c.CreatedAt = time.UnixMilli(created).UTC()
	c.ExpiresAt, c.RevokedAt, c.LastUsedAt = msTime(exp), msTime(rev), msTime(used)
	return &c, nil
}

func msTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

// ListCredentials lists credentials of a binding (newest first).
func (s *Service) ListCredentials(ctx context.Context, bindingID string, includeChildren bool) ([]*Credential, error) {
	q := `SELECT ` + credCols + ` FROM gateway_credentials WHERE binding_id = ?`
	if !includeChildren {
		q += ` AND parent_id IS NULL`
	}
	q += ` ORDER BY created_at DESC LIMIT 500`
	return store.Query(ctx, s.db.R, func(r *sql.Rows) (*Credential, error) { return scanCred(r) }, q, bindingID)
}

// GetCredential loads one credential.
func (s *Service) GetCredential(ctx context.Context, id string) (*Credential, error) {
	c, err := scanCred(s.db.R.QueryRowContext(ctx, `SELECT `+credCols+` FROM gateway_credentials WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Service) validateCredInput(in *CredentialInput) []FieldError {
	var out []FieldError
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		out = append(out, FieldError{Location: "body.name", Message: "must be 1-80 characters"})
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{ScopeInvoke, ScopeModels}
	}
	in.Scopes = SortedUnique(in.Scopes)
	for _, sc := range in.Scopes {
		if !slices.Contains(CredentialScopes, sc) {
			out = append(out, FieldError{Location: "body.scopes", Message: "unknown scope " + sc})
		}
	}
	if in.TTL < 0 || in.TTL > 2*365*24*time.Hour {
		out = append(out, FieldError{Location: "body.ttl_seconds", Message: "must be between 0 and two years"})
	}
	in.BoundRefs = EmptyMap(in.BoundRefs)
	v := &Validator{}
	checkRefs(v, "bound_refs", in.BoundRefs)
	out = append(out, v.Errors...)
	if in.ManagedBy == "" {
		in.ManagedBy = ManagedAPI
	}
	return out
}

// IssueCredential mints a credential for a binding and returns the
// plaintext token exactly once.
func (s *Service) IssueCredential(ctx context.Context, bindingID string, in CredentialInput) (*Credential, string, error) {
	if errs := s.validateCredInput(&in); len(errs) > 0 {
		return nil, "", &ValidationError{Errors: errs}
	}
	b, err := s.Get(ctx, BindingDef, bindingID)
	if err != nil {
		return nil, "", err
	}
	return s.insertCredential(ctx, b.ID, "", in)
}

// MintChild mints a short-lived child credential using a parent credential
// that holds tokens:mint. Children inherit the binding, cannot widen scopes,
// cannot outlive the parent, cannot mint further children, and carry trusted
// bound references (for example a SpaceMan run id).
func (s *Service) MintChild(ctx context.Context, parent *AuthenticatedCredential, in CredentialInput) (*Credential, string, error) {
	if !slices.Contains(parent.Scopes, ScopeMint) {
		return nil, "", fmt.Errorf("%w: parent credential lacks %s", ErrCredentialInvalid, ScopeMint)
	}
	if parent.ParentID != "" {
		return nil, "", errors.New("child credentials cannot mint further credentials")
	}
	if in.TTL <= 0 {
		in.TTL = 12 * time.Hour
	}
	if in.TTL > 7*24*time.Hour {
		return nil, "", &ValidationError{Errors: []FieldError{{Location: "body.ttl_seconds", Message: "child credentials live at most 7 days"}}}
	}
	if errs := s.validateCredInput(&in); len(errs) > 0 {
		return nil, "", &ValidationError{Errors: errs}
	}
	for _, sc := range in.Scopes {
		if sc == ScopeMint || !slices.Contains(parent.Scopes, sc) {
			return nil, "", &ValidationError{Errors: []FieldError{{Location: "body.scopes", Message: "child scopes must be a subset of the parent's, excluding tokens:mint"}}}
		}
	}
	if parent.ExpiresAt != nil && s.o.Clock.Now().Add(in.TTL).After(*parent.ExpiresAt) {
		in.TTL = parent.ExpiresAt.Sub(s.o.Clock.Now())
	}
	merged := map[string]string{}
	for k, v := range parent.BoundRefs {
		merged[k] = v
	}
	snap := s.Snapshot()
	binding := snap.Bindings[parent.BindingID]
	for k, v := range in.BoundRefs {
		if pv, ok := parent.BoundRefs[k]; ok && pv != v {
			return nil, "", &ValidationError{Errors: []FieldError{{Location: "body.bound_refs." + k, Message: "cannot override a reference bound to the parent credential"}}}
		}
		if err := checkChildRef(binding, k, v); err != nil {
			return nil, "", &ValidationError{Errors: []FieldError{{Location: "body.bound_refs." + k, Message: err.Error()}}}
		}
		merged[k] = v
	}
	in.BoundRefs = merged
	if in.BudgetCheck != nil {
		sc := ChildScope{BindingID: parent.BindingID, ProjectID: CredentialProject(snap, binding, merged)}
		if pr := snap.Projects[sc.ProjectID]; pr != nil {
			sc.WorkspaceID = pr.Spec.WorkspaceID
		}
		if err := in.BudgetCheck(ctx, sc); err != nil {
			return nil, "", err
		}
	}
	// The minting credential is the creator: it is recorded as created_by
	// and as the audit actor (the gateway request has no console principal).
	ctx = auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindCredential, ID: parent.ID, Name: parent.Name, AuthMethod: "gateway_token"})
	return s.insertCredential(ctx, parent.BindingID, parent.ID, in)
}

// ChildProjectAllowed reports whether a child credential of binding may be
// bound to project: only the binding's own project or one it allows.
func ChildProjectAllowed(binding *Res[BindingSpec], project string) bool {
	if binding == nil {
		return false
	}
	return project == binding.Spec.ProjectID || slices.Contains(binding.Spec.AllowedProjectIDs, project)
}

// CredentialProject is the project a credential's requests are attributed
// to before any request hint: a bound project_id that exists and that the
// binding still allows, otherwise the binding's own project.
func CredentialProject(snap *Snapshot, binding *Res[BindingSpec], boundRefs map[string]string) string {
	if binding == nil {
		return ""
	}
	if pid := boundRefs["project_id"]; pid != "" && snap != nil && snap.Projects[pid] != nil && ChildProjectAllowed(binding, pid) {
		return pid
	}
	return binding.Spec.ProjectID
}

// checkChildRef limits what a minter may bind: a project must be one the
// binding may attribute to, and references the operator configured on the
// binding cannot be replaced.
func checkChildRef(binding *Res[BindingSpec], key, val string) error {
	if key == "project_id" && !ChildProjectAllowed(binding, val) {
		return errors.New("project must be the client's project or one of its allowed projects")
	}
	if binding != nil {
		if bv, ok := binding.Spec.ExternalRefs[key]; ok && bv != val {
			return errors.New("cannot override a reference configured on the client")
		}
	}
	return nil
}

func (s *Service) insertCredential(ctx context.Context, bindingID, parentID string, in CredentialInput) (*Credential, string, error) {
	g := auth.NewToken(auth.TokenKindGateway)
	now := s.o.Clock.Now()
	id := ids.NewAt(ids.Credential, now)
	var exp any
	if in.TTL > 0 {
		exp = now.Add(in.TTL).UnixMilli()
	}
	scopes, _ := json.Marshal(in.Scopes)
	refs, _ := json.Marshal(in.BoundRefs)
	var parent any
	if parentID != "" {
		parent = parentID
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_credentials(id, prefix, verifier, binding_id, name, scopes, parent_id, bound_refs,
			managed_by, expires_at, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, g.Prefix, auth.Verifier(s.fpKey, auth.TokenKindGateway, g.Prefix, g.Secret), bindingID, in.Name, string(scopes), parent, string(refs),
			in.ManagedBy, exp, auth.FromContext(ctx).ID, now.UnixMilli()); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "gateway_credential.issue", TargetKind: KindClientBinding, TargetID: bindingID,
			Details: map[string]any{"credential_id": id, "prefix": g.Prefix, "scopes": in.Scopes, "parent_id": parentID, "bound_refs": in.BoundRefs}}); err != nil {
			return err
		}
		_, err := events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "access", Entity: "credential", Action: "issued"},
			Subjects: map[string]string{"binding_id": bindingID, "credential_id": id},
			Data:     map[string]any{"prefix": g.Prefix, "scopes": in.Scopes, "child": parentID != ""}})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	c, err := s.GetCredential(ctx, id)
	return c, g.Plaintext, err
}

// RevokeCredential revokes a credential and its children.
func (s *Service) RevokeCredential(ctx context.Context, bindingID, credID, reason string) error {
	return s.RevokeCredentialBy(ctx, bindingID, credID, reason, "")
}

// RevokeCredentialBy revokes a credential and records the change source
// (ui, api, terraform, ...) in the audit log.
func (s *Service) RevokeCredentialBy(ctx context.Context, bindingID, credID, reason, source string) error {
	now := s.o.Clock.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE gateway_credentials SET revoked_at = COALESCE(revoked_at, ?), revoke_reason = COALESCE(revoke_reason, ?)
			WHERE id = ? AND binding_id = ?`, now, reason, credID, bindingID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_credentials SET revoked_at = COALESCE(revoked_at, ?), revoke_reason = COALESCE(revoke_reason, 'parent revoked')
			WHERE parent_id = ?`, now, credID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "gateway_credential.revoke", TargetKind: KindClientBinding, TargetID: bindingID,
			Details: map[string]any{"credential_id": credID, "reason": reason, "source": source}}); err != nil {
			return err
		}
		_, err = events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "access", Entity: "credential", Action: "revoked"},
			Subjects: map[string]string{"binding_id": bindingID, "credential_id": credID}, Data: map[string]any{"reason": reason}})
		return err
	})
}

// RevokeByBoundRef revokes every child credential carrying ref key=value
// (e.g. when an orchestrator run ends).
func (s *Service) RevokeByBoundRef(ctx context.Context, key, value, reason string) (int64, error) {
	res, err := s.db.W.ExecContext(ctx, `UPDATE gateway_credentials SET revoked_at = ?, revoke_reason = ?
		WHERE revoked_at IS NULL AND parent_id IS NOT NULL AND json_extract(bound_refs, '$."'||?||'"') = ?`,
		s.o.Clock.Now().UnixMilli(), reason, key, value)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AuthenticatedCredential is the trusted identity of a gateway request.
type AuthenticatedCredential struct {
	ID        string
	BindingID string
	Name      string
	Scopes    []string
	ParentID  string
	BoundRefs map[string]string
	ExpiresAt *time.Time
}

// Has reports whether the credential holds a scope.
func (a *AuthenticatedCredential) Has(scope string) bool { return slices.Contains(a.Scopes, scope) }

// AuthenticateGateway verifies an hpk_ token. The error never distinguishes
// unknown, revoked or expired tokens.
func (s *Service) AuthenticateGateway(ctx context.Context, token string) (*AuthenticatedCredential, error) {
	kind, prefix, secret, err := auth.ParseToken(token)
	if err != nil || kind != auth.TokenKindGateway {
		return nil, ErrCredentialInvalid
	}
	var c AuthenticatedCredential
	var verifier []byte
	var scopes, refs string
	var parent sql.NullString
	var exp, rev, used sql.NullInt64
	err = s.db.R.QueryRowContext(ctx, `SELECT id, binding_id, name, verifier, scopes, parent_id, bound_refs, expires_at, revoked_at, last_used_at
		FROM gateway_credentials WHERE prefix = ?`, prefix).Scan(&c.ID, &c.BindingID, &c.Name, &verifier, &scopes, &parent, &refs, &exp, &rev, &used)
	computed := auth.Verifier(s.fpKey, kind, prefix, secret)
	if err != nil {
		_ = auth.VerifierEqual(computed, make([]byte, len(computed)))
		return nil, ErrCredentialInvalid
	}
	if !auth.VerifierEqual(computed, verifier) {
		return nil, ErrCredentialInvalid
	}
	now := s.o.Clock.Now().UnixMilli()
	if rev.Valid || (exp.Valid && exp.Int64 <= now) {
		return nil, ErrCredentialInvalid
	}
	_ = json.Unmarshal([]byte(scopes), &c.Scopes)
	_ = json.Unmarshal([]byte(refs), &c.BoundRefs)
	c.ParentID = parent.String
	c.ExpiresAt = msTime(exp)
	if !used.Valid || now-used.Int64 > time.Minute.Milliseconds() {
		_, _ = s.db.W.ExecContext(ctx, `UPDATE gateway_credentials SET last_used_at = ?, last_used_ip = ? WHERE id = ?`,
			now, nullString(reqmeta.From(ctx).IP), c.ID)
	}
	return &c, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
