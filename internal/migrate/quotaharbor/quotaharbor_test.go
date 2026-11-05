package quotaharbor_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/migrate/quotaharbor"

	_ "modernc.org/sqlite"
)

const mainConfig = `
include:
  - accounts.d/*.yaml
accounts:
  - id: DeepSeek Main
    provider: deepseek
    alias: DeepSeek
    poll_interval: 10m
    credentials:
      type: env
      name: HP_SECRET_DEEPSEEK_API_KEY
  - id: claude-me
    provider: claude_subscription
    unofficial:
      acknowledge_terms_risk: true
    shared_scope: Claude Max
    options:
      token_source: env
    credentials:
      access_token:
        type: env_file
        path: /etc/qh/env
        key: CLAUDE_TOKEN
  - id: mystery
    provider: no_such_adapter
  - id: existing-one
    provider: deepseek
pricing:
  - provider: openai
    model: gpt-x
budgets:
  - id: monthly
notifications:
  telegram:
    enabled: true
    chat_id: "-1001"
    bot_token:
      type: env
      name: QH_TELEGRAM_TOKEN
    cooldown: 30m
    quiet_hours:
      start: "22:00"
      end: "07:00"
      timezone: Europe/Vilnius
`

const includedConfig = `
accounts:
  - id: openai-org
    provider: openai_admin
    enabled: false
    credentials:
      admin_key:
        type: file
        path: /run/secrets/openai
`

func writeConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "accounts.d"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"quota-harbor.yaml": mainConfig, "accounts.d/openai.yaml": includedConfig} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "quota-harbor.yaml")
}

// writeDB creates a synthetic source database with the tables the importer
// reads: two buckets of the DeepSeek account and one of an account that is
// not imported.
func writeDB(t *testing.T, now time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qh.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE quota_buckets (id TEXT PRIMARY KEY, account_id TEXT NOT NULL, key TEXT NOT NULL, label TEXT NOT NULL, scope TEXT NOT NULL,
			kind TEXT NOT NULL, window_seconds INTEGER, unit TEXT NOT NULL, currency TEXT, model TEXT, model_family TEXT, shared_scope_id TEXT,
			reset_semantics TEXT NOT NULL, period_seconds INTEGER, global INTEGER NOT NULL, official INTEGER NOT NULL)`,
		`CREATE TABLE bucket_observations (id INTEGER PRIMARY KEY, bucket_id TEXT NOT NULL, observed_at INTEGER NOT NULL, source_time INTEGER,
			limit_value REAL, used_value REAL, remaining_value REAL, limit_e10 INTEGER, used_e10 INTEGER, remaining_e10 INTEGER, reset_at INTEGER,
			quality TEXT NOT NULL, source TEXT NOT NULL)`,
		`INSERT INTO quota_buckets VALUES ('b1', 'DeepSeek Main', 'balance', 'Balance', 'account', 'balance', NULL, 'currency', 'USD', NULL, NULL, NULL,
			'none', NULL, 1, 1)`,
		`INSERT INTO quota_buckets VALUES ('b2', 'mystery', 'requests', 'Requests', 'account', 'rate_limit', 60, 'requests', NULL, NULL, NULL, NULL,
			'rolling', NULL, 1, 1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	add := func(bucket string, at time.Time, remaining float64) {
		e10 := int64(remaining * 1e10)
		if _, err := db.Exec(`INSERT INTO bucket_observations(bucket_id, observed_at, remaining_value, remaining_e10, quality, source)
			VALUES (?, ?, ?, ?, 'provider_reported', 'adapter')`, bucket, at.UnixMilli(), remaining, e10); err != nil {
			t.Fatal(err)
		}
	}
	add("b1", now.Add(-200*24*time.Hour), 99) // older than --since
	add("b1", now.Add(-48*time.Hour), 42.5)
	add("b1", now.Add(-24*time.Hour), 40)
	add("b1", now.Add(-time.Hour), 37.25)
	add("b2", now.Add(-time.Hour), 5)
	return path
}

func TestImport(t *testing.T) {
	env := apptest.New(t)
	ctx := auth.WithPrincipal(context.Background(), auth.System)
	env.Admin.MustDo(http.StatusCreated, http.MethodPost, "/api/v1/quota-monitors",
		map[string]any{"name": "existing-one", "spec": map[string]any{"adapter": "deepseek", "enabled": false}}, nil)

	src, err := quotaharbor.Load(writeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Accounts) != 5 {
		t.Fatalf("accounts with includes = %d, want 5", len(src.Accounts))
	}

	plan := quotaharbor.Build(src, env.App.CP.Snapshot())
	got := map[string]quotaharbor.Item{}
	for _, it := range plan.Items {
		got[it.Kind+"/"+it.Name] = it
	}
	cases := []struct {
		key, action, reason string
	}{
		{"quota_monitor/deepseek-main", "create", ""},
		{"quota_monitor/claude-me", "create", `unsupported "env_file" reference`},
		{"quota_monitor/mystery", "skip", "unknown quota adapter"},
		{"quota_monitor/existing-one", "exists", "already exists"},
		{"quota_monitor/openai-org", "create", "admin_key is not supported"},
		{"notification_channel/telegram", "create", "rename it to HP_SECRET_QH_TELEGRAM_TOKEN"},
		{"notification_rule/telegram-quota-alerts", "create", ""},
		{"pricing/", "skip", "1 price entries"},
		{"budget/", "skip", "1 budgets"},
	}
	for _, c := range cases {
		t.Run("plan "+c.key, func(t *testing.T) {
			it, ok := got[c.key]
			if !ok {
				t.Fatalf("missing plan item; have %v", plan.Items)
			}
			if it.Action != c.action || !strings.Contains(it.Reason, c.reason) {
				t.Fatalf("got %s (%s), want %s containing %q", it.Action, it.Reason, c.action, c.reason)
			}
		})
	}

	// Dry run wrote nothing.
	var list struct {
		Items []struct {
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			ManagedBy string         `json:"managed_by"`
			Spec      map[string]any `json:"spec"`
		}
	}
	env.Admin.MustDo(http.StatusOK, http.MethodGet, "/api/v1/quota-monitors", nil, &list)
	if len(list.Items) != 1 {
		t.Fatalf("monitors before apply = %d, want 1", len(list.Items))
	}

	monitors, err := quotaharbor.Apply(ctx, env.App.CP, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 4 {
		t.Fatalf("mapped monitors = %v, want deepseek, claude, openai and the existing one", monitors)
	}
	env.Admin.MustDo(http.StatusOK, http.MethodGet, "/api/v1/quota-monitors", nil, &list)
	byName := map[string]map[string]any{}
	for _, m := range list.Items {
		byName[m.Name] = m.Spec
		if want := map[bool]string{true: "ui", false: "import"}[m.Name == "existing-one"]; m.ManagedBy != want {
			t.Errorf("%s managed_by = %q, want %q", m.Name, m.ManagedBy, want)
		}
	}
	ds := byName["deepseek-main"]
	if ds["api_key_ref"] != "env:HP_SECRET_DEEPSEEK_API_KEY" || ds["poll_interval_secs"] != float64(600) || ds["enabled"] != true {
		t.Fatalf("deepseek monitor spec = %v", ds)
	}
	if cl := byName["claude-me"]; cl["acknowledge_terms_risk"] != true || cl["shared_scope_id"] != "claude-max" || cl["access_token_ref"] != nil && cl["access_token_ref"] != "" {
		t.Fatalf("claude monitor spec = %v", cl)
	}
	if oa := byName["openai-org"]; oa["enabled"] != false {
		t.Fatalf("openai monitor spec = %v", oa)
	}

	var rules struct {
		Items []struct {
			Spec struct {
				ChannelIDs      []string `json:"channel_ids"`
				CooldownSeconds int      `json:"cooldown_seconds"`
			}
		}
	}
	env.Admin.MustDo(http.StatusOK, http.MethodGet, "/api/v1/notification-rules", nil, &rules)
	if len(rules.Items) != 1 || len(rules.Items[0].Spec.ChannelIDs) != 1 || rules.Items[0].Spec.CooldownSeconds != 1800 {
		t.Fatalf("rules = %+v", rules.Items)
	}

	// A second run creates nothing and maps the same monitors.
	again := quotaharbor.Build(src, env.App.CP.Snapshot())
	for _, it := range again.Items {
		if it.Action == "create" {
			t.Errorf("re-run plans to create %s/%s again", it.Kind, it.Name)
		}
	}
	remapped, err := quotaharbor.Apply(ctx, env.App.CP, again)
	if err != nil || len(remapped) != len(monitors) || remapped["DeepSeek Main"] != monitors["DeepSeek Main"] {
		t.Fatalf("re-run mapped %v (err %v), want %v", remapped, err, monitors)
	}

	now := time.Now()
	dbPath := writeDB(t, now)
	n, err := quotaharbor.ImportHistory(ctx, env.App.DB, dbPath, monitors, now.Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("imported %d observations, want 3 (old and unmapped ones skipped)", n)
	}
	if n, err := quotaharbor.ImportHistory(ctx, env.App.DB, dbPath, monitors, now.Add(-90*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("re-import added %d observations (err %v), want 0", n, err)
	}
	var hist struct {
		Points []struct {
			Remaining *float64 `json:"remaining"`
		} `json:"points"`
	}
	env.Admin.MustDo(http.StatusOK, http.MethodGet, fmt.Sprintf("/api/v1/quota/buckets/%s/history?bucket=balance&hours=720", monitors["DeepSeek Main"]), nil, &hist)
	if len(hist.Points) != 3 || hist.Points[2].Remaining == nil || *hist.Points[2].Remaining != 37.25 {
		t.Fatalf("history = %+v", hist.Points)
	}
}
