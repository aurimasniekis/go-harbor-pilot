package app

import (
	"context"
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
}
