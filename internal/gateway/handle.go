package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/httpapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

// Outcome summarizes a finished logical request (for events and tests).
type Outcome struct {
	RequestID   string
	BindingID   string
	ProjectID   string
	Status      string
	Decision    *routing.Decision
	Attempts    []AttemptOutcome
	FinalIndex  int // index into Attempts of the delivering attempt, -1 if none
	Error       *ir.Error
	Blocked     bool
	BlockReason string
}

// AttemptOutcome summarizes one attempt.
type AttemptOutcome struct {
	ID        string
	Candidate routing.Candidate
	Err       *ir.Error
	Executed  bool
	Step      string // fallback step that permitted this attempt
}

// reqCtx is the per-request state carried through the pipeline.
type reqCtx struct {
	id        string
	start     time.Time
	inbound   ir.Protocol
	cred      *cp.AuthenticatedCredential
	binding   *cp.Res[cp.BindingSpec]
	plan      *cp.PlanView
	snap      *cp.Snapshot
	ir        *ir.Request
	raw       []byte
	header    http.Header
	session   telemetry.SessionResult
	harness   telemetry.Harness
	project   string
	workspace string
	refs      map[string]string
	claims    map[string]string
	rec       telemetry.RequestRecord
	outcome   Outcome
	// current is the attempt being delivered, recorded as final by finish.
	current *AttemptOutcome
	// privacy is the masking policy applied to ir; unmasked keeps the
	// client's original request (never sent upstream, never persisted
	// except in explicitly authorized unredacted traces).
	privacy   *privacy.Policy
	privScope string
	unmasked  *ir.Request
	// trace collects content for short-lived traces (nil = not captured).
	trace       *traces.Capture
	traceEvents []ir.Event
	traceResp   *ir.Response
	started     int // upstream attempts started
}

func (g *Gateway) handler(p ir.Protocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { g.serve(w, r, p, "", false) }
}

// geminiHandler parses "/v1beta/models/{model}:{method}".
func (g *Gateway) geminiHandler(w http.ResponseWriter, r *http.Request) {
	target := chi.URLParam(r, "target")
	if u, err := url.PathUnescape(target); err == nil {
		target = u
	}
	i := strings.LastIndexByte(target, ':')
	if i < 0 {
		g.writeError(w, ir.Gemini, &ir.Error{Kind: ir.ErrNotFound, Message: "expected models/{model}:generateContent"}, "")
		return
	}
	model, method := target[:i], target[i+1:]
	switch method {
	case "generateContent":
		g.serve(w, r, ir.Gemini, model, false)
	case "streamGenerateContent":
		g.serve(w, r, ir.Gemini, model, true)
	case "countTokens":
		g.geminiCountTokens(w, r, model)
	default:
		g.writeError(w, ir.Gemini, &ir.Error{Kind: ir.ErrNotFound, Message: "unsupported method " + method}, "")
	}
}

func (g *Gateway) serve(w http.ResponseWriter, r *http.Request, p ir.Protocol, pathModel string, pathStream bool) {
	ctx := r.Context()
	rc := &reqCtx{id: ids.New(ids.Request), start: g.Clock.Now(), inbound: p, header: r.Header, claims: map[string]string{}, refs: map[string]string{}}
	rc.outcome = Outcome{RequestID: rc.id, FinalIndex: -1}
	ctx = observability.WithRequestID(ctx, rc.id)
	w.Header().Set("X-Request-ID", rc.id)
	if g.Metrics != nil {
		g.Metrics.GatewayInflight.Inc()
		defer g.Metrics.GatewayInflight.Dec()
	}

	// 1. Authenticate and establish trusted context.
	cred, aerr := g.authenticate(r)
	if aerr != nil {
		g.writeError(w, p, aerr, rc.id)
		return
	}
	if !cred.Has(cp.ScopeInvoke) {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrPermission, Message: "credential lacks the gateway:invoke scope"}, rc.id)
		return
	}
	rc.cred = cred
	rc.snap = g.CP.Snapshot()
	rc.binding = rc.snap.Bindings[cred.BindingID]
	if rc.binding == nil || !rc.binding.Spec.Enabled {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrPermission, Message: "this client is disabled"}, rc.id)
		return
	}
	rc.plan = rc.snap.Plans[rc.binding.Spec.PlanID]
	if lim := rc.binding.Spec.Limits.RequestsPerMinute; lim > 0 {
		if ok, wait := g.limiter("binding:"+rc.binding.ID, lim).Allow(rc.binding.ID); !ok {
			g.writeError(w, p, &ir.Error{Kind: ir.ErrRateLimited, Message: "client request rate limit reached", RetryAfterSeconds: int(wait.Seconds()) + 1}, rc.id)
			return
		}
	}

	// 2. Read and parse the request (bounded).
	maxBytes := g.Limits.MaxRequestBytes
	if b := rc.binding.Spec.Limits.MaxRequestBytes; b > 0 {
		maxBytes = int64(b)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInvalidRequest, Message: "could not read request body"}, rc.id)
		return
	}
	if int64(len(body)) > maxBytes {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInvalidRequest, Message: "request body exceeds the size limit", HTTPStatus: http.StatusRequestEntityTooLarge}, rc.id)
		return
	}
	rc.raw = body
	req, err := parse(p, body, pathModel, pathStream)
	if err != nil {
		var ie *ir.Error
		if !errors.As(err, &ie) {
			ie = &ir.Error{Kind: ir.ErrInvalidRequest, Message: err.Error()}
		}
		g.writeError(w, p, ie, rc.id)
		return
	}
	rc.ir = req

	// 3. Identity, attribution and client hints (hints are recorded, never
	// trusted for authorization).
	rc.harness = telemetry.DetectHarness(r.Header)
	for k, v := range cred.BoundRefs {
		rc.refs[k] = v
	}
	for k, v := range rc.binding.Spec.ExternalRefs {
		if _, set := rc.refs[k]; !set {
			rc.refs[k] = v
		}
	}
	// Mint-time checks already confine bound projects; re-check against the
	// binding as it is now (its allowed projects may have changed).
	rc.project = cp.CredentialProject(rc.snap, rc.binding, cred.BoundRefs)
	if hint := r.Header.Get("X-HP-Project"); hint != "" {
		rc.claims["project"] = truncate(hint, 128)
		if rc.binding.Spec.Trust.ProjectHints && slices.Contains(rc.binding.Spec.AllowedProjectIDs, hint) && cred.BoundRefs["project_id"] == "" {
			rc.project = hint
		}
	}
	if pr := rc.snap.Projects[rc.project]; pr != nil {
		rc.workspace = pr.Spec.WorkspaceID
	}
	if v := r.Header.Get("X-HP-Intent"); v != "" {
		rc.claims["intent"] = truncate(v, 64)
	}
	if v := r.Header.Get("X-HP-External-Ref"); v != "" {
		rc.claims["external_ref"] = truncate(v, 200)
	}
	rc.session = g.Sessions.Resolve(ctx, rc.binding.ID, cred.BoundRefs, r.Header, rc.binding.Spec.Trust.SessionHints, rc.harness.Name, rc.start)
	if rc.session.Claim != "" {
		rc.claims["session:"+rc.session.ClaimFrom] = truncate(rc.session.Claim, 200)
	}
	if exp := rc.binding.Spec.ExpectedHarness; exp != "" && exp != "generic" && rc.harness.Name != exp {
		rc.claims["harness_mismatch"] = "expected " + exp + ", detected " + rc.harness.Name
	}
	tools := ir.ObserveTools(req)
	m := reqmeta.From(ctx)
	rc.rec = telemetry.RequestRecord{ID: rc.id, CreatedAt: rc.start, BindingID: rc.binding.ID, CredentialID: cred.ID, WorkspaceID: rc.workspace,
		ProjectID: rc.project, TrustedRefs: rc.refs, Claims: rc.claims, SessionID: rc.session.ID, SessionConfidence: rc.session.Confidence,
		Harness: rc.harness.Name, HarnessConfidence: rc.harness.Confidence, HarnessVersion: rc.harness.Version, InboundProtocol: string(p),
		Stream: req.Stream, RequestedModel: req.Model, RequiredCaps: ir.Requirements(req), Status: "in_progress",
		ToolsOffered: len(tools.Offered), ToolResults: tools.ResultsSeen, ToolResultErrors: tools.ResultErrors, ToolNames: capNames(tools.Offered),
		ClientIP: m.IP, UserAgent: truncate(r.UserAgent(), 200)}
	rc.outcome.BindingID, rc.outcome.ProjectID = rc.binding.ID, rc.project
	g.Tel.Request(rc.rec)

	rc.rec.Fingerprints, rc.rec.ToolResultIDs = telemetry.MessageFingerprints(req)

	// Outbound masking runs before anything (including an intent
	// classifier) can send content out.
	if !g.applyPrivacy(ctx, w, rc) {
		return
	}

	// 4–6. Capability requirements, filtering and ranking.
	intent, intentSrc := "", ""
	var intentDetail map[string]any
	if v := rc.claims["intent"]; v != "" && rc.binding.Spec.Trust.IntentHints {
		intent, intentSrc = v, "declared"
	} else if rc.plan.Published.Document.Intent.Mode != "off" {
		if l, d := g.classify(ctx, rc); l != "" {
			intent, intentSrc = l, "classifier"
			intentDetail = d
		} else {
			intentDetail = d
		}
	}
	reqs := ir.Requirements(req)
	dec, derr := routing.Decide(routing.Input{Snapshot: rc.snap, Binding: rc.binding, Plan: rc.plan, RequestedModel: req.Model,
		Required: reqs, SealedFamilies: ir.SealedFamilies(req), Inbound: p, SessionKey: rc.session.Key, RequestSeed: rc.id,
		Now: rc.start, HealthOf: g.health, UpstreamFor: g.upstreamFor,
		Harness: rc.harness.Name, HarnessConfidence: rc.harness.Confidence, Stream: req.Stream, ToolNames: tools.Offered,
		Images: slices.Contains(reqs, "vision"), InputChars: inputChars(req), Intent: intent, IntentSource: intentSrc,
		SessionConfidence: rc.session.Confidence, Refs: rc.refs, ProjectID: rc.project, WorkspaceID: rc.workspace})
	rc.outcome.Decision = dec
	rc.rec.Decision = dec
	if dec != nil {
		rc.rec.Intent, rc.rec.IntentSource = dec.Intent, dec.IntentSource
	}
	if intentDetail != nil {
		rc.rec.IntentDetail = intentDetail
	}
	if dec != nil {
		rc.rec.AliasID, rc.rec.Resolution, rc.rec.PlanID, rc.rec.PlanVersion = dec.AliasID, dec.Resolution, dec.PlanID, dec.PlanVersion
		rc.rec.RequiredCaps = dec.Required
	}
	if derr != nil {
		var re *routing.Error
		e := &ir.Error{Kind: ir.ErrPolicy, Message: derr.Error()}
		if errors.As(derr, &re) {
			e.Kind = re.Kind
		}
		rc.outcome.Blocked, rc.outcome.BlockReason = true, e.Message
		g.finish(ctx, rc, "blocked", e, nil)
		g.writeError(w, p, e, rc.id)
		return
	}

	g.startTrace(ctx, rc)

	// 7–9. Reserve, dispatch, reconcile with policy-controlled failover.
	g.dispatch(ctx, w, rc, dec)
}

func parse(p ir.Protocol, body []byte, pathModel string, pathStream bool) (*ir.Request, error) {
	switch p {
	case ir.AnthropicMessages:
		return anthropic.ParseRequest(body, anthropic.DefaultLimits)
	case ir.OpenAIChat:
		return openaichat.ParseRequest(body, openaichat.DefaultLimits)
	case ir.OpenAIResponses:
		return openairesponses.ParseRequest(body, openairesponses.DefaultLimits)
	case ir.Gemini:
		return gemini.ParseRequest(body, pathModel, pathStream)
	}
	return nil, &ir.Error{Kind: ir.ErrUnsupported, Message: "unsupported protocol"}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func capNames(names []string) []string {
	if len(names) > 64 {
		return names[:64]
	}
	return names
}

// retryableKinds are failures where another candidate may succeed.
func failoverAllowed(ae *executor.AttemptError) bool {
	if ae.Executed {
		return false // ambiguous execution is never replayed
	}
	switch ae.Err.Kind {
	case ir.ErrRateLimited, ir.ErrQuotaExhausted, ir.ErrOverloaded, ir.ErrUpstream, ir.ErrAuthentication, ir.ErrPermission,
		ir.ErrUnsupported, ir.ErrNotFound, ir.ErrTimeout, ir.ErrProtocol:
		return true
	}
	return false
}

func (g *Gateway) dispatch(ctx context.Context, w http.ResponseWriter, rc *reqCtx, dec *routing.Decision) {
	if e := refusedRequestField(rc, dec); e != nil {
		g.finish(ctx, rc, "error", e, nil)
		g.writeError(w, rc.inbound, e, rc.id)
		return
	}
	policy := rc.plan.Published.Document.Fallback
	var lastErr *ir.Error
	var prev *routing.Candidate
	for i := range dec.Candidates {
		cand := dec.Candidates[i]
		if len(rc.outcome.Attempts) >= policy.MaxAttempts {
			break
		}
		step := "primary"
		if prev != nil {
			s, ok := routing.Transition(policy, *prev, cand)
			if !ok {
				rc.outcome.Attempts = append(rc.outcome.Attempts, AttemptOutcome{Candidate: cand, Step: "not_permitted:" + s})
				lastErr = &ir.Error{Kind: lastErr.Kind, Message: lastErr.Message + " (fallback to " + cand.Account + "/" + cand.Model + " not permitted by plan: " + s + ")",
					RetryAfterSeconds: lastErr.RetryAfterSeconds}
				break
			}
			step = s
		}
		acc := rc.snap.Accounts[cand.AccountID]
		prov := rc.snap.Providers[cand.ProviderID]
		if lim := acc.Spec.RequestsPerMinute; lim > 0 {
			if ok, _ := g.limiter("account:"+acc.ID, lim).Allow(acc.ID); !ok {
				lastErr = &ir.Error{Kind: ir.ErrRateLimited, Message: "local request rate limit for account " + acc.Name}
				rc.outcome.Attempts = append(rc.outcome.Attempts, AttemptOutcome{Candidate: cand, Err: lastErr, Step: step})
				prev = &cand
				continue
			}
		}
		mode, ok, cerr := g.compatMode(rc, &cand)
		if !ok {
			lastErr = cerr
			rc.outcome.Attempts = append(rc.outcome.Attempts, AttemptOutcome{Candidate: cand, Err: lastErr, Step: step + ":compat_open"})
			prev = &cand
			continue
		}
		if mode.passthrough {
			rc.trace = nil // unsafe content capture is suspended
		}
		attID := ids.New(ids.Attempt)
		ao := AttemptOutcome{ID: attID, Candidate: cand, Step: step}
		lease := 12 * time.Minute
		if s := prov.Spec.RequestTimeoutSecs; s > 0 {
			lease = time.Duration(s)*time.Second + 2*time.Minute
		}
		est := reserveEstimate(rc, &cand, acc.Spec.Billing)
		rsv, err := g.Acct.Reserve(ctx, accounting.ReserveInput{RequestID: rc.id, AttemptID: attID, AccountID: acc.ID, BindingID: rc.binding.ID,
			ProjectID: rc.project, WorkspaceID: rc.workspace, AccountMax: acc.Spec.MaxConcurrency, BindingMax: rc.binding.Spec.Limits.MaxConcurrency,
			Lease: lease, Billing: acc.Spec.Billing, EstimatedCost: est.Amount, Currency: est.Currency})
		if err != nil {
			var ce *accounting.CapacityError
			if errors.As(err, &ce) {
				lastErr = &ir.Error{Kind: ir.ErrRateLimited, Message: ce.Error()}
				if strings.HasPrefix(ce.Limit, "budget") {
					lastErr.Kind = ir.ErrBudget
				}
				ao.Err = lastErr
				rc.outcome.Attempts = append(rc.outcome.Attempts, ao)
				if g.Metrics != nil {
					g.Metrics.ReservationsTotal.WithLabelValues("rejected").Inc()
				}
				prev = &cand
				continue
			}
			g.finish(ctx, rc, "error", &ir.Error{Kind: ir.ErrInternal, Message: "could not reserve capacity"}, nil)
			g.writeError(w, rc.inbound, &ir.Error{Kind: ir.ErrInternal, Message: "internal error"}, rc.id)
			return
		}
		if g.Metrics != nil {
			g.Metrics.ReservationsTotal.WithLabelValues("taken").Inc()
		}
		rc.current = &ao
		delivered, aerr := g.attempt(ctx, w, rc, &cand, attID, len(rc.outcome.Attempts)+1, step, rsv.ID, mode)
		rc.current = nil
		if delivered {
			// finish (called from delivery) recorded this attempt as final.
			return
		}
		ao.Err, ao.Executed = aerr.Err, aerr.Executed
		rc.outcome.Attempts = append(rc.outcome.Attempts, ao)
		lastErr = aerr.Err
		if !failoverAllowed(aerr) || ctx.Err() != nil {
			break
		}
		prev = &cand
	}
	if lastErr == nil {
		lastErr = &ir.Error{Kind: ir.ErrQuotaExhausted, Message: "no candidate could serve the request"}
	}
	status := "error"
	if budgetRefusedAll(rc.outcome.Attempts) {
		// Blocking budgets refused every candidate at reservation, before
		// any provider was contacted: a policy refusal, reported like other
		// blocks (D-040).
		status = "blocked"
		rc.outcome.Blocked, rc.outcome.BlockReason = true, lastErr.Message
	}
	g.finish(ctx, rc, status, lastErr, nil)
	g.writeError(w, rc.inbound, lastErr, rc.id)
}

// refusedRequestField applies the reject policy of per-model request fields
// (D-046) before any candidate is tried. Every candidate is checked and the
// strictest answer wins: if any candidate's model refuses a field the client
// sets, the request is refused, so the outcome never depends on which
// candidate happens to come first.
func refusedRequestField(rc *reqCtx, dec *routing.Decision) *ir.Error {
	var cs httpapi.ClientSettings
	for i := range dec.Candidates {
		cand := &dec.Candidates[i]
		prov := rc.snap.Providers[cand.ProviderID]
		if cand.ModelSpec == nil || prov == nil || cand.ModelSpec.RequestFieldPolicy != cp.FieldPolicyReject {
			continue
		}
		if cs == nil {
			cs = httpapi.ClientSettingsOf(rc.ir, rc.raw)
		}
		if name, ok := httpapi.RefusedRequestField(prov.Spec.Type, cand.ModelSpec, cs); ok {
			return &ir.Error{Kind: ir.ErrInvalidRequest, Message: httpapi.RefusedFieldMessage(name)}
		}
	}
	return nil
}

// budgetRefusedAll reports whether there were attempts and a blocking
// budget refused each of them.
func budgetRefusedAll(attempts []AttemptOutcome) bool {
	for _, a := range attempts {
		if a.Err == nil || a.Err.Kind != ir.ErrBudget {
			return false
		}
	}
	return len(attempts) > 0
}

// finish records the final request state and runs post-request hooks.
func (g *Gateway) finish(ctx context.Context, rc *reqCtx, status string, e *ir.Error, final *finalInfo) {
	now := g.Clock.Now()
	rc.rec.Status = status
	rc.rec.FinishedAt = &now
	d := now.Sub(rc.start).Milliseconds()
	rc.rec.DurationMs = &d
	rc.rec.AttemptCount = rc.started
	if e != nil {
		rc.rec.ErrorKind, rc.rec.ErrorMessage = string(e.Kind), truncate(e.Message, 500)
		rc.outcome.Error = e
	}
	if final != nil {
		rc.rec.FinalAccountID, rc.rec.FinalProviderID, rc.rec.FinalModel, rc.rec.Mode = final.cand.AccountID, final.cand.ProviderID, final.cand.Model, string(final.mode)
		rc.rec.StopReason = string(final.stop)
		rc.rec.TTFTms = final.ttft
		u := final.usage
		rc.rec.InputTokens, rc.rec.OutputTokens = u.UncachedInput(), u.OutputTokens
		rc.rec.CacheReadTokens, rc.rec.CacheWriteTokens, rc.rec.ReasoningTokens = u.CacheReadTokens, u.CacheWriteTokens, u.ReasoningTokens
		rc.rec.UsageSource = final.usageSource
		if final.cost != nil {
			c := int64(final.cost.Amount)
			rc.rec.CostE10, rc.rec.Currency, rc.rec.CostKind = &c, final.cost.Currency, final.cost.Kind
		}
		rc.rec.ToolCalls = final.toolCalls
		rc.rec.Losses = final.losses
		rc.rec.BytesOut = final.bytesOut
	}
	if final != nil && rc.current != nil {
		rc.outcome.Attempts = append(rc.outcome.Attempts, *rc.current)
		rc.outcome.FinalIndex = len(rc.outcome.Attempts) - 1
	}
	rc.outcome.Status = status
	g.Tel.Request(rc.rec)
	g.logCompletion(rc, status)
	if final != nil {
		g.finishTrace(rc, final.usage)
		var perr *ir.Error
		if e != nil && e.Kind == ir.ErrProtocol {
			perr = e
		}
		g.recordCompat(rc, &final.cand, final.compat, perr)
	}
	if g.Metrics != nil {
		g.Metrics.GatewayRequests.WithLabelValues(string(rc.inbound), status).Inc()
		g.Metrics.GatewayDuration.WithLabelValues(string(rc.inbound)).Observe(float64(d) / 1000)
	}
	if g.AfterRequest != nil {
		out := rc.outcome
		go g.AfterRequest(context.WithoutCancel(ctx), &out)
	}
}

type finalInfo struct {
	cand        routing.Candidate
	mode        executor.Mode
	stop        ir.StopReason
	ttft        *int64
	usage       ir.Usage
	usageSource string
	cost        *accounting.Cost
	toolCalls   int
	losses      []ir.Loss
	bytesOut    int64
	compat      attemptMode
}

// reserveEstimate prices the worst case of an attempt (input estimated at
// four characters per token, output at the requested maximum) so budgets
// can account for in-flight work. Unknown prices yield a zero estimate.
func reserveEstimate(rc *reqCtx, cand *routing.Candidate, billing string) accounting.Cost {
	m, _ := rc.snap.ModelOf(cand.ProviderID, cand.Model)
	in := int64(inputChars(rc.ir)/4 + 1)
	out := int64(4096)
	if rc.ir.MaxOutputTokens != nil && *rc.ir.MaxOutputTokens > 0 {
		out = int64(*rc.ir.MaxOutputTokens)
	} else if m != nil && m.MaxOutputTokens > 0 {
		out = int64(m.MaxOutputTokens)
	}
	c, ok := accounting.Estimate(m, billing, ir.Usage{InputTokens: &in, OutputTokens: &out})
	if !ok {
		return accounting.Cost{}
	}
	return *c
}

// inputChars measures text size (not content) for routing expressions.
func inputChars(r *ir.Request) int {
	n := 0
	for _, p := range r.System {
		n += len(p.Text)
	}
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			n += len(p.Text)
			if p.ToolCall != nil {
				n += len(p.ToolCall.Arguments)
			}
			if p.ToolResult != nil {
				for _, c := range p.ToolResult.Content {
					n += len(c.Text)
				}
			}
		}
	}
	return n
}

// logCompletion writes one structured, correlated line per request
// (identifiers and figures only; never content) for log pipelines such as
// OpenSearch.
func (g *Gateway) logCompletion(rc *reqCtx, status string) {
	if g.Log == nil {
		return
	}
	r := rc.rec
	attrs := []any{"request_id", r.ID, "status", status, "binding_id", r.BindingID, "project_id", r.ProjectID, "session_id", r.SessionID,
		"plan_id", r.PlanID, "harness", r.Harness, "inbound_protocol", r.InboundProtocol, "requested_model", r.RequestedModel,
		"final_model", r.FinalModel, "account_id", r.FinalAccountID, "provider_id", r.FinalProviderID, "mode", r.Mode, "attempts", r.AttemptCount,
		"intent", r.Intent, "error_kind", r.ErrorKind}
	if r.TTFTms != nil {
		attrs = append(attrs, "ttft_ms", *r.TTFTms)
	}
	if r.DurationMs != nil {
		attrs = append(attrs, "duration_ms", *r.DurationMs)
	}
	if r.InputTokens != nil {
		attrs = append(attrs, "input_tokens", *r.InputTokens)
	}
	if r.OutputTokens != nil {
		attrs = append(attrs, "output_tokens", *r.OutputTokens)
	}
	g.Log.Info("gateway.request.completed", attrs...)
}
