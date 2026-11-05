// Package traces stores short-lived, encrypted interaction content.
//
// Content lives in traces.db, separate from long-lived accounting metadata.
// Each trace section is sealed with a data key shared by one (trace policy,
// expiry day) bucket; the key is wrapped by the master key and destroyed
// once every trace in its bucket has expired (cryptographic erasure).
// Expiry is enforced at read time, rows and search entries are removed in
// bounded batches, and the search index only ever holds redacted text.
//
// Capture never blocks a request: jobs are queued on a bounded channel and
// dropped (and counted) when the writer is behind.
package traces

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Capture is one request's trace material, assembled by the gateway.
type Capture struct {
	RequestID string
	BindingID string
	SessionID string
	PlanID    string
	PolicyID  string
	Policy    cp.TracePolicySpec
	// Redaction redacts masked modes (nil = built-in credential detectors).
	Redaction *privacy.Policy
	// Masking is the plan's outbound masking policy, when one ran. Its
	// detectors and configured values are also redacted from masked modes
	// and always from the search index, so nothing the policy kept from the
	// provider is stored in clear.
	Masking *privacy.Policy
	// Original is the client's request before outbound masking; Sent is
	// what went upstream.
	Original *ir.Request
	Sent     *ir.Request
	Response *ir.Response
	Events   []ir.Event
	Attempts any
	Routing  any
	Usage    any
	Time     time.Time
}

// Meta is a trace's metadata (no content).
type Meta struct {
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
	BindingID string    `json:"binding_id"`
	SessionID string    `json:"session_id"`
	PlanID    string    `json:"plan_id"`
	PolicyID  string    `json:"policy_id"`
	Mode      string    `json:"mode" enum:"masked_diagnostic,masked_full,unredacted"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	SizeBytes int64     `json:"size_bytes"`
	Truncated bool      `json:"truncated"`
	Sections  []string  `json:"sections"`
}

// Content is a decrypted trace.
type Content struct {
	Meta
	Sections map[string]json.RawMessage `json:"sections"`
}

// Errors.
var (
	ErrNotFound = errors.New("trace not found or expired")
	ErrErased   = errors.New("trace content was erased")
)

// Service is the trace store.
type Service struct {
	DB      *store.DB
	Keyring *crypto.Keyring
	Clock   clock.Clock
	Log     *slog.Logger

	q        chan *Capture
	dropped  atomic.Int64
	overQuot atomic.Int64
	mu       sync.Mutex
	deks     map[string][]byte // key id -> DEK
}

// New creates the service.
func New(db *store.DB, kr *crypto.Keyring, c clock.Clock, log *slog.Logger) *Service {
	if c == nil {
		c = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{DB: db, Keyring: kr, Clock: c, Log: log, q: make(chan *Capture, 256), deks: map[string][]byte{}}
}

// Dropped reports captures dropped because the writer was behind;
// OverQuota reports captures skipped by per-plan quotas.
func (s *Service) Dropped() int64   { return s.dropped.Load() }
func (s *Service) OverQuota() int64 { return s.overQuot.Load() }

// Enqueue queues a capture without blocking.
func (s *Service) Enqueue(c *Capture) {
	select {
	case s.q <- c:
	default:
		s.dropped.Add(1)
	}
}

// Run writes captures and sweeps expired content until ctx ends.
func (s *Service) Run(ctx context.Context) {
	// FTS5 keeps deleted terms in its segments until they are merged;
	// secure-delete removes them at delete time (persistent table option).
	if _, err := s.DB.W.ExecContext(ctx, `INSERT INTO trace_search(trace_search, rank) VALUES('secure-delete', 1)`); err != nil {
		s.Log.Warn("traces.search_secure_delete_unavailable", "error", err.Error())
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-s.q:
			if err := s.write(ctx, c); err != nil {
				s.Log.Warn("traces.write_failed", "request_id", c.RequestID, "error", err.Error())
			}
		case <-t.C:
			if _, err := s.Sweep(ctx); err != nil {
				s.Log.Warn("traces.sweep_failed", "error", err.Error())
			}
		}
	}
}

// Flush writes queued captures synchronously (tests and shutdown).
func (s *Service) Flush(ctx context.Context) {
	for {
		select {
		case c := <-s.q:
			_ = s.write(ctx, c)
		default:
			return
		}
	}
}

// ---------- building sections ----------

const diagnosticKeep = 1000

func truncateMiddle(s string) string {
	if len(s) <= 2*diagnosticKeep {
		return s
	}
	return s[:diagnosticKeep] + fmt.Sprintf(" …[%d characters omitted]… ", len(s)-2*diagnosticKeep) + s[len(s)-diagnosticKeep:]
}

// view renders a request for the trace in the given mode.
// redactors applies each policy's redaction in turn (nil entries skipped).
type redactors []*privacy.Policy

func (rs redactors) Redact(s string) string {
	for _, p := range rs {
		if p != nil {
			s = p.Redact(s)
		}
	}
	return s
}

func view(r *ir.Request, mode string, red redactors, search *strings.Builder) any {
	if r == nil {
		return nil
	}
	text := func(s string) string {
		if mode != cp.TraceUnredacted {
			s = red.Redact(s)
		}
		search.WriteString(red.Redact(s))
		search.WriteByte('\n')
		if mode == cp.TraceMaskedDiagnostic {
			s = truncateMiddle(s)
		}
		return s
	}
	type part struct {
		Kind     string `json:"kind"`
		Text     string `json:"text,omitempty"`
		ToolName string `json:"tool_name,omitempty"`
		ToolID   string `json:"tool_id,omitempty"`
		Args     string `json:"arguments,omitempty"`
		Media    any    `json:"media,omitempty"`
		Sealed   string `json:"sealed,omitempty"`
	}
	var parts func(ps []ir.Part) []part
	parts = func(ps []ir.Part) []part {
		out := []part{}
		for _, p := range ps {
			v := part{Kind: string(p.Kind)}
			switch {
			case p.ToolCall != nil:
				v.ToolName, v.ToolID, v.Args = p.ToolCall.Name, p.ToolCall.ID, text(p.ToolCall.Arguments)
			case p.ToolResult != nil:
				v.ToolID = p.ToolResult.CallID
				var b strings.Builder
				for _, c := range parts(p.ToolResult.Content) {
					b.WriteString(c.Text)
				}
				v.Text = b.String()
			case p.Media != nil:
				// Attachments are recorded as metadata only.
				v.Media = map[string]any{"mime_type": p.Media.MimeType, "bytes": len(p.Media.Data), "remote": p.Media.URL != ""}
			case p.Sealed != nil:
				v.Sealed = p.Sealed.Family + "/" + p.Sealed.Kind + " (opaque, not captured)"
			case p.Reasoning != nil:
				v.Text = text(p.Reasoning.Text)
			default:
				v.Text = text(p.Text)
			}
			out = append(out, v)
		}
		return out
	}
	type msg struct {
		Role  string `json:"role"`
		Parts []part `json:"parts"`
	}
	msgs := []msg{}
	for _, m := range r.Messages {
		msgs = append(msgs, msg{Role: string(m.Role), Parts: parts(m.Parts)})
	}
	tools := []string{}
	for _, t := range r.Tools {
		tools = append(tools, t.Name)
	}
	return map[string]any{"model": r.Model, "system": parts(r.System), "messages": msgs, "tools": tools, "stream": r.Stream}
}

func (s *Service) sections(c *Capture, mode string) (map[string]any, string) {
	base := c.Redaction
	if base == nil {
		base = privacy.DefaultRedaction()
	}
	red := redactors{base, c.Masking}
	var search strings.Builder
	want := map[string]bool{}
	for _, x := range c.Policy.Sections {
		want[x] = true
	}
	out := map[string]any{}
	src := c.Original
	if src == nil {
		src = c.Sent
	}
	if want["inbound_request"] {
		out["inbound_request"] = view(src, mode, red, &search)
	}
	if want["upstream_request"] && c.Sent != nil {
		// What left the gateway is already masked; it is redacted again for
		// masked modes so trace redaction stays independent.
		out["upstream_request"] = view(c.Sent, mode, red, &search)
	}
	if want["response"] && c.Response != nil {
		out["response"] = view(&ir.Request{Messages: []ir.Message{{Role: ir.RoleAssistant, Parts: c.Response.Parts}}}, mode, red, &search)
	}
	if want["stream_events"] && len(c.Events) > 0 {
		type ev struct {
			Kind  string `json:"kind"`
			Index int    `json:"index"`
			Bytes int    `json:"bytes,omitempty"`
			Stop  string `json:"stop_reason,omitempty"`
		}
		evs := make([]ev, 0, len(c.Events))
		for _, e := range c.Events {
			evs = append(evs, ev{Kind: string(e.Kind), Index: e.Index, Bytes: len(e.Delta), Stop: string(e.StopReason)})
		}
		out["stream_events"] = evs
	}
	if want["attempts"] {
		out["attempts"] = c.Attempts
	}
	if want["routing"] {
		out["routing"] = c.Routing
	}
	if want["usage"] {
		out["usage"] = c.Usage
	}
	return out, search.String()
}

// ---------- write ----------

func (s *Service) dek(ctx context.Context, tx *sql.Tx, bucket string, expires time.Time) (string, []byte, error) {
	var id, kekID string
	var wrapped []byte
	err := tx.QueryRowContext(ctx, `SELECT id, kek_id, wrapped_dek FROM trace_keys WHERE bucket = ?`, bucket).Scan(&id, &kekID, &wrapped)
	if err == nil {
		s.mu.Lock()
		d, ok := s.deks[id]
		s.mu.Unlock()
		if ok {
			return id, d, nil
		}
		d, err = s.Keyring.UnwrapDEK(kekID, wrapped, "trace-key:"+bucket)
		if err != nil {
			return "", nil, err
		}
		s.mu.Lock()
		s.deks[id] = d
		s.mu.Unlock()
		return id, d, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	d := crypto.RandomBytes(32)
	kekID, wrapped, err = s.Keyring.WrapDEK(d, "trace-key:"+bucket)
	if err != nil {
		return "", nil, err
	}
	id = ids.New(ids.TraceKey)
	if _, err := tx.ExecContext(ctx, `INSERT INTO trace_keys(id, bucket, kek_id, wrapped_dek, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, bucket, kekID, wrapped, expires.UnixMilli(), s.Clock.Now().UnixMilli()); err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	s.deks[id] = d
	s.mu.Unlock()
	return id, d, nil
}

func (s *Service) write(ctx context.Context, c *Capture) error {
	now := c.Time
	if now.IsZero() {
		now = s.Clock.Now()
	}
	mode := c.Policy.EffectiveMode(now)
	if mode == cp.TraceOff {
		return nil
	}
	secs, search := s.sections(c, mode)
	// Serialize, truncating the bulkiest sections first when over the limit.
	limit := c.Policy.MaxTraceBytes
	enc := map[string][]byte{}
	total := 0
	for k, v := range secs {
		b, _ := json.Marshal(v)
		enc[k] = b
		total += len(b)
	}
	truncated := false
	for _, k := range []string{"stream_events", "response", "upstream_request", "inbound_request", "routing", "attempts", "usage"} {
		if total <= limit {
			break
		}
		if b, ok := enc[k]; ok {
			total -= len(b)
			enc[k] = []byte(`{"truncated":true}`)
			total += len(enc[k])
			truncated = true
		}
	}
	expires := now.Add(c.Policy.TTL())
	day := expires.UTC().Truncate(24 * time.Hour)
	bucket := c.PolicyID + "|" + day.Format("2006-01-02")
	id := ids.NewAt(ids.Trace, now)
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var used int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM traces WHERE plan_id = ? AND expires_at > ?`, c.PlanID, now.UnixMilli()).Scan(&used); err != nil {
			return err
		}
		if used+int64(total) > c.Policy.MaxTotalBytes {
			s.overQuot.Add(1)
			return nil
		}
		keyID, dek, err := s.dek(ctx, tx, bucket, day.Add(24*time.Hour))
		if err != nil {
			return err
		}
		names := make([]string, 0, len(enc))
		for k := range enc {
			names = append(names, k)
		}
		sortStrings(names)
		sj, _ := json.Marshal(names)
		if _, err := tx.ExecContext(ctx, `INSERT INTO traces(id, request_id, key_id, mode, created_at, expires_at, size_bytes, truncated, sections, plan_id, policy_id, binding_id, session_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, c.RequestID, keyID, mode, now.UnixMilli(), expires.UnixMilli(), total, b2i(truncated), string(sj),
			c.PlanID, c.PolicyID, nullable(c.BindingID), nullable(c.SessionID)); err != nil {
			return err
		}
		for _, k := range names {
			ct, err := crypto.SealWithDEK(dek, enc[k], id+"|"+k)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO trace_parts(trace_id, section, ord, ciphertext) VALUES (?, ?, 0, ?)`, id, k, ct); err != nil {
				return err
			}
		}
		if strings.TrimSpace(search) != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO trace_search(trace_id, body) VALUES (?, ?)`, id, search); err != nil {
				return err
			}
		}
		return nil
	})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------- read ----------

const metaCols = `id, request_id, COALESCE(binding_id,''), COALESCE(session_id,''), COALESCE(plan_id,''), COALESCE(policy_id,''), mode, created_at, expires_at, size_bytes, truncated, sections`

func scanMeta(r interface{ Scan(...any) error }) (Meta, error) {
	var m Meta
	var created, expires int64
	var trunc int
	var secs string
	err := r.Scan(&m.ID, &m.RequestID, &m.BindingID, &m.SessionID, &m.PlanID, &m.PolicyID, &m.Mode, &created, &expires, &m.SizeBytes, &trunc, &secs)
	m.CreatedAt, m.ExpiresAt, m.Truncated = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC(), trunc == 1
	_ = json.Unmarshal([]byte(secs), &m.Sections)
	if m.Sections == nil {
		m.Sections = []string{}
	}
	return m, err
}

// Filter selects traces.
type Filter struct {
	RequestID string
	SessionID string
	BindingID string
	Limit     int
}

// List returns live traces' metadata, newest first.
func (s *Service) List(ctx context.Context, f Filter) ([]Meta, error) {
	q := `SELECT ` + metaCols + ` FROM traces WHERE expires_at > ?`
	args := []any{s.Clock.Now().UnixMilli()}
	for col, v := range map[string]string{"request_id": f.RequestID, "session_id": f.SessionID, "binding_id": f.BindingID} {
		if v != "" {
			q += ` AND ` + col + ` = ?` //nolint:gosec // col comes from the static map
			args = append(args, v)
		}
	}
	lim := f.Limit
	if lim <= 0 || lim > 500 {
		lim = 100
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, lim)
	return store.Query(ctx, s.DB.R, func(r *sql.Rows) (Meta, error) { return scanMeta(r) }, q, args...)
}

// Get decrypts a live trace. Expired traces are refused even if their rows
// have not been swept yet.
func (s *Service) Get(ctx context.Context, id string) (*Content, error) {
	m, err := scanMeta(s.DB.R.QueryRowContext(ctx, `SELECT `+metaCols+` FROM traces WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !m.ExpiresAt.After(s.Clock.Now()) {
		return nil, ErrNotFound
	}
	var keyID, bucket, kekID string
	var wrapped []byte
	err = s.DB.R.QueryRowContext(ctx, `SELECT k.id, k.bucket, k.kek_id, k.wrapped_dek FROM traces t JOIN trace_keys k ON k.id = t.key_id WHERE t.id = ?`, id).
		Scan(&keyID, &bucket, &kekID, &wrapped)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrErased
	}
	if err != nil {
		return nil, err
	}
	dek, err := s.Keyring.UnwrapDEK(kekID, wrapped, "trace-key:"+bucket)
	if err != nil {
		return nil, ErrErased
	}
	parts, err := store.Query(ctx, s.DB.R, func(r *sql.Rows) (struct {
		sec string
		ct  []byte
	}, error) {
		var x struct {
			sec string
			ct  []byte
		}
		err := r.Scan(&x.sec, &x.ct)
		return x, err
	}, `SELECT section, ciphertext FROM trace_parts WHERE trace_id = ? ORDER BY section, ord`, id)
	if err != nil {
		return nil, err
	}
	c := &Content{Meta: m, Sections: map[string]json.RawMessage{}}
	for _, p := range parts {
		pt, err := crypto.OpenWithDEK(dek, p.ct, id+"|"+p.sec)
		if err != nil {
			return nil, ErrErased
		}
		c.Sections[p.sec] = pt
	}
	return c, nil
}

// Hit is a search result.
type Hit struct {
	Meta
	Snippet string `json:"snippet" doc:"Excerpt of redacted text."`
}

// Search matches redacted text of live traces.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := ftsQuery(query)
	if q == "" {
		return []Hit{}, nil
	}
	return store.Query(ctx, s.DB.R, func(r *sql.Rows) (Hit, error) {
		var h Hit
		var created, expires int64
		var trunc int
		var secs string
		err := r.Scan(&h.ID, &h.RequestID, &h.BindingID, &h.SessionID, &h.PlanID, &h.PolicyID, &h.Mode, &created, &expires, &h.SizeBytes, &trunc, &secs, &h.Snippet)
		h.CreatedAt, h.ExpiresAt, h.Truncated = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC(), trunc == 1
		_ = json.Unmarshal([]byte(secs), &h.Sections)
		return h, err
	}, `SELECT t.id, t.request_id, COALESCE(t.binding_id,''), COALESCE(t.session_id,''), COALESCE(t.plan_id,''), COALESCE(t.policy_id,''), t.mode,
		t.created_at, t.expires_at, t.size_bytes, t.truncated, t.sections, snippet(trace_search, 1, '[', ']', '…', 12)
		FROM trace_search JOIN traces t ON t.id = trace_search.trace_id
		WHERE trace_search MATCH ? AND t.expires_at > ? ORDER BY t.id DESC LIMIT ?`, q, s.Clock.Now().UnixMilli(), limit)
}

// ftsQuery quotes user terms so FTS syntax cannot be injected.
func ftsQuery(q string) string {
	var terms []string
	for _, t := range strings.Fields(q) {
		t = strings.ReplaceAll(t, `"`, `""`)
		if t != "" {
			terms = append(terms, `"`+t+`"`)
		}
	}
	return strings.Join(terms, " ")
}

// Delete removes traces by id, request or session; it returns how many.
func (s *Service) Delete(ctx context.Context, id, requestID, sessionID string) (int64, error) {
	var where string
	var arg string
	switch {
	case id != "":
		where, arg = "id = ?", id
	case requestID != "":
		where, arg = "request_id = ?", requestID
	case sessionID != "":
		where, arg = "session_id = ?", sessionID
	default:
		return 0, errors.New("traces: delete needs an id, request or session")
	}
	var n int64
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := secureDelete(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM trace_search WHERE trace_id IN (SELECT id FROM traces WHERE `+where+`)`, arg); err != nil { //nolint:gosec // where is one of three constants
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM traces WHERE `+where, arg) //nolint:gosec // where is one of three constants
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return n, err
	}
	if n > 0 {
		s.purge(ctx)
	}
	return n, nil
}

// secureDelete makes the writer overwrite freed pages with zeros, so
// deleted trace content does not linger in the database file.
func secureDelete(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `PRAGMA secure_delete = ON`)
	return err
}

// purge merges the search index so deleted entries leave its segments, and
// truncates the write-ahead log, which may still hold copies of deleted
// pages. Failures are logged: the rows are already gone.
func (s *Service) purge(ctx context.Context) {
	if _, err := s.DB.W.ExecContext(ctx, `INSERT INTO trace_search(trace_search) VALUES('optimize')`); err != nil {
		s.Log.Warn("traces.search_optimize_failed", "error", err.Error())
	}
	if _, err := s.DB.W.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		s.Log.Warn("traces.checkpoint_failed", "error", err.Error())
	}
}

// SweepResult reports one expiry pass.
type SweepResult struct {
	Traces int64 `json:"traces"`
	Keys   int64 `json:"keys"`
}

// Sweep deletes expired traces (with their parts and search entries) in
// bounded batches, then destroys expired bucket keys.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	var out SweepResult
	now := s.Clock.Now().UnixMilli()
	for i := 0; i < 20; i++ {
		var n int64
		err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
			if err := secureDelete(ctx, tx); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM trace_search WHERE trace_id IN (SELECT id FROM traces WHERE expires_at <= ? LIMIT 500)`, now); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `DELETE FROM traces WHERE id IN (SELECT id FROM traces WHERE expires_at <= ? LIMIT 500)`, now)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			return nil
		})
		if err != nil {
			return out, err
		}
		out.Traces += n
		if n < 500 {
			break
		}
	}
	rows, err := store.Query(ctx, s.DB.R, func(r *sql.Rows) (string, error) {
		var id string
		err := r.Scan(&id)
		return id, err
	}, `SELECT id FROM trace_keys WHERE expires_at <= ?`, now)
	if err != nil {
		return out, err
	}
	for _, id := range rows {
		res, err := s.DB.W.ExecContext(ctx, `DELETE FROM trace_keys WHERE id = ? AND NOT EXISTS (SELECT 1 FROM traces WHERE key_id = ? AND expires_at > ?)`, id, id, now)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out.Keys += n
			s.mu.Lock()
			delete(s.deks, id)
			s.mu.Unlock()
		}
	}
	if out.Traces > 0 || out.Keys > 0 {
		s.purge(ctx)
	}
	return out, nil
}
