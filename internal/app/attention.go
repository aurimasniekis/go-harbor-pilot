package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ops"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// AttentionItem is one condition an operator should act on.
type AttentionItem struct {
	ID       string     `json:"id" doc:"Stable key for the condition (for dismiss state in clients)."`
	Severity string     `json:"severity" enum:"critical,warning,info"`
	Area     string     `json:"area" enum:"security,quota,budget,provider,compatibility,delivery,system"`
	Title    string     `json:"title" doc:"Short plain-language summary."`
	Detail   string     `json:"detail,omitempty" required:"false"`
	Link     string     `json:"link" doc:"UI path where the condition can be resolved."`
	Since    *time.Time `json:"since,omitempty" required:"false"`
}

// backupMaxAge is how old the newest backup may be before it is flagged.
const backupMaxAge = 7 * 24 * time.Hour

var severityRank = map[string]int{"critical": 0, "warning": 1, "info": 2}

// Attention collects current conditions that need an operator, most severe
// first. Each check is cheap and reads in-memory state or small queries.
func (a *App) Attention(ctx context.Context) ([]AttentionItem, error) {
	now := a.Clock.Now()
	snap := a.CP.Snapshot()
	var out []AttentionItem
	add := func(it AttentionItem) { out = append(out, it) }

	// Security alerts (break-glass recovery and similar).
	alerts, err := a.Auth.ListSecurityAlerts(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, al := range alerts {
		t := al.CreatedAt
		add(AttentionItem{ID: "security:" + al.ID, Severity: "critical", Area: "security", Title: "Security alert: " + al.Message,
			Link: "/settings/audit", Since: &t})
	}

	// Quota tracking: failing credentials and used-up limits.
	mons, accounts := a.Quota.Board()
	for _, m := range mons {
		if !m.Enabled {
			continue
		}
		switch m.Health {
		case "auth_failed":
			add(AttentionItem{ID: "quota-auth:" + m.ID, Severity: "critical", Area: "quota", Title: "Quota tracking for " + m.Name + " can't sign in",
				Detail: "The provider rejected its credential. Update the credential to resume tracking.", Link: "/quotas/monitors", Since: m.Poll.LastAttemptAt})
		case "error", "stale":
			add(AttentionItem{ID: "quota-poll:" + m.ID, Severity: "warning", Area: "quota", Title: "Quota data for " + m.Name + " is out of date",
				Link: "/quotas/monitors", Since: m.Poll.LastSuccessAt})
		}
		for _, b := range m.Buckets {
			if b.Exhausted && !b.ResetPassed {
				add(exhausted("quota-used:"+m.ID+":"+b.Key, m.Name, b.Label, b.ResetAt, m.AccountID != "" && b.Global))
			}
		}
	}
	for _, acc := range accounts {
		name := acc.AccountID
		if r := snap.Accounts[acc.AccountID]; r != nil {
			name = r.Name
		}
		for _, b := range acc.Buckets {
			if b.Exhausted && !b.ResetPassed && b.Global {
				add(exhausted("quota-used:"+acc.AccountID+":"+b.Key, name, b.Label, b.ResetAt, true))
			}
		}
	}

	// Budgets at or over their warning threshold.
	usage, err := a.Budgets.Status(ctx, a.DB.R, now)
	if err != nil {
		return nil, err
	}
	for _, u := range usage {
		if !u.Enabled {
			continue
		}
		pct := fmt.Sprintf("%.0f%%", u.Fraction*100)
		switch u.State {
		case "exceeded":
			title := "Budget " + u.Name + " is used up"
			if u.Enforcement == "block" {
				title += "; new requests in its scope are blocked"
			}
			add(AttentionItem{ID: "budget:" + u.BudgetID, Severity: "critical", Area: "budget", Title: title,
				Detail: pct + " of " + u.Currency + " " + u.Limit.String() + " this " + u.Period + ".", Link: "/usage/budgets"})
		case "warning":
			add(AttentionItem{ID: "budget:" + u.BudgetID, Severity: "warning", Area: "budget", Title: "Budget " + u.Name + " is at " + pct,
				Detail: u.Currency + " " + u.Limit.String() + " per " + u.Period + ".", Link: "/usage/budgets"})
		}
	}

	// CLI bridges that accounts depend on but that cannot run.
	bridgeKinds := map[string]bool{}
	for _, acc := range snap.Accounts {
		if p := snap.Providers[acc.Spec.ProviderID]; p != nil && acc.Spec.Enabled && (p.Spec.Type == "claude_code" || p.Spec.Type == "codex") {
			bridgeKinds[p.Spec.Type] = true
		}
	}
	a.bridgeMu.Lock()
	for _, b := range a.bridgeStatus {
		if bridgeKinds[b.Kind] && (b.Binary == "" || b.LastError != "") {
			add(AttentionItem{ID: "bridge:" + b.Kind, Severity: "critical", Area: "provider", Title: bridgeTitle(b.Kind) + " CLI is not available",
				Detail: "Accounts that run through it can't serve requests. Install the CLI or set its path.", Link: "/providers"})
		}
	}
	a.bridgeMu.Unlock()

	// Paused protocol paths.
	for _, br := range a.Compat.List() {
		if br.State == "closed" {
			continue
		}
		prov := br.ProviderID
		if p := snap.Providers[br.ProviderID]; p != nil {
			prov = p.Name
		}
		add(AttentionItem{ID: "breaker:" + br.Key, Severity: "warning", Area: "compatibility",
			Title: "Requests to " + prov + " over " + br.Path + " are paused after repeated failures", Link: "/compatibility", Since: br.OpenedAt})
	}

	// Deliveries that gave up.
	if err := a.deadDeliveries(ctx, snap, add); err != nil {
		return nil, err
	}

	// System setup.
	if !a.Cfg.Demo.Enabled {
		if !a.Cfg.SecureCookies() && !loopbackURL(a.Cfg.Server.PublicURL) {
			add(AttentionItem{ID: "system:plain-http", Severity: "warning", Area: "system", Title: "Sign-in uses plain HTTP",
				Detail: "Set public_url to an https address so session cookies are protected.", Link: "/settings/system"})
		}
		if last := a.newestBackup(); last.IsZero() {
			add(AttentionItem{ID: "system:no-backup", Severity: "warning", Area: "system", Title: "No backup has been taken",
				Link: "/settings/system"})
		} else if now.Sub(last) > backupMaxAge {
			add(AttentionItem{ID: "system:old-backup", Severity: "warning", Area: "system", Title: "The newest backup is more than a week old",
				Link: "/settings/system", Since: &last})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return severityRank[out[i].Severity] < severityRank[out[j].Severity] })
	if out == nil {
		out = []AttentionItem{}
	}
	return out, nil
}

// exhausted reports a used-up quota window; routes says whether routing
// avoids the linked account until the reset.
func exhausted(id, owner, label string, reset *time.Time, routes bool) AttentionItem {
	it := AttentionItem{ID: id, Severity: "warning", Area: "quota", Title: label + " limit reached on " + owner, Link: "/quotas"}
	var parts []string
	if reset != nil {
		parts = append(parts, "Resets "+reset.UTC().Format("2 Jan 15:04 MST")+".")
	}
	if routes {
		parts = append(parts, "Routing skips this account until then.")
	}
	it.Detail = strings.Join(parts, " ")
	return it
}

func bridgeTitle(kind string) string {
	if kind == "claude_code" {
		return "Claude Code"
	}
	return "Codex"
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *App) deadDeliveries(ctx context.Context, snap *cp.Snapshot, add func(AttentionItem)) error {
	type row struct {
		consumer string
		n        int
	}
	rows, err := store.Query(ctx, a.DB.R, func(r *sql.Rows) (row, error) {
		var x row
		err := r.Scan(&x.consumer, &x.n)
		return x, err
	}, `SELECT consumer, count(*) FROM event_deliveries WHERE status = 'dead' GROUP BY consumer`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		id := strings.TrimPrefix(r.consumer, "dst:")
		name := id
		if d := snap.Destinations[id]; d != nil {
			name = d.Name
		}
		noun := "events"
		if r.n == 1 {
			noun = "event"
		}
		add(AttentionItem{ID: "delivery:" + id, Severity: "warning", Area: "delivery", Title: fmt.Sprintf("%d %s could not be delivered to %s", r.n, noun, name),
			Link: "/events/destinations/" + id})
	}
	var n int
	if err := a.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE status = 'dead' AND kind != 'test'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		add(AttentionItem{ID: "delivery:notifications", Severity: "warning", Area: "delivery", Title: fmt.Sprintf("%d notifications could not be sent", n),
			Link: "/notifications/history"})
	}
	return nil
}

// newestBackup returns the creation time of the newest backup in the data
// directory, or zero when there is none.
func (a *App) newestBackup() time.Time {
	root := filepath.Join(a.Cfg.Server.DataDir, "backups")
	entries, err := os.ReadDir(root)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "manifest.json")) //nolint:gosec // data directory
		if err != nil {
			continue
		}
		var m ops.Manifest
		if json.Unmarshal(b, &m) == nil && m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}
	return newest
}

func (a *App) registerAttention(ap *api.API) {
	type out struct {
		Body struct {
			Items []AttentionItem `json:"items"`
		}
	}
	api.Register(ap, api.Op{ID: "list-attention", Method: http.MethodGet, Path: "/attention", Summary: "Conditions that need an operator",
		Description: "Failing credentials, used-up quotas and budgets, unavailable CLI bridges, paused protocol paths, failed deliveries and setup gaps, most severe first.",
		Tags:        []string{"System"}, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*out, error) {
		items, err := a.Attention(ctx)
		if err != nil {
			return nil, err
		}
		o := &out{}
		o.Body.Items = items
		return o, nil
	})
}
