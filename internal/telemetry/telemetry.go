// Package telemetry records request activity without ever blocking the
// gateway: records go through a bounded channel to a batch writer. When the
// channel is full, records are dropped, counted and the loss is reported as
// reduced coverage.
package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// RequestRecord is a logical request snapshot. Start writes the row; Finish
// upserts the final state.
type RequestRecord struct {
	ID                string
	CreatedAt         time.Time
	FinishedAt        *time.Time
	BindingID         string
	CredentialID      string
	WorkspaceID       string
	ProjectID         string
	TrustedRefs       map[string]string
	Claims            map[string]string
	SessionID         string
	SessionConfidence string
	Harness           string
	HarnessConfidence string
	HarnessVersion    string
	InboundProtocol   string
	Stream            bool
	RequestedModel    string
	AliasID           string
	Resolution        string
	PlanID            string
	PlanVersion       int64
	RequiredCaps      []string
	Status            string
	ErrorKind         string
	ErrorMessage      string
	StopReason        string
	TTFTms            *int64
	DurationMs        *int64
	AttemptCount      int
	FinalAccountID    string
	FinalProviderID   string
	FinalModel        string
	Mode              string
	InputTokens       *int64
	OutputTokens      *int64
	CacheReadTokens   *int64
	CacheWriteTokens  *int64
	ReasoningTokens   *int64
	UsageSource       string
	CostE10           *int64
	Currency          string
	CostKind          string
	ToolsOffered      int
	ToolCalls         int
	ToolResults       int
	ToolResultErrors  int
	ToolNames         []string
	Losses            any
	Decision          any
	Privacy           any
	Intent            string
	IntentSource      string
	IntentDetail      any
	// Fingerprints identify the request's messages (hashes only) and
	// ToolResultIDs its tool results; the writer deduplicates them per
	// session once the request finishes.
	Fingerprints  []string
	ToolResultIDs []string
	BytesOut      int64
	ClientIP      string
	UserAgent     string
}

// AttemptRecord is one upstream attempt.
type AttemptRecord struct {
	ID               string
	RequestID        string
	N                int
	StartedAt        time.Time
	FinishedAt       *time.Time
	AccountID        string
	ProviderID       string
	Model            string
	UpstreamProtocol string
	Mode             string
	Status           string
	HTTPStatus       int
	ErrorKind        string
	ErrorMessage     string
	Executed         bool
	TTFTms           *int64
	DurationMs       *int64
	FailoverReason   string
	RetryAfterS      int
	InputTokens      *int64
	OutputTokens     *int64
	UnknownEvents    []string
}

type item struct {
	req   *RequestRecord
	att   *AttemptRecord
	ses   *sessionRow
	flush chan struct{}
}

// Writer persists activity asynchronously.
type Writer struct {
	db      *store.DB
	log     *slog.Logger
	m       *observability.Metrics
	ch      chan item
	dropped atomic.Int64
	wg      sync.WaitGroup
	running atomic.Bool
}

// NewWriter creates a writer with a bounded queue.
func NewWriter(db *store.DB, log *slog.Logger, m *observability.Metrics, capacity int) *Writer {
	if capacity <= 0 {
		capacity = 8192
	}
	return &Writer{db: db, log: log, m: m, ch: make(chan item, capacity)}
}

// Dropped returns how many records were dropped under back-pressure.
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

func (w *Writer) enqueue(it item) {
	select {
	case w.ch <- it:
	default:
		w.dropped.Add(1)
		if w.m != nil {
			w.m.TelemetryDropped.WithLabelValues("activity").Inc()
		}
	}
}

// Request enqueues a request snapshot (insert or update).
func (w *Writer) Request(r RequestRecord) { w.enqueue(item{req: &r}) }

// Attempt enqueues an attempt snapshot (insert or update).
func (w *Writer) Attempt(a AttemptRecord) { w.enqueue(item{att: &a}) }

// Run drains the queue until ctx is done, then flushes what remains.
func (w *Writer) Run(ctx context.Context) {
	w.wg.Add(1)
	defer w.wg.Done()
	w.running.Store(true)
	defer w.running.Store(false)
	batch := make([]item, 0, 256)
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.write(context.Background(), batch); err != nil {
			w.log.Warn("telemetry.write.failed", "error", err, "records", len(batch))
			w.dropped.Add(int64(len(batch)))
		}
		batch = batch[:0]
	}
	for {
		select {
		case it := <-w.ch:
			if it.flush != nil {
				flush()
				close(it.flush)
				continue
			}
			batch = append(batch, it)
			if len(batch) >= 256 {
				flush()
			}
		case <-t.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case it := <-w.ch:
					if it.flush != nil {
						close(it.flush)
						continue
					}
					batch = append(batch, it)
				default:
					flush()
					return
				}
			}
		}
	}
}

// Flush synchronously writes queued records (tests and shutdown).
func (w *Writer) Flush(ctx context.Context) error {
	if w.running.Load() {
		done := make(chan struct{})
		select {
		case w.ch <- item{flush: done}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var batch []item
	for {
		select {
		case it := <-w.ch:
			if it.flush != nil {
				close(it.flush)
				continue
			}
			batch = append(batch, it)
		default:
			return w.write(ctx, batch)
		}
	}
}

func js(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func jsOr(v any, def string) string {
	s := js(v)
	if s == "null" {
		return def
	}
	return s
}

func ms(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func i64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func ns(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (w *Writer) write(ctx context.Context, batch []item) error {
	if len(batch) == 0 {
		return nil
	}
	return w.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, it := range batch {
			switch {
			case it.req != nil:
				r := it.req
				if _, err := tx.ExecContext(ctx, `INSERT INTO requests(id, created_at, finished_at, binding_id, credential_id, workspace_id, project_id,
					trusted_refs, claims, session_id, session_confidence, harness, harness_confidence, harness_version, inbound_protocol, stream,
					requested_model, alias_id, resolution, plan_id, plan_version, required_caps, status, error_kind, error_message, stop_reason,
					ttft_ms, duration_ms, attempt_count, final_account_id, final_provider_id, final_model, mode, input_tokens, output_tokens,
					cache_read_tokens, cache_write_tokens, reasoning_tokens, usage_source, cost_e10, currency, cost_kind, tools_offered, tool_calls,
					tool_results, tool_result_errors, tool_names, losses, decision, bytes_out, client_ip, user_agent, privacy)
					VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
					ON CONFLICT(id) DO UPDATE SET finished_at=excluded.finished_at, workspace_id=excluded.workspace_id, project_id=excluded.project_id,
					session_id=excluded.session_id, session_confidence=excluded.session_confidence, alias_id=excluded.alias_id, resolution=excluded.resolution,
					plan_id=excluded.plan_id, plan_version=excluded.plan_version, required_caps=excluded.required_caps, status=excluded.status,
					error_kind=excluded.error_kind, error_message=excluded.error_message, stop_reason=excluded.stop_reason, ttft_ms=excluded.ttft_ms,
					duration_ms=excluded.duration_ms, attempt_count=excluded.attempt_count, final_account_id=excluded.final_account_id,
					final_provider_id=excluded.final_provider_id, final_model=excluded.final_model, mode=excluded.mode, input_tokens=excluded.input_tokens,
					output_tokens=excluded.output_tokens, cache_read_tokens=excluded.cache_read_tokens, cache_write_tokens=excluded.cache_write_tokens,
					reasoning_tokens=excluded.reasoning_tokens, usage_source=excluded.usage_source, cost_e10=excluded.cost_e10, currency=excluded.currency,
					cost_kind=excluded.cost_kind, tools_offered=excluded.tools_offered, tool_calls=excluded.tool_calls, tool_results=excluded.tool_results,
					tool_result_errors=excluded.tool_result_errors, tool_names=excluded.tool_names, losses=excluded.losses, decision=excluded.decision,
					bytes_out=excluded.bytes_out, privacy=excluded.privacy`,
					r.ID, r.CreatedAt.UnixMilli(), ms(r.FinishedAt), ns(r.BindingID), ns(r.CredentialID), ns(r.WorkspaceID), ns(r.ProjectID),
					jsOr(r.TrustedRefs, "{}"), jsOr(r.Claims, "{}"), ns(r.SessionID), nonEmpty(r.SessionConfidence, "unknown"), ns(r.Harness),
					ns(r.HarnessConfidence), ns(r.HarnessVersion), r.InboundProtocol, b2i(r.Stream), ns(r.RequestedModel), ns(r.AliasID),
					ns(r.Resolution), ns(r.PlanID), nullInt(r.PlanVersion), jsOr(r.RequiredCaps, "[]"), r.Status, ns(r.ErrorKind), ns(r.ErrorMessage),
					ns(r.StopReason), i64(r.TTFTms), i64(r.DurationMs), r.AttemptCount, ns(r.FinalAccountID), ns(r.FinalProviderID), ns(r.FinalModel),
					ns(r.Mode), i64(r.InputTokens), i64(r.OutputTokens), i64(r.CacheReadTokens), i64(r.CacheWriteTokens), i64(r.ReasoningTokens),
					ns(r.UsageSource), i64(r.CostE10), ns(r.Currency), ns(r.CostKind), r.ToolsOffered, r.ToolCalls, r.ToolResults, r.ToolResultErrors,
					jsOr(r.ToolNames, "[]"), jsOr(r.Losses, "[]"), jsOr(r.Decision, "{}"), r.BytesOut, ns(r.ClientIP), ns(r.UserAgent), jsOr(r.Privacy, "{}")); err != nil {
					return err
				}
				if r.Status != "in_progress" && r.Fingerprints != nil {
					if err := dedupe(ctx, tx, r); err != nil {
						return err
					}
				}
			case it.att != nil:
				a := it.att
				if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(id, request_id, n, started_at, finished_at, account_id, provider_id, model,
					upstream_protocol, mode, status, http_status, error_kind, error_message, executed, ttft_ms, duration_ms, failover_reason,
					retry_after_s, input_tokens, output_tokens, unknown_events) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
					ON CONFLICT(id) DO UPDATE SET finished_at=excluded.finished_at, status=excluded.status, http_status=excluded.http_status,
					error_kind=excluded.error_kind, error_message=excluded.error_message, executed=excluded.executed, ttft_ms=excluded.ttft_ms,
					duration_ms=excluded.duration_ms, failover_reason=excluded.failover_reason, retry_after_s=excluded.retry_after_s,
					input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens, unknown_events=excluded.unknown_events,
					mode=excluded.mode, upstream_protocol=excluded.upstream_protocol`,
					a.ID, a.RequestID, a.N, a.StartedAt.UnixMilli(), ms(a.FinishedAt), a.AccountID, a.ProviderID, a.Model, ns(a.UpstreamProtocol),
					ns(a.Mode), a.Status, nullInt(int64(a.HTTPStatus)), ns(a.ErrorKind), ns(a.ErrorMessage), b2i(a.Executed), i64(a.TTFTms),
					i64(a.DurationMs), ns(a.FailoverReason), nullInt(int64(a.RetryAfterS)), i64(a.InputTokens), i64(a.OutputTokens),
					jsOr(a.UnknownEvents, "[]")); err != nil {
					return err
				}
			case it.ses != nil:
				s := it.ses
				if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id, binding_id, key_hash, source, confidence, harness, first_seen, last_seen, requests)
					VALUES (?,?,?,?,?,?,?,?,1) ON CONFLICT(binding_id, key_hash) DO UPDATE SET last_seen = excluded.last_seen, requests = requests + 1,
					harness = COALESCE(excluded.harness, harness)`,
					s.id, s.binding, s.keyHash, s.source, s.confidence, ns(s.harness), s.at.UnixMilli(), s.at.UnixMilli()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func nonEmpty(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// dedupe records which of a finished request's messages and tool results
// are new for its session. Without a known session every occurrence is
// recorded as uncertain (never merged across conversations).
func dedupe(ctx context.Context, tx *sql.Tx, r *RequestRecord) error {
	newMsgs, repeated, uniqueTools := 0, 0, 0
	occurrence := "uncertain"
	if r.SessionID != "" && r.SessionConfidence != "unknown" && r.SessionConfidence != "" {
		occurrence = "unique"
		seen := func(fp string) (bool, error) {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_messages(session_id, fingerprint, first_request_id, first_seen) VALUES (?, ?, ?, ?)`,
				r.SessionID, fp, r.ID, r.CreatedAt.UnixMilli())
			if err != nil {
				return false, err
			}
			n, _ := res.RowsAffected()
			return n == 0, nil
		}
		for _, fp := range r.Fingerprints {
			old, err := seen(fp)
			if err != nil {
				return err
			}
			if old {
				repeated++
			} else {
				newMsgs++
			}
		}
		for _, id := range r.ToolResultIDs {
			old, err := seen("tool_result:" + id)
			if err != nil {
				return err
			}
			if !old {
				uniqueTools++
			}
		}
	} else {
		newMsgs, uniqueTools = len(r.Fingerprints), len(r.ToolResultIDs)
	}
	_, err := tx.ExecContext(ctx, `UPDATE requests SET new_messages = ?, repeated_messages = ?, unique_tool_results = ?, occurrence = ?,
		intent = ?, intent_source = ?, intent_detail = ? WHERE id = ?`, newMsgs, repeated, uniqueTools, occurrence,
		ns(r.Intent), ns(r.IntentSource), jsOr(r.IntentDetail, "{}"), r.ID)
	return err
}
