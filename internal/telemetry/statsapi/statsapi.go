// Package statsapi aggregates gateway activity by client, harness,
// project, plan, intent, model, account, provider, session or time period.
// Figures come from request metadata; unknown usage is counted, never
// assumed zero, and history occurrences are split into unique (known
// session) and uncertain (no session) counts.
package statsapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Deps are the API's dependencies.
type Deps struct {
	DB       func() *store.DB
	Snapshot func() *cp.Snapshot
	Clock    clock.Clock
}

// Latency summarizes a duration distribution in milliseconds.
type Latency struct {
	Count int64    `json:"count"`
	Avg   *float64 `json:"avg_ms"`
	P50   *int64   `json:"p50_ms"`
	P95   *int64   `json:"p95_ms"`
}

// Cost is an amount in one category and currency.
type Cost struct {
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// Group is one row of statistics.
type Group struct {
	Key               string  `json:"key"`
	Label             string  `json:"label"`
	Requests          int64   `json:"requests"`
	OK                int64   `json:"ok"`
	Errors            int64   `json:"errors"`
	Blocked           int64   `json:"blocked"`
	Attempts          int64   `json:"attempts"`
	Failovers         int64   `json:"failovers" doc:"Requests that needed more than one attempt."`
	Passthrough       int64   `json:"passthrough"`
	CompatFailures    int64   `json:"compat_failures" doc:"Attempts that failed on unparseable or incompatible upstream output."`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CacheReadTokens   int64   `json:"cache_read_tokens"`
	CacheWriteTokens  int64   `json:"cache_write_tokens"`
	ReasoningTokens   int64   `json:"reasoning_tokens"`
	UnknownUsage      int64   `json:"unknown_usage" doc:"Completed requests whose usage the upstream did not report."`
	Costs             []Cost  `json:"costs"`
	ToolsOffered      int64   `json:"tools_offered"`
	ToolCalls         int64   `json:"tool_calls" doc:"Calls the model generated (not proof the tool ran)."`
	ToolResultsUnique int64   `json:"tool_results_unique" doc:"Tool results first seen in this request's session."`
	ToolResultErrors  int64   `json:"tool_result_errors"`
	UniqueRequests    int64   `json:"unique_requests" doc:"Requests whose session is known (history deduplicated)."`
	UncertainRequests int64   `json:"uncertain_requests" doc:"Requests without a known session; occurrences may repeat."`
	NewMessages       int64   `json:"new_messages"`
	RepeatedMessages  int64   `json:"repeated_messages"`
	TTFT              Latency `json:"ttft"`
	Duration          Latency `json:"duration"`
	ClassifierRuns    int64   `json:"classifier_runs"`
	ClassifierTokens  int64   `json:"classifier_tokens"`
	ClassifierLatency Latency `json:"classifier_latency"`
	ClassifierCosts   []Cost  `json:"classifier_costs"`
	ttft, dur, cls    []int64
	costs, clsCosts   map[[2]string]money.Amount
}

// Stats is a statistics report.
type Stats struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	By     string    `json:"by"`
	Groups []Group   `json:"groups"`
	Totals Group     `json:"totals"`
	// Switches counts account switch and fallback transitions in the range.
	Switches  int64 `json:"account_switches"`
	Fallbacks int64 `json:"fallbacks"`
	// Unattributed counts ledger entries with no client binding (for
	// example live canaries).
	Unattributed int64 `json:"unattributed_ledger_entries"`
	Truncated    bool  `json:"truncated" doc:"More than 200,000 requests matched; narrow the range."`
}

var dims = map[string]string{
	"client": "binding_id", "harness": "harness", "project": "project_id", "plan": "plan_id", "intent": "intent",
	"model": "final_model", "account": "final_account_id", "provider": "final_provider_id", "session": "session_id", "period": "",
}

func label(s *cp.Snapshot, by, key string) string {
	if s == nil || key == "" {
		if key == "" {
			return "(none)"
		}
		return key
	}
	switch by {
	case "client":
		if v := s.Bindings[key]; v != nil {
			return v.Name
		}
	case "project":
		if v := s.Projects[key]; v != nil {
			return v.Name
		}
	case "plan":
		if v := s.Plans[key]; v != nil {
			return v.Name
		}
	case "account":
		if v := s.Accounts[key]; v != nil {
			return v.Name
		}
	case "provider":
		if v := s.Providers[key]; v != nil {
			return v.Name
		}
	}
	return key
}

func summarize(v []int64) Latency {
	l := Latency{Count: int64(len(v))}
	if len(v) == 0 {
		return l
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	var sum int64
	for _, x := range v {
		sum += x
	}
	avg := float64(sum) / float64(len(v))
	p50, p95 := v[(len(v)-1)*50/100], v[(len(v)-1)*95/100]
	l.Avg, l.P50, l.P95 = &avg, &p50, &p95
	return l
}

func costList(m map[[2]string]money.Amount) []Cost {
	out := []Cost{}
	for k, v := range m {
		out = append(out, Cost{Kind: k[0], Currency: k[1], Amount: v.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind+out[i].Currency < out[j].Kind+out[j].Currency })
	return out
}

func (g *Group) finish() {
	g.TTFT, g.Duration, g.ClassifierLatency = summarize(g.ttft), summarize(g.dur), summarize(g.cls)
	g.Costs, g.ClassifierCosts = costList(g.costs), costList(g.clsCosts)
}

func newGroup(key, lbl string) *Group {
	return &Group{Key: key, Label: lbl, costs: map[[2]string]money.Amount{}, clsCosts: map[[2]string]money.Amount{}}
}

const maxRows = 200_000

// Compute builds statistics.
func Compute(ctx context.Context, q store.Execer, snap *cp.Snapshot, by string, from, to time.Time, step time.Duration) (*Stats, error) {
	col, ok := dims[by]
	if !ok {
		return nil, api.Invalid("unknown dimension", api.FieldError{Location: "query.by", Message: "must be client, harness, project, plan, intent, model, account, provider, session or period"})
	}
	type row struct {
		id, key, status, mode, occ, usageSrc, costKind, cur string
		created                                             int64
		attempts, in, out, cr, cw, rs, cost                 sql.NullInt64
		ttft, dur                                           sql.NullInt64
		offered, calls, uniq, terr, newM, rep               int64
		detail                                              string
	}
	keyExpr := "COALESCE(" + col + ", '')"
	if by == "period" {
		keyExpr = "''"
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (row, error) {
		var x row
		var mode, usage, ck, cur sql.NullString
		err := r.Scan(&x.id, &x.key, &x.status, &mode, &x.occ, &usage, &ck, &cur, &x.created, &x.attempts, &x.in, &x.out, &x.cr, &x.cw, &x.rs, &x.cost,
			&x.ttft, &x.dur, &x.offered, &x.calls, &x.uniq, &x.terr, &x.newM, &x.rep, &x.detail)
		x.mode, x.usageSrc, x.costKind, x.cur = mode.String, usage.String, ck.String, cur.String
		return x, err
	}, `SELECT id, `+keyExpr+`, status, mode, occurrence, usage_source, cost_kind, currency, created_at, attempt_count, input_tokens, output_tokens,
		cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_e10, ttft_ms, duration_ms, tools_offered, tool_calls, unique_tool_results,
		tool_result_errors, new_messages, repeated_messages, intent_detail
		FROM requests WHERE created_at >= ? AND created_at < ? ORDER BY created_at LIMIT ?`, from.UnixMilli(), to.UnixMilli(), maxRows+1) //nolint:gosec // column from a static map
	if err != nil {
		return nil, err
	}
	st := &Stats{From: from, To: to, By: by, Groups: []Group{}}
	if len(rows) > maxRows {
		rows, st.Truncated = rows[:maxRows], true
	}
	// Per-request compatibility failures and classifier usage.
	compat := map[string]int64{}
	cf, err := store.Query(ctx, q, func(r *sql.Rows) (struct {
		id string
		n  int64
	}, error) {
		var x struct {
			id string
			n  int64
		}
		err := r.Scan(&x.id, &x.n)
		return x, err
	}, `SELECT request_id, COUNT(*) FROM attempts WHERE error_kind = 'protocol' AND started_at >= ? AND started_at < ? GROUP BY request_id`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	for _, x := range cf {
		compat[x.id] = x.n
	}
	type cls struct {
		tokens int64
		cost   sql.NullInt64
		cur    sql.NullString
		kind   string
	}
	classifier := map[string]cls{}
	cl, err := store.Query(ctx, q, func(r *sql.Rows) (struct {
		id string
		c  cls
	}, error) {
		var x struct {
			id string
			c  cls
		}
		err := r.Scan(&x.id, &x.c.tokens, &x.c.cost, &x.c.cur, &x.c.kind)
		return x, err
	}, `SELECT request_id, COALESCE(input_tokens,0) + COALESCE(output_tokens,0), cost_e10, currency, cost_kind FROM ledger
		WHERE idempotency_key LIKE 'classifier:%' AND at >= ? AND at < ?`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	for _, x := range cl {
		classifier[x.id] = x.c
	}
	groups := map[string]*Group{}
	total := newGroup("total", "Total")
	for _, r := range rows {
		key := r.key
		if by == "period" {
			ms := step.Milliseconds()
			key = time.UnixMilli(r.created / ms * ms).UTC().Format(time.RFC3339)
		}
		g := groups[key]
		if g == nil {
			lbl := key
			if by != "period" {
				lbl = label(snap, by, key)
			}
			g = newGroup(key, lbl)
			groups[key] = g
		}
		for _, x := range []*Group{g, total} {
			x.Requests++
			switch r.status {
			case "ok":
				x.OK++
			case "blocked":
				x.Blocked++
			case "error", "canceled":
				x.Errors++
			}
			x.Attempts += r.attempts.Int64
			if r.attempts.Int64 > 1 {
				x.Failovers++
			}
			if r.mode == "passthrough" {
				x.Passthrough++
			}
			x.CompatFailures += compat[r.id]
			x.InputTokens += r.in.Int64
			x.OutputTokens += r.out.Int64
			x.CacheReadTokens += r.cr.Int64
			x.CacheWriteTokens += r.cw.Int64
			x.ReasoningTokens += r.rs.Int64
			if r.status == "ok" && (r.usageSrc == "unknown" || r.usageSrc == "" || (!r.in.Valid && !r.out.Valid)) {
				x.UnknownUsage++
			}
			if r.cost.Valid && r.costKind != "" && r.costKind != "none" {
				x.costs[[2]string{r.costKind, r.cur}] += money.Amount(r.cost.Int64)
			}
			x.ToolsOffered += r.offered
			x.ToolCalls += r.calls
			x.ToolResultsUnique += r.uniq
			x.ToolResultErrors += r.terr
			if r.occ == "unique" {
				x.UniqueRequests++
			} else {
				x.UncertainRequests++
			}
			x.NewMessages += r.newM
			x.RepeatedMessages += r.rep
			if r.ttft.Valid {
				x.ttft = append(x.ttft, r.ttft.Int64)
			}
			if r.dur.Valid {
				x.dur = append(x.dur, r.dur.Int64)
			}
			if c, ok := classifier[r.id]; ok {
				x.ClassifierRuns++
				x.ClassifierTokens += c.tokens
				if c.cost.Valid && c.kind != "none" {
					x.clsCosts[[2]string{c.kind, c.cur.String}] += money.Amount(c.cost.Int64)
				}
			}
			var d struct {
				LatencyMs *int64 `json:"latency_ms"`
				Version   string `json:"version"`
			}
			if json.Unmarshal([]byte(r.detail), &d) == nil && d.LatencyMs != nil && d.Version != "" {
				x.cls = append(x.cls, *d.LatencyMs)
				if _, counted := classifier[r.id]; !counted {
					x.ClassifierRuns++
				}
			}
		}
	}
	for _, g := range groups {
		g.finish()
		st.Groups = append(st.Groups, *g)
	}
	total.finish()
	st.Totals = *total
	sort.Slice(st.Groups, func(i, j int) bool {
		if by == "period" {
			return st.Groups[i].Key < st.Groups[j].Key
		}
		if st.Groups[i].Requests != st.Groups[j].Requests {
			return st.Groups[i].Requests > st.Groups[j].Requests
		}
		return st.Groups[i].Key < st.Groups[j].Key
	})
	_ = q.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM event_outbox WHERE type = 'harborpilot.routing.account_switched.v1' AND occurred_at >= ? AND occurred_at < ?),
		(SELECT COUNT(*) FROM event_outbox WHERE type = 'harborpilot.routing.fallback_selected.v1' AND occurred_at >= ? AND occurred_at < ?),
		(SELECT COUNT(*) FROM ledger WHERE binding_id IS NULL AND at >= ? AND at < ?)`,
		from.UnixMilli(), to.UnixMilli(), from.UnixMilli(), to.UnixMilli(), from.UnixMilli(), to.UnixMilli()).Scan(&st.Switches, &st.Fallbacks, &st.Unattributed)
	return st, nil
}

// Register mounts GET /stats.
func Register(a *api.API, d Deps) {
	type in struct {
		By   string `query:"by" enum:"client,harness,project,plan,intent,model,account,provider,session,period," required:"false"`
		From string `query:"from" doc:"RFC 3339; default 7 days before to."`
		To   string `query:"to" doc:"RFC 3339; default now."`
		Step string `query:"step" enum:"hour,day," required:"false" doc:"Bucket size for by=period."`
	}
	type out struct{ Body *Stats }
	api.Register(a, api.Op{ID: "activity-stats", Method: http.MethodGet, Path: "/stats", Summary: "Activity statistics by dimension",
		Tags: []string{"Activity"}, Permission: auth.PermActivityRead}, func(ctx context.Context, i *in) (*out, error) {
		now := d.Clock.Now().UTC()
		to := now
		var from time.Time
		var err error
		if i.To != "" {
			if to, err = time.Parse(time.RFC3339, i.To); err != nil {
				return nil, api.Invalid("invalid to", api.FieldError{Location: "query.to", Message: "must be RFC 3339"})
			}
		}
		if i.From != "" {
			if from, err = time.Parse(time.RFC3339, i.From); err != nil {
				return nil, api.Invalid("invalid from", api.FieldError{Location: "query.from", Message: "must be RFC 3339"})
			}
		} else {
			from = to.Add(-7 * 24 * time.Hour)
		}
		if !to.After(from) || to.Sub(from) > 400*24*time.Hour {
			return nil, api.Invalid("invalid range", api.FieldError{Location: "query.from", Message: "range must be positive and at most 400 days"})
		}
		by := i.By
		if by == "" {
			by = "client"
		}
		step := time.Hour
		if i.Step == "day" {
			step = 24 * time.Hour
		}
		s, err := Compute(ctx, d.DB().R, d.Snapshot(), by, from, to, step)
		if err != nil {
			return nil, err
		}
		return &out{Body: s}, nil
	})
}
