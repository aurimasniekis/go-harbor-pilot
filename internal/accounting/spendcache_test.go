package accounting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
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

func cacheEntry(bs *Budgets, id string) spendEntry {
	bs.sums.mu.Lock()
	defer bs.sums.mu.Unlock()
	return bs.sums.m[id]
}

func TestWalkGap(t *testing.T) {
	for _, tt := range []struct {
		name       string
		mark, rows int64
		gap        int64
		walk       bool
	}{
		{"small gap, no rows", 100, 0, 10, true},
		{"at the floor", 100, 0, gapFloor, true},
		{"past the floor, no rows", 100, 0, gapFloor + 1, false},
		{"past the floor, many rows", 100, 10_000, 50_000, true},
		{"at the ratio", 100, 10_000, gapPerRow * 10_000, true},
		{"past the ratio", 100, 10_000, gapPerRow*10_000 + 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := walkGap(spendEntry{mark: tt.mark, rows: tt.rows}, tt.mark+tt.gap); got != tt.walk {
				t.Fatalf("walkGap = %v, want %v", got, tt.walk)
			}
		})
	}
}

// A quiet project's budget stays exact when other projects settle more
// rows than walkGap allows, and its row count matches a full read.
func TestSpendCacheLargeForeignGap(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	created := now.AddDate(0, -1, 0)
	once := budget("bdg_once", created, cp.BudgetSpec{Period: "once", Limit: "100"})
	month := budget("bdg_month", created, cp.BudgetSpec{Period: "month", Limit: "100"})
	bs := budgets(once, month)
	for i := range 20 {
		row(t, db.W, now.Add(-time.Duration(i)*time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 1e9)
	}
	assertMatchesScan(t, db, bs, now)
	for _, gap := range []int{gapFloor / 2, gapFloor * 3} {
		before := cacheEntry(bs, once.ID)
		fillLedger(t, db, gap, now.Add(-time.Hour), now, func(int) (string, string) { return "wsp_b", "prj_b" })
		row(t, db.W, now.Add(-time.Minute), "prj_a", cp.CostEstimatedAPI, "USD", 2e9)
		if walk := walkGap(before, before.mark+int64(gap)+1); walk != (gap <= gapFloor) {
			t.Fatalf("gap %d: walkGap %v", gap, walk)
		}
		assertMatchesScan(t, db, bs, now)
		got := cacheEntry(bs, once.ID)
		fresh := budgets(once)
		if _, err := fresh.Status(ctx, db.R, now, StatusFilter{}); err != nil {
			t.Fatal(err)
		}
		if want := cacheEntry(fresh, once.ID); got.rows != want.rows || got.mark != want.mark || got.rows != before.rows+1 {
			t.Fatalf("gap %d: entry rows %d mark %d, full read rows %d mark %d (before %d rows)", gap, got.rows, got.mark, want.rows, want.mark, before.rows)
		}
	}
}

// A Check that read on the snapshot before a reset and commits after it
// must not replace the reset window's entry with the old one.
func TestSpendCacheKeepsCurrentKey(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	b := budget("bdg_once", now.AddDate(0, -1, 0), cp.BudgetSpec{Period: "once", Limit: "100"})
	var cur atomic.Pointer[cp.Snapshot]
	cur.Store(&cp.Snapshot{Budgets: map[string]*cp.Res[cp.BudgetSpec]{b.ID: b}})
	bs := &Budgets{Snapshot: cur.Load}
	row(t, db.W, now.Add(-time.Hour), "prj_a", cp.CostEstimatedAPI, "USD", 1e9)
	in := ReserveInput{ProjectID: "prj_a", WorkspaceID: "wsp_a", BindingID: "cbd_a", Billing: cp.BillingMetered, Currency: "USD"}
	var committed func()
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		committed, err = bs.Check(ctx, tx, in, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Start over lands, and the primer stores the new window first.
	reset := *b
	reset.Spec.StartsAt = now.Add(-30 * time.Minute).Format(time.RFC3339)
	cur.Store(&cp.Snapshot{Budgets: map[string]*cp.Res[cp.BudgetSpec]{b.ID: &reset}})
	if _, err := bs.Status(ctx, db.R, now, StatusFilter{}); err != nil {
		t.Fatal(err)
	}
	want := cacheEntry(bs, b.ID).key
	committed()
	if got := cacheEntry(bs, b.ID).key; got != want {
		t.Fatalf("a late commit replaced the current key %+v with %+v", want, got)
	}
	assertMatchesScan(t, db, bs, now)
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

// Without a Prime, the periodic pass still moves a quiet budget's watermark
// up to the ledger head.
func TestBudgetPrimerTicks(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	b := budget("bdg_month", now.AddDate(0, -1, 0), cp.BudgetSpec{Period: "month", Limit: "100"})
	bs := budgets(b)
	bs.PrimeEvery = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		bs.RunPrimer(db.R, func() time.Time { return now }, slog.New(slog.DiscardHandler))(ctx)
	}()
	for i := range 3 {
		row(t, db.W, now.Add(-time.Hour), "prj_b", cp.CostEstimatedAPI, "USD", 1e9) // another project
		want := int64(i + 1)
		deadline := time.Now().Add(5 * time.Second)
		for cacheMark(bs, b.ID) != want {
			if time.Now().After(deadline) {
				t.Fatalf("watermark %d, want %d", cacheMark(bs, b.ID), want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
}

// fillLedger inserts n USD rows at evenly spaced times in [from, to), in a
// workspace and project chosen by pick(i).
func fillLedger(tb testing.TB, db *store.DB, n int, from, to time.Time, pick func(i int) (ws, prj string)) {
	tb.Helper()
	ctx := context.Background()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `INSERT INTO ledger(id, at, kind, workspace_id, project_id, binding_id, account_id, usage_source,
			cost_e10, currency, cost_kind, idempotency_key) VALUES (?, ?, 'usage', ?, ?, ?, 'acc_a', 'estimated', 1000, 'USD', 'estimated_api_charge', ?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		step := to.Sub(from) / time.Duration(n)
		for i := range n {
			ledgerSeq++
			ws, prj := pick(i)
			if _, err := st.ExecContext(ctx, fmt.Sprintf("led_g%d", ledgerSeq), from.Add(step*time.Duration(i)).UnixMilli(), ws, prj, "cbd_"+prj,
				fmt.Sprintf("g%d", ledgerSeq)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkCheckQuietScopeGap measures the request-path check of a primed
// budget on a quiet project after other projects settled gap ledger rows
// since its watermark (round 3 of D-044's spend cache). Each iteration
// restores the primed entry, so every check reads the same gap.
func BenchmarkCheckQuietScopeGap(b *testing.B) {
	ctx := context.Background()
	db := openDB(b)
	now := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	start := now.AddDate(-1, 0, 0)
	// 200k rows over a year; 1 in 50 is the quiet project's (about 330 in
	// the last month).
	fillLedger(b, db, 200_000, start, now, func(i int) (string, string) {
		if i%50 == 0 {
			return "wsp_0", "prj_quiet"
		}
		return fmt.Sprintf("wsp_%d", i%2), fmt.Sprintf("prj_%d", i%4)
	})
	if _, err := db.W.ExecContext(ctx, `ANALYZE`); err != nil {
		b.Fatal(err)
	}
	month := budget("bdg_month", start, cp.BudgetSpec{Period: "month", Scope: cp.BudgetProject, ScopeID: "prj_quiet", Limit: "1000000"})
	once := budget("bdg_once", start, cp.BudgetSpec{Period: "once", Scope: cp.BudgetProject, ScopeID: "prj_quiet", Limit: "1000000"})
	in := ReserveInput{ProjectID: "prj_quiet", WorkspaceID: "wsp_0", BindingID: "cbd_prj_quiet", Billing: cp.BillingMetered, Currency: "USD", EstimatedCost: 1}
	type primed struct {
		bs    *Budgets
		entry spendEntry
	}
	prime := map[string]primed{}
	for _, bud := range []*cp.Res[cp.BudgetSpec]{month, once} {
		bs := budgets(bud)
		if _, err := bs.Status(ctx, db.R, now, StatusFilter{}); err != nil {
			b.Fatal(err)
		}
		bs.sums.mu.Lock()
		prime[bud.Spec.Period] = primed{bs, bs.sums.m[bud.ID]}
		bs.sums.mu.Unlock()
	}
	added := 0
	for _, gap := range []int{1_000, 50_000, 200_000} {
		// Foreign traffic since the watermark, in the last hour.
		fillLedger(b, db, gap-added, now.Add(-time.Hour), now, func(i int) (string, string) {
			return fmt.Sprintf("wsp_%d", i%2), fmt.Sprintf("prj_%d", i%4)
		})
		added = gap
		for _, period := range []string{"month", "once"} {
			p := prime[period]
			b.Run(fmt.Sprintf("%s/gap=%d", period, gap), func(b *testing.B) {
				for b.Loop() {
					p.bs.sums.mu.Lock()
					p.bs.sums.m[p.entry.key.budgetID] = p.entry
					p.bs.sums.mu.Unlock()
					if err := check(ctx, db, p.bs, in, now); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
	for _, period := range []string{"month", "once"} {
		p := prime[period]
		b.Run(period+"/full-read", func(b *testing.B) {
			for b.Loop() {
				p.bs.sums.mu.Lock()
				delete(p.bs.sums.m, p.entry.key.budgetID)
				p.bs.sums.mu.Unlock()
				if err := check(ctx, db, p.bs, in, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
