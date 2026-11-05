package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry"
)

// attempt runs one candidate. It returns delivered=true once a response
// (success or in-band failure after output began) has been written.
func (g *Gateway) attempt(ctx context.Context, w http.ResponseWriter, rc *reqCtx, cand *routing.Candidate, attID string, n int, step, rsvID string) (bool, *executor.AttemptError) {
	rc.started++
	snap := rc.snap
	acc := snap.Accounts[cand.AccountID]
	prov := snap.Providers[cand.ProviderID]
	started := g.Clock.Now()
	ar := telemetry.AttemptRecord{ID: attID, RequestID: rc.id, N: n, StartedAt: started, AccountID: acc.ID, ProviderID: prov.ID, Model: cand.Model,
		UpstreamProtocol: string(cand.Upstream), Status: "in_progress"}
	if step != "primary" {
		ar.FailoverReason = step
	}
	g.Tel.Attempt(ar)

	fail := func(ae *executor.AttemptError) (bool, *executor.AttemptError) {
		now := g.Clock.Now()
		d := now.Sub(started).Milliseconds()
		ar.FinishedAt, ar.DurationMs = &now, &d
		ar.Status, ar.ErrorKind, ar.ErrorMessage, ar.Executed, ar.HTTPStatus = "error", string(ae.Err.Kind), truncate(ae.Err.Message, 300), ae.Executed, ae.Status
		ar.RetryAfterS = ae.Err.RetryAfterSeconds
		if ae.Err.Kind == ir.ErrCanceled {
			ar.Status = "canceled"
		}
		g.Tel.Attempt(ar)
		if ae.Executed {
			_ = g.Acct.Settle(ctx, rsvID, "committed", "ambiguous execution")
			_, _ = g.Acct.Record(context.WithoutCancel(ctx), accounting.Entry{Kind: "unknown_usage", RequestID: rc.id, AttemptID: attID,
				SessionID: rc.session.ID, WorkspaceID: rc.workspace, ProjectID: rc.project, RunRef: runRef(rc.refs), BindingID: rc.binding.ID,
				AccountID: acc.ID, ProviderID: prov.ID, Model: cand.Model, Billing: acc.Spec.Billing, UsageSource: "unknown",
				IdempotencyKey: "usage:" + attID, Provenance: map[string]any{"reason": string(ae.Err.Kind)}})
		} else {
			_ = g.Acct.Settle(ctx, rsvID, "released", string(ae.Err.Kind))
		}
		if ae.AccountScoped {
			wait := time.Duration(ae.Err.RetryAfterSeconds) * time.Second
			switch {
			case wait > 0:
			case ae.Err.Kind == ir.ErrAuthentication || ae.Err.Kind == ir.ErrPermission:
				wait = 5 * time.Minute
			default:
				wait = 30 * time.Second
			}
			g.cooldown(acc.ID, g.Clock.Now().Add(min(wait, time.Hour)))
		}
		if g.Metrics != nil {
			g.Metrics.GatewayAttempts.WithLabelValues(string(cand.Upstream), string(ae.Err.Kind)).Inc()
		}
		return false, ae
	}

	var secret credentials.Secret
	if pt := cp.ProviderTypes[prov.Spec.Type]; pt.CredentialKind != "none" {
		s, err := g.CP.ResolveSecret(ctx, cp.AccountDef, acc.ID, "credential")
		if err != nil {
			return fail(&executor.AttemptError{Err: &ir.Error{Kind: ir.ErrAuthentication, Message: "account " + acc.Name + " has no usable credential"}, AccountScoped: true})
		}
		secret = s
	}
	target := executor.Target{Provider: prov, Account: acc, Model: cand.Model, ModelSpec: cand.ModelSpec}
	ereq := executor.Request{IR: rc.ir, Inbound: rc.inbound, RawBody: rc.raw, RequestID: rc.id, ClientHeader: rc.header}
	res, aerr := g.executorFor(prov.Spec.Type).Execute(ctx, target, secret, ereq, rc.ir.Stream)
	if g.ObserveResponse != nil {
		if aerr != nil {
			g.ObserveResponse(acc.ID, prov.Spec.Type, cand.Model, aerr.Header)
		} else {
			g.ObserveResponse(acc.ID, prov.Spec.Type, cand.Model, res.Header)
		}
	}
	if aerr != nil {
		return fail(aerr)
	}
	ar.Mode = string(res.Mode)
	if g.Observe != nil && len(res.Losses) > 0 {
		g.Observe(Observation{At: started, Direction: "request", Protocol: rc.inbound, ProviderID: prov.ID, Mode: res.Mode, Losses: res.Losses,
			Harness: rc.harness.Name, HarnessVer: rc.harness.Version})
	}
	fi := &finalInfo{cand: *cand, mode: res.Mode, losses: res.Losses}
	if res.Response != nil {
		return g.deliverComplete(ctx, w, rc, res, fi, &ar, started, rsvID)
	}
	return g.deliverStream(ctx, w, rc, res, fi, &ar, started, rsvID, fail)
}

func runRef(refs map[string]string) string {
	for _, k := range []string{"run", "spaceman.run", "ci.job"} {
		if v := refs[k]; v != "" {
			return v
		}
	}
	return ""
}

// settleUsage commits the reservation and writes the ledger entry.
func (g *Gateway) settleUsage(ctx context.Context, rc *reqCtx, fi *finalInfo, attID, rsvID string, complete bool) {
	acc := rc.snap.Accounts[fi.cand.AccountID]
	fi.usageSource = "unknown"
	kind := "unknown_usage"
	if fi.usage.Known() {
		fi.usageSource = "upstream_reported"
		kind = "usage"
	}
	var cost *accounting.Cost
	if c, ok := accounting.Estimate(fi.cand.ModelSpec, acc.Spec.Billing, fi.usage); ok {
		cost = c
	}
	fi.cost = cost
	e := accounting.Entry{Kind: kind, RequestID: rc.id, AttemptID: attID, SessionID: rc.session.ID, WorkspaceID: rc.workspace, ProjectID: rc.project,
		RunRef: runRef(rc.refs), BindingID: rc.binding.ID, AccountID: acc.ID, ProviderID: fi.cand.ProviderID, Model: fi.cand.Model,
		Billing: acc.Spec.Billing, Usage: fi.usage, UsageSource: fi.usageSource, IdempotencyKey: "usage:" + attID,
		Provenance: map[string]any{"complete": complete, "mode": string(fi.mode)}}
	if cost != nil {
		a := cost.Amount
		e.Cost, e.Currency, e.CostKind, e.PriceVersion = &a, cost.Currency, cost.Kind, cost.PriceVersion
	}
	bg := context.WithoutCancel(ctx)
	if _, err := g.Acct.Record(bg, e); err != nil {
		g.Log.Error("ledger.record.failed", "request_id", rc.id, "error", err)
	}
	_ = g.Acct.Settle(bg, rsvID, "committed", "completed")
	if p := rc.snap.Pools[rc.outcome.Decision.PoolID]; p != nil {
		routing.RecordAffinity(p.ID, rc.session.Key, fi.cand.AccountID, fi.cand.Model)
	}
}

func (g *Gateway) deliverComplete(ctx context.Context, w http.ResponseWriter, rc *reqCtx, res *executor.Result, fi *finalInfo, ar *telemetry.AttemptRecord, started time.Time, rsvID string) (bool, *executor.AttemptError) {
	r := res.Response
	now := g.Clock.Now()
	ttft := now.Sub(started).Milliseconds()
	fi.ttft, fi.stop, fi.usage = &ttft, r.StopReason, r.Usage
	for _, p := range r.Parts {
		if p.Kind == ir.PartToolCall {
			fi.toolCalls++
		}
	}
	body := protocol.EncodeResponse(rc.inbound, r, rc.ir.Model, rc.start)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	n, _ := w.Write(body)
	fi.bytesOut = int64(n)
	g.settleUsage(ctx, rc, fi, ar.ID, rsvID, true)
	d := now.Sub(started).Milliseconds()
	ar.FinishedAt, ar.DurationMs, ar.TTFTms, ar.Status = &now, &d, &ttft, "ok"
	ar.InputTokens, ar.OutputTokens = fi.usage.UncachedInput(), fi.usage.OutputTokens
	g.Tel.Attempt(*ar)
	g.finish(ctx, rc, "ok", nil, fi)
	return true, nil
}

func (g *Gateway) deliverStream(ctx context.Context, w http.ResponseWriter, rc *reqCtx, res *executor.Result, fi *finalInfo, ar *telemetry.AttemptRecord,
	started time.Time, rsvID string, fail func(*executor.AttemptError) (bool, *executor.AttemptError)) (bool, *executor.AttemptError) {
	defer res.Cancel()
	sw := wire.NewSSEWriter(w)
	headersSent := false
	var cs protocol.ClientStream
	sendHeaders := func() {
		if !headersSent {
			headersSent = true
			sw.Header(http.StatusOK)
		}
	}
	semantic := false
	commit := time.NewTimer(g.Limits.HeaderCommit)
	defer commit.Stop()
	keep := time.NewTicker(g.Limits.Keepalive)
	defer keep.Stop()
	var buffered []ir.Event // events before the first semantic event (start/usage)
	var stop ir.StopReason
	sawStop := false
	var streamErr *ir.Error
	write := func(fn func() error) bool {
		_ = sw.SetWriteDeadline(time.Now().Add(g.Limits.SlowClientWrite))
		if err := fn(); err != nil {
			streamErr = &ir.Error{Kind: ir.ErrCanceled, Message: "client connection failed"}
			return false
		}
		return true
	}
	startClient := func() {
		sendHeaders()
		cs = protocol.NewClientStream(rc.inbound, sw, protocol.StreamMeta{ResponseID: rc.id, Model: rc.ir.Model, Created: rc.start,
			IncludeUsage: includeUsage(rc.ir), UpstreamFamily: res.Upstream.Family()})
	}
loop:
	for {
		select {
		case <-ctx.Done():
			streamErr = &ir.Error{Kind: ir.ErrCanceled, Message: "client canceled the request"}
			break loop
		case <-commit.C:
			if !semantic && !headersSent {
				sendHeaders()
				if !write(func() error { return sw.Comment("waiting for upstream") }) {
					break loop
				}
			}
		case <-keep.C:
			if !headersSent {
				continue
			}
			if cs != nil {
				if !write(cs.Keepalive) {
					break loop
				}
			} else if !write(func() error { return sw.Comment("keepalive") }) {
				break loop
			}
		case ev, ok := <-res.Stream:
			if !ok {
				break loop
			}
			keep.Reset(g.Limits.Keepalive)
			switch ev.Kind {
			case ir.EvUsage:
				fi.usage.Merge(*ev.Usage)
			case ir.EvStop:
				stop, sawStop = ev.StopReason, true
			}
			if !semantic {
				if ev.Kind == ir.EvError {
					// Upstream failed before producing output: nothing reached
					// the client, so another candidate may be tried.
					if !headersSent {
						ae := &executor.AttemptError{Err: ev.Err, AccountScoped: ev.Err.Kind == ir.ErrRateLimited || ev.Err.Kind == ir.ErrQuotaExhausted}
						return fail(ae)
					}
					streamErr = ev.Err
					break loop
				}
				if ev.Kind == ir.EvStart || ev.Kind == ir.EvUsage {
					buffered = append(buffered, ev)
					continue
				}
				semantic = true
				ttft := g.Clock.Now().Sub(started).Milliseconds()
				fi.ttft = &ttft
				if g.Metrics != nil {
					g.Metrics.GatewayTTFT.WithLabelValues(string(res.Upstream)).Observe(float64(ttft) / 1000)
				}
				startClient()
				for _, b := range buffered {
					if !write(func() error { return cs.Event(b) }) {
						break loop
					}
				}
			}
			if ev.Kind == ir.EvPartStart && ev.Part != nil && ev.Part.Kind == ir.PartToolCall {
				fi.toolCalls++
			}
			if ev.Kind == ir.EvError {
				streamErr = ev.Err
				break loop
			}
			if !write(func() error { return cs.Event(ev) }) {
				break loop
			}
		}
	}
	if streamErr == nil {
		if err := res.Err(); err != nil {
			if ie, ok := err.(*ir.Error); ok {
				streamErr = ie
			} else {
				streamErr = &ir.Error{Kind: ir.ErrUpstream, Message: "upstream stream failed"}
			}
		}
	}
	if !semantic && !headersSent {
		// Nothing was sent to the client. The upstream accepted the request
		// (200) and then failed or ended: execution is ambiguous, so it is
		// not replayed.
		if streamErr == nil {
			streamErr = &ir.Error{Kind: ir.ErrUpstream, Message: "upstream stream ended without output"}
		}
		return fail(&executor.AttemptError{Err: streamErr, Executed: true, Status: res.Status})
	}
	if cs == nil {
		startClient()
	}
	status := "ok"
	if streamErr != nil {
		status = "error"
		if streamErr.Kind == ir.ErrCanceled {
			status = "canceled"
		} else {
			_ = cs.Fail(streamErr)
		}
	} else if !cs.Done() {
		if !sawStop {
			stop = ir.StopEndTurn
		}
		_ = cs.Finish(stop)
	}
	fi.stop = stop
	fi.bytesOut = sw.Bytes
	g.settleUsage(ctx, rc, fi, ar.ID, rsvID, streamErr == nil)
	now := g.Clock.Now()
	d := now.Sub(started).Milliseconds()
	ar.FinishedAt, ar.DurationMs, ar.TTFTms = &now, &d, fi.ttft
	ar.Status = status
	if streamErr != nil {
		ar.ErrorKind, ar.ErrorMessage, ar.Executed = string(streamErr.Kind), truncate(streamErr.Message, 300), true
	}
	ar.InputTokens, ar.OutputTokens = fi.usage.UncachedInput(), fi.usage.OutputTokens
	if res.Unknown != nil {
		ar.UnknownEvents = res.Unknown()
		if g.Observe != nil && len(ar.UnknownEvents) > 0 {
			g.Observe(Observation{At: started, Direction: "response", Protocol: res.Upstream, ProviderID: fi.cand.ProviderID, Mode: res.Mode,
				Unknown: ar.UnknownEvents, Harness: rc.harness.Name, HarnessVer: rc.harness.Version})
		}
	}
	g.Tel.Attempt(*ar)
	if g.Metrics != nil {
		g.Metrics.GatewayAttempts.WithLabelValues(string(res.Upstream), status).Inc()
	}
	var ferr *ir.Error
	if streamErr != nil {
		ferr = streamErr
	}
	g.finish(ctx, rc, status, ferr, fi)
	return true, nil
}

func includeUsage(r *ir.Request) bool {
	if raw, ok := r.Ext["openai_chat:stream_options"]; ok {
		return string(raw) != "" && containsTrue(raw)
	}
	return false
}

func containsTrue(raw []byte) bool {
	for i := 0; i+4 <= len(raw); i++ {
		if string(raw[i:i+4]) == "true" {
			return true
		}
	}
	return false
}
