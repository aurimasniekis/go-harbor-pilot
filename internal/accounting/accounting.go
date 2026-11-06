// Package accounting reserves capacity before dispatch and records usage in
// an append-only, idempotent ledger afterwards.
//
// Reservations are taken inside one immediate SQLite write transaction. The
// single writer connection serializes those transactions, so concurrent
// requests cannot over-allocate local limits. Reservations are leases: a
// reaper expires abandoned ones, and on startup every reservation left
// active by a previous process is expired and its usage recorded as unknown.
// Local limits are estimates enforced by Harbor Pilot; they are not provider
// caps.
package accounting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// CapacityError reports which local limit prevented a reservation.
type CapacityError struct {
	Limit   string // account_concurrency | binding_concurrency | budget:<id>
	Current int
	Max     int
	Detail  string
}

func (e *CapacityError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("%s limit reached (%d/%d)", e.Limit, e.Current, e.Max)
}

// Service implements reservations and the ledger.
type Service struct {
	db    *store.DB
	clock clock.Clock
	log   *slog.Logger
	// BudgetCheck, when set, is evaluated inside the reservation transaction
	// (installed by the budgets module).
	BudgetCheck func(ctx context.Context, tx *sql.Tx, in ReserveInput, now time.Time) error
	// OnBudgetExceeded records a blocked reservation after its transaction
	// rolled back (one event per budget period).
	OnBudgetExceeded func(ctx context.Context, be *BudgetExceeded, now time.Time)
}

// New creates the service.
func New(db *store.DB, c clock.Clock, log *slog.Logger) *Service {
	if c == nil {
		c = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, clock: c, log: log}
}

// ReserveInput describes a reservation.
type ReserveInput struct {
	RequestID      string
	AttemptID      string
	AccountID      string
	BindingID      string
	ProjectID      string
	WorkspaceID    string
	AccountMax     int // 0 = unlimited
	BindingMax     int // 0 = unlimited
	Lease          time.Duration
	EstimatedCost  money.Amount
	Currency       string
	Billing        string
	ConsumesBudget bool
}

// Reservation is an active lease.
type Reservation struct {
	ID         string
	LeaseUntil time.Time
}

// Reserve atomically checks local limits and takes a lease.
func (s *Service) Reserve(ctx context.Context, in ReserveInput) (*Reservation, error) {
	now := s.clock.Now()
	if in.Lease <= 0 {
		in.Lease = 15 * time.Minute
	}
	r := &Reservation{ID: ids.NewAt(ids.Reservation, now), LeaseUntil: now.Add(in.Lease)}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if in.AccountMax > 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM reservations WHERE account_id = ? AND status = 'active' AND lease_until > ?`,
				in.AccountID, now.UnixMilli()).Scan(&n); err != nil {
				return err
			}
			if n >= in.AccountMax {
				return &CapacityError{Limit: "account_concurrency", Current: n, Max: in.AccountMax}
			}
		}
		if in.BindingMax > 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM reservations WHERE binding_id = ? AND status = 'active' AND lease_until > ?`,
				in.BindingID, now.UnixMilli()).Scan(&n); err != nil {
				return err
			}
			if n >= in.BindingMax {
				return &CapacityError{Limit: "binding_concurrency", Current: n, Max: in.BindingMax}
			}
		}
		if s.BudgetCheck != nil {
			if err := s.BudgetCheck(ctx, tx, in, now); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO reservations(id, request_id, attempt_id, account_id, binding_id, project_id, workspace_id, status, est_cost_e10, currency, created_at, lease_until)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`, r.ID, in.RequestID, nullStr(in.AttemptID), in.AccountID, in.BindingID, nullStr(in.ProjectID), nullStr(in.WorkspaceID),
			int64(in.EstimatedCost), nullStr(in.Currency), now.UnixMilli(), r.LeaseUntil.UnixMilli())
		return err
	})
	if err != nil {
		var be *BudgetExceeded
		if errors.As(err, &be) && s.OnBudgetExceeded != nil {
			s.OnBudgetExceeded(context.WithoutCancel(ctx), be, now)
		}
		return nil, err
	}
	return r, nil
}

// Settle ends a reservation (committed after a completed attempt, released
// when nothing was executed).
func (s *Service) Settle(ctx context.Context, id, status, reason string) error {
	if status != "committed" && status != "released" {
		return fmt.Errorf("invalid settle status %q", status)
	}
	_, err := s.db.W.ExecContext(ctx, `UPDATE reservations SET status = ?, settled_at = ?, settle_reason = ? WHERE id = ? AND status = 'active'`,
		status, s.clock.Now().UnixMilli(), reason, id)
	return err
}

// Entry is one ledger row.
type Entry struct {
	Kind           string // usage | unknown_usage | correction | external_observed
	RequestID      string
	AttemptID      string
	SessionID      string
	WorkspaceID    string
	ProjectID      string
	RunRef         string
	BindingID      string
	AccountID      string
	ProviderID     string
	Model          string
	Billing        string
	Usage          ir.Usage
	UsageSource    string
	Cost           *money.Amount
	Currency       string
	CostKind       string
	PriceVersion   string
	CorrectsID     string
	IdempotencyKey string
	Provenance     map[string]any
	// Refs are the request's trusted references (mint-time bound_refs and
	// the client's external_refs; never client headers). They are indexed in
	// ledger_refs in the same transaction as the row (D-041). A correction
	// without refs inherits the corrected row's.
	Refs map[string]string
	At   time.Time
}

// Record appends an entry and its refs in one transaction. A repeated
// idempotency key is a no-op; it returns the id of the stored row.
// An entry with nothing to index (no non-empty refs and no correction to
// copy them from) skips the transaction, so the gateway's pre-dispatch writes
// stay a single statement.
func (s *Service) Record(ctx context.Context, e Entry) (string, error) {
	if !hasRefs(e.Refs) && e.CorrectsID == "" {
		return s.RecordTx(ctx, s.db.W, e)
	}
	var id string
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = s.RecordTx(ctx, tx, e)
		return err
	})
	return id, err
}

// RecordTx appends an entry using ex. Callers pass a transaction so the
// row and its ref index rows commit together.
func (s *Service) RecordTx(ctx context.Context, ex store.Execer, e Entry) (string, error) {
	if e.IdempotencyKey == "" {
		return "", errors.New("ledger entry requires an idempotency key")
	}
	if e.At.IsZero() {
		e.At = s.clock.Now()
	}
	if e.CostKind == "" {
		e.CostKind = "none"
	}
	u := e.Usage
	if u.InputIncludesCache && u.InputTokens != nil {
		// Store input tokens excluding cache categories so sums are
		// comparable across providers.
		u.InputTokens = u.UncachedInput()
	}
	prov, _ := json.Marshal(e.Provenance)
	if len(prov) == 0 || string(prov) == "null" {
		prov = []byte("{}")
	}
	var cost any
	if e.Cost != nil {
		cost = int64(*e.Cost)
	}
	id := ids.NewAt(ids.Ledger, e.At)
	res, err := ex.ExecContext(ctx, `INSERT INTO ledger(id, at, kind, request_id, attempt_id, session_id, workspace_id, project_id, run_ref,
		binding_id, account_id, provider_id, model, billing, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
		usage_source, cost_e10, currency, cost_kind, price_version, corrects_id, idempotency_key, provenance)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO NOTHING`,
		id, e.At.UnixMilli(), e.Kind, nullStr(e.RequestID), nullStr(e.AttemptID), nullStr(e.SessionID), nullStr(e.WorkspaceID),
		nullStr(e.ProjectID), nullStr(e.RunRef), nullStr(e.BindingID), nullStr(e.AccountID), nullStr(e.ProviderID), nullStr(e.Model),
		nullStr(e.Billing), ptr(u.InputTokens), ptr(u.OutputTokens), ptr(u.CacheReadTokens), ptr(u.CacheWriteTokens), ptr(u.ReasoningTokens),
		e.UsageSource, cost, nullStr(e.Currency), e.CostKind, nullStr(e.PriceVersion), nullStr(e.CorrectsID), e.IdempotencyKey, string(prov))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var existing string
		_ = ex.QueryRowContext(ctx, `SELECT id FROM ledger WHERE idempotency_key = ?`, e.IdempotencyKey).Scan(&existing)
		return existing, nil
	}
	if hasRefs(e.Refs) {
		// One statement for every ref. Empty values are not indexed: they
		// would merge into the "" group of rows without the key.
		var q strings.Builder
		q.WriteString(`INSERT OR IGNORE INTO ledger_refs(key, value, at, ledger_id) VALUES `)
		args := make([]any, 0, 4*len(e.Refs))
		for k, v := range e.Refs {
			if v == "" {
				continue
			}
			if len(args) > 0 {
				q.WriteString(", ")
			}
			q.WriteString("(?, ?, ?, ?)")
			args = append(args, k, v, e.At.UnixMilli(), id)
		}
		if _, err := ex.ExecContext(ctx, q.String(), args...); err != nil {
			return "", err
		}
	} else if e.CorrectsID != "" {
		if _, err := ex.ExecContext(ctx, `INSERT OR IGNORE INTO ledger_refs(key, value, at, ledger_id)
			SELECT key, value, ?, ? FROM ledger_refs WHERE ledger_id = ?`, e.At.UnixMilli(), id, e.CorrectsID); err != nil {
			return "", err
		}
	}
	return id, nil
}

// hasRefs reports whether refs holds a value worth indexing.
func hasRefs(refs map[string]string) bool {
	for _, v := range refs {
		if v != "" {
			return true
		}
	}
	return false
}

// requestRefs reads the trusted refs stored on a request row (nil when the
// row is missing, e.g. its telemetry was dropped).
func requestRefs(ctx context.Context, ex store.Execer, requestID string) (map[string]string, error) {
	var raw string
	err := ex.QueryRowContext(ctx, `SELECT trusted_refs FROM requests WHERE id = ?`, requestID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var refs map[string]string
	if raw != "" && json.Unmarshal([]byte(raw), &refs) != nil {
		refs = nil // malformed refs leave the row unattributed
	}
	return refs, nil
}

// Reap expires leases that ran out (abandoned reservations).
func (s *Service) Reap(ctx context.Context) (int64, error) {
	res, err := s.db.W.ExecContext(ctx, `UPDATE reservations SET status = 'expired', settled_at = ?, settle_reason = 'lease expired'
		WHERE status = 'active' AND lease_until <= ?`, s.clock.Now().UnixMilli(), s.clock.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecoverStartup expires reservations left active by a previous process and
// records their usage as unknown (the upstream may or may not have executed
// them; nothing is replayed).
func (s *Service) RecoverStartup(ctx context.Context) (int, error) {
	type row struct{ id, req, att, acc, binding string }
	rows, err := store.Query(ctx, s.db.W, func(r *sql.Rows) (row, error) {
		var x row
		var att sql.NullString
		err := r.Scan(&x.id, &x.req, &att, &x.acc, &x.binding)
		x.att = att.String
		return x, err
	}, `SELECT id, request_id, attempt_id, account_id, binding_id FROM reservations WHERE status = 'active'`)
	if err != nil {
		return 0, err
	}
	now := s.clock.Now()
	for _, x := range rows {
		err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE reservations SET status = 'expired', settled_at = ?, settle_reason = 'process restart' WHERE id = ?`,
				now.UnixMilli(), x.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE requests SET status = 'error', error_kind = 'interrupted', error_message = 'gateway restarted during the request',
				finished_at = ? WHERE id = ? AND status = 'in_progress'`, now.UnixMilli(), x.req); err != nil {
				return err
			}
			refs, err := requestRefs(ctx, tx, x.req)
			if err != nil {
				return err
			}
			_, err = s.RecordTx(ctx, tx, Entry{Kind: "unknown_usage", RequestID: x.req, AttemptID: x.att, AccountID: x.acc, BindingID: x.binding,
				UsageSource: "unknown", IdempotencyKey: "recover:" + x.id, Provenance: map[string]any{"reason": "process restart", "reservation_id": x.id},
				Refs: refs, At: now})
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	if len(rows) > 0 {
		s.log.Warn("accounting.recovered", "reservations", len(rows))
	}
	return len(rows), nil
}

// Run reaps expired leases periodically.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.Reap(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("accounting.reap.failed", "error", err)
			} else if n > 0 {
				s.log.Info("accounting.reaped", "reservations", n)
			}
		}
	}
}

// ---------- cost ----------

// Cost is an estimated cost for usage at list prices.
type Cost struct {
	Amount       money.Amount
	Currency     string
	PriceVersion string
	Kind         string // estimated_api_charge | list_equivalent | none
}

// Estimate prices usage with a model's declared list prices. Billing class
// decides how the figure is labelled: metered accounts get an estimated API
// charge; subscription accounts get a list-price equivalent that is never
// presented as an invoice; local and free accounts get no cost.
func Estimate(m *controlplane.ModelSpec, billing string, u ir.Usage) (*Cost, bool) {
	if m == nil || m.Pricing == nil || billing == controlplane.BillingLocal || billing == controlplane.BillingFree {
		return nil, false
	}
	if u.InputTokens == nil && u.OutputTokens == nil {
		return nil, false
	}
	p := m.Pricing
	price := func(s string) money.Amount {
		a, err := money.ParseDecimal(s)
		if err != nil {
			return 0
		}
		return a
	}
	var total money.Amount
	add := func(tokens *int64, per string) {
		if tokens == nil || per == "" {
			return
		}
		c, err := money.CostForTokens(*tokens, price(per))
		if err == nil {
			total, _ = money.Add(total, c)
		}
	}
	add(u.UncachedInput(), p.InputPerMTok)
	add(u.OutputTokens, p.OutputPerMTok)
	cacheRead := p.CacheReadPerMTok
	if cacheRead == "" {
		cacheRead = p.InputPerMTok
	}
	add(u.CacheReadTokens, cacheRead)
	cacheWrite := p.CacheWritePerMTok
	if cacheWrite == "" {
		cacheWrite = p.InputPerMTok
	}
	add(u.CacheWriteTokens, cacheWrite)
	kind := "estimated_api_charge"
	if billing == controlplane.BillingSubscription {
		kind = "list_equivalent"
	}
	return &Cost{Amount: total, Currency: p.Currency, PriceVersion: p.PriceVersion, Kind: kind}, true
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ptr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
