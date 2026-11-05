package accounting

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// BudgetExceeded is returned by the budget check when a reservation would
// exceed a blocking budget. It is a CapacityError so the gateway treats it
// like other local limits.
type BudgetExceeded struct {
	CapacityError
	BudgetID    string
	BudgetName  string
	PeriodStart time.Time
	Spent       money.Amount
	Limit       money.Amount
	Currency    string
}

func (e *BudgetExceeded) Unwrap() error { return &e.CapacityError }

// Budgets enforces budget resources from the configuration snapshot.
type Budgets struct {
	Snapshot func() *cp.Snapshot
}

// BudgetUsage is the state of one budget in its current period.
type BudgetUsage struct {
	BudgetID    string       `json:"budget_id"`
	Name        string       `json:"name"`
	Scope       string       `json:"scope"`
	ScopeID     string       `json:"scope_id"`
	Period      string       `json:"period"`
	PeriodStart time.Time    `json:"period_start"`
	PeriodEnd   time.Time    `json:"period_end"`
	Currency    string       `json:"currency"`
	CostKinds   []string     `json:"cost_kinds"`
	Enforcement string       `json:"enforcement"`
	Enabled     bool         `json:"enabled"`
	Limit       money.Amount `json:"limit_e10"`
	// Spent is settled ledger cost in the counted categories.
	Spent money.Amount `json:"spent_e10"`
	// Reserved is the estimated cost of in-flight attempts.
	Reserved money.Amount `json:"reserved_e10"`
	// ByKind breaks Spent down by cost category.
	ByKind map[string]money.Amount `json:"by_kind_e10"`
	// Fraction is (spent+reserved)/limit.
	Fraction float64 `json:"fraction"`
	WarnAt   float64 `json:"warn_at"`
	// ProjectedSpend linearly extrapolates the period's spend rate to its
	// end; nil early in the period when the rate is not meaningful.
	ProjectedSpend *money.Amount `json:"projected_spend_e10"`
	State          string        `json:"state" enum:"ok,warning,exceeded,disabled"`
}

func scopeColumn(scope string) string {
	switch scope {
	case cp.BudgetWorkspace:
		return "workspace_id"
	case cp.BudgetProject:
		return "project_id"
	case cp.BudgetBinding:
		return "binding_id"
	case cp.BudgetAccount:
		return "account_id"
	}
	return ""
}

func applies(b *cp.Res[cp.BudgetSpec], in ReserveInput) bool {
	switch b.Spec.Scope {
	case cp.BudgetGlobal:
		return true
	case cp.BudgetWorkspace:
		return in.WorkspaceID == b.Spec.ScopeID
	case cp.BudgetProject:
		return in.ProjectID == b.Spec.ScopeID
	case cp.BudgetBinding:
		return in.BindingID == b.Spec.ScopeID
	case cp.BudgetAccount:
		return in.AccountID == b.Spec.ScopeID
	}
	return false
}

// usage computes spend for one budget. q may be a transaction.
func usage(ctx context.Context, q store.Execer, b *cp.Res[cp.BudgetSpec], now time.Time) (BudgetUsage, error) {
	start := b.Spec.PeriodStart(now)
	end := b.Spec.PeriodEnd(start)
	limit, _ := money.ParseDecimal(b.Spec.Limit)
	u := BudgetUsage{BudgetID: b.ID, Name: b.Name, Scope: b.Spec.Scope, ScopeID: b.Spec.ScopeID, Period: b.Spec.Period,
		PeriodStart: start.UTC(), PeriodEnd: end.UTC(), Currency: b.Spec.Currency, CostKinds: b.Spec.CostKinds, Enforcement: b.Spec.Enforcement,
		Enabled: b.Spec.Enabled, Limit: limit, ByKind: map[string]money.Amount{}, WarnAt: b.Spec.WarnAt}
	where := "at >= ? AND at < ? AND currency = ?"
	args := []any{start.UnixMilli(), end.UnixMilli(), b.Spec.Currency}
	rwhere := "status = 'active' AND currency = ?"
	rargs := []any{b.Spec.Currency}
	if col := scopeColumn(b.Spec.Scope); col != "" {
		where += " AND " + col + " = ?"
		args = append(args, b.Spec.ScopeID)
		rwhere += " AND " + col + " = ?"
		rargs = append(rargs, b.Spec.ScopeID)
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (struct {
		kind string
		sum  int64
	}, error) {
		var x struct {
			kind string
			sum  int64
		}
		err := r.Scan(&x.kind, &x.sum)
		return x, err
	}, `SELECT cost_kind, COALESCE(SUM(cost_e10), 0) FROM ledger WHERE `+where+` AND cost_e10 IS NOT NULL GROUP BY cost_kind`, args...)
	if err != nil {
		return u, err
	}
	counted := map[string]bool{}
	for _, k := range b.Spec.CostKinds {
		counted[k] = true
	}
	for _, r := range rows {
		u.ByKind[r.kind] = money.Amount(r.sum)
		if counted[r.kind] {
			u.Spent += money.Amount(r.sum)
		}
	}
	var reserved int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(est_cost_e10), 0) FROM reservations WHERE `+rwhere, rargs...).Scan(&reserved); err != nil {
		return u, err
	}
	u.Reserved = money.Amount(reserved)
	if limit > 0 {
		u.Fraction = float64(u.Spent+u.Reserved) / float64(limit)
	}
	elapsed := now.Sub(start)
	if total := end.Sub(start); elapsed > total/20 && elapsed > time.Hour {
		p := money.Amount(float64(u.Spent) * float64(total) / float64(elapsed))
		u.ProjectedSpend = &p
	}
	switch {
	case !b.Spec.Enabled:
		u.State = "disabled"
	case u.Fraction >= 1:
		u.State = "exceeded"
	case u.Fraction >= b.Spec.WarnAt:
		u.State = "warning"
	default:
		u.State = "ok"
	}
	return u, nil
}

// Check is installed as Service.BudgetCheck. It runs inside the reservation
// transaction, so concurrent reservations are serialized by the writer.
func (bs *Budgets) Check(ctx context.Context, tx *sql.Tx, in ReserveInput, now time.Time) error {
	snap := bs.Snapshot()
	if snap == nil || len(snap.Budgets) == 0 || in.Billing == cp.BillingLocal || in.Billing == cp.BillingFree {
		return nil
	}
	ids := make([]string, 0, len(snap.Budgets))
	for id := range snap.Budgets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b := snap.Budgets[id]
		if !b.Spec.Enabled || !applies(b, in) {
			continue
		}
		u, err := usage(ctx, tx, b, now)
		if err != nil {
			return err
		}
		next := u.Spent + u.Reserved
		if in.Currency == b.Spec.Currency {
			next += in.EstimatedCost
		}
		over := next > u.Limit || (in.EstimatedCost == 0 && u.Spent+u.Reserved >= u.Limit)
		if over && b.Spec.Enforcement == "block" {
			return &BudgetExceeded{CapacityError: CapacityError{Limit: "budget:" + b.ID,
				Detail: fmt.Sprintf("budget %s is exhausted for this %s (%s of %s %s)", b.Name, b.Spec.Period,
					(u.Spent + u.Reserved).String(), (u.Limit).String(), b.Spec.Currency)},
				BudgetID: b.ID, BudgetName: b.Name, PeriodStart: u.PeriodStart, Spent: u.Spent + u.Reserved, Limit: u.Limit, Currency: b.Spec.Currency}
		}
		frac := 0.0
		if u.Limit > 0 {
			frac = float64(next) / float64(u.Limit)
		}
		if frac >= b.Spec.WarnAt {
			kind := "warning"
			if over {
				kind = "exceeded"
			}
			if err := recordThreshold(ctx, tx, b, u, kind, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// recordThreshold writes one event per budget, period and threshold.
func recordThreshold(ctx context.Context, ex store.Execer, b *cp.Res[cp.BudgetSpec], u BudgetUsage, kind string, now time.Time) error {
	col := "warned_at"
	action := "warning"
	sev := events.SeverityWarning
	if kind == "exceeded" {
		col, action, sev = "exceeded_at", "exceeded", events.SeverityCritical
	}
	res, err := ex.ExecContext(ctx, `INSERT INTO budget_state(budget_id, period_start, `+col+`) VALUES (?, ?, ?)
		ON CONFLICT(budget_id, period_start) DO UPDATE SET `+col+` = excluded.`+col+` WHERE budget_state.`+col+` IS NULL`,
		b.ID, u.PeriodStart.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	_, err = events.Append(ctx, ex, events.Event{Kind: events.Kind{Area: "budget", Entity: "threshold", Action: action}, Severity: sev,
		OccurredAt: now, DedupeKey: "budget:" + b.ID + ":" + action,
		Subjects: budgetSubjects(b),
		Data: map[string]any{"budget": b.Name, "scope": b.Spec.Scope, "period": b.Spec.Period, "period_start": u.PeriodStart,
			"spent": (u.Spent + u.Reserved).String(), "limit": (u.Limit).String(), "currency": b.Spec.Currency,
			"fraction": u.Fraction, "enforcement": b.Spec.Enforcement}})
	return err
}

func budgetSubjects(b *cp.Res[cp.BudgetSpec]) map[string]string {
	out := map[string]string{"budget_id": b.ID}
	if col := scopeColumn(b.Spec.Scope); col != "" {
		out[col] = b.Spec.ScopeID
	}
	return out
}

// Exceeded is installed as Service.OnBudgetExceeded.
func (bs *Budgets) Exceeded(db *sql.DB) func(ctx context.Context, be *BudgetExceeded, now time.Time) {
	return func(ctx context.Context, be *BudgetExceeded, now time.Time) {
		snap := bs.Snapshot()
		b := snap.Budgets[be.BudgetID]
		if b == nil {
			return
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		u := BudgetUsage{PeriodStart: be.PeriodStart, Spent: be.Spent, Limit: be.Limit}
		if be.Limit > 0 {
			u.Fraction = float64(be.Spent) / float64(be.Limit)
		}
		if err := recordThreshold(ctx, tx, b, u, "exceeded", now); err != nil {
			_ = tx.Rollback()
			return
		}
		_ = tx.Commit()
	}
}

// Status reports every budget's current period.
func (bs *Budgets) Status(ctx context.Context, q store.Execer, now time.Time) ([]BudgetUsage, error) {
	snap := bs.Snapshot()
	out := []BudgetUsage{}
	if snap == nil {
		return out, nil
	}
	for _, b := range snap.Budgets {
		u, err := usage(ctx, q, b, now)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
