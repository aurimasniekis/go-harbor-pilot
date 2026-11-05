// Package activityapi exposes recorded gateway activity: logical requests,
// upstream attempts, ledger entries, sessions and summaries. Content is never
// stored here; only metadata.
package activityapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Tokens groups token counts (nil = not reported by the upstream).
type Tokens struct {
	Input      *int64 `json:"input"`
	Output     *int64 `json:"output"`
	CacheRead  *int64 `json:"cache_read"`
	CacheWrite *int64 `json:"cache_write"`
	Reasoning  *int64 `json:"reasoning"`
}

// CostDTO is a cost figure with its meaning.
type CostDTO struct {
	Amount       string `json:"amount" doc:"Exact decimal."`
	Currency     string `json:"currency"`
	Kind         string `json:"kind" enum:"estimated_api_charge,list_equivalent,actual_api_charge,none" doc:"list_equivalent is not an invoice."`
	PriceVersion string `json:"price_version,omitempty"`
}

// RequestSummary is a row in the activity list.
type RequestSummary struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	BindingID      string     `json:"binding_id"`
	ProjectID      string     `json:"project_id"`
	SessionID      string     `json:"session_id"`
	Harness        string     `json:"harness"`
	Protocol       string     `json:"inbound_protocol"`
	Stream         bool       `json:"stream"`
	RequestedModel string     `json:"requested_model"`
	AliasID        string     `json:"alias_id"`
	Status         string     `json:"status"`
	ErrorKind      string     `json:"error_kind"`
	StopReason     string     `json:"stop_reason"`
	TTFTms         *int64     `json:"ttft_ms"`
	DurationMs     *int64     `json:"duration_ms"`
	Attempts       int        `json:"attempts"`
	AccountID      string     `json:"final_account_id"`
	ProviderID     string     `json:"final_provider_id"`
	Model          string     `json:"final_model"`
	Mode           string     `json:"mode"`
	Tokens         Tokens     `json:"tokens"`
	UsageSource    string     `json:"usage_source"`
	Cost           *CostDTO   `json:"cost,omitempty"`
	ToolCalls      int        `json:"tool_calls"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Intent         string     `json:"intent,omitempty" doc:"Request intent label, when one was declared, derived by a rule or classified."`
	IntentSource   string     `json:"intent_source,omitempty" enum:"declared,rule,classifier," doc:"Where the intent came from."`
	// IntentDetail describes classifier runs (version, latency, outcome).
	IntentDetail map[string]any `json:"intent_detail,omitempty"`
}

// AttemptDTO is one upstream attempt.
type AttemptDTO struct {
	ID             string     `json:"id"`
	N              int        `json:"n"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	AccountID      string     `json:"account_id"`
	ProviderID     string     `json:"provider_id"`
	Model          string     `json:"model"`
	Upstream       string     `json:"upstream_protocol"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	HTTPStatus     *int64     `json:"http_status,omitempty"`
	ErrorKind      string     `json:"error_kind"`
	ErrorMessage   string     `json:"error_message"`
	Executed       bool       `json:"executed" doc:"The upstream may have processed this attempt (never replayed)."`
	TTFTms         *int64     `json:"ttft_ms"`
	DurationMs     *int64     `json:"duration_ms"`
	FailoverReason string     `json:"failover_reason"`
	RetryAfterS    *int64     `json:"retry_after_s,omitempty"`
	Tokens         Tokens     `json:"tokens"`
	UnknownEvents  []string   `json:"unknown_events"`
}

// LedgerDTO is one ledger entry.
type LedgerDTO struct {
	ID          string          `json:"id"`
	At          time.Time       `json:"at"`
	Kind        string          `json:"kind"`
	AttemptID   string          `json:"attempt_id"`
	AccountID   string          `json:"account_id"`
	Model       string          `json:"model"`
	Billing     string          `json:"billing"`
	Tokens      Tokens          `json:"tokens"`
	UsageSource string          `json:"usage_source"`
	Cost        *CostDTO        `json:"cost,omitempty"`
	Provenance  json.RawMessage `json:"provenance"`
}

// RequestDetail is the full explanation of a request ("Why this route?").
type RequestDetail struct {
	RequestSummary
	CredentialID      string            `json:"credential_id"`
	WorkspaceID       string            `json:"workspace_id"`
	TrustedRefs       map[string]string `json:"trusted_refs"`
	Claims            map[string]string `json:"claims" doc:"Untrusted client hints, recorded with provenance."`
	SessionConfidence string            `json:"session_confidence"`
	HarnessConfidence string            `json:"harness_confidence"`
	HarnessVersion    string            `json:"harness_version"`
	Resolution        string            `json:"resolution"`
	PlanID            string            `json:"plan_id"`
	PlanVersion       *int64            `json:"plan_version"`
	RequiredCaps      []string          `json:"required_capabilities"`
	ErrorMessage      string            `json:"error_message"`
	ToolsOffered      int               `json:"tools_offered"`
	ToolResults       int               `json:"tool_results_seen"`
	ToolResultErrors  int               `json:"tool_result_errors"`
	ToolNames         []string          `json:"tool_names"`
	Losses            json.RawMessage   `json:"losses"`
	Decision          json.RawMessage   `json:"decision"`
	Privacy           json.RawMessage   `json:"privacy" doc:"Masking summary: rule names and counts, never values."`
	BytesOut          int64             `json:"bytes_out"`
	AttemptList       []AttemptDTO      `json:"attempt_list"`
	Ledger            []LedgerDTO       `json:"ledger"`
}

// ObservedSession is an observed session.
type ObservedSession struct {
	ID         string    `json:"id"`
	BindingID  string    `json:"binding_id"`
	Source     string    `json:"source"`
	Confidence string    `json:"confidence"`
	Harness    string    `json:"harness"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Requests   int       `json:"requests"`
}

// ActivitySummary aggregates recent activity.
type ActivitySummary struct {
	From      time.Time        `json:"from"`
	To        time.Time        `json:"to"`
	Requests  int64            `json:"requests"`
	ByStatus  map[string]int64 `json:"by_status"`
	ByHarness map[string]int64 `json:"by_harness"`
	ByModel   map[string]int64 `json:"by_model"`
	Tokens    Tokens           `json:"tokens"`
	Costs     []CostDTO        `json:"costs" doc:"Totals per currency and cost kind (never summed across kinds)."`
	AvgTTFTms *float64         `json:"avg_ttft_ms"`
	Fallbacks int64            `json:"fallbacks" doc:"Requests that needed more than one attempt."`
	Unknown   int64            `json:"unknown_usage" doc:"Requests whose usage the upstream did not report."`
	Dropped   int64            `json:"telemetry_dropped" doc:"Records dropped under back-pressure since start (coverage gap)."`
}

// RequestPage is a page of requests with the total matching the filters.
type RequestPage struct {
	api.Page[RequestSummary]
	Total int64 `json:"total" doc:"Requests matching the filters across all pages."`
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Register adds activity operations.
func Register(a *api.API, db func() *store.DB, dropped func() int64) {
	tags := []string{"Activity"}
	type listIn struct {
		api.PageParams
		BindingID string `query:"binding_id" maxLength:"64"`
		ProjectID string `query:"project_id" maxLength:"64"`
		SessionID string `query:"session_id" maxLength:"64"`
		AccountID string `query:"account_id" maxLength:"64"`
		Status    string `query:"status" enum:"in_progress,ok,error,blocked,canceled," required:"false"`
		Model     string `query:"model" maxLength:"200"`
		Harness   string `query:"harness" maxLength:"32"`
		From      string `query:"from" doc:"RFC 3339; only requests created at or after this time."`
		To        string `query:"to" doc:"RFC 3339; only requests created before this time."`
		Fallbacks bool   `query:"fallbacks" doc:"Only requests that needed more than one attempt."`
		Search    string `query:"q" maxLength:"200" doc:"Request id, or a prefix of the requested or served model."`
	}
	type listOut struct{ Body RequestPage }
	api.Register(a, api.Op{ID: "list-requests", Method: http.MethodGet, Path: "/activity/requests", Summary: "List gateway requests (newest first)",
		Tags: tags, Permission: auth.PermActivityRead}, func(ctx context.Context, in *listIn) (*listOut, error) {
		before, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		where := ` WHERE 1=1`
		var args []any
		for col, v := range map[string]string{"binding_id": in.BindingID, "project_id": in.ProjectID, "session_id": in.SessionID,
			"final_account_id": in.AccountID, "status": in.Status, "requested_model": in.Model, "harness": in.Harness} {
			if v != "" {
				where += ` AND ` + col + ` = ?` //nolint:gosec // col comes from the static map above
				args = append(args, v)
			}
		}
		for _, t := range []struct {
			raw, op, field string
		}{{in.From, ">=", "from"}, {in.To, "<", "to"}} {
			if t.raw == "" {
				continue
			}
			at, err := time.Parse(time.RFC3339, t.raw)
			if err != nil {
				return nil, api.Invalid("invalid "+t.field, api.FieldError{Location: "query." + t.field, Message: "must be RFC 3339"})
			}
			where += ` AND created_at ` + t.op + ` ?`
			args = append(args, at.UnixMilli())
		}
		if in.Fallbacks {
			where += ` AND attempt_count > 1`
		}
		if q := strings.TrimSpace(in.Search); q != "" {
			like := escapeLike(q) + "%"
			where += ` AND (id = ? OR requested_model LIKE ? ESCAPE '\' OR final_model LIKE ? ESCAPE '\')`
			args = append(args, q, like, like)
		}
		out := &listOut{}
		if err := db().R.QueryRowContext(ctx, `SELECT count(*) FROM requests`+where, args...).Scan(&out.Body.Total); err != nil {
			return nil, err
		}
		q := `SELECT ` + summaryCols + ` FROM requests` + where
		if before != "" {
			q += ` AND id < ?`
			args = append(args, before)
		}
		q += ` ORDER BY id DESC LIMIT ?`
		args = append(args, lim+1)
		items, err := store.Query(ctx, db().R, scanSummary, q, args...)
		if err != nil {
			return nil, err
		}
		out.Body.Page = api.Paginate(items, lim, func(r RequestSummary) string { return r.ID })
		return out, nil
	})

	type idIn struct {
		ID string `path:"id" maxLength:"64"`
	}
	type detailOut struct{ Body RequestDetail }
	api.Register(a, api.Op{ID: "get-request", Method: http.MethodGet, Path: "/activity/requests/{id}", Summary: "Request detail with routing explanation, attempts and accounting",
		Tags: tags, Permission: auth.PermActivityRead}, func(ctx context.Context, in *idIn) (*detailOut, error) {
		d, err := loadDetail(ctx, db().R, in.ID)
		if err == sql.ErrNoRows {
			return nil, api.NotFound("request")
		}
		if err != nil {
			return nil, err
		}
		return &detailOut{Body: *d}, nil
	})

	type sessIn struct {
		api.PageParams
		BindingID string `query:"binding_id" maxLength:"64"`
	}
	type sessOut struct{ Body api.Page[ObservedSession] }
	api.Register(a, api.Op{ID: "list-sessions", Method: http.MethodGet, Path: "/activity/sessions", Summary: "List observed sessions (most recent first)",
		Tags: tags, Permission: auth.PermActivityRead}, func(ctx context.Context, in *sessIn) (*sessOut, error) {
		before, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		q := `SELECT id, binding_id, source, confidence, harness, first_seen, last_seen, requests FROM sessions WHERE 1=1`
		var args []any
		if before != "" {
			q += ` AND id < ?`
			args = append(args, before)
		}
		if in.BindingID != "" {
			q += ` AND binding_id = ?`
			args = append(args, in.BindingID)
		}
		q += ` ORDER BY id DESC LIMIT ?`
		args = append(args, lim+1)
		items, err := store.Query(ctx, db().R, func(r *sql.Rows) (ObservedSession, error) {
			var s ObservedSession
			var h sql.NullString
			var first, last int64
			err := r.Scan(&s.ID, &s.BindingID, &s.Source, &s.Confidence, &h, &first, &last, &s.Requests)
			s.Harness, s.FirstSeen, s.LastSeen = h.String, time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
			return s, err
		}, q, args...)
		if err != nil {
			return nil, err
		}
		return &sessOut{Body: api.Paginate(items, lim, func(s ObservedSession) string { return s.ID })}, nil
	})

	type sumIn struct {
		Hours int `query:"hours" minimum:"1" maximum:"720" default:"24"`
	}
	type sumOut struct{ Body ActivitySummary }
	api.Register(a, api.Op{ID: "get-activity-summary", Method: http.MethodGet, Path: "/activity/summary", Summary: "Aggregate recent activity",
		Tags: tags, Permission: auth.PermActivityRead}, func(ctx context.Context, in *sumIn) (*sumOut, error) {
		hours := in.Hours
		if hours <= 0 {
			hours = 24
		}
		to := time.Now().UTC()
		var maxAt sql.NullInt64
		_ = db().R.QueryRowContext(ctx, `SELECT MAX(created_at) FROM requests`).Scan(&maxAt)
		if maxAt.Valid && time.UnixMilli(maxAt.Int64).After(to) {
			to = time.UnixMilli(maxAt.Int64).UTC()
		}
		from := to.Add(-time.Duration(hours) * time.Hour)
		s, err := summarize(ctx, db().R, from, to)
		if err != nil {
			return nil, err
		}
		s.Dropped = dropped()
		return &sumOut{Body: *s}, nil
	})
}

const summaryCols = `id, created_at, finished_at, COALESCE(binding_id,''), COALESCE(project_id,''), COALESCE(session_id,''), COALESCE(harness,''),
	inbound_protocol, stream, COALESCE(requested_model,''), COALESCE(alias_id,''), status, COALESCE(error_kind,''), COALESCE(stop_reason,''),
	ttft_ms, duration_ms, attempt_count, COALESCE(final_account_id,''), COALESCE(final_provider_id,''), COALESCE(final_model,''), COALESCE(mode,''),
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, COALESCE(usage_source,''), cost_e10,
	COALESCE(currency,''), COALESCE(cost_kind,''), tool_calls, COALESCE(intent,''), COALESCE(intent_source,''), COALESCE(intent_detail,'{}')`

func scanSummary(r *sql.Rows) (RequestSummary, error) {
	var s RequestSummary
	var created int64
	var finished, ttft, dur, cost sql.NullInt64
	var in, out, cr, cw, rs sql.NullInt64
	var stream int64
	var currency, costKind, intentDetail string
	err := r.Scan(&s.ID, &created, &finished, &s.BindingID, &s.ProjectID, &s.SessionID, &s.Harness, &s.Protocol, &stream, &s.RequestedModel,
		&s.AliasID, &s.Status, &s.ErrorKind, &s.StopReason, &ttft, &dur, &s.Attempts, &s.AccountID, &s.ProviderID, &s.Model, &s.Mode,
		&in, &out, &cr, &cw, &rs, &s.UsageSource, &cost, &currency, &costKind, &s.ToolCalls,
		&s.Intent, &s.IntentSource, &intentDetail)
	if intentDetail != "" && intentDetail != "{}" {
		_ = json.Unmarshal([]byte(intentDetail), &s.IntentDetail)
	}
	s.CreatedAt = time.UnixMilli(created).UTC()
	s.FinishedAt = tptr(finished)
	s.Stream = stream == 1
	s.TTFTms, s.DurationMs = iptr(ttft), iptr(dur)
	s.Tokens = Tokens{Input: iptr(in), Output: iptr(out), CacheRead: iptr(cr), CacheWrite: iptr(cw), Reasoning: iptr(rs)}
	if cost.Valid {
		s.Cost = &CostDTO{Amount: money.Amount(cost.Int64).String(), Currency: currency, Kind: costKind}
	}
	return s, err
}

func iptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func tptr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

func loadDetail(ctx context.Context, q store.Execer, id string) (*RequestDetail, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+summaryCols+` FROM requests WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var d RequestDetail
	found := false
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		d.RequestSummary, found = s, true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	var refs, claims, caps, names, losses, decision, priv string
	var cred, ws, sconf, hconf, hver, res, plan, emsg sql.NullString
	var pver sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT credential_id, workspace_id, trusted_refs, claims, session_confidence, harness_confidence, harness_version,
		resolution, plan_id, plan_version, required_caps, error_message, tools_offered, tool_results, tool_result_errors, tool_names, losses, decision, bytes_out, privacy
		FROM requests WHERE id = ?`, id).Scan(&cred, &ws, &refs, &claims, &sconf, &hconf, &hver, &res, &plan, &pver, &caps, &emsg,
		&d.ToolsOffered, &d.ToolResults, &d.ToolResultErrors, &names, &losses, &decision, &d.BytesOut, &priv); err != nil {
		return nil, err
	}
	d.CredentialID, d.WorkspaceID, d.SessionConfidence, d.HarnessConfidence, d.HarnessVersion = cred.String, ws.String, sconf.String, hconf.String, hver.String
	d.Resolution, d.PlanID, d.ErrorMessage = res.String, plan.String, emsg.String
	d.PlanVersion = iptr(pver)
	_ = json.Unmarshal([]byte(refs), &d.TrustedRefs)
	_ = json.Unmarshal([]byte(claims), &d.Claims)
	_ = json.Unmarshal([]byte(caps), &d.RequiredCaps)
	_ = json.Unmarshal([]byte(names), &d.ToolNames)
	if d.TrustedRefs == nil {
		d.TrustedRefs = map[string]string{}
	}
	if d.Claims == nil {
		d.Claims = map[string]string{}
	}
	if d.RequiredCaps == nil {
		d.RequiredCaps = []string{}
	}
	if d.ToolNames == nil {
		d.ToolNames = []string{}
	}
	d.Losses, d.Decision, d.Privacy = json.RawMessage(losses), json.RawMessage(decision), json.RawMessage(priv)
	d.AttemptList, err = store.Query(ctx, q, func(r *sql.Rows) (AttemptDTO, error) {
		var a AttemptDTO
		var started int64
		var fin, http_, ttft, dur, ra, in, out sql.NullInt64
		var up, mode, ek, em, fr sql.NullString
		var executed int64
		var unknown string
		err := r.Scan(&a.ID, &a.N, &started, &fin, &a.AccountID, &a.ProviderID, &a.Model, &up, &mode, &a.Status, &http_, &ek, &em, &executed,
			&ttft, &dur, &fr, &ra, &in, &out, &unknown)
		a.StartedAt, a.FinishedAt = time.UnixMilli(started).UTC(), tptr(fin)
		a.Upstream, a.Mode, a.ErrorKind, a.ErrorMessage, a.FailoverReason = up.String, mode.String, ek.String, em.String, fr.String
		a.HTTPStatus, a.TTFTms, a.DurationMs, a.RetryAfterS = iptr(http_), iptr(ttft), iptr(dur), iptr(ra)
		a.Executed = executed == 1
		a.Tokens = Tokens{Input: iptr(in), Output: iptr(out)}
		_ = json.Unmarshal([]byte(unknown), &a.UnknownEvents)
		if a.UnknownEvents == nil {
			a.UnknownEvents = []string{}
		}
		return a, err
	}, `SELECT id, n, started_at, finished_at, account_id, provider_id, model, upstream_protocol, mode, status, http_status, error_kind, error_message,
		executed, ttft_ms, duration_ms, failover_reason, retry_after_s, input_tokens, output_tokens, unknown_events FROM attempts WHERE request_id = ? ORDER BY n`, id)
	if err != nil {
		return nil, err
	}
	if d.AttemptList == nil {
		d.AttemptList = []AttemptDTO{}
	}
	d.Ledger, err = store.Query(ctx, q, func(r *sql.Rows) (LedgerDTO, error) {
		var l LedgerDTO
		var at int64
		var att, acc, model, billing, cur, ck, pv sql.NullString
		var in, out, cr, cw, rs, cost sql.NullInt64
		var prov string
		err := r.Scan(&l.ID, &at, &l.Kind, &att, &acc, &model, &billing, &in, &out, &cr, &cw, &rs, &l.UsageSource, &cost, &cur, &ck, &pv, &prov)
		l.At = time.UnixMilli(at).UTC()
		l.AttemptID, l.AccountID, l.Model, l.Billing = att.String, acc.String, model.String, billing.String
		l.Tokens = Tokens{Input: iptr(in), Output: iptr(out), CacheRead: iptr(cr), CacheWrite: iptr(cw), Reasoning: iptr(rs)}
		if cost.Valid {
			l.Cost = &CostDTO{Amount: money.Amount(cost.Int64).String(), Currency: cur.String, Kind: ck.String, PriceVersion: pv.String}
		}
		l.Provenance = json.RawMessage(prov)
		return l, err
	}, `SELECT id, at, kind, attempt_id, account_id, model, billing, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
		usage_source, cost_e10, currency, cost_kind, price_version, provenance FROM ledger WHERE request_id = ? ORDER BY at`, id)
	if d.Ledger == nil {
		d.Ledger = []LedgerDTO{}
	}
	return &d, err
}

func summarize(ctx context.Context, q store.Execer, from, to time.Time) (*ActivitySummary, error) {
	s := &ActivitySummary{From: from, To: to, ByStatus: map[string]int64{}, ByHarness: map[string]int64{}, ByModel: map[string]int64{}, Costs: []CostDTO{}}
	f, t := from.UnixMilli(), to.UnixMilli()
	group := func(col string, into map[string]int64) error {
		rows, err := q.QueryContext(ctx, `SELECT COALESCE(`+col+`,'unknown'), count(*) FROM requests WHERE created_at BETWEEN ? AND ? GROUP BY 1`, f, t) //nolint:gosec // static column names
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int64
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			into[k] = n
			if col == "status" {
				s.Requests += n
			}
		}
		return rows.Err()
	}
	for col, m := range map[string]map[string]int64{"status": s.ByStatus, "harness": s.ByHarness, "requested_model": s.ByModel} {
		if err := group(col, m); err != nil {
			return nil, err
		}
	}
	var in, out, cr, cw, rs sql.NullInt64
	var ttft sql.NullFloat64
	if err := q.QueryRowContext(ctx, `SELECT SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens),
		AVG(ttft_ms), SUM(CASE WHEN attempt_count > 1 THEN 1 ELSE 0 END), SUM(CASE WHEN usage_source = 'unknown' THEN 1 ELSE 0 END)
		FROM requests WHERE created_at BETWEEN ? AND ?`, f, t).Scan(&in, &out, &cr, &cw, &rs, &ttft, new(sql.NullInt64), new(sql.NullInt64)); err != nil {
		return nil, err
	}
	s.Tokens = Tokens{Input: iptr(in), Output: iptr(out), CacheRead: iptr(cr), CacheWrite: iptr(cw), Reasoning: iptr(rs)}
	if ttft.Valid {
		v := ttft.Float64
		s.AvgTTFTms = &v
	}
	_ = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN attempt_count > 1 THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN usage_source = 'unknown' THEN 1 ELSE 0 END),0)
		FROM requests WHERE created_at BETWEEN ? AND ?`, f, t).Scan(&s.Fallbacks, &s.Unknown)
	rows, err := q.QueryContext(ctx, `SELECT currency, cost_kind, SUM(cost_e10) FROM ledger WHERE at BETWEEN ? AND ? AND cost_e10 IS NOT NULL GROUP BY 1, 2 ORDER BY 1, 2`, f, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cur, kind string
		var sum int64
		if err := rows.Scan(&cur, &kind, &sum); err != nil {
			return nil, err
		}
		s.Costs = append(s.Costs, CostDTO{Amount: money.Amount(sum).String(), Currency: strings.ToUpper(cur), Kind: kind})
	}
	return s, rows.Err()
}
