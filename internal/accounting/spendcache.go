package accounting

import (
	"context"
	"database/sql"
	"sync"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// The spend cache keeps each budget's settled ledger spend per cost kind, so
// the request-path check does not re-sum a long window on every reservation
// (D-044). The ledger is insert-only and its rows keep their rowid, so a
// cached sum stays exact once it covers every row up to a watermark: a
// refresh reads only the rows past it.
//
// Rowids of committed rows only ever grow: rows are inserted by the single
// writer and nothing updates or deletes them. A rolled-back transaction can
// leave rowids that a later insert reuses, so an entry is stored only from
// rows known to be committed: a read outside a transaction, or a read inside
// one once it has committed (Check's commit func).
//
// Anything that changes what a budget counts (its window, which moves on a
// reset, an edited start or end or a new calendar period; its scope or
// currency) changes the key, and a lookup with a different key is a miss.
// Cost kinds are not part of the key: the entry holds every kind's sum.
type spendCache struct {
	mu sync.Mutex
	m  map[string]spendEntry // by budget id; one window per budget
}

// spendKey is what a cached sum depends on.
type spendKey struct {
	budgetID   string
	start, end int64 // unix ms; end is 0 for an open window
	currency   string
	scope      string
	scopeID    string
}

// spendEntry is the settled spend of a key over ledger rows with rowid <= mark.
type spendEntry struct {
	key    spendKey
	mark   int64
	rows   int64 // ledger rows counted in byKind: what an indexed read visits
	byKind map[string]int64
}

// A refresh walks the rowid range past the watermark, which visits every
// row settled since then in any scope. A budget on a quiet project or
// binding, while others keep settling, would pay for the whole gap inside
// the writer transaction, so past a point it re-reads its window through
// the scope (or at) index instead, which visits only its own rows.
// BenchmarkCheckQuietScopeGap (and the same queries in the sqlite3 shell)
// measured the walk at 0.3 to 0.5 µs per gap row and the indexed read at
// 2.5 to 5.5 µs per counted row (its table fetches are scattered), about
// ten times as much. Hence:
const (
	// gapFloor: below this many rows the walk costs about a millisecond at
	// most, so it is always taken.
	gapFloor = 2048
	// gapPerRow: above the floor, the walk is taken while the gap is at most
	// this many times the rows the entry counts.
	gapPerRow = 10
)

// walkGap reports whether reading rows (base.mark, mark] by rowid is
// cheaper than re-reading base's window through an index.
func walkGap(base spendEntry, mark int64) bool {
	gap := mark - base.mark
	return gap <= gapFloor || gap <= gapPerRow*base.rows
}

type stagedSum struct {
	key   spendKey
	entry spendEntry
}

func spendKeyOf(b *cp.Res[cp.BudgetSpec], start, end time.Time, open bool) spendKey {
	k := spendKey{budgetID: b.ID, start: start.UnixMilli(), currency: b.Spec.Currency, scope: b.Spec.Scope, scopeID: b.Spec.ScopeID}
	if !open {
		k.end = end.UnixMilli()
	}
	return k
}

// lookup returns the cached entry for k, or an empty one (mark 0) on a miss.
func (c *spendCache) lookup(k spendKey) spendEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[k.budgetID]; ok && e.key == k {
		return e
	}
	return spendEntry{key: k}
}

// store keeps e unless the cache already holds the same key at a later
// watermark (a read on an older snapshot finishing late). Callers go
// through Budgets.keep, which drops entries of a key that is no longer
// current.
func (c *spendCache) store(k spendKey, e spendEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]spendEntry{}
	}
	if cur, ok := c.m[k.budgetID]; ok && cur.key == k && cur.mark >= e.mark {
		return
	}
	c.m[k.budgetID] = e
}

// retain drops entries of budgets that no longer exist.
func (c *spendCache) retain(budgets map[string]*cp.Res[cp.BudgetSpec]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.m {
		if _, ok := budgets[id]; !ok {
			delete(c.m, id)
		}
	}
}

// refresh returns k's spend over every ledger row q can see, reading only
// the rows past the cached watermark, or the whole window when the gap is
// large (walkGap). It does not store the result.
//
// The watermark is read first and the sum is bounded by it: a later
// statement sees at least the rows the first one saw, and no row at or
// below a committed maximum can appear afterwards.
func (c *spendCache) refresh(ctx context.Context, q store.Execer, k spendKey) (spendEntry, error) {
	base := c.lookup(k)
	var mark int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid), 0) FROM ledger`).Scan(&mark); err != nil {
		return base, err
	}
	if mark <= base.mark {
		// Nothing new, or q reads an older snapshot than the cache: the
		// cache is the newer figure.
		return base, nil
	}
	where := "at >= ? AND currency = ?"
	args := []any{k.start, k.currency}
	if k.end != 0 {
		where = "at >= ? AND at < ? AND currency = ?"
		args = []any{k.start, k.end, k.currency}
	}
	if col := scopeColumn(k.scope); col != "" {
		where += " AND " + col + " = ?"
		args = append(args, k.scopeID)
	}
	if base.mark > 0 && !walkGap(base, mark) {
		base = spendEntry{key: k}
	}
	from := "ledger"
	if base.mark > 0 {
		// Only the rows past the watermark: walk the rowid range rather
		// than an index over the whole window, which the planner takes
		// for this query without ANALYZE statistics.
		from = "ledger NOT INDEXED"
		where += " AND rowid > ? AND rowid <= ?"
		args = append(args, base.mark, mark)
	} else {
		// A full scan: the unary + keeps the bound from steering the plan
		// away from the at and scope indexes.
		where += " AND +rowid <= ?"
		args = append(args, mark)
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (struct {
		kind      string
		sum, rows int64
	}, error) {
		var x struct {
			kind      string
			sum, rows int64
		}
		err := r.Scan(&x.kind, &x.sum, &x.rows)
		return x, err
	}, `SELECT cost_kind, COALESCE(SUM(cost_e10), 0), COUNT(*) FROM `+from+` WHERE `+where+` AND cost_e10 IS NOT NULL GROUP BY cost_kind`, args...) //nolint:gosec // where is built from static column names
	if err != nil {
		return base, err
	}
	next := spendEntry{key: k, mark: mark, rows: base.rows, byKind: make(map[string]int64, len(base.byKind)+len(rows))}
	for kind, sum := range base.byKind {
		next.byKind[kind] = sum
	}
	for _, r := range rows {
		next.byKind[r.kind] += r.sum
		next.rows += r.rows
	}
	return next, nil
}
