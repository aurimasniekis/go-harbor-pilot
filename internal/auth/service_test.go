package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

type fixture struct {
	svc   *Service
	db    *store.DB
	clock *clock.Fake
	ctx   context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := reqmeta.With(context.Background(), reqmeta.Meta{IP: "203.0.113.7", UserAgent: "test"})
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "h.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	kek, _ := crypto.ParseKEK(crypto.GenerateKEKString())
	kr, _ := crypto.NewKeyring(kek)
	fc := clock.NewFake(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	svc, err := New(ctx, Options{DB: db, Keys: store.NewServerKeys(db, kr), Keyring: kr, Clock: fc,
		SecureCookies: true, PublicOrigin: "https://hp.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: svc, db: db, clock: fc, ctx: ctx}
}

func (f *fixture) createUser(t *testing.T, email string, role Role, pw string) *User {
	t.Helper()
	var u *User
	if err := f.db.Tx(f.ctx, func(tx *sql.Tx) error {
		var err error
		u, err = f.svc.CreateUser(f.ctx, tx, NewUser{Email: email, Role: role, Password: pw})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBootstrapAndLogin(t *testing.T) {
	f := newFixture(t)
	if req, _ := f.svc.SetupRequired(f.ctx); !req {
		t.Fatal("setup should be required on a fresh database")
	}
	tok, _, err := f.svc.IssueOneTime(f.ctx, PurposeSetup, "", "", "", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.Bootstrap(f.ctx, "wrong", "admin@example.test", "Admin", "a-strong-password"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("wrong token: %v", err)
	}
	sess, info, u, err := f.svc.Bootstrap(f.ctx, tok, "Admin@Example.test", "Admin", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleAdmin || u.Email != "admin@example.test" {
		t.Fatalf("unexpected user %+v", u)
	}
	if _, _, _, err := f.svc.Bootstrap(f.ctx, tok, "x@example.test", "X", "a-strong-password"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("setup token reusable or setup still open")
	}
	p, got, err := f.svc.ValidateSession(f.ctx, sess, true)
	if err != nil || got.DisplayID != info.DisplayID || !p.Can(PermUsersManage) || p.Can(PermTraceContentRead) {
		t.Fatalf("session: %+v %v (content perms must not be implied by admin)", p, err)
	}
	if !f.svc.CheckCSRF(got, f.svc.CSRFToken(got)) || f.svc.CheckCSRF(got, "forged") {
		t.Fatal("csrf check broken")
	}

	if _, _, err := f.svc.Login(f.ctx, "admin@example.test", "a-strong-password"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, _, err := f.svc.Login(f.ctx, "admin@example.test", "wrong-password!"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad password: %v", err)
	}
	if _, _, err := f.svc.Login(f.ctx, "nobody@example.test", "whatever-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user must look identical: %v", err)
	}
}

func TestLoginThrottleAndDecay(t *testing.T) {
	f := newFixture(t)
	f.createUser(t, "a@example.test", RoleViewer, "correct-password")
	for range throttleFree + 1 {
		_, _, _ = f.svc.Login(f.ctx, "a@example.test", "wrong-password!")
	}
	_, _, err := f.svc.Login(f.ctx, "a@example.test", "correct-password")
	var te *ThrottledError
	if !errors.As(err, &te) || te.RetryAfter <= 0 {
		t.Fatalf("expected throttle, got %v", err)
	}
	f.clock.Advance(throttleMax + time.Second)
	if _, _, err := f.svc.Login(f.ctx, "a@example.test", "correct-password"); err != nil {
		t.Fatalf("login after lockout window: %v", err)
	}
}

func TestSessionExpiryAndRevocation(t *testing.T) {
	f := newFixture(t)
	u := f.createUser(t, "a@example.test", RoleOperator, "correct-password")
	tok, info, err := f.svc.CreateSession(f.ctx, f.db.W, u.ID, "password", "", false)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(29 * time.Minute)
	if _, _, err := f.svc.ValidateSession(f.ctx, tok, true); err != nil {
		t.Fatalf("still idle-valid: %v", err)
	}
	f.clock.Advance(29 * time.Minute) // touched above, so still valid
	if _, _, err := f.svc.ValidateSession(f.ctx, tok, false); err != nil {
		t.Fatalf("idle extended by touch: %v", err)
	}
	f.clock.Advance(31 * time.Minute)
	if _, _, err := f.svc.ValidateSession(f.ctx, tok, true); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("idle timeout not enforced: %v", err)
	}
	tok2, info2, _ := f.svc.CreateSession(f.ctx, f.db.W, u.ID, "password", "", false)
	if err := f.svc.RevokeSession(f.ctx, info2.DisplayID, u.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.ValidateSession(f.ctx, tok2, false); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("revoked session accepted")
	}
	_ = info
	// Disabling the user invalidates sessions.
	tok3, _, _ := f.svc.CreateSession(f.ctx, f.db.W, u.ID, "password", "", false)
	disabled := true
	f.createUser(t, "admin@example.test", RoleAdmin, "correct-password")
	if err := f.db.Tx(f.ctx, func(tx *sql.Tx) error { return f.svc.UpdateUser(f.ctx, tx, u.ID, UserPatch{Disabled: &disabled}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.ValidateSession(f.ctx, tok3, false); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("disabled user's session accepted")
	}
}

func TestLastAdminProtection(t *testing.T) {
	f := newFixture(t)
	a := f.createUser(t, "admin@example.test", RoleAdmin, "")
	viewer := RoleViewer
	err := f.db.Tx(f.ctx, func(tx *sql.Tx) error { return f.svc.UpdateUser(f.ctx, tx, a.ID, UserPatch{Role: &viewer}) })
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting last admin: %v", err)
	}
	if err := f.db.Tx(f.ctx, func(tx *sql.Tx) error { return f.svc.DeleteUser(f.ctx, tx, a.ID) }); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deleting last admin: %v", err)
	}
}

func TestRecoveryRaisesAlert(t *testing.T) {
	f := newFixture(t)
	u := f.createUser(t, "admin@example.test", RoleAdmin, "")
	tok, _, _ := f.svc.IssueOneTime(f.ctx, PurposeRecovery, u.ID, "", "", "cli")
	sess, _, err := f.svc.Recover(f.ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := f.svc.ValidateSession(f.ctx, sess, false)
	if err != nil || p.AuthMethod != "breakglass" {
		t.Fatalf("recovery session: %+v %v", p, err)
	}
	alerts, _ := f.svc.ListSecurityAlerts(f.ctx, false)
	if len(alerts) != 1 || alerts[0].Kind != "breakglass" {
		t.Fatalf("alerts: %+v", alerts)
	}
	if _, _, err := f.svc.Recover(f.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("recovery token reusable")
	}
}

func TestAPITokens(t *testing.T) {
	f := newFixture(t)
	var sp *ServicePrincipal
	var plain string
	var tokID string
	err := f.db.Tx(f.ctx, func(tx *sql.Tx) error {
		var err error
		sp, err = f.svc.CreateServicePrincipal(f.ctx, tx, SPInput{Name: "terraform", Permissions: []Permission{PermConfigRead, PermConfigWrite}}, "usr_x")
		if err != nil {
			return err
		}
		t, p, err := f.svc.IssueAPIToken(f.ctx, tx, sp.ID, "ci", time.Hour, "usr_x")
		plain, tokID = p, t.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.AuthenticateAPIToken(f.ctx, plain)
	if err != nil || !p.Can(PermConfigWrite) || p.Can(PermUsersManage) || p.Kind != KindService {
		t.Fatalf("token principal %+v %v", p, err)
	}
	// Tampered secret.
	mid := len(plain) - 10
	flip := byte('a')
	if plain[mid] == 'a' {
		flip = 'b'
	}
	if _, err := f.svc.AuthenticateAPIToken(f.ctx, plain[:mid]+string(flip)+plain[mid+1:]); err == nil {
		t.Fatal("tampered token accepted")
	}
	f.clock.Advance(2 * time.Hour)
	if _, err := f.svc.AuthenticateAPIToken(f.ctx, plain); err == nil {
		t.Fatal("expired token accepted")
	}
	_ = f.db.Tx(f.ctx, func(tx *sql.Tx) error { return f.svc.RevokeAPIToken(f.ctx, tx, sp.ID, tokID) })
	toks, _ := f.svc.ListAPITokens(f.ctx, sp.ID)
	if len(toks) != 1 || toks[0].RevokedAt == nil {
		t.Fatalf("tokens: %+v", toks)
	}
}

func TestParseToken(t *testing.T) {
	g := NewToken(TokenKindGateway)
	k, p, s, err := ParseToken(g.Plaintext)
	if err != nil || k != TokenKindGateway || p != g.Prefix || s != g.Secret {
		t.Fatalf("parse: %v", err)
	}
	for _, bad := range []string{"", "hpk_abc", "xyz_aaaaaaaa_" + g.Secret, "hpk_AAAAAAAA_" + g.Secret} {
		if _, _, _, err := ParseToken(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
