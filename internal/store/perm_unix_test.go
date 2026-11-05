//go:build unix

package store

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

// Database, WAL, shared-memory and backup files are owner-only even under a
// permissive umask (Docker and bare runs do not set one).
func TestDatabaseFilesArePrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "data")
	ctx := context.Background()
	db, err := Open(ctx, Options{Path: filepath.Join(dir, "harbor.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backup := filepath.Join(dir, "copy.db")
	if err := db.BackupTo(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("data dir mode %v", st.Mode().Perm())
	}
	for _, f := range []string{"harbor.db", "harbor.db-wal", "harbor.db-shm", "copy.db"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", f, st.Mode().Perm())
		}
	}
}
