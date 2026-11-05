package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// OIDC flow purposes.
const (
	OIDCPurposeLogin = "login"
	OIDCPurposeLink  = "link"
	OIDCPurposeTest  = "test"
)

// OIDC errors (safe for users).
var (
	ErrOIDCState         = errors.New("sign-in request expired or was not started from this browser")
	ErrOIDCNoAccount     = errors.New("no Harbor Pilot account is linked to this identity; ask an administrator to invite you")
	ErrOIDCNoRole        = errors.New("your identity provider groups do not grant access to Harbor Pilot")
	ErrOIDCDisabled      = errors.New("this sign-in provider is disabled")
	ErrOIDCEmailDomain   = errors.New("your email domain is not allowed to sign in")
	ErrOIDCAlreadyLinked = errors.New("this identity is already linked to another account")
)

// RoleMapping grants a role to members of a group.
type RoleMapping struct {
	Group string `json:"group"`
	Role  Role   `json:"role"`
}

// OIDCProvider is a configured upstream identity provider.
type OIDCProvider struct {
	ID                  string
	Slug                string
	DisplayName         string
	Issuer              string
	ClientID            string
	ClientSecretSet     bool
	ClientSecretRef     string
	Scopes              []string
	GroupsClaim         string
	RoleMappings        []RoleMapping
	DefaultRole         Role // empty = deny when no mapping matches
	Provisioning        string
	AllowedDomains      []string
	AllowedAlgs         []string
	AllowPrivateNetwork bool
	Enabled             bool
	ManagedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Revision            int64
	clientSecretEnv     string
}

const oidcCols = `id, slug, display_name, issuer, client_id, client_secret, client_secret_ref, scopes, groups_claim,
	role_mappings, default_role, provisioning, allowed_domains, allowed_algs, allow_private_network, enabled, managed_by,
	created_at, updated_at, revision`

func scanOIDC(sc interface{ Scan(...any) error }) (*OIDCProvider, error) {
	var p OIDCProvider
	var secret, ref, defRole sql.NullString
	var scopes, maps, domains, algs string
	var priv, enabled, created, updated int64
	if err := sc.Scan(&p.ID, &p.Slug, &p.DisplayName, &p.Issuer, &p.ClientID, &secret, &ref, &scopes, &p.GroupsClaim,
		&maps, &defRole, &p.Provisioning, &domains, &algs, &priv, &enabled, &p.ManagedBy, &created, &updated, &p.Revision); err != nil {
		return nil, err
	}
	p.ClientSecretSet = secret.Valid || ref.Valid
	p.clientSecretEnv = secret.String
	p.ClientSecretRef = ref.String
	_ = json.Unmarshal([]byte(scopes), &p.Scopes)
	_ = json.Unmarshal([]byte(maps), &p.RoleMappings)
	_ = json.Unmarshal([]byte(domains), &p.AllowedDomains)
	_ = json.Unmarshal([]byte(algs), &p.AllowedAlgs)
	p.DefaultRole = Role(defRole.String)
	p.AllowPrivateNetwork, p.Enabled = priv == 1, enabled == 1
	p.CreatedAt, p.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return &p, nil
}

// ListOIDCProviders returns all providers.
func (s *Service) ListOIDCProviders(ctx context.Context) ([]*OIDCProvider, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+oidcCols+` FROM oidc_providers ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OIDCProvider
	for rows.Next() {
		p, err := scanOIDC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetOIDCProvider loads by id or slug.
func (s *Service) GetOIDCProvider(ctx context.Context, idOrSlug string) (*OIDCProvider, error) {
	p, err := scanOIDC(s.db.R.QueryRowContext(ctx, `SELECT `+oidcCols+` FROM oidc_providers WHERE id = ? OR slug = ?`, idOrSlug, idOrSlug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// OIDCInput is a provider create/replace request.
type OIDCInput struct {
	Slug                string
	DisplayName         string
	Issuer              string
	ClientID            string
	ClientSecret        *string // nil = unchanged; "" = clear
	ClientSecretRef     string
	Scopes              []string
	GroupsClaim         string
	RoleMappings        []RoleMapping
	DefaultRole         Role
	Provisioning        string
	AllowedDomains      []string
	AllowedAlgs         []string
	AllowPrivateNetwork bool
	Enabled             bool
	ManagedBy           string
}

func (in *OIDCInput) validate() error {
	var errs []error
	if !ValidName(in.Slug) {
		errs = append(errs, errors.New("slug must be a lowercase slug"))
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		errs = append(errs, errors.New("display_name is required"))
	}
	u, err := url.Parse(in.Issuer)
	if err != nil || u.Host == "" || (u.Scheme != "https" && (u.Scheme != "http" || !in.AllowPrivateNetwork)) {
		errs = append(errs, errors.New("issuer must be an https URL (http only for private-network issuers)"))
	}
	if in.ClientID == "" {
		errs = append(errs, errors.New("client_id is required"))
	}
	if in.ClientSecretRef != "" {
		if _, err := ParseSecretRef(in.ClientSecretRef); err != nil {
			errs = append(errs, err)
		} else if err := credentials.CheckRef(in.ClientSecretRef); err != nil {
			errs = append(errs, fmt.Errorf("client_secret_ref: %w", err))
		}
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{"openid", "profile", "email"}
	}
	if !slices.Contains(in.Scopes, "openid") {
		errs = append(errs, errors.New("scopes must include openid"))
	}
	for _, m := range in.RoleMappings {
		if m.Group == "" || !ValidRole(m.Role) {
			errs = append(errs, fmt.Errorf("invalid role mapping %q -> %q", m.Group, m.Role))
		}
	}
	if in.DefaultRole != "" && !ValidRole(in.DefaultRole) {
		errs = append(errs, errors.New("invalid default_role"))
	}
	switch in.Provisioning {
	case "preprovisioned":
	case "jit":
		if len(in.AllowedDomains) == 0 && len(in.RoleMappings) == 0 {
			errs = append(errs, errors.New("just-in-time provisioning requires allowed_domains or role_mappings"))
		}
	default:
		errs = append(errs, errors.New("provisioning must be preprovisioned or jit"))
	}
	if len(in.AllowedAlgs) == 0 {
		in.AllowedAlgs = []string{oidc.ES256, oidc.RS256, oidc.EdDSA}
	}
	for _, a := range in.AllowedAlgs {
		if !slices.Contains([]string{oidc.ES256, oidc.ES384, oidc.RS256, oidc.RS384, oidc.PS256, oidc.EdDSA}, a) {
			errs = append(errs, fmt.Errorf("unsupported signing algorithm %q", a))
		}
	}
	return errors.Join(errs...)
}

// SaveOIDCProvider creates (id == "") or replaces a provider in tx.
func (s *Service) SaveOIDCProvider(ctx context.Context, tx *sql.Tx, id string, in OIDCInput) (*OIDCProvider, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	if in.RoleMappings == nil {
		in.RoleMappings = []RoleMapping{}
	}
	if in.AllowedDomains == nil {
		in.AllowedDomains = []string{}
	}
	mb := in.ManagedBy
	if mb == "" {
		mb = "ui"
	}
	now := s.nowMS()
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	if id == "" {
		id = ids.New(ids.OIDCProvider)
		_, err := tx.ExecContext(ctx, `INSERT INTO oidc_providers(id, slug, display_name, issuer, client_id, scopes, groups_claim, role_mappings,
			default_role, provisioning, allowed_domains, allowed_algs, allow_private_network, enabled, managed_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, in.Slug, in.DisplayName, strings.TrimRight(in.Issuer, "/"), in.ClientID, enc(in.Scopes), in.GroupsClaim, enc(in.RoleMappings),
			nullStr(string(in.DefaultRole)), in.Provisioning, enc(in.AllowedDomains), enc(in.AllowedAlgs), b2i(in.AllowPrivateNetwork),
			b2i(in.Enabled), mb, now, now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, ErrConflict
			}
			return nil, err
		}
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE oidc_providers SET slug = ?, display_name = ?, issuer = ?, client_id = ?, scopes = ?,
			groups_claim = ?, role_mappings = ?, default_role = ?, provisioning = ?, allowed_domains = ?, allowed_algs = ?,
			allow_private_network = ?, enabled = ?, managed_by = ?, updated_at = ?, revision = revision + 1 WHERE id = ?`,
			in.Slug, in.DisplayName, strings.TrimRight(in.Issuer, "/"), in.ClientID, enc(in.Scopes), in.GroupsClaim, enc(in.RoleMappings),
			nullStr(string(in.DefaultRole)), in.Provisioning, enc(in.AllowedDomains), enc(in.AllowedAlgs), b2i(in.AllowPrivateNetwork),
			b2i(in.Enabled), mb, now, id)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, ErrConflict
			}
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
	}
	if in.ClientSecret != nil {
		var sealed any
		if *in.ClientSecret != "" {
			env, err := s.o.Keyring.Seal([]byte(*in.ClientSecret), "oidc-client-secret|"+id)
			if err != nil {
				return nil, err
			}
			sealed = env
		}
		if _, err := tx.ExecContext(ctx, `UPDATE oidc_providers SET client_secret = ?, client_secret_ref = NULL WHERE id = ?`, sealed, id); err != nil {
			return nil, err
		}
	}
	if in.ClientSecretRef != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE oidc_providers SET client_secret_ref = ?, client_secret = NULL WHERE id = ?`, in.ClientSecretRef, id); err != nil {
			return nil, err
		}
	}
	s.invalidateOIDC(id)
	return scanOIDC(tx.QueryRowContext(ctx, `SELECT `+oidcCols+` FROM oidc_providers WHERE id = ?`, id))
}

// DeleteOIDCProvider removes a provider and its identity links.
func (s *Service) DeleteOIDCProvider(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_identities WHERE provider_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM oidc_providers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.invalidateOIDC(id)
	return nil
}

// ParseSecretRef validates "env:NAME" or "file:/path".
func ParseSecretRef(ref string) (credentials.Ref, error) {
	kind, val, ok := strings.Cut(ref, ":")
	if !ok || val == "" {
		return credentials.Ref{}, errors.New("secret reference must be env:NAME or file:/path")
	}
	switch kind {
	case "env":
		return credentials.Ref{Type: "env", Name: val}, nil
	case "file":
		return credentials.Ref{Type: "file", Path: val}, nil
	}
	return credentials.Ref{}, errors.New("secret reference must be env:NAME or file:/path")
}

// ---------- flow ----------

type oidcRuntime struct {
	provider *oidc.Provider
	cfg      oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
	loadedAt time.Time
}

var (
	oidcMu    sync.Mutex
	oidcCache = map[string]*oidcRuntime{}
)

func (s *Service) invalidateOIDC(id string) {
	oidcMu.Lock()
	delete(oidcCache, id)
	oidcMu.Unlock()
}

// SecretResolver resolves secret references for OIDC client secrets.
var SecretResolver = credentials.NewResolver(nil)

func (s *Service) oidcRuntime(ctx context.Context, p *OIDCProvider) (*oidcRuntime, error) {
	oidcMu.Lock()
	rt, ok := oidcCache[p.ID]
	oidcMu.Unlock()
	if ok && time.Since(rt.loadedAt) < time.Hour {
		return rt, nil
	}
	policy := netpolicy.Policy{AllowPrivate: p.AllowPrivateNetwork}
	client := &http.Client{Timeout: 15 * time.Second, Transport: netpolicy.Transport(policy, netpolicy.DefaultTransportOptions()),
		CheckRedirect: netpolicy.SameHostRedirects(3)}
	dctx := oidc.ClientContext(ctx, client)
	prov, err := oidc.NewProvider(dctx, p.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s failed: %w", p.Slug, err)
	}
	secret := ""
	switch {
	case p.ClientSecretRef != "":
		ref, err := ParseSecretRef(p.ClientSecretRef)
		if err != nil {
			return nil, err
		}
		sv, err := SecretResolver.Resolve(ref)
		if err != nil {
			return nil, fmt.Errorf("oidc client secret: %w", err)
		}
		secret = sv.Reveal()
	case p.clientSecretEnv != "":
		b, err := s.o.Keyring.Open(p.clientSecretEnv, "oidc-client-secret|"+p.ID)
		if err != nil {
			return nil, fmt.Errorf("oidc client secret: %w", err)
		}
		secret = string(b)
	}
	rt = &oidcRuntime{
		provider: prov,
		client:   client,
		cfg: oauth2.Config{ClientID: p.ClientID, ClientSecret: secret, Endpoint: prov.Endpoint(),
			RedirectURL: s.o.PublicOrigin + "/api/v1/auth/oidc/callback", Scopes: p.Scopes},
		verifier: prov.VerifierContext(dctx, &oidc.Config{ClientID: p.ClientID, SupportedSigningAlgs: p.AllowedAlgs, Now: s.clock.Now}),
		loadedAt: time.Now(),
	}
	oidcMu.Lock()
	oidcCache[p.ID] = rt
	oidcMu.Unlock()
	return rt, nil
}

// OIDCStart is the result of starting a flow.
type OIDCStart struct {
	AuthURL       string
	BrowserCookie string
	ExpiresAt     time.Time
}

// StartOIDC begins an authorization-code flow with PKCE (S256), state and
// nonce. Flow state is stored server-side, hashed (verifier sealed), single
// use, valid for 10 minutes and bound to the browser by a cookie.
func (s *Service) StartOIDC(ctx context.Context, idOrSlug, purpose, linkUserID, returnTo string) (*OIDCStart, error) {
	p, err := s.GetOIDCProvider(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}
	if !p.Enabled && purpose != OIDCPurposeTest {
		return nil, ErrOIDCDisabled
	}
	rt, err := s.oidcRuntime(ctx, p)
	if err != nil {
		return nil, err
	}
	state, nonce, browser := RandomString(24), RandomString(24), RandomString(24)
	verifier := oauth2.GenerateVerifier()
	stateHash := HashOpaque(s.sessionPepper, "oidc-state", state)
	sealed, err := s.o.Keyring.Seal([]byte(verifier), "oidc-pkce|"+stateHash)
	if err != nil {
		return nil, err
	}
	exp := s.clock.Now().Add(10 * time.Minute)
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO oidc_auth_state(state_hash, provider_id, browser_hash, nonce_hash, pkce_verifier,
		purpose, link_user_id, return_to, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stateHash, p.ID, HashOpaque(s.sessionPepper, "oidc-browser", browser), HashOpaque(s.sessionPepper, "oidc-nonce", nonce),
		sealed, purpose, nullStr(linkUserID), SafeReturnTo(returnTo), s.nowMS(), exp.UnixMilli()); err != nil {
		return nil, err
	}
	opts := []oauth2.AuthCodeOption{oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)}
	if purpose == OIDCPurposeLink {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
	}
	return &OIDCStart{AuthURL: rt.cfg.AuthCodeURL(state, opts...), BrowserCookie: browser, ExpiresAt: exp}, nil
}

// SafeReturnTo only allows same-site relative paths.
func SafeReturnTo(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || len(p) > 512 {
		return "/"
	}
	return p
}

// OIDCResult is the outcome of a callback.
type OIDCResult struct {
	Purpose    string
	ReturnTo   string
	User       *User
	Session    string
	Info       *SessionInfo
	Claims     map[string]any // test flow only (no tokens, bounded)
	ProviderID string
}

// CompleteOIDC finishes a flow started by StartOIDC.
func (s *Service) CompleteOIDC(ctx context.Context, state, code, issParam, browserCookie string, linkingSession *Principal) (*OIDCResult, error) {
	if state == "" || code == "" || browserCookie == "" {
		return nil, ErrOIDCState
	}
	stateHash := HashOpaque(s.sessionPepper, "oidc-state", state)
	var providerID, browserHash, nonceHash, sealed, purpose, returnTo string
	var linkUser sql.NullString
	var exp int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT provider_id, browser_hash, nonce_hash, pkce_verifier, purpose, link_user_id, return_to, expires_at
			FROM oidc_auth_state WHERE state_hash = ?`, stateHash).
			Scan(&providerID, &browserHash, &nonceHash, &sealed, &purpose, &linkUser, &returnTo, &exp)
		if err != nil {
			return ErrOIDCState
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM oidc_auth_state WHERE state_hash = ?`, stateHash)
		return err
	})
	if err != nil {
		return nil, err
	}
	if exp <= s.nowMS() || HashOpaque(s.sessionPepper, "oidc-browser", browserCookie) != browserHash {
		return nil, ErrOIDCState
	}
	p, err := s.GetOIDCProvider(ctx, providerID)
	if err != nil {
		return nil, ErrOIDCState
	}
	rt, err := s.oidcRuntime(ctx, p)
	if err != nil {
		return nil, err
	}
	if issParam != "" && strings.TrimRight(issParam, "/") != strings.TrimRight(p.Issuer, "/") {
		return nil, errors.New("authorization response issuer mismatch")
	}
	verifier, err := s.o.Keyring.Open(sealed, "oidc-pkce|"+stateHash)
	if err != nil {
		return nil, ErrOIDCState
	}
	xctx := oidc.ClientContext(ctx, rt.client)
	tok, err := rt.cfg.Exchange(xctx, code, oauth2.VerifierOption(string(verifier)))
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", sanitizeOAuthErr(err))
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return nil, errors.New("provider did not return an ID token")
	}
	idt, err := rt.verifier.Verify(xctx, raw)
	if err != nil {
		return nil, fmt.Errorf("ID token rejected: %w", err)
	}
	if len(idt.Audience) > 1 {
		var azp struct {
			AZP string `json:"azp"`
		}
		_ = idt.Claims(&azp)
		if azp.AZP != p.ClientID {
			return nil, errors.New("ID token azp does not match client")
		}
	}
	if HashOpaque(s.sessionPepper, "oidc-nonce", idt.Nonce) != nonceHash {
		return nil, errors.New("ID token nonce mismatch")
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return nil, err
	}
	email, _ := claims["email"].(string)
	emailVerified, _ := claims["email_verified"].(bool)
	name, _ := claims["name"].(string)
	groups := ClaimStrings(claims, p.GroupsClaim)
	res := &OIDCResult{Purpose: purpose, ReturnTo: returnTo, ProviderID: p.ID}

	if purpose == OIDCPurposeTest {
		res.Claims = map[string]any{"iss": idt.Issuer, "sub": idt.Subject, "email": email, "email_verified": emailVerified,
			"name": name, "groups": groups, "mapped_role": string(mapRole(p, groups))}
		return res, nil
	}

	var userID string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var linked string
		lerr := tx.QueryRowContext(ctx, `SELECT user_id FROM user_identities WHERE issuer = ? AND subject = ?`, idt.Issuer, idt.Subject).Scan(&linked)
		switch {
		case purpose == OIDCPurposeLink:
			if linkingSession == nil || !linkUser.Valid || linkingSession.ID != linkUser.String {
				return ErrOIDCState
			}
			if lerr == nil && linked != linkUser.String {
				return ErrOIDCAlreadyLinked
			}
			userID = linkUser.String
			if lerr == nil {
				return nil
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO user_identities(provider_id, issuer, subject, user_id, email_at_link, linked_at) VALUES (?, ?, ?, ?, ?, ?)`,
				p.ID, idt.Issuer, idt.Subject, userID, nullStr(email), s.nowMS())
			return err
		case lerr == nil:
			userID = linked
		case errors.Is(lerr, sql.ErrNoRows):
			uid, err := s.provisionOIDC(ctx, tx, p, idt.Issuer, idt.Subject, email, emailVerified, name, groups)
			if err != nil {
				return err
			}
			userID = uid
		default:
			return lerr
		}
		// Apply group-derived roles for SSO-managed users on every login.
		if role := mapRole(p, groups); len(p.RoleMappings) > 0 || p.DefaultRole != "" {
			var src string
			if err := tx.QueryRowContext(ctx, `SELECT role_source FROM users WHERE id = ?`, userID).Scan(&src); err != nil {
				return err
			}
			if src == "oidc:"+p.ID {
				if role == "" {
					return ErrOIDCNoRole
				}
				if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ? AND role <> ?`, string(role), s.nowMS(), userID, string(role)); err != nil {
					return err
				}
			}
		}
		var disabled sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT disabled_at FROM users WHERE id = ?`, userID).Scan(&disabled); err != nil {
			return err
		}
		if disabled.Valid {
			return ErrOIDCNoAccount
		}
		if _, err := tx.ExecContext(ctx, `UPDATE user_identities SET last_seen_at = ? WHERE issuer = ? AND subject = ?`, s.nowMS(), idt.Issuer, idt.Subject); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, s.nowMS(), userID); err != nil {
			return err
		}
		if purpose == OIDCPurposeLogin {
			res.Session, res.Info, err = s.CreateSession(ctx, tx, userID, "oidc", p.ID, false)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.User, err = s.GetUser(ctx, userID)
	return res, err
}

func (s *Service) provisionOIDC(ctx context.Context, tx *sql.Tx, p *OIDCProvider, iss, sub, email string, verified bool, name string, groups []string) (string, error) {
	link := func(uid string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_identities(provider_id, issuer, subject, user_id, email_at_link, linked_at) VALUES (?, ?, ?, ?, ?, ?)`,
			p.ID, iss, sub, uid, nullStr(email), s.nowMS())
		return err
	}
	switch p.Provisioning {
	case "preprovisioned":
		// Link to an existing account only by verified email, and only when
		// that account has no identity from this issuer yet.
		if email == "" || !verified {
			return "", ErrOIDCNoAccount
		}
		u, _, err := s.userByEmail(ctx, tx, email)
		if err != nil {
			return "", ErrOIDCNoAccount
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_identities WHERE user_id = ? AND issuer = ?`, u.ID, iss).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return "", ErrOIDCAlreadyLinked
		}
		return u.ID, link(u.ID)
	case "jit":
		if email == "" || !verified {
			return "", ErrOIDCNoAccount
		}
		if len(p.AllowedDomains) > 0 {
			domain := strings.ToLower(email[strings.LastIndexByte(email, '@')+1:])
			if !slices.Contains(p.AllowedDomains, domain) {
				return "", ErrOIDCEmailDomain
			}
		}
		role := mapRole(p, groups)
		if role == "" {
			return "", ErrOIDCNoRole
		}
		if _, _, err := s.userByEmail(ctx, tx, email); err == nil {
			// An unlinked local account with this email exists: never take it
			// over automatically.
			return "", ErrOIDCAlreadyLinked
		}
		u, err := s.CreateUser(ctx, tx, NewUser{Email: email, DisplayName: name, Role: role, RoleSource: "oidc:" + p.ID})
		if err != nil {
			return "", err
		}
		return u.ID, link(u.ID)
	}
	return "", ErrOIDCNoAccount
}

var rolePriority = map[Role]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

// mapRole returns the highest role granted by matching groups, else the
// default role (empty = no access).
func mapRole(p *OIDCProvider, groups []string) Role {
	var best Role
	for _, m := range p.RoleMappings {
		if slices.Contains(groups, m.Group) && rolePriority[m.Role] > rolePriority[best] {
			best = m.Role
		}
	}
	if best == "" {
		return p.DefaultRole
	}
	return best
}

// ClaimStrings reads a dotted-path claim as a list of strings (a single
// string is accepted as a one-element list).
func ClaimStrings(claims map[string]any, path string) []string {
	if path == "" {
		return nil
	}
	var cur any = claims
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	switch v := cur.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && len(out) < 256 {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func sanitizeOAuthErr(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode != "" {
			return fmt.Errorf("%s", re.ErrorCode)
		}
		return fmt.Errorf("status %d", re.Response.StatusCode)
	}
	return errors.New("request failed")
}

// UnlinkIdentity removes an identity link and revokes the user's SSO sessions.
func (s *Service) UnlinkIdentity(ctx context.Context, tx *sql.Tx, userID, issuer, subject string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM user_identities WHERE user_id = ? AND issuer = ? AND subject = ?`, userID, issuer, subject)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = ?, revoke_reason = 'identity unlinked'
		WHERE user_id = ? AND auth_method = 'oidc' AND revoked_at IS NULL`, s.nowMS(), userID)
	return err
}

var _ store.Execer = (*sql.Tx)(nil)
