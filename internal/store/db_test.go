package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	main, err := Open(ctx, Options{Path: filepath.Join(dir, "harbor.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	traces, err := Open(ctx, Options{Path: filepath.Join(dir, "traces.db"), Migrations: migrations.Traces()})
	if err != nil {
		t.Fatal(err)
	}
	defer traces.Close()

	if v, err := main.SchemaVersion(ctx); err != nil || v == 0 {
		t.Fatalf("schema version %d %v", v, err)
	}
	// FTS5 must be available in the pure-Go driver.
	if _, err := traces.W.ExecContext(ctx, `INSERT INTO trace_search(trace_id, body) VALUES ('trc_x', 'hello masked world')`); err != nil {
		t.Fatalf("fts5 insert: %v", err)
	}
	var n int
	if err := traces.R.QueryRowContext(ctx, `SELECT count(*) FROM trace_search WHERE trace_search MATCH 'masked'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fts5 match: %d %v", n, err)
	}
	// Reader handle is query-only.
	if _, err := main.R.ExecContext(ctx, `INSERT INTO kv(key,value,updated_at) VALUES ('a','b',0)`); err == nil {
		t.Fatal("reader accepted a write")
	}
}

func TestMigratorRefusesUnknownAndOutOfOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	v1 := fstest.MapFS{"10000000000000001_one_up.sql": {Data: []byte(`CREATE TABLE one(a INTEGER) STRICT;`)}}
	v3 := fstest.MapFS{
		"10000000000000001_one_up.sql":   {Data: []byte(`CREATE TABLE one(a INTEGER) STRICT;`)},
		"10000000000000003_three_up.sql": {Data: []byte(`CREATE TABLE three(a INTEGER) STRICT;`)},
	}
	db, err := Open(ctx, Options{Path: path, Migrations: v3, BackupBeforeMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := Open(ctx, Options{Path: path, Migrations: v1}); err == nil {
		t.Fatal("expected refusal: database newer than binary")
	}
	withGap := fstest.MapFS{
		"10000000000000001_one_up.sql":   v3["10000000000000001_one_up.sql"],
		"10000000000000002_two_up.sql":   {Data: []byte(`CREATE TABLE two(a INTEGER) STRICT;`)},
		"10000000000000003_three_up.sql": v3["10000000000000003_three_up.sql"],
	}
	if _, err := Open(ctx, Options{Path: path, Migrations: withGap}); err == nil {
		t.Fatal("expected refusal: out-of-order migration")
	}
	v4 := fstest.MapFS{
		"10000000000000001_one_up.sql":   v3["10000000000000001_one_up.sql"],
		"10000000000000003_three_up.sql": v3["10000000000000003_three_up.sql"],
		"10000000000000004_four_up.sql":  {Data: []byte(`CREATE TABLE four(a INTEGER) STRICT;`)},
	}
	db, err = Open(ctx, Options{Path: path, Migrations: v4, BackupBeforeMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(path + ".pre-10000000000000004.bak"); err != nil {
		t.Fatalf("pre-migration backup missing: %v", err)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "h.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	boom := errors.New("boom")
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv(key,value,updated_at) VALUES ('k','v',1)`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	var n int
	_ = db.R.QueryRowContext(ctx, `SELECT count(*) FROM kv`).Scan(&n)
	if n != 0 {
		t.Fatal("transaction was not rolled back")
	}
}

func TestBackupTo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, Options{Path: filepath.Join(dir, "h.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dst := filepath.Join(dir, "backup.db")
	if err := db.BackupTo(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if err := db.BackupTo(ctx, dst); err == nil {
		t.Fatal("expected refusal to overwrite existing backup")
	}
}
