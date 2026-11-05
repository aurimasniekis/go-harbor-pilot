// Package store owns SQLite access: connection setup, schema migrations,
// transactions and repositories. Each database runs in WAL mode with a single
// writer connection (immediate transactions) and a small read-only pool.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// DB wraps separate writer and reader handles for one SQLite file.
type DB struct {
	W    *sql.DB
	R    *sql.DB
	path string
}

// Options configures Open.
type Options struct {
	Path       string
	Migrations fs.FS
	Readers    int
	Log        *slog.Logger
	// BackupBeforeMigrate writes <path>.pre-<version>.bak before applying
	// pending migrations to an existing database.
	BackupBeforeMigrate bool
}

// Open opens (creating if needed) the database and applies pending migrations.
func Open(ctx context.Context, o Options) (*DB, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Readers <= 0 {
		o.Readers = 4
	}
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	base := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		base.Add("_pragma", p)
	}
	wq := cloneValues(base)
	wq.Set("_txlock", "immediate")
	w, err := sql.Open("sqlite", "file:"+o.Path+"?"+wq.Encode())
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetConnMaxIdleTime(0)
	rq := cloneValues(base)
	rq.Add("_pragma", "query_only(1)")
	r, err := sql.Open("sqlite", "file:"+o.Path+"?"+rq.Encode())
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(o.Readers)
	db := &DB{W: w, R: r, path: o.Path}
	if err := w.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if o.Migrations != nil {
		if err := db.migrate(ctx, o.Migrations, o.BackupBeforeMigrate, o.Log); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// Close closes both handles.
func (db *DB) Close() error { return errors.Join(db.R.Close(), db.W.Close()) }

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Ping checks the writer and reader connections.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.W.PingContext(ctx); err != nil {
		return err
	}
	return db.R.PingContext(ctx)
}

// Tx runs fn in an immediate write transaction. The single writer connection
// serializes all write transactions, which is what makes reservations atomic.
func (db *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// BackupTo writes a consistent online copy of the database to dst using
// VACUUM INTO. dst must not exist.
func (db *DB) BackupTo(ctx context.Context, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("backup target %s already exists", dst)
	}
	_, err := db.W.ExecContext(ctx, `VACUUM INTO ?`, dst)
	return err
}

var migrationName = regexp.MustCompile(`^(\d{17})_([a-z0-9]+(?:_[a-z0-9]+)*)_up\.sql$`)

type migration struct {
	version int64
	name    string
	sql     string
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, _ := strconv.ParseInt(m[1], 10, 64)
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: m[2], sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (db *DB) migrate(ctx context.Context, fsys fs.FS, backup bool, log *slog.Logger) error {
	if _, err := db.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("init migrations table: %w", err)
	}
	all, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	applied, maxApplied, err := appliedVersions(ctx, db.W)
	if err != nil {
		return err
	}
	known := map[int64]bool{}
	for _, m := range all {
		known[m.version] = true
	}
	for v := range applied {
		if !known[v] {
			return fmt.Errorf("database has migration %d unknown to this binary; refusing to run an older version against a newer schema", v)
		}
	}
	var pending []migration
	for _, m := range all {
		if applied[m.version] {
			continue
		}
		if m.version < maxApplied {
			return fmt.Errorf("migration %d is older than the newest applied migration %d; refusing out-of-order apply", m.version, maxApplied)
		}
		pending = append(pending, m)
	}
	if len(pending) == 0 {
		return nil
	}
	if backup && len(applied) > 0 {
		dst := fmt.Sprintf("%s.pre-%d.bak", db.path, pending[0].version)
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			if _, err := db.W.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
				return fmt.Errorf("pre-migration backup: %w", err)
			}
			log.Info("db.migration.backup", "path", dst)
		}
	}
	for _, m := range pending {
		tx, err := db.W.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d_%s: %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		log.Info("db.migration.applied", "db", filepath.Base(db.path), "version", m.version, "name", m.name)
	}
	return nil
}

func appliedVersions(ctx context.Context, q *sql.DB) (map[int64]bool, int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	applied := map[int64]bool{}
	var maxApplied int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, 0, err
		}
		applied[v] = true
		maxApplied = max(maxApplied, v)
	}
	return applied, maxApplied, rows.Err()
}

// SchemaVersion returns the newest applied migration version.
func (db *DB) SchemaVersion(ctx context.Context) (int64, error) {
	var v sql.NullInt64
	err := db.R.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return v.Int64, err
}

// Placeholders returns "?, ?, ?" for n parameters.
func Placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
