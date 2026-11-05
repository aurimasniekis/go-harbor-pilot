// Package ops implements online backup, verification and restore.
//
// A backup is a directory with consistent SQLite snapshots taken with
// VACUUM INTO (safe while the server runs) and a manifest of checksums.
// Trace content (traces.db) is excluded unless explicitly requested.
// Restore needs the server stopped: it verifies checksums and SQLite
// integrity, checks that the configured master key can open the backup's
// sealed secrets, moves the current files aside, and installs the backup.
package ops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"

	_ "modernc.org/sqlite" // database/sql driver
)

// FormatVersion of the backup layout.
const FormatVersion = 1

// File is one backed-up database.
type File struct {
	Name          string `json:"name"`
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"bytes"`
	SchemaVersion int64  `json:"schema_version"`
}

// Manifest describes a backup.
type Manifest struct {
	Format        int       `json:"format"`
	CreatedAt     time.Time `json:"created_at"`
	ServerVersion string    `json:"server_version"`
	MasterKeyIDs  []string  `json:"master_key_ids" doc:"Key ids that sealed secrets in this backup."`
	IncludeTraces bool      `json:"include_traces"`
	Files         []File    `json:"files"`
}

// Sources are the live database files.
type Sources struct {
	DB     string
	Traces string
}

func openRO(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
}

func schemaVersion(db *sql.DB) int64 {
	var v sql.NullInt64
	_ = db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return v.Int64
}

// column reads the first column of every row of q. Tables that do not
// exist yet (older schema) read as empty.
func column(db *sql.DB, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v.String)
	}
	return out, rows.Err()
}

// keyIDs lists the master key ids that sealed values in a database.
func keyIDs(db *sql.DB) ([]string, error) {
	seen := map[string]bool{}
	for _, q := range []string{`SELECT envelope FROM resource_secrets`, `SELECT envelope FROM quota_oauth_tokens`, `SELECT envelope FROM server_keys`} {
		envs, err := column(db, q)
		if err != nil {
			return nil, err
		}
		for _, env := range envs {
			if id, err := crypto.EnvelopeKEK(env); err == nil {
				seen[id] = true
			}
		}
	}
	ids, err := column(db, `SELECT kek_id FROM trace_keys`)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id != "" {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func checksum(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // backup file path
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func integrity(path string) error {
	db, err := openRO(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("%s failed the integrity check: %s", filepath.Base(path), res)
	}
	violations, err := column(db, `SELECT "table" FROM pragma_foreign_key_check`)
	if err != nil {
		return err
	}
	fk := len(violations)
	if fk > 0 {
		return fmt.Errorf("%s has %d foreign key violations", filepath.Base(path), fk)
	}
	return nil
}

// Backup writes a backup into outDir (which must not exist or be empty).
func Backup(ctx context.Context, src Sources, outDir string, includeTraces bool, serverVersion string) (*Manifest, error) {
	if entries, err := os.ReadDir(outDir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("backup directory %s is not empty", outDir)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	m := &Manifest{Format: FormatVersion, CreatedAt: time.Now().UTC(), ServerVersion: serverVersion, IncludeTraces: includeTraces}
	type item struct{ src, name string }
	items := []item{{src.DB, "harbor.db"}}
	if includeTraces && src.Traces != "" {
		items = append(items, item{src.Traces, "traces.db"})
	}
	keys := map[string]bool{}
	for _, it := range items {
		db, err := openRO(it.src)
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(outDir, it.name)
		// Created 0600 first: VACUUM INTO keeps an existing empty file's mode.
		if err := store.CreatePrivate(dst); err != nil {
			_ = db.Close()
			return nil, err
		}
		if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("snapshot %s: %w", it.name, err)
		}
		_ = db.Close()
		if err := os.Chmod(dst, 0o600); err != nil {
			return nil, err
		}
		if err := integrity(dst); err != nil {
			return nil, err
		}
		snap, err := openRO(dst)
		if err != nil {
			return nil, err
		}
		f := File{Name: it.name, SchemaVersion: schemaVersion(snap)}
		ids, err := keyIDs(snap)
		_ = snap.Close()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			keys[id] = true
		}
		if f.SHA256, f.Bytes, err = checksum(dst); err != nil {
			return nil, err
		}
		m.Files = append(m.Files, f)
	}
	for id := range keys {
		m.MasterKeyIDs = append(m.MasterKeyIDs, id)
	}
	sort.Strings(m.MasterKeyIDs)
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), b, 0o600); err != nil {
		return nil, err
	}
	return m, nil
}

// Verify checks a backup's manifest, checksums and SQLite integrity.
func Verify(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // operator-chosen backup directory
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON: %w", err)
	}
	if m.Format != FormatVersion {
		return nil, fmt.Errorf("unsupported backup format %d", m.Format)
	}
	hasMain := false
	for _, f := range m.Files {
		if f.Name != "harbor.db" && f.Name != "traces.db" {
			return nil, fmt.Errorf("unexpected file %q in manifest", f.Name)
		}
		hasMain = hasMain || f.Name == "harbor.db"
		sum, n, err := checksum(filepath.Join(dir, f.Name))
		if err != nil {
			return nil, err
		}
		if sum != f.SHA256 || n != f.Bytes {
			return nil, fmt.Errorf("%s does not match its checksum", f.Name)
		}
		if err := integrity(filepath.Join(dir, f.Name)); err != nil {
			return nil, err
		}
	}
	if !hasMain {
		return nil, errors.New("the backup has no harbor.db")
	}
	return &m, nil
}

// RestoreResult reports what restore did.
type RestoreResult struct {
	Manifest *Manifest
	// MovedAside lists the previous files kept next to the data.
	MovedAside []string
}

// Restore installs a verified backup. The server must be stopped (the
// caller holds the data-directory lock). kr must hold every master key
// that sealed the backup's secrets.
func Restore(from string, dst Sources, kr *crypto.Keyring) (*RestoreResult, error) {
	m, err := Verify(from)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, id := range m.MasterKeyIDs {
		if !kr.Has(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the configured master key cannot open this backup (needs key ids %s); add them as previous master keys", strings.Join(missing, ", "))
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	res := &RestoreResult{Manifest: m}
	moveAside := func(path string) error {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			p := path + suffix
			if _, err := os.Stat(p); err == nil {
				aside := p + ".pre-restore-" + stamp
				if err := os.Rename(p, aside); err != nil {
					return err
				}
				res.MovedAside = append(res.MovedAside, aside)
			}
		}
		return nil
	}
	install := func(name, path string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		in, err := os.Open(filepath.Join(from, name)) //nolint:gosec // verified backup file
		if err != nil {
			return err
		}
		defer in.Close()
		tmp := path + ".restore-tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // data directory path
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Sync(); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	if err := moveAside(dst.DB); err != nil {
		return res, err
	}
	if err := install("harbor.db", dst.DB); err != nil {
		return res, err
	}
	// Trace content is restored only when the backup includes it;
	// otherwise the previous traces database is moved aside and a fresh one
	// is created on start, since its content belongs to the replaced state.
	if dst.Traces != "" {
		if err := moveAside(dst.Traces); err != nil {
			return res, err
		}
		for _, f := range m.Files {
			if f.Name == "traces.db" {
				if err := install("traces.db", dst.Traces); err != nil {
					return res, err
				}
			}
		}
	}
	return res, nil
}
