package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/bridge/claudecode"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota"
)

// BridgeStatus reports a CLI bridge's availability.
type BridgeStatus struct {
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	LastError string `json:"last_error,omitempty"`
}

func lookBinary(configured, name string) string {
	if configured != "" {
		return configured
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// setupBridges registers CLI bridges whose binaries are available.
func (a *App) setupBridges(o Options) {
	cc := a.Cfg.Bridges.ClaudeCode
	st := &BridgeStatus{Kind: cp.ProviderClaudeCode}
	a.bridgeStatus = append(a.bridgeStatus, st)
	if cc.Disabled {
		return
	}
	bin := lookBinary(cc.Binary, "claude")
	if bin == "" {
		st.LastError = "claude CLI not found on PATH (set bridges.claude_code.binary)"
		return
	}
	b := claudecode.New(claudecode.Options{Binary: bin, Root: filepath.Join(a.Cfg.Server.DataDir, "bridges", "claude_code"),
		ExtraEnv: o.BridgeEnv, MaxProcesses: cc.MaxProcesses, MaxPerAccount: cc.MaxPerAccount, IdleTimeout: cc.IdleTimeout,
		ToolTimeout: cc.ToolTimeout, Log: a.Log, Now: a.Clock.Now,
		OnRateLimit: func(accountID string, info claudecode.RateLimitInfo) { a.Quota.ObserveCLI(accountID, cliWindows(info)) }})
	a.Gateway.Bridges[cp.ProviderClaudeCode] = b
	a.closers = append(a.closers, b.Close)
	st.Enabled, st.Binary = true, bin
	a.AddBackground(func(ctx context.Context) {
		v, err := b.Version(ctx)
		a.bridgeMu.Lock()
		defer a.bridgeMu.Unlock()
		if err != nil {
			st.LastError = err.Error()
			a.Log.Warn("bridge.claude_code.self_test_failed", "error", err.Error())
			return
		}
		st.Version = v
		a.Log.Info("bridge.claude_code.ready", "version", v)
	})
}

func cliWindows(info claudecode.RateLimitInfo) []quota.CLIWindow {
	out := make([]quota.CLIWindow, 0, len(info.UnifiedWindows)+1)
	for name, w := range info.UnifiedWindows {
		out = append(out, quota.CLIWindow{Name: name, Utilization: w.Utilization, ResetsAt: time.Unix(w.ResetsAt, 0).UTC(), Status: w.Status})
	}
	if len(out) == 0 && info.RateLimitType != "" {
		cw := quota.CLIWindow{Name: info.RateLimitType, Status: info.Status, ResetsAt: time.Unix(info.ResetsAt, 0).UTC()}
		if info.Utilization != nil {
			cw.Utilization = *info.Utilization
		} else if info.Status == "rejected" {
			cw.Utilization = 1
		}
		out = append(out, cw)
	}
	return out
}
