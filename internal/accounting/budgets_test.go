package accounting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

func openDB(t testing.TB) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(t.TempDir(), "h.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var ledgerSeq int

// spend writes one settled ledger row of amount USD in project prj_a
// (workspace wsp_a) at at.
func spend(t testing.TB, ex store.Execer, at time.Time, amount string) {
	t.Helper()
	a, err := money.ParseDecimal(amount)
	if err != nil {
		t.Fatal(err)
	}
	ledgerSeq++
	if _, err := ex.ExecContext(context.Background(), `INSERT INTO ledger(id, at, kind, workspace_id, project_id, binding_id, account_id, usage_source,
		cost_e10, currency, cost_kind, idempotency_key) VALUES (?, ?, 'usage', 'wsp_a', 'prj_a', 'cbd_a', 'acc_a', 'estimated', ?, 'USD', 'estimated_api_charge', ?)`,
		fmt.Sprintf("led_%d", ledgerSeq), at.UnixMilli(), int64(a), fmt.Sprintf("k%d", ledgerSeq)); err != nil {
		t.Fatal(err)
	}
}

func budget(id string, created time.Time, s cp.BudgetSpec) *cp.Res[cp.BudgetSpec] {
	if s.Scope == "" {
		s.Scope, s.ScopeID = cp.BudgetProject, "prj_a"
	}
	if s.Limit == "" {
		s.Limit = "10"
	}
	s.Currency, s.Enabled, s.Enforcement = "USD", true, "block"
	if s.WarnAt == 0 {
		s.WarnAt = 0.8
	}
	s.CostKinds = []string{cp.CostActualAPI, cp.CostEstimatedAPI}
	return &cp.Res[cp.BudgetSpec]{ID: id, Name: id, Spec: s, CreatedAt: created}
}

func budgets(bs ...*cp.Res[cp.BudgetSpec]) *Budgets {
	snap := &cp.Snapshot{Budgets: map[string]*cp.Res[cp.BudgetSpec]{}}
	for _, b := range bs {
		snap.Budgets[b.ID] = b
	}
	return &Budgets{Snapshot: func() *cp.Snapshot { return snap }}
}

// check runs Check in a write transaction as Service.Reserve does: the
// spend cache is updated only once the transaction commits.
func check(ctx context.Context, db *store.DB, bs *Budgets, in ReserveInput, now time.Time) error {
	var committed func()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		committed, err = bs.Check(ctx, tx, in, now)
		return err
	})
	if err == nil && committed != nil {
		committed()
	}
	return err
}

func usd(s string) money.Amount {
	a, err := money.ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return a
}

// A one-time window counts from starts_at (or creation) and, when set,
// until ends_at; a recurring budget counts only its calendar period.
func TestBudgetUsageWindow(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	jan := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	spend(t, db.W, time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "1")
	spend(t, db.W, jan, "2")
	spend(t, db.W, time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC), "4")
	spend(t, db.W, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), "8")
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name  string
		spec  cp.BudgetSpec
		spent string
		end   bool
	}{
		{"month counts this month only", cp.BudgetSpec{Period: "month", Limit: "100"}, "8", true},
		{"once from creation", cp.BudgetSpec{Period: "once", Limit: "100"}, "14", false},
		{"once backdated", cp.BudgetSpec{Period: "once", Limit: "100", StartsAt: "2025-01-01T00:00:00Z"}, "15", false},
		{"once with an end", cp.BudgetSpec{Period: "once", Limit: "100", EndsAt: "2026-03-01T00:00:00Z"}, "6", true},
		{"start is inclusive, end exclusive", cp.BudgetSpec{Period: "once", Limit: "100", StartsAt: "2026-01-20T12:00:00Z", EndsAt: "2026-03-05T00:00:00Z"}, "6", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u, err := budgets().usage(ctx, db.R, budget("bdg_x", created, tt.spec), now, nil)
			if err != nil {
				t.Fatal(err)
			}
			if u.Spent != usd(tt.spent) || (u.PeriodEnd != nil) != tt.end {
				t.Fatalf("spent %s end %v, want %s end set %v", u.Spent, u.PeriodEnd, tt.spent, tt.end)
			}
		})
	}
}

func TestBudgetFinish(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 10)
	mid := start.AddDate(0, 0, 5)
	type in struct {
		enabled         bool
		spent, reserved string
		end             time.Time
		open            bool
		now             time.Time
	}
	for _, tt := range []struct {
		name      string
		in        in
		state     string
		remaining string
		projected string // "" = nil
	}{
		{"ok with projection", in{true, "2", "0", end, false, mid}, "ok", "8", "4"},
		{"warning", in{true, "7", "1", end, false, mid}, "warning", "2", "14"},
		{"exceeded, remaining floors at 0", in{true, "9", "3", end, false, mid}, "exceeded", "0", "18"},
		{"open window has no projection", in{true, "2", "0", time.Time{}, true, mid}, "ok", "8", ""},
		{"too early to project", in{true, "2", "0", end, false, start.Add(30 * time.Minute)}, "ok", "8", ""},
		{"scheduled", in{true, "0", "0", end, false, start.Add(-time.Second)}, "scheduled", "10", ""},
		{"expired at the end", in{true, "12", "0", end, false, end}, "expired", "0", ""},
		{"disabled beats expired", in{false, "12", "0", end, false, end}, "disabled", "0", ""},
		{"expired beats exceeded", in{true, "20", "0", end, false, end.Add(time.Hour)}, "expired", "0", ""},
		{"scheduled beats exceeded", in{true, "20", "0", time.Time{}, true, start.Add(-time.Hour)}, "scheduled", "0", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := budget("bdg_x", start, cp.BudgetSpec{Period: "once"})
			b.Spec.Enabled = tt.in.enabled
			u := BudgetUsage{Limit: usd("10"), Spent: usd(tt.in.spent), Reserved: usd(tt.in.reserved)}
			finish(&u, b, start, tt.in.end, tt.in.open, tt.in.now)
			if u.State != tt.state || u.Remaining != usd(tt.remaining) {
				t.Fatalf("state %s remaining %s, want %s %s", u.State, u.Remaining, tt.state, tt.remaining)
			}
			switch {
			case tt.projected == "" && u.ProjectedSpend != nil:
				t.Fatalf("projection %s, want none", *u.ProjectedSpend)
			case tt.projected != "" && (u.ProjectedSpend == nil || *u.ProjectedSpend != usd(tt.projected)):
				t.Fatalf("projection %v, want %s", u.ProjectedSpend, tt.projected)
			}
		})
	}
}

func TestBudgetActive(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		spec    cp.BudgetSpec
		enabled bool
		want    bool
	}{
		{"recurring", cp.BudgetSpec{Period: "day"}, true, true},
		{"recurring disabled", cp.BudgetSpec{Period: "day"}, false, false},
		{"once open", cp.BudgetSpec{Period: "once"}, true, true},
		{"once disabled", cp.BudgetSpec{Period: "once"}, false, false},
		{"once scheduled", cp.BudgetSpec{Period: "once", StartsAt: "2026-06-01T00:00:01Z"}, true, false},
		{"once starts now", cp.BudgetSpec{Period: "once", StartsAt: "2026-06-01T00:00:00Z"}, true, true},
		{"once ends now", cp.BudgetSpec{Period: "once", EndsAt: "2026-06-01T00:00:00Z"}, true, false},
		{"once ends later", cp.BudgetSpec{Period: "once", EndsAt: "2026-06-01T00:00:01Z"}, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := budget("bdg_x", created, tt.spec)
			b.Spec.Enabled = tt.enabled
			if got := active(b, now); got != tt.want {
				t.Fatalf("active %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPeriodPhrase(t *testing.T) {
	for in, want := range map[string]string{"day": "this day", "week": "this week", "month": "this month", "once": "in total"} {
		if got := PeriodPhrase(in); got != want {
			t.Fatalf("PeriodPhrase(%q) = %q, want %q", in, got, want)
		}
	}
	b := budget("bdg_x", time.Time{}, cp.BudgetSpec{Period: "once"})
	if got := exceededError(b, BudgetUsage{Spent: usd("8.2"), Limit: usd("10")}).Detail; got != "budget bdg_x is exhausted (8.20 of 10.00 USD in total)" {
		t.Fatalf("detail %q", got)
	}
	b.Spec.Period = "month"
	if got := exceededError(b, BudgetUsage{Spent: usd("8.2"), Limit: usd("10")}).Detail; got != "budget bdg_x is exhausted for this month (8.20 of 10.00 USD)" {
		t.Fatalf("detail %q", got)
	}
}

// Threshold events fire once per window and threshold, and again only when
// the limit was raised since they last fired (D-044).
func TestRecordThresholdRearmsOnRaise(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	b := budget("bdg_x", now, cp.BudgetSpec{Period: "once"})
	count := func() int64 {
		var n int64
		if err := db.R.QueryRow(`SELECT count(*) FROM event_outbox WHERE dedupe_key LIKE 'budget:bdg_x:%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	start := now
	for _, step := range []struct {
		name  string
		limit string
		start time.Time
		fired bool
	}{
		{"first time", "10", start, true},
		{"same limit", "10", start, false},
		{"raised", "20", start, true},
		{"raised again is still once", "20", start, false},
		{"lowered", "15", start, false},
		{"back to the recorded limit", "20", start, false},
		{"new window", "15", start.Add(time.Hour), true},
	} {
		t.Run(step.name, func(t *testing.T) {
			before := count()
			u := BudgetUsage{PeriodStart: step.start, Limit: usd(step.limit), Spent: usd(step.limit)}
			if err := recordThreshold(ctx, db.W, b, u, "exceeded", now); err != nil {
				t.Fatal(err)
			}
			if fired := count() > before; fired != step.fired {
				t.Fatalf("fired %v, want %v", fired, step.fired)
			}
		})
	}
}

// Check and Exhausted ignore one-time budgets outside their window.
func TestBudgetCheckWindow(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	spend(t, db.W, now.Add(-time.Hour), "10")
	in := ReserveInput{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a", Billing: cp.BillingMetered, Currency: "USD"}
	for _, tt := range []struct {
		name    string
		spec    cp.BudgetSpec
		blocked bool
	}{
		{"open window", cp.BudgetSpec{Period: "once", StartsAt: "2026-05-01T00:00:00Z"}, true},
		{"scheduled", cp.BudgetSpec{Period: "once", StartsAt: "2026-06-02T00:00:00Z"}, false},
		{"expired", cp.BudgetSpec{Period: "once", StartsAt: "2026-05-01T00:00:00Z", EndsAt: "2026-06-01T00:00:00Z"}, false},
		{"workspace scope", cp.BudgetSpec{Period: "once", Scope: cp.BudgetWorkspace, ScopeID: "wsp_a"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bs := budgets(budget("bdg_x", now.AddDate(0, -2, 0), tt.spec))
			err := check(ctx, db, bs, in, now)
			var be *BudgetExceeded
			if errors.As(err, &be) != tt.blocked {
				t.Fatalf("Check: %v, want blocked %v", err, tt.blocked)
			}
			got, err := bs.Exhausted(ctx, db.R, Scope{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a"}, now)
			if err != nil || (got != nil) != tt.blocked {
				t.Fatalf("Exhausted: %v %v, want blocked %v", got, err, tt.blocked)
			}
		})
	}
}

// Status filters keep budgets covering a scope (by applies, whatever their
// state) or defined on exactly one object.
func TestBudgetStatusFilter(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	created := now.AddDate(0, -1, 0)
	bs := budgets(
		budget("global", created, cp.BudgetSpec{Period: "month", Scope: cp.BudgetGlobal}),
		budget("ws", created, cp.BudgetSpec{Period: "month", Scope: cp.BudgetWorkspace, ScopeID: "wsp_a"}),
		budget("ws-other", created, cp.BudgetSpec{Period: "month", Scope: cp.BudgetWorkspace, ScopeID: "wsp_b"}),
		budget("prj", created, cp.BudgetSpec{Period: "month"}),
		budget("prj-ended", created, cp.BudgetSpec{Period: "once", EndsAt: "2026-05-20T00:00:00Z"}),
		budget("prj-other", created, cp.BudgetSpec{Period: "month", Scope: cp.BudgetProject, ScopeID: "prj_b"}),
		budget("bind", created, cp.BudgetSpec{Period: "month", Scope: cp.BudgetBinding, ScopeID: "cbd_a"}),
		budget("acc", created, cp.BudgetSpec{Period: "month", Scope: cp.BudgetAccount, ScopeID: "acc_a"}),
	)
	cover := &Scope{ProjectID: "prj_a", WorkspaceID: "wsp_a"}
	withBinding := &Scope{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a"}
	for _, tt := range []struct {
		name string
		f    StatusFilter
		want []string
	}{
		{"none", StatusFilter{}, []string{"acc", "bind", "global", "prj", "prj-ended", "prj-other", "ws", "ws-other"}},
		{"project", StatusFilter{Cover: cover}, []string{"global", "prj", "prj-ended", "ws"}},
		{"project and binding", StatusFilter{Cover: withBinding}, []string{"bind", "global", "prj", "prj-ended", "ws"}},
		{"exact account", StatusFilter{Exact: true, Scope: cp.BudgetAccount, ScopeID: "acc_a"}, []string{"acc"}},
		{"exact global", StatusFilter{Exact: true, Scope: cp.BudgetGlobal}, []string{"global"}},
		{"exact workspace", StatusFilter{Exact: true, Scope: cp.BudgetWorkspace, ScopeID: "wsp_b"}, []string{"ws-other"}},
		{"exact, nothing", StatusFilter{Exact: true, Scope: cp.BudgetProject, ScopeID: "prj_z"}, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			items, err := bs.Status(ctx, db.R, now, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, u := range items {
				got = append(got, u.Name)
				if u.Name == "prj-ended" && u.State != "expired" {
					t.Fatalf("prj-ended state %s", u.State)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("budgets %v, want %v", got, tt.want)
			}
		})
	}
}

// BenchmarkCheckOneTimeBudget measures the request-path budget check for an
// open-ended one-time budget over 200k ledger rows spread across 12 months
// (the ledger scan risk of D-044), with one new ledger row settled before
// each reservation. ns/op is the warm path (spend cache hit plus the new
// rows); cold-ms is the first check, which reads the whole window.
func BenchmarkCheckOneTimeBudget(b *testing.B) {
	ctx := context.Background()
	db := openDB(b)
	now := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	start := now.AddDate(-1, 0, 0)
	const rows = 200_000 // about 550 settled requests a day
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `INSERT INTO ledger(id, at, kind, workspace_id, project_id, binding_id, account_id, usage_source,
			cost_e10, currency, cost_kind, idempotency_key) VALUES (?, ?, 'usage', ?, ?, ?, 'acc_a', 'estimated', 1000, 'USD', 'estimated_api_charge', ?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		step := now.Sub(start) / rows
		for i := range rows {
			// A quarter of the rows are in the measured project; the rest
			// spread over other projects of the same and other workspaces.
			ws, prj := fmt.Sprintf("wsp_%d", i%2), fmt.Sprintf("prj_%d", i%4)
			if _, err := st.ExecContext(ctx, fmt.Sprintf("led_%d", i), start.Add(step*time.Duration(i)).UnixMilli(), ws, prj, "cbd_"+prj, fmt.Sprintf("b%d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := db.W.ExecContext(ctx, `ANALYZE`); err != nil {
		b.Fatal(err)
	}
	seq := rows
	for _, sc := range []struct {
		name   string
		period string
		scopes []string // budget scopes, all covering prj_0
	}{
		{"once/project", "once", []string{cp.BudgetProject}},
		{"once/workspace", "once", []string{cp.BudgetWorkspace}},
		{"once/global", "once", []string{cp.BudgetGlobal}},
		{"once/global+workspace+project", "once", []string{cp.BudgetGlobal, cp.BudgetWorkspace, cp.BudgetProject}},
		// Baselines: the same data under monthly budgets.
		{"month/project", "month", []string{cp.BudgetProject}},
		{"month/workspace", "month", []string{cp.BudgetWorkspace}},
		{"month/global+workspace+project", "month", []string{cp.BudgetGlobal, cp.BudgetWorkspace, cp.BudgetProject}},
	} {
		b.Run(sc.name, func(b *testing.B) {
			var list []*cp.Res[cp.BudgetSpec]
			for _, scope := range sc.scopes {
				id := map[string]string{cp.BudgetGlobal: "", cp.BudgetWorkspace: "wsp_0", cp.BudgetProject: "prj_0"}[scope]
				list = append(list, budget("bdg_"+scope, start, cp.BudgetSpec{Period: sc.period, Scope: scope, ScopeID: id, Limit: "1000000"}))
			}
			bs := budgets(list...)
			in := ReserveInput{ProjectID: "prj_0", WorkspaceID: "wsp_0", BindingID: "cbd_prj_0", Billing: cp.BillingMetered, Currency: "USD", EstimatedCost: 1}
			// The first check after start-up, a spec edit or a reset reads
			// the whole window (a spend cache miss); reported as cold-ms.
			t0 := time.Now()
			if err := check(ctx, db, bs, in, now); err != nil {
				b.Fatal(err)
			}
			cold := time.Since(t0)
			for b.Loop() {
				// Traffic keeps settling: one new row per reservation.
				seq++
				if _, err := db.W.ExecContext(ctx, `INSERT INTO ledger(id, at, kind, workspace_id, project_id, binding_id, account_id, usage_source,
					cost_e10, currency, cost_kind, idempotency_key) VALUES (?, ?, 'usage', 'wsp_0', 'prj_0', 'cbd_prj_0', 'acc_a', 'estimated', 1000, 'USD', 'estimated_api_charge', ?)`,
					fmt.Sprintf("led_%d", seq), now.Add(-time.Minute).UnixMilli(), fmt.Sprintf("b%d", seq)); err != nil {
					b.Fatal(err)
				}
				if err := check(ctx, db, bs, in, now); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(cold.Microseconds())/1000, "cold-ms")
		})
	}
}
