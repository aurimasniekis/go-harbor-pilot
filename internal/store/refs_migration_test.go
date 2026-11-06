package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

const refIndexMigration = "20261107100000000_add_trusted_ref_index_up.sql"

// before returns the main migrations that precede the trusted-ref index (the
// 0.1.1 schema).
func before(t *testing.T, name string) fs.FS {
	t.Helper()
	src := migrations.Main()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() >= name {
			continue
		}
		b, err := fs.ReadFile(src, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = &fstest.MapFile{Data: b}
	}
	return out
}

func TestTrustedRefMigrationBackfills(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "h.db")
	if _, err := fs.Stat(migrations.Main(), refIndexMigration); err != nil {
		t.Fatal(err)
	}
	old, err := Open(ctx, Options{Path: path, Migrations: before(t, refIndexMigration)})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO requests(id, created_at, trusted_refs, inbound_protocol, stream, status) VALUES
			('req_a', 1, '{"spaceman.run":"run_a","session":"s_a"}', 'anthropic', 0, 'ok'),
			('req_b', 2, '{"spaceman.run":"run_b"}', 'anthropic', 0, 'ok'),
			('req_c', 3, '{}', 'anthropic', 0, 'ok')`,
		`INSERT INTO ledger(id, at, kind, request_id, usage_source, cost_kind, idempotency_key) VALUES
			('led_a', 10, 'usage', 'req_a', 'upstream_reported', 'none', 'k_a'),
			('led_b', 20, 'usage', 'req_b', 'upstream_reported', 'none', 'k_b'),
			('led_c', 30, 'usage', 'req_c', 'upstream_reported', 'none', 'k_c'),
			('led_x', 40, 'usage', 'req_missing', 'unknown', 'none', 'k_x')`,
	} {
		if _, err := old.W.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	db, err := Open(ctx, Options{Path: path, Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tt := range []struct {
		name, q string
		want    int
	}{
		{"request refs", `SELECT count(*) FROM request_refs`, 3},
		{"run_a request", `SELECT count(*) FROM request_refs WHERE key = 'spaceman.run' AND value = 'run_a' AND request_id = 'req_a'`, 1},
		{"ledger refs", `SELECT count(*) FROM ledger_refs`, 3},
		{"run_b ledger row keeps its time", `SELECT count(*) FROM ledger_refs WHERE key = 'spaceman.run' AND value = 'run_b' AND at = 20 AND ledger_id = 'led_b'`, 1},
		{"session ref", `SELECT count(*) FROM ledger_refs WHERE key = 'session' AND value = 's_a' AND ledger_id = 'led_a'`, 1},
		{"no refs for missing requests", `SELECT count(*) FROM ledger_refs WHERE ledger_id IN ('led_c', 'led_x')`, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var n int
			if err := db.R.QueryRowContext(ctx, tt.q).Scan(&n); err != nil || n != tt.want {
				t.Fatalf("got %d %v, want %d", n, err, tt.want)
			}
		})
	}
}
