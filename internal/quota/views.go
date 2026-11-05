package quota

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/analytics"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/forecasting"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/polling"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// AdapterView describes a quota adapter.
type AdapterView struct {
	ID                  string                                      `json:"id"`
	DisplayName         string                                      `json:"display_name"`
	Family              string                                      `json:"family"`
	Version             string                                      `json:"adapter_version"`
	Tier                string                                      `json:"tier" enum:"official,unofficial,client_reported,demo"`
	CredentialTypes     []string                                    `json:"credential_types"`
	Capabilities        map[domain.Capability]domain.CapabilityInfo `json:"capabilities"`
	Limitations         []string                                    `json:"limitations"`
	DocsURLs            []string                                    `json:"docs_urls"`
	DefaultPollInterval int64                                       `json:"default_poll_interval_secs"`
	PushOnly            bool                                        `json:"push_only"`
	RequiresTermsAck    bool                                        `json:"requires_terms_ack"`
}

// Adapters lists the registered adapters.
func (s *Service) Adapters() []AdapterView {
	out := []AdapterView{}
	for _, a := range s.reg.All() {
		i := a.Info()
		out = append(out, AdapterView{ID: string(i.ID), DisplayName: i.DisplayName, Family: i.Family, Version: i.AdapterVersion, Tier: string(i.Tier),
			CredentialTypes: nonNil(i.CredentialTypes), Capabilities: i.Capabilities, Limitations: nonNil(i.Limitations), DocsURLs: nonNil(i.DocsURLs),
			DefaultPollInterval: int64(i.DefaultPollInterval / time.Second), PushOnly: i.PushOnly, RequiresTermsAck: i.Tier == domain.TierUnofficial})
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// PollView is a monitor's polling state.
type PollView struct {
	LastAttemptAt       *time.Time `json:"last_attempt_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	NextAttemptAt       *time.Time `json:"next_attempt_at"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	AuthState           string     `json:"auth_state" enum:"unknown,ok,failed,permission_denied"`
	CooldownUntil       *time.Time `json:"cooldown_until"`
	ErrorKind           string     `json:"error_kind"`
	ErrorMessage        string     `json:"error_message"`
	LatencyMs           *int64     `json:"latency_ms"`
}

// MonitorView is one quota monitor with its latest buckets.
type MonitorView struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Adapter          string       `json:"adapter"`
	AdapterName      string       `json:"adapter_name"`
	Tier             string       `json:"tier"`
	AccountID        string       `json:"account_id"`
	Enabled          bool         `json:"enabled"`
	Health           string       `json:"health" enum:"healthy,degraded,auth_failed,stale,error,disabled,pending,unsupported"`
	MaskedIdentifier *string      `json:"masked_identifier"`
	Poll             PollView     `json:"poll"`
	Buckets          []BucketView `json:"buckets"`
}

// AccountQuotaView is quota state derived from gateway responses for a
// provider account, plus its combined routing health.
type AccountQuotaView struct {
	AccountID string       `json:"account_id"`
	Buckets   []BucketView `json:"buckets"`
}

// Board returns every monitor and every account with response-derived
// observations.
func (s *Service) Board() ([]MonitorView, []AccountQuotaView) {
	now := s.o.Clock.Now()
	snap := s.o.CP.Snapshot()
	mons := []MonitorView{}
	if snap != nil {
		for _, m := range snap.Monitors {
			v := MonitorView{ID: m.ID, Name: m.Name, Adapter: m.Spec.Adapter, AccountID: m.Spec.AccountID, Enabled: m.Spec.Enabled,
				Buckets: s.board.buckets(m.ID, now)}
			var info domain.ProviderInfo
			if a, ok := s.reg.Get(domain.ProviderID(m.Spec.Adapter)); ok {
				info = a.Info()
				v.AdapterName, v.Tier = info.DisplayName, string(info.Tier)
			}
			p, havePoll := s.board.poll(m.ID)
			if havePoll {
				st := p.state
				v.MaskedIdentifier = p.identity
				v.Poll = PollView{LastAttemptAt: st.LastAttemptAt, LastSuccessAt: st.LastSuccessAt, NextAttemptAt: st.NextAttemptAt,
					ConsecutiveFailures: st.ConsecutiveFailures, AuthState: string(st.AuthState), CooldownUntil: st.CooldownUntil, LatencyMs: st.LastLatencyMS}
				if st.LastError != nil {
					v.Poll.ErrorKind, v.Poll.ErrorMessage = string(st.LastError.Kind), st.LastError.Message
				}
			}
			if v.Poll.AuthState == "" {
				v.Poll.AuthState = string(polling.AuthUnknown)
			}
			if next, ok := s.sched.NextAttempt(domain.AccountID(m.ID)); ok {
				v.Poll.NextAttemptAt = &next
			}
			v.Health = monitorHealth(m, info, p, havePoll, s.board.link(m.ID).staleAfter, now)
			mons = append(mons, v)
		}
	}
	sort.Slice(mons, func(i, j int) bool { return mons[i].Name < mons[j].Name })
	accs := []AccountQuotaView{}
	s.board.mu.RLock()
	var owners []string
	for owner, bs := range s.board.owners {
		for _, b := range bs {
			if b.source == sourceHeaders {
				owners = append(owners, owner)
			}
			break
		}
	}
	s.board.mu.RUnlock()
	sort.Strings(owners)
	for _, o := range owners {
		accs = append(accs, AccountQuotaView{AccountID: o, Buckets: s.board.buckets(o, now)})
	}
	return mons, accs
}

func monitorHealth(m *cp.Res[cp.QuotaMonitorSpec], info domain.ProviderInfo, p pollInfo, havePoll bool, staleAfter time.Duration, now time.Time) string {
	switch {
	case !m.Spec.Enabled:
		return string(domain.HealthDisabled)
	case info.ID == "":
		return string(domain.HealthUnsupported)
	case info.PushOnly:
		return string(domain.HealthHealthy)
	case !havePoll || p.state.LastAttemptAt == nil:
		return string(domain.HealthPending)
	case p.state.AuthState == polling.AuthFailed || p.state.AuthState == polling.AuthDenied:
		return string(domain.HealthAuthFailed)
	}
	if p.state.LastSuccessAt == nil {
		return string(domain.HealthError)
	}
	if staleAfter > 0 && now.Sub(*p.state.LastSuccessAt) > staleAfter {
		return string(domain.HealthStale)
	}
	if p.state.LastError != nil {
		return string(domain.HealthDegraded)
	}
	return string(domain.HealthHealthy)
}

// HistoryPoint is one observation of a bucket.
type HistoryPoint struct {
	T         time.Time  `json:"t"`
	Limit     *float64   `json:"limit"`
	Used      *float64   `json:"used"`
	Remaining *float64   `json:"remaining"`
	ResetAt   *time.Time `json:"reset_at"`
}

// ErrUnknownBucket means the owner has no such bucket.
var ErrUnknownBucket = errors.New("unknown quota bucket")

// History returns observations of a bucket since a time, oldest first.
func (s *Service) History(ctx context.Context, owner, bucket string, since time.Time) ([]HistoryPoint, error) {
	return store.Query(ctx, s.o.DB.R, func(r *sql.Rows) (HistoryPoint, error) {
		var p HistoryPoint
		var at int64
		var lim, used, rem sql.NullFloat64
		var reset sql.NullInt64
		err := r.Scan(&at, &lim, &used, &rem, &reset)
		p.T = time.UnixMilli(at).UTC()
		p.Limit, p.Used, p.Remaining, p.ResetAt = nf(lim), nf(used), nf(rem), tptr(reset)
		return p, err
	}, `SELECT observed_at, limit_value, used_value, remaining_value, reset_at FROM quota_observations
		WHERE owner_key = ? AND bucket_key = ? AND observed_at >= ? ORDER BY observed_at LIMIT 20000`, owner, bucket, since.UnixMilli())
}

// Forecast projects a bucket's remaining capacity from its history.
func (s *Service) Forecast(ctx context.Context, owner, bucket string) (forecasting.Result, error) {
	now := s.o.Clock.Now()
	s.board.mu.RLock()
	st, ok := s.board.owners[owner][bucket]
	staleAfter := s.board.staleAfter(st)
	s.board.mu.RUnlock()
	if !ok {
		return forecasting.Result{}, ErrUnknownBucket
	}
	hist, err := s.History(ctx, owner, bucket, now.Add(-14*24*time.Hour))
	if err != nil {
		return forecasting.Result{}, err
	}
	pts := make([]analytics.Point, 0, len(hist))
	for _, h := range hist {
		pts = append(pts, analytics.Point{T: h.T, Remaining: h.Remaining, Used: h.Used, Limit: h.Limit, ResetAt: h.ResetAt})
	}
	var rate *forecasting.Rate
	burn := analytics.ComputeBurn(pts, st.obs.Spec, now, analytics.BurnOptions{Horizon: 6 * time.Hour})
	if burn.Rate != nil {
		rate = &forecasting.Rate{PerHour: *burn.Rate, Unit: burn.Unit, Source: "average depletion over the last 6 hours (" + burn.Method + ")", Confidence: burn.Confidence}
	}
	return forecasting.Forecast(st.obs.Spec, pts, now, rate, forecasting.Options{StaleAfter: staleAfter}), nil
}

// ExternalUsageRow is provider-reported usage (never summed across
// breakdowns).
type ExternalUsageRow struct {
	MonitorID     string    `json:"monitor_id"`
	Breakdown     string    `json:"breakdown"`
	Dimensions    string    `json:"dimensions" doc:"JSON object of the breakdown's dimension values."`
	IntervalStart time.Time `json:"interval_start"`
	IntervalEnd   time.Time `json:"interval_end"`
	Requests      *int64    `json:"requests"`
	InputTokens   *int64    `json:"input_tokens"`
	OutputTokens  *int64    `json:"output_tokens"`
	ActualCost    *string   `json:"actual_cost" doc:"Provider-reported charge (actual_api_charge)."`
	EstimatedCost *string   `json:"estimated_cost"`
	Currency      string    `json:"currency"`
	Partial       bool      `json:"partial"`
	Quality       string    `json:"quality"`
}

// ExternalUsage lists provider-reported usage rows in a time range.
func (s *Service) ExternalUsage(ctx context.Context, monitorID, breakdown string, from, to time.Time) ([]ExternalUsageRow, error) {
	q := `SELECT monitor_id, breakdown, dimensions, interval_start, interval_end, requests, input_tokens, output_tokens, actual_cost_e10,
		estimated_cost_e10, currency, partial, quality FROM external_usage WHERE interval_start >= ? AND interval_start < ?`
	args := []any{from.UnixMilli(), to.UnixMilli()}
	if monitorID != "" {
		q += ` AND monitor_id = ?`
		args = append(args, monitorID)
	}
	if breakdown != "" {
		q += ` AND breakdown = ?`
		args = append(args, breakdown)
	}
	q += ` ORDER BY interval_start, monitor_id LIMIT 5000`
	return store.Query(ctx, s.o.DB.R, func(r *sql.Rows) (ExternalUsageRow, error) {
		var x ExternalUsageRow
		var st, en int64
		var req, in, out, act, est sql.NullInt64
		var cur sql.NullString
		var partial int
		err := r.Scan(&x.MonitorID, &x.Breakdown, &x.Dimensions, &st, &en, &req, &in, &out, &act, &est, &cur, &partial, &x.Quality)
		x.IntervalStart, x.IntervalEnd = time.UnixMilli(st).UTC(), time.UnixMilli(en).UTC()
		x.Requests, x.InputTokens, x.OutputTokens = ip(req), ip(in), ip(out)
		x.ActualCost, x.EstimatedCost = amountStr(na(act)), amountStr(na(est))
		x.Currency, x.Partial = cur.String, partial == 1
		return x, err
	}, q, args...)
}

func ip(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

// ReconciliationRow compares provider-reported usage with what the gateway
// observed for the linked account on one UTC day. Differences are usage
// that bypassed the gateway, reporting lag, or pricing differences; they
// are reported, never silently merged.
type ReconciliationRow struct {
	MonitorID         string    `json:"monitor_id"`
	AccountID         string    `json:"account_id"`
	Day               time.Time `json:"day"`
	ProviderInput     *int64    `json:"provider_input_tokens"`
	ProviderOutput    *int64    `json:"provider_output_tokens"`
	ProviderRequests  *int64    `json:"provider_requests"`
	ProviderActual    *string   `json:"provider_actual_cost" doc:"actual_api_charge reported by the provider."`
	GatewayInput      int64     `json:"gateway_input_tokens"`
	GatewayOutput     int64     `json:"gateway_output_tokens"`
	GatewayAttempts   int64     `json:"gateway_attempts"`
	GatewayEstimated  *string   `json:"gateway_estimated_cost" doc:"estimated_api_charge from configured list prices."`
	Currency          string    `json:"currency"`
	UnattributedInput *int64    `json:"unattributed_input_tokens" doc:"Provider input tokens not seen by the gateway (null when the provider does not report tokens)."`
	Partial           bool      `json:"partial"`
}

// Reconciliation reports daily provider vs gateway usage for monitors
// linked to provider accounts.
func (s *Service) Reconciliation(ctx context.Context, days int) ([]ReconciliationRow, error) {
	snap := s.o.CP.Snapshot()
	now := s.o.Clock.Now().UTC()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days+1)
	out := []ReconciliationRow{}
	if snap == nil {
		return out, nil
	}
	const dayMs = int64(24 * time.Hour / time.Millisecond)
	for _, id := range sortedKeys(snap.Monitors) {
		m := snap.Monitors[id]
		if m.Spec.AccountID == "" {
			continue
		}
		type prow struct {
			day               int64
			in, out, req, act sql.NullInt64
			cur               sql.NullString
			partial           int
		}
		prov, err := store.Query(ctx, s.o.DB.R, func(r *sql.Rows) (prow, error) {
			var x prow
			err := r.Scan(&x.day, &x.in, &x.out, &x.req, &x.act, &x.cur, &x.partial)
			return x, err
		}, `SELECT (interval_start / ?) * ?, SUM(input_tokens), SUM(output_tokens), SUM(requests), SUM(actual_cost_e10), MAX(currency), MAX(partial)
			FROM external_usage WHERE monitor_id = ? AND breakdown = 'total' AND interval_start >= ? GROUP BY 1`, dayMs, dayMs, m.ID, from.UnixMilli())
		if err != nil {
			return nil, err
		}
		if len(prov) == 0 {
			continue
		}
		type grow struct {
			day, in, out, n, cost int64
			cur                   sql.NullString
		}
		gw, err := store.Query(ctx, s.o.DB.R, func(r *sql.Rows) (grow, error) {
			var x grow
			err := r.Scan(&x.day, &x.in, &x.out, &x.n, &x.cost, &x.cur)
			return x, err
		}, `SELECT (at / ?) * ?, COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COUNT(*),
			COALESCE(SUM(CASE WHEN cost_kind = 'estimated_api_charge' THEN cost_e10 END),0), MAX(currency)
			FROM ledger WHERE account_id = ? AND at >= ? AND kind IN ('usage','unknown_usage') GROUP BY 1`, dayMs, dayMs, m.Spec.AccountID, from.UnixMilli())
		if err != nil {
			return nil, err
		}
		gByDay := map[int64]grow{}
		for _, g := range gw {
			gByDay[g.day] = g
		}
		for _, p := range prov {
			g := gByDay[p.day]
			row := ReconciliationRow{MonitorID: m.ID, AccountID: m.Spec.AccountID, Day: time.UnixMilli(p.day).UTC(),
				ProviderInput: ip(p.in), ProviderOutput: ip(p.out), ProviderRequests: ip(p.req), ProviderActual: amountStr(na(p.act)),
				GatewayInput: g.in, GatewayOutput: g.out, GatewayAttempts: g.n, Currency: p.cur.String, Partial: p.partial == 1}
			if g.cost != 0 {
				a := money.Amount(g.cost)
				row.GatewayEstimated = amountStr(&a)
			}
			if p.in.Valid {
				d := p.in.Int64 - g.in
				row.UnattributedInput = &d
			}
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.After(out[j].Day)
		}
		return out[i].MonitorID < out[j].MonitorID
	})
	return out, nil
}
