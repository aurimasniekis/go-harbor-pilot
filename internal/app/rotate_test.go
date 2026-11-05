package app_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

// Rotation re-wraps every sealed store, including quota OAuth tokens and
// trace bucket keys, so nothing depends on the previous key afterwards.
func TestRotateCoversEverySealedStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "harbor.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	traces, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "traces.db"), Migrations: migrations.Traces()})
	if err != nil {
		t.Fatal(err)
	}
	defer traces.Close()
	oldK, _ := crypto.ParseKEK(crypto.GenerateKEKString())
	newK, _ := crypto.ParseKEK(crypto.GenerateKEKString())
	oldKR, _ := crypto.NewKeyring(oldK)

	tok, err := oldKR.Seal([]byte("refresh-token-value"), "quota-oauth-token:qmn_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO quota_oauth_tokens(monitor_id, envelope, updated_at) VALUES ('qmn_1', ?, 0)`, tok); err != nil {
		t.Fatal(err)
	}
	dek := []byte("0123456789abcdef0123456789abcdef")
	kek, wrapped, err := oldKR.WrapDEK(dek, "trace-key:pol_1|2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := traces.W.Exec(`INSERT INTO trace_keys(id, bucket, kek_id, wrapped_dek, expires_at, created_at) VALUES ('tk_1', 'pol_1|2026-11-08', ?, ?, 0, 0)`, kek, wrapped); err != nil {
		t.Fatal(err)
	}

	kr, _ := crypto.NewKeyring(newK, oldK)
	if stale, _ := app.StaleKeyIDs(ctx, db, traces, kr); len(stale) == 0 {
		t.Fatal("before rotation the old key should be reported as still in use")
	}
	if _, err := app.RotateSecrets(ctx, db, kr); err != nil {
		t.Fatal(err)
	}
	if n, err := app.RotateTraceKeys(ctx, traces, kr); err != nil || n != 1 {
		t.Fatalf("trace keys rotated %d, err %v", n, err)
	}
	if stale, err := app.StaleKeyIDs(ctx, db, traces, kr); err != nil || len(stale) != 0 {
		t.Fatalf("still sealed by %v (err %v)", stale, err)
	}

	onlyNew, _ := crypto.NewKeyring(newK)
	var env string
	_ = db.R.QueryRow(`SELECT envelope FROM quota_oauth_tokens WHERE monitor_id = 'qmn_1'`).Scan(&env)
	if b, err := onlyNew.Open(env, "quota-oauth-token:qmn_1"); err != nil || string(b) != "refresh-token-value" {
		t.Fatalf("token after rotation: %q %v", b, err)
	}
	var gotKEK string
	var gotWrapped []byte
	_ = traces.R.QueryRow(`SELECT kek_id, wrapped_dek FROM trace_keys WHERE id = 'tk_1'`).Scan(&gotKEK, &gotWrapped)
	if d, err := onlyNew.UnwrapDEK(gotKEK, gotWrapped, "trace-key:pol_1|2026-11-08"); err != nil || string(d) != string(dek) {
		t.Fatalf("trace key after rotation: %v", err)
	}
}
