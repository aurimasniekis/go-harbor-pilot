package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ops"
)

// MetaDTO describes the server instance.
type MetaDTO struct {
	Product    string    `json:"product"`
	Version    string    `json:"version"`
	APIVersion string    `json:"api_version"`
	Demo       bool      `json:"demo" doc:"True when serving the deterministic synthetic demo dataset."`
	ServerTime time.Time `json:"server_time"`
	PublicURL  string    `json:"public_url"`
	StartedAt  time.Time `json:"started_at"`
}

// StatusDTO describes storage and key state for operators.
type StatusDTO struct {
	SchemaVersion       int64  `json:"schema_version"`
	TracesSchemaVersion int64  `json:"traces_schema_version"`
	MasterKeyID         string `json:"master_key_id" doc:"Fingerprint of the active master key (not secret)."`
	DataDir             string `json:"data_dir"`
	SecureCookies       bool   `json:"secure_cookies" doc:"False when the public URL is plain http (development only)."`
}

func (a *App) registerSystem(ap *api.API) {
	type metaOut struct{ Body MetaDTO }
	api.Register(ap, api.Op{ID: "get-meta", Method: http.MethodGet, Path: "/meta", Summary: "Server identity, version and clock",
		Tags: []string{"System"}, Permission: auth.PermPublic}, func(ctx context.Context, _ *struct{}) (*metaOut, error) {
		return &metaOut{Body: MetaDTO{Product: "Harbor Pilot", Version: a.Version, APIVersion: api.Version, Demo: a.Cfg.Demo.Enabled,
			ServerTime: a.Clock.Now().UTC(), PublicURL: a.Cfg.Server.PublicURL, StartedAt: a.Started.UTC()}}, nil
	})
	type statusOut struct{ Body StatusDTO }
	api.Register(ap, api.Op{ID: "get-system-status", Method: http.MethodGet, Path: "/system/status", Summary: "Storage and key status",
		Tags: []string{"System"}, Permission: auth.PermSystemManage}, func(ctx context.Context, _ *struct{}) (*statusOut, error) {
		v, err := a.DB.SchemaVersion(ctx)
		if err != nil {
			return nil, err
		}
		tv, err := a.Traces.SchemaVersion(ctx)
		if err != nil {
			return nil, err
		}
		return &statusOut{Body: StatusDTO{SchemaVersion: v, TracesSchemaVersion: tv, MasterKeyID: a.Keyring.CurrentID(),
			DataDir: a.Cfg.Server.DataDir, SecureCookies: a.Cfg.SecureCookies()}}, nil
	})
	type bridgesOut struct {
		Body struct {
			Items []BridgeStatus `json:"items"`
		}
	}
	api.Register(ap, api.Op{ID: "list-bridges", Method: http.MethodGet, Path: "/system/bridges", Summary: "CLI bridge availability",
		Tags: []string{"System"}, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*bridgesOut, error) {
		out := &bridgesOut{}
		out.Body.Items = []BridgeStatus{}
		a.bridgeMu.Lock()
		for _, b := range a.bridgeStatus {
			out.Body.Items = append(out.Body.Items, *b)
		}
		a.bridgeMu.Unlock()
		return out, nil
	})
	type backupIn struct {
		Body struct {
			IncludeTraces bool `json:"include_traces" required:"false" doc:"Also back up encrypted trace content (excluded by default)."`
		}
	}
	type backupOut struct {
		Body struct {
			Path     string        `json:"path"`
			Manifest *ops.Manifest `json:"manifest"`
		}
	}
	api.Register(ap, api.Op{ID: "create-backup", Method: http.MethodPost, Path: "/system/backups", Summary: "Take an online backup",
		Description: "Consistent SQLite snapshots with a checksum manifest, written under <data_dir>/backups. Restore with `harbor-pilot restore` while the server is stopped.",
		Tags:        []string{"System"}, Permission: auth.PermSystemManage}, func(ctx context.Context, in *backupIn) (*backupOut, error) {
		dir := filepath.Join(a.Cfg.Server.DataDir, "backups", a.Clock.Now().UTC().Format("20060102T150405.000Z"))
		m, err := ops.Backup(ctx, ops.Sources{DB: a.Cfg.DBPath(), Traces: a.Cfg.TracesDBPath()}, dir, in.Body.IncludeTraces, a.Version)
		if err != nil {
			return nil, err
		}
		_ = audit.Record(ctx, a.DB.W, audit.Entry{Action: "system.backup", TargetKind: "backup", TargetID: filepath.Base(dir),
			Details: map[string]any{"include_traces": in.Body.IncludeTraces}})
		out := &backupOut{}
		out.Body.Path, out.Body.Manifest = dir, m
		return out, nil
	})
	type listOut struct {
		Body struct {
			Items []BackupDTO `json:"items"`
		}
	}
	api.Register(ap, api.Op{ID: "list-backups", Method: http.MethodGet, Path: "/system/backups", Summary: "Backups in the data directory",
		Tags: []string{"System"}, Permission: auth.PermSystemManage}, func(ctx context.Context, _ *struct{}) (*listOut, error) {
		out := &listOut{}
		out.Body.Items = []BackupDTO{}
		root := filepath.Join(a.Cfg.Server.DataDir, "backups")
		entries, _ := os.ReadDir(root)
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if !e.IsDir() {
				continue
			}
			d := BackupDTO{Name: e.Name(), Path: filepath.Join(root, e.Name())}
			if m, err := ops.Verify(d.Path); err == nil {
				d.Manifest, d.Intact = m, true
			} else {
				d.Problem = err.Error()
			}
			out.Body.Items = append(out.Body.Items, d)
		}
		return out, nil
	})
	a.registerBackupFiles(ap)
}

// backupNameRE matches backup directory names (no separators or dots-only
// names), so a request can never reach outside <data_dir>/backups.
var backupNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// backupDir resolves a backup by name inside <data_dir>/backups.
func (a *App) backupDir(name string) (string, error) {
	if !backupNameRE.MatchString(name) || strings.Contains(name, "..") {
		return "", api.NotFound("backup")
	}
	dir := filepath.Join(a.Cfg.Server.DataDir, "backups", name)
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return "", api.NotFound("backup")
	}
	return dir, nil
}

func (a *App) registerBackupFiles(ap *api.API) {
	type nameIn struct {
		Name string `path:"name" maxLength:"64"`
	}
	api.Register(ap, api.Op{ID: "delete-backup", Method: http.MethodDelete, Path: "/system/backups/{name}", Summary: "Delete a backup from the data directory",
		Tags: []string{"System"}, Permission: auth.PermSystemManage, Status: http.StatusNoContent}, func(ctx context.Context, in *nameIn) (*struct{}, error) {
		dir, err := a.backupDir(in.Name)
		if err != nil {
			return nil, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		_ = audit.Record(ctx, a.DB.W, audit.Entry{Action: "system.backup.delete", TargetKind: "backup", TargetID: in.Name})
		return nil, nil
	})
	api.Register(ap, api.Op{ID: "download-backup", Method: http.MethodGet, Path: "/system/backups/{name}/archive", Summary: "Download a backup as a .tar.gz archive",
		Description: "The archive holds the manifest and database snapshots exactly as stored; sealed secrets stay encrypted and need the same master key to restore.",
		Tags:        []string{"System"}, Permission: auth.PermSystemManage}, func(ctx context.Context, in *nameIn) (*huma.StreamResponse, error) {
		dir, err := a.backupDir(in.Name)
		if err != nil {
			return nil, err
		}
		if _, err := ops.Verify(dir); err != nil {
			return nil, api.Conflict("this backup failed verification and cannot be downloaded: " + err.Error())
		}
		_ = audit.Record(ctx, a.DB.W, audit.Entry{Action: "system.backup.download", TargetKind: "backup", TargetID: in.Name})
		return &huma.StreamResponse{Body: func(hc huma.Context) {
			hc.SetHeader("Content-Type", "application/gzip")
			hc.SetHeader("Content-Disposition", `attachment; filename="harbor-pilot-backup-`+in.Name+`.tar.gz"`)
			_ = writeBackupArchive(hc.BodyWriter(), dir, in.Name)
		}}, nil
	})
}

// writeBackupArchive streams the regular files of a backup directory as a
// gzipped tar under a folder named after the backup.
func writeBackupArchive(w io.Writer, dir, name string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if err := addFile(tw, filepath.Join(dir, e.Name()), name+"/"+e.Name()); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addFile(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path) //nolint:gosec // a regular file inside a validated backup directory
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// BackupDTO describes a stored backup.
type BackupDTO struct {
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Intact   bool          `json:"intact"`
	Problem  string        `json:"problem,omitempty"`
	Manifest *ops.Manifest `json:"manifest,omitempty"`
}
