package app

import (
	"os"
	"path/filepath"

	"context"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ops"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
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
}

// BackupDTO describes a stored backup.
type BackupDTO struct {
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Intact   bool          `json:"intact"`
	Problem  string        `json:"problem,omitempty"`
	Manifest *ops.Manifest `json:"manifest,omitempty"`
}
