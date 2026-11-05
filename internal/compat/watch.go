package compat

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// AdapterVersion identifies this build's protocol adapters in records.
const AdapterVersion = "hp-adapters-1"

// Change kinds.
const (
	KindNewEvent       = "new_event"
	KindNewField       = "new_field"
	KindNewEnum        = "new_enum_value"
	KindTypeChange     = "type_change"
	KindExpectedAbsent = "expected_absent"
	KindSequence       = "unsupported_sequence"
	KindSchemaReject   = "schema_rejection"
	KindParseFailure   = "parse_failure"
	KindRestored       = "restored"
)

// Work item statuses.
var WorkStatuses = []string{"new", "acknowledged", "investigating", "fix_available", "verified", "resolved", "ignored"}

// Signal is one observation handed to the watcher.
type Signal struct {
	Direction  string // request | response
	Protocol   ir.Protocol
	ProviderID string
	Endpoint   string
	Harness    string
	HarnessVer string
	RequestID  string
	Route      string // provider|path
	Action     map[string]any
	// One of:
	Unknown []string // decoder-reported unknown events/fields ("event:x", "delta:y")
	Raw     []byte   // bounded raw SSE or JSON sample (passthrough)
	Err     *ir.Error
	Learn   bool // Raw is healthy traffic used to extend the baseline
}

// Change is a recorded structural change.
type Change struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Direction     string          `json:"direction"`
	Protocol      string          `json:"protocol"`
	ProviderID    string          `json:"provider_id"`
	Endpoint      string          `json:"endpoint"`
	AdapterVer    string          `json:"adapter_version"`
	ClientHarness string          `json:"client_harness"`
	ClientVersion string          `json:"client_version"`
	Path          string          `json:"path"`
	Detail        json.RawMessage `json:"detail"`
	Confidence    string          `json:"confidence" enum:"high,medium,low"`
	Impact        string          `json:"impact" enum:"breaking,additive,unknown"`
	FirstSeen     time.Time       `json:"first_seen"`
	LastSeen      time.Time       `json:"last_seen"`
	Count         int64           `json:"count"`
	// Recency distinguishes "not observed recently" from current changes;
	// it is never evidence of removal.
	Recency string `json:"recency" enum:"current,not_observed_recently"`
}

// Watch records observed protocol changes. It is fed through a bounded
// queue and never blocks the caller; dropped signals reduce coverage.
type Watch struct {
	DB    *store.DB
	Clock clock.Clock
	Log   *slog.Logger

	q         chan Signal
	processed atomic.Int64
	dropped   atomic.Int64

	mu       sync.Mutex
	baseline map[string]map[string]string // protocol -> path -> type
	enums    map[string]map[string]bool   // protocol|path -> values
}

// NewWatch creates a watcher with a baseline derived from this build's
// own protocol encoders.
func NewWatch(db *store.DB, c clock.Clock, log *slog.Logger) *Watch {
	if c == nil {
		c = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	w := &Watch{DB: db, Clock: c, Log: log, q: make(chan Signal, 1024), baseline: map[string]map[string]string{}, enums: map[string]map[string]bool{}}
	w.seedBaseline()
	return w
}

// Observe queues a signal; it drops (and counts) when the queue is full.
func (w *Watch) Observe(s Signal) {
	select {
	case w.q <- s:
	default:
		w.dropped.Add(1)
	}
}

// Coverage reports processed and dropped signals since start.
func (w *Watch) Coverage() (processed, dropped int64) { return w.processed.Load(), w.dropped.Load() }

// Run processes signals until ctx ends.
func (w *Watch) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-w.q:
			w.handle(ctx, s)
		}
	}
}

// Drain processes queued signals synchronously (tests).
func (w *Watch) Drain(ctx context.Context) {
	for {
		select {
		case s := <-w.q:
			w.handle(ctx, s)
		default:
			return
		}
	}
}

// ---------- structural fingerprints ----------

// dynamicParents hold user-defined or dynamic keys (tool schemas, tool
// arguments, metadata); their keys are not protocol structure.
var dynamicParents = map[string]bool{"input": true, "arguments": true, "parameters": true, "properties": true, "input_schema": true,
	"metadata": true, "schema": true, "args": true, "json_schema": true, "tool_input": true, "logit_bias": true}

// enumPaths carry protocol enum values worth tracking.
var enumPaths = map[string]bool{"type": true, "delta.type": true, "content_block.type": true, "delta.stop_reason": true, "stop_reason": true,
	"choices[].finish_reason": true, "choices[].delta.role": true, "role": true, "status": true, "item.type": true, "response.status": true,
	"object": true, "candidates[].finishReason": true}

var idLike = regexp.MustCompile(`^[A-Za-z0-9_-]*\d[A-Za-z0-9_-]{15,}$`)

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// walk collects path -> type and enum values; values themselves are
// never kept except short enum tokens.
func walk(v any, path string, depth int, out map[string]string, enums map[string]string) {
	if depth > 8 || len(out) > 400 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			seg := k
			if idLike.MatchString(k) {
				seg = "*"
			}
			p := seg
			if path != "" {
				p = path + "." + seg
			}
			out[p] = jsonType(x[k])
			if dynamicParents[k] {
				continue // user-defined structure
			}
			if s, ok := x[k].(string); ok && enumPaths[p] && len(s) <= 64 && !strings.ContainsAny(s, " \n") {
				enums[p] = s
			}
			walk(x[k], p, depth+1, out, enums)
		}
	case []any:
		for i, it := range x {
			if i >= 8 {
				break
			}
			walk(it, path+"[]", depth+1, out, enums)
		}
	}
}

type sseEvent struct {
	name string
	data map[string]any
}

// parseSample splits a raw sample into JSON events (SSE or a JSON body).
func parseSample(raw []byte) []sseEvent {
	var out []sseEvent
	trim := bytes.TrimSpace(raw)
	if len(trim) > 0 && trim[0] == '{' {
		var m map[string]any
		if json.Unmarshal(trim, &m) == nil {
			return []sseEvent{{data: m}}
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	name := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var m map[string]any
			if d != "[DONE]" && json.Unmarshal([]byte(d), &m) == nil {
				out = append(out, sseEvent{name: name, data: m})
			}
		case line == "":
			name = ""
		}
		if len(out) >= 2000 {
			break
		}
	}
	return out
}

func eventType(e sseEvent) string {
	if t, ok := e.data["type"].(string); ok && len(t) <= 64 {
		return t
	}
	if o, ok := e.data["object"].(string); ok {
		return o
	}
	if e.name != "" {
		return e.name
	}
	if _, ok := e.data["candidates"]; ok {
		return "generate_content_response"
	}
	return "unknown"
}

// requiredPaths are fields whose absence is reported (low confidence).
var requiredPaths = map[string][]string{
	"anthropic_messages|message_start":       {"message", "message.id", "message.model"},
	"anthropic_messages|content_block_start": {"index", "content_block", "content_block.type"},
	"anthropic_messages|content_block_delta": {"index", "delta", "delta.type"},
	"anthropic_messages|message_delta":       {"delta", "usage"},
	"openai_chat|chat.completion.chunk":      {"id", "choices"},
	"openai_responses|response.created":      {"response", "response.id"},
	"openai_responses|response.completed":    {"response", "response.status"},
}

// ---------- baseline ----------

type bufWriter struct {
	h http.Header
	b bytes.Buffer
}

func (w *bufWriter) Header() http.Header         { return w.h }
func (w *bufWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
func (w *bufWriter) WriteHeader(int)             {}
func (w *bufWriter) Flush()                      {}

func (w *Watch) seedBaseline() {
	one := int64(1)
	evs := []ir.Event{
		{Kind: ir.EvStart, ResponseID: "resp_baseline", Model: "baseline"},
		{Kind: ir.EvUsage, Usage: &ir.Usage{InputTokens: &one, OutputTokens: &one, CacheReadTokens: &one, CacheWriteTokens: &one}},
		{Kind: ir.EvPartStart, Index: 0, Part: &ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{}}},
		{Kind: ir.EvDelta, Index: 0, Delta: "r"},
		{Kind: ir.EvPartStop, Index: 0, Part: &ir.Part{Kind: ir.PartReasoning, Sealed: &ir.Sealed{Family: "anthropic", Kind: "thinking_signature", Data: "s"}}},
		{Kind: ir.EvPartStart, Index: 1, Part: &ir.Part{Kind: ir.PartText}},
		{Kind: ir.EvDelta, Index: 1, Delta: "t"},
		{Kind: ir.EvPartStop, Index: 1},
		{Kind: ir.EvPartStart, Index: 2, Part: &ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: "call_1", Name: "f", Kind: "function"}}},
		{Kind: ir.EvDelta, Index: 2, Delta: `{}`},
		{Kind: ir.EvPartStop, Index: 2},
		{Kind: ir.EvUsage, Usage: &ir.Usage{OutputTokens: &one}},
	}
	for _, p := range []ir.Protocol{ir.AnthropicMessages, ir.OpenAIChat, ir.OpenAIResponses, ir.Gemini} {
		for _, stop := range []ir.StopReason{ir.StopEndTurn, ir.StopToolUse, ir.StopMaxTokens} {
			bw := &bufWriter{h: http.Header{}}
			cs := protocol.NewClientStream(p, wire.NewSSEWriter(bw), protocol.StreamMeta{ResponseID: "resp_baseline", Model: "baseline",
				Created: time.Unix(0, 0), IncludeUsage: true, UpstreamFamily: p.Family()})
			for _, e := range evs {
				_ = cs.Event(e)
			}
			_ = cs.Finish(stop)
			w.learn(string(p), bw.b.Bytes())
		}
	}
}

func (w *Watch) learn(proto string, raw []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	base := w.baseline[proto]
	if base == nil {
		base = map[string]string{}
		w.baseline[proto] = base
	}
	for _, e := range parseSample(raw) {
		et := eventType(e)
		paths, enums := map[string]string{}, map[string]string{}
		walk(e.data, "", 0, paths, enums)
		base["event:"+et] = "event"
		for p, t := range paths {
			base[et+"|"+p] = t
		}
		for p, v := range enums {
			k := proto + "|" + et + "|" + p
			if w.enums[k] == nil {
				w.enums[k] = map[string]bool{}
			}
			w.enums[k][v] = true
		}
	}
}

// ---------- analysis ----------

type finding struct {
	kind, path, confidence, impact string
	detail                         map[string]any
}

func (w *Watch) analyze(proto string, raw []byte) []finding {
	w.mu.Lock()
	defer w.mu.Unlock()
	base := w.baseline[proto]
	var out []finding
	seen := map[string]bool{}
	add := func(f finding) {
		k := f.kind + "|" + f.path
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	started := map[float64]bool{}
	for _, e := range parseSample(raw) {
		et := eventType(e)
		if base["event:"+et] == "" {
			add(finding{KindNewEvent, et, "medium", "additive", map[string]any{"event": et}})
			continue
		}
		paths, enums := map[string]string{}, map[string]string{}
		walk(e.data, "", 0, paths, enums)
		for p, t := range paths {
			if bt, ok := base[et+"|"+p]; !ok {
				add(finding{KindNewField, et + "." + p, "medium", "additive", map[string]any{"event": et, "path": p, "type": t}})
			} else if bt != t && bt != "null" && t != "null" {
				add(finding{KindTypeChange, et + "." + p, "medium", "breaking", map[string]any{"event": et, "path": p, "expected": bt, "observed": t}})
			}
		}
		for p, v := range enums {
			k := proto + "|" + et + "|" + p
			if known := w.enums[k]; known != nil && !known[v] && p != "type" {
				add(finding{KindNewEnum, et + "." + p, "medium", "additive", map[string]any{"event": et, "path": p, "value": v}})
			}
		}
		for _, rp := range requiredPaths[proto+"|"+et] {
			if _, ok := paths[rp]; !ok {
				add(finding{KindExpectedAbsent, et + "." + rp, "low", "unknown", map[string]any{"event": et, "path": rp,
					"note": "expected field absent in observed traffic; not proof of removal"}})
			}
		}
		if proto == string(ir.AnthropicMessages) {
			idx, _ := e.data["index"].(float64)
			switch et {
			case "content_block_start":
				started[idx] = true
			case "content_block_delta", "content_block_stop":
				if !started[idx] {
					add(finding{KindSequence, et + " before content_block_start", "medium", "breaking", map[string]any{"event": et}})
				}
			}
		}
	}
	return out
}

var numberish = regexp.MustCompile(`\d+`)
var quoted = regexp.MustCompile("[\"'`][^\"'`]{0,200}[\"'`]")

// normalizeRejection strips values from an error message so that equal
// rejection patterns deduplicate.
func normalizeRejection(msg string) string {
	m := quoted.ReplaceAllString(msg, "<v>")
	m = numberish.ReplaceAllString(m, "<n>")
	if len(m) > 200 {
		m = m[:200]
	}
	return m
}

func (w *Watch) handle(ctx context.Context, s Signal) {
	w.processed.Add(1)
	proto := string(s.Protocol)
	if s.Learn {
		w.learn(proto, s.Raw)
		return
	}
	var fs []finding
	switch {
	case len(s.Unknown) > 0:
		for _, u := range s.Unknown {
			kind := KindNewField
			if strings.HasPrefix(u, "event:") || strings.HasPrefix(u, "content_block:") {
				kind = KindNewEvent
			}
			fs = append(fs, finding{kind, u, "high", "additive", map[string]any{"reported_by": "decoder", "name": u}})
		}
	case s.Err != nil:
		kind := KindSchemaReject
		if s.Err.Kind == ir.ErrProtocol {
			kind = KindParseFailure
		}
		fs = append(fs, finding{kind, normalizeRejection(s.Err.Message), "high", "breaking", map[string]any{"pattern": normalizeRejection(s.Err.Message),
			"error_kind": string(s.Err.Kind)}})
	case len(s.Raw) > 0:
		fs = w.analyze(proto, s.Raw)
	case s.Action != nil:
		fs = append(fs, finding{KindRestored, s.Route, "high", "unknown", s.Action})
	}
	for _, f := range fs {
		if err := w.record(ctx, s, f); err != nil {
			w.Log.Warn("protocol_watch.record_failed", "error", err.Error())
		}
	}
}

func fingerprint(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:16])
}

func (w *Watch) record(ctx context.Context, s Signal, f finding) error {
	now := w.Clock.Now()
	fp := fingerprint(f.kind, s.Direction, string(s.Protocol), s.ProviderID, f.path, AdapterVersion)
	detail, _ := json.Marshal(f.detail)
	return w.DB.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM protocol_changes WHERE fingerprint = ?`, fp).Scan(&id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE protocol_changes SET last_seen = ?, count = count + 1,
				client_harness = COALESCE(?, client_harness), client_version = COALESCE(?, client_version) WHERE id = ?`,
				now.UnixMilli(), nullable(s.Harness), nullable(s.HarnessVer), id)
			if err != nil {
				return err
			}
			return w.touchWorkItem(ctx, tx, id, s, now, false)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		id = ids.NewAt(ids.ProtocolChange, now)
		if _, err := tx.ExecContext(ctx, `INSERT INTO protocol_changes(id, fingerprint, kind, direction, protocol, provider_id, endpoint, adapter_version,
			client_harness, client_version, path, detail, confidence, impact, first_seen, last_seen, count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			id, fp, f.kind, s.Direction, string(s.Protocol), nullable(s.ProviderID), nullable(s.Endpoint), AdapterVersion, nullable(s.Harness),
			nullable(s.HarnessVer), f.path, string(detail), f.confidence, f.impact, now.UnixMilli(), now.UnixMilli()); err != nil {
			return err
		}
		if err := w.touchWorkItem(ctx, tx, id, s, now, true); err != nil {
			return err
		}
		_, err = events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "protocol", Entity: "change", Action: "detected"},
			Severity: map[string]string{"breaking": events.SeverityWarning}[f.impact], OccurredAt: now, DedupeKey: "protocol:" + fp,
			Subjects: map[string]string{"change_id": id, "provider_id": s.ProviderID},
			Data:     map[string]any{"kind": f.kind, "direction": s.Direction, "protocol": string(s.Protocol), "path": f.path, "impact": f.impact, "confidence": f.confidence}})
		return err
	})
}

// touchWorkItem creates the change's work item or adds evidence to it. A
// resolved item that is observed again is reopened; ignored stays ignored.
func (w *Watch) touchWorkItem(ctx context.Context, tx *sql.Tx, changeID string, s Signal, now time.Time, created bool) error {
	if created {
		var row struct{ kind, path, detail string }
		_ = tx.QueryRowContext(ctx, `SELECT kind, path, detail FROM protocol_changes WHERE id = ?`, changeID).Scan(&row.kind, &row.path, &row.detail)
		title := fmt.Sprintf("%s: %s (%s, %s)", row.kind, truncate(row.path, 120), s.Protocol, s.Direction)
		diff, _ := json.Marshal(map[string]any{"kind": row.kind, "path": row.path, "detail": json.RawMessage(row.detail)})
		evidence := []string{}
		if s.RequestID != "" {
			evidence = append(evidence, s.RequestID)
		}
		ev, _ := json.Marshal(evidence)
		routes := []string{}
		if s.Route != "" {
			routes = append(routes, s.Route)
		}
		rt, _ := json.Marshal(routes)
		act, _ := json.Marshal(s.Action)
		if s.Action == nil {
			act = []byte("{}")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO work_items(id, change_id, title, diff, evidence, routes, action, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'new', ?, ?)`, ids.NewAt(ids.WorkItem, now), changeID, title, string(diff), string(ev), string(rt), string(act),
			now.UnixMilli(), now.UnixMilli())
		return err
	}
	var id, status, evidence, routes string
	err := tx.QueryRowContext(ctx, `SELECT id, status, evidence, routes FROM work_items WHERE change_id = ?`, changeID).Scan(&id, &status, &evidence, &routes)
	if err != nil {
		return nil //nolint:nilerr // a change without a work item (should not happen) is still recorded
	}
	var ev, rt []string
	_ = json.Unmarshal([]byte(evidence), &ev)
	_ = json.Unmarshal([]byte(routes), &rt)
	if s.RequestID != "" && len(ev) < 20 && !contains(ev, s.RequestID) {
		ev = append(ev, s.RequestID)
	}
	if s.Route != "" && !contains(rt, s.Route) {
		rt = append(rt, s.Route)
	}
	if status == "resolved" || status == "verified" {
		status = "new"
	}
	eb, _ := json.Marshal(ev)
	rb, _ := json.Marshal(rt)
	_, err = tx.ExecContext(ctx, `UPDATE work_items SET evidence = ?, routes = ?, status = ?, updated_at = ? WHERE id = ?`, string(eb), string(rb), status, now.UnixMilli(), id)
	return err
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- queries ----------

// Changes lists recorded changes, most recent first.
func (w *Watch) Changes(ctx context.Context, limit int) ([]Change, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	recentCut := w.Clock.Now().Add(-7 * 24 * time.Hour)
	return store.Query(ctx, w.DB.R, func(r *sql.Rows) (Change, error) {
		var c Change
		var prov, ep, h, hv sql.NullString
		var detail string
		var first, last int64
		err := r.Scan(&c.ID, &c.Kind, &c.Direction, &c.Protocol, &prov, &ep, &c.AdapterVer, &h, &hv, &c.Path, &detail, &c.Confidence, &c.Impact, &first, &last, &c.Count)
		c.ProviderID, c.Endpoint, c.ClientHarness, c.ClientVersion = prov.String, ep.String, h.String, hv.String
		c.Detail = json.RawMessage(detail)
		c.FirstSeen, c.LastSeen = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
		c.Recency = "current"
		if c.LastSeen.Before(recentCut) {
			c.Recency = "not_observed_recently"
		}
		return c, err
	}, `SELECT id, kind, direction, protocol, provider_id, endpoint, adapter_version, client_harness, client_version, path, detail, confidence, impact,
		first_seen, last_seen, count FROM protocol_changes ORDER BY last_seen DESC LIMIT ?`, limit)
}

// WorkItem is a deduplicated follow-up for a change.
type WorkItem struct {
	ID              string          `json:"id"`
	ChangeID        string          `json:"change_id"`
	Title           string          `json:"title"`
	Diff            json.RawMessage `json:"diff" doc:"Sanitized structural diff (no content values)."`
	Evidence        []string        `json:"evidence" doc:"Request ids; their content may already have expired."`
	Routes          []string        `json:"routes"`
	Action          json.RawMessage `json:"action" doc:"Action taken (e.g. passthrough activated) and features lost."`
	Status          string          `json:"status" enum:"new,acknowledged,investigating,fix_available,verified,resolved,ignored"`
	IssueURL        string          `json:"issue_url"`
	CommitRef       string          `json:"commit_ref"`
	ReleaseRef      string          `json:"release_ref"`
	Fixture         json.RawMessage `json:"fixture,omitempty"`
	FixtureReviewed bool            `json:"fixture_reviewed"`
	Notes           string          `json:"notes"`
	Revision        int64           `json:"revision"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

const workCols = `id, change_id, title, diff, evidence, routes, action, status, issue_url, commit_ref, release_ref, fixture, fixture_reviewed, notes, revision, created_at, updated_at`

func scanWork(r interface{ Scan(...any) error }) (WorkItem, error) {
	var wi WorkItem
	var diff, ev, rt, act string
	var issue, commit, release, fixture, notes sql.NullString
	var reviewed int
	var created, updated int64
	err := r.Scan(&wi.ID, &wi.ChangeID, &wi.Title, &diff, &ev, &rt, &act, &wi.Status, &issue, &commit, &release, &fixture, &reviewed, &notes, &wi.Revision, &created, &updated)
	wi.Diff, wi.Action = json.RawMessage(diff), json.RawMessage(act)
	_ = json.Unmarshal([]byte(ev), &wi.Evidence)
	_ = json.Unmarshal([]byte(rt), &wi.Routes)
	if wi.Evidence == nil {
		wi.Evidence = []string{}
	}
	if wi.Routes == nil {
		wi.Routes = []string{}
	}
	wi.IssueURL, wi.CommitRef, wi.ReleaseRef, wi.Notes = issue.String, commit.String, release.String, notes.String
	if fixture.Valid {
		wi.Fixture = json.RawMessage(fixture.String)
	}
	wi.FixtureReviewed = reviewed == 1
	wi.CreatedAt, wi.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return wi, err
}

// WorkItems lists work items, optionally by status.
func (w *Watch) WorkItems(ctx context.Context, status string) ([]WorkItem, error) {
	q := `SELECT ` + workCols + ` FROM work_items`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY updated_at DESC LIMIT 500`
	return store.Query(ctx, w.DB.R, func(r *sql.Rows) (WorkItem, error) { return scanWork(r) }, q, args...)
}

// GetWorkItem loads one work item.
func (w *Watch) GetWorkItem(ctx context.Context, id string) (WorkItem, error) {
	return scanWork(w.DB.R.QueryRowContext(ctx, `SELECT `+workCols+` FROM work_items WHERE id = ?`, id))
}

// WorkUpdate changes a work item's triage fields.
type WorkUpdate struct {
	Status          *string `json:"status,omitempty" enum:"new,acknowledged,investigating,fix_available,verified,resolved,ignored"`
	IssueURL        *string `json:"issue_url,omitempty" maxLength:"500"`
	CommitRef       *string `json:"commit_ref,omitempty" maxLength:"200"`
	ReleaseRef      *string `json:"release_ref,omitempty" maxLength:"200"`
	Notes           *string `json:"notes,omitempty" maxLength:"4000"`
	FixtureReviewed *bool   `json:"fixture_reviewed,omitempty"`
}

// ErrStale means the revision did not match.
var ErrStale = errors.New("work item was changed concurrently")

// UpdateWorkItem applies an update when rev matches the current revision
// (rev 0 skips the check).
func (w *Watch) UpdateWorkItem(ctx context.Context, id string, rev int64, u WorkUpdate) (WorkItem, error) {
	err := w.DB.Tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanWork(tx.QueryRowContext(ctx, `SELECT `+workCols+` FROM work_items WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if rev != 0 && rev != cur.Revision {
			return ErrStale
		}
		set := func(p *string, v string) string {
			if p != nil {
				return *p
			}
			return v
		}
		reviewed := cur.FixtureReviewed
		if u.FixtureReviewed != nil {
			reviewed = *u.FixtureReviewed
		}
		_, err = tx.ExecContext(ctx, `UPDATE work_items SET status = ?, issue_url = ?, commit_ref = ?, release_ref = ?, notes = ?, fixture_reviewed = ?,
			revision = revision + 1, updated_at = ? WHERE id = ?`, set(u.Status, cur.Status), nullable(set(u.IssueURL, cur.IssueURL)),
			nullable(set(u.CommitRef, cur.CommitRef)), nullable(set(u.ReleaseRef, cur.ReleaseRef)), nullable(set(u.Notes, cur.Notes)), b2i(reviewed),
			w.Clock.Now().UnixMilli(), id)
		return err
	})
	if err != nil {
		return WorkItem{}, err
	}
	return w.GetWorkItem(ctx, id)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GenerateFixture builds a synthetic fixture from a work item's structural
// diff: placeholder values only, never captured content. It needs review
// before use.
func (w *Watch) GenerateFixture(ctx context.Context, id string) (WorkItem, error) {
	wi, err := w.GetWorkItem(ctx, id)
	if err != nil {
		return wi, err
	}
	var diff struct {
		Kind   string         `json:"kind"`
		Path   string         `json:"path"`
		Detail map[string]any `json:"detail"`
	}
	_ = json.Unmarshal(wi.Diff, &diff)
	ev, _ := diff.Detail["event"].(string)
	path, _ := diff.Detail["path"].(string)
	typ, _ := diff.Detail["type"].(string)
	if typ == "" {
		typ, _ = diff.Detail["observed"].(string)
	}
	obj := map[string]any{}
	if ev != "" {
		obj["type"] = ev
	}
	if path != "" {
		setPath(obj, path, placeholder(typ, diff.Detail["value"]))
	}
	fx, _ := json.MarshalIndent(map[string]any{"synthetic": true, "source_work_item": wi.ID, "kind": diff.Kind, "event": obj,
		"note": "Generated from a structural diff with placeholder values; review before adding to testdata/protocol."}, "", "  ")
	_, err = w.DB.W.ExecContext(ctx, `UPDATE work_items SET fixture = ?, fixture_reviewed = 0, revision = revision + 1, updated_at = ? WHERE id = ?`,
		string(fx), w.Clock.Now().UnixMilli(), id)
	if err != nil {
		return wi, err
	}
	return w.GetWorkItem(ctx, id)
}

func placeholder(typ string, enumValue any) any {
	if s, ok := enumValue.(string); ok {
		return s // enum tokens are protocol vocabulary, not content
	}
	switch typ {
	case "number":
		return 0
	case "bool":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	case "null":
		return nil
	}
	return "<synthetic>"
}

func setPath(obj map[string]any, path string, v any) {
	segs := strings.Split(strings.ReplaceAll(path, "[]", ""), ".")
	cur := obj
	for i, s := range segs {
		if i == len(segs)-1 {
			cur[s] = v
			return
		}
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
}
