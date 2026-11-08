package accounting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// row writes one ledger row; scope and kind vary so the cache must filter
// the rows past its watermark like a full scan does.
func row(t *testing.T, ex store.Execer, at time.Time, project, kind, currency string, cost int64) {
	t.Helper()
	ledgerSeq++
	if _, err := ex.ExecContext(context.Background(), `INSERT INTO ledger(id, at, kind, workspace_id, project_id, binding_id, account_id, usage_source,
		cost_e10, currency, cost_kind, idempotency_key) VALUES (?, ?, 'usage', 'wsp_a', ?, 'cbd_a', 'acc_a', 'estimated', ?, ?, ?, ?)`,
		fmt.Sprintf("led_%d", ledgerSeq), at.UnixMilli(), project, cost, currency, kind, fmt.Sprintf("k%d", ledgerSeq)); err != nil {
		t.Fatal(err)
	}
}

// assertMatchesScan compares bs's cached figures (Status, Exhausted and a
// Check) with a fresh Budgets, which always reads the whole window.
func assertMatchesScan(t *testing.T, db *store.DB, bs *Budgets, now time.Time) {
	t.Helper()
	ctx := context.Background()
	fresh := &Budgets{Snapshot: bs.Snapshot}
	want, err := fresh.Status(ctx, db.R, now, StatusFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// Check first, so its staged entries are what Status then reads.
	in := ReserveInput{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a", Billing: cp.BillingMetered, Currency: "USD"}
	cerr := check(ctx, db, bs, in, now)
	var be *BudgetExceeded
	if cerr != nil && !errors.As(cerr, &be) {
		t.Fatal(cerr)
	}
	got, err := bs.Status(ctx, db.R, now, StatusFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("cached status\n%v\nfull scan\n%v", got, want)
	}
	ex, err := bs.Exhausted(ctx, db.R, Scope{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	exWant, _ := fresh.Exhausted(ctx, db.R, Scope{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a"}, now)
	if fmt.Sprint(ex) != fmt.Sprint(exWant) {
		t.Fatalf("cached Exhausted %v, full scan %v", ex, exWant)
	}
	if (be != nil) != (exWant != nil) {
		t.Fatalf("Check blocked %v, Exhausted %v", be, exWant)
	}
}

func cacheMark(bs *Budgets, id string) int64 {
	bs.sums.mu.Lock()
	defer bs.sums.mu.Unlock()
	return bs.sums.m[id].mark
}

// The cache gives the full scan's figures as rows arrive, across a reset
// and a spec edit, and never keeps sums read in a rolled-back transaction.
func TestSpendCacheMatchesFullScan(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	created := now.AddDate(0, -2, 0)
	once := budget("bdg_once", created, cp.BudgetSpec{Period: "once", Limit: "100"})
	month := budget("bdg_month", created, cp.BudgetSpec{Period: "month", Limit: "100"})
	global := budget("bdg_global", created, cp.BudgetSpec{Period: "once", Scope: cp.BudgetGlobal, Limit: "100"})
	snap := &cp.Snapshot{Budgets: map[string]*cp.Res[cp.BudgetSpec]{once.ID: once, month.ID: month, global.ID: global}}
	bs := &Budgets{Snapshot: func() *cp.Snapshot { return snap }}

	t.Run("inserts", func(t *testing.T) {
		assertMatchesScan(t, db, bs, now) // empty ledger
		for i := range 12 {
			at := now.Add(-time.Duration(i*5) * 24 * time.Hour)
			row(t, db.W, at, "prj_a", cp.CostEstimatedAPI, "USD", int64(i+1)*1e9)
			row(t, db.W, at, "prj_b", cp.CostEstimatedAPI, "USD", 7e9)
			row(t, db.W, at, "prj_a", cp.CostListEquivalent, "USD", 3e9) // not counted
			row(t, db.W, at, "prj_a", cp.CostEstimatedAPI, "EUR", 5e9)
			row(t, db.W, created.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 9e9) // before the window, inserted late
			assertMatchesScan(t, db, bs, now)
		}
		if cacheMark(bs, once.ID) == 0 {
			t.Fatal("the cache was never stored")
		}
	})

	t.Run("reset", func(t *testing.T) {
		reset := *once
		reset.Spec.StartsAt = now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
		snap.Budgets[once.ID] = &reset
		assertMatchesScan(t, db, bs, now)
		row(t, db.W, now.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 2e9)
		assertMatchesScan(t, db, bs, now)
	})

	t.Run("spec edit", func(t *testing.T) {
		edited := *snap.Budgets[once.ID]
		edited.Spec.Scope, edited.Spec.ScopeID = cp.BudgetWorkspace, "wsp_a"
		snap.Budgets[once.ID] = &edited
		assertMatchesScan(t, db, bs, now)
	})

	t.Run("new period", func(t *testing.T) {
		assertMatchesScan(t, db, bs, now.AddDate(0, 1, 0))
		row(t, db.W, now.AddDate(0, 1, 0).Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 4e9)
		assertMatchesScan(t, db, bs, now.AddDate(0, 1, 0))
	})

	t.Run("rollback", func(t *testing.T) {
		in := ReserveInput{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a", Billing: cp.BillingMetered, Currency: "USD"}
		before := cacheMark(bs, once.ID)
		// Rows written and read inside a transaction that rolls back: their
		// rowids are reused by the next insert, so their sums must not stay.
		var committed func()
		err := db.Tx(ctx, func(tx *sql.Tx) error {
			for range 3 {
				row(t, tx, now.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 50e9)
			}
			var err error
			committed, err = bs.Check(ctx, tx, in, now)
			if err != nil {
				return err
			}
			if _, err := bs.Status(ctx, tx, now, StatusFilter{}); err != nil {
				return err
			}
			return errors.New("roll back")
		})
		if err == nil || err.Error() != "roll back" {
			t.Fatalf("tx: %v", err)
		}
		if committed == nil {
			t.Fatal("Check returned no commit func")
		}
		if got := cacheMark(bs, once.ID); got != before {
			t.Fatalf("watermark moved to %d inside a rolled-back tx (was %d)", got, before)
		}
		row(t, db.W, now.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 1e9)
		assertMatchesScan(t, db, bs, now)
	})

	t.Run("older snapshot", func(t *testing.T) {
		// A late store at an older watermark keeps the newer entry.
		mark := cacheMark(bs, once.ID)
		bs.sums.mu.Lock()
		cur := bs.sums.m[once.ID]
		bs.sums.mu.Unlock()
		bs.sums.store(cur.key, spendEntry{key: cur.key, mark: mark - 1})
		if got := cacheMark(bs, once.ID); got != mark {
			t.Fatalf("watermark %d, want %d", got, mark)
		}
	})
}

// The primer fills the cache off the request path at start and on Prime.
func TestBudgetPrimer(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	b := budget("bdg_once", now.AddDate(0, -1, 0), cp.BudgetSpec{Period: "once", Limit: "100"})
	snap := &cp.Snapshot{Budgets: map[string]*cp.Res[cp.BudgetSpec]{b.ID: b}}
	bs := &Budgets{Snapshot: func() *cp.Snapshot { return snap }}
	row(t, db.W, now.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 1e9)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		bs.RunPrimer(db.R, func() time.Time { return now }, slog.New(slog.DiscardHandler))(ctx)
	}()
	waitMark := func(want int64) {
		t.Helper()
		for range 500 {
			if cacheMark(bs, b.ID) == want {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("watermark %d, want %d", cacheMark(bs, b.ID), want)
	}
	waitMark(1)
	row(t, db.W, now.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 1e9)
	bs.Prime()
	waitMark(2)
	cancel()
	<-done
}
