package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/compat"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry"
)

// attemptMode is how one candidate will be executed.
type attemptMode struct {
	passthrough bool
	probe       bool
	losses      []ir.Loss
}

// compatMode consults the candidate's breaker. ok=false means the
// candidate must be skipped, with err explaining why.
func (g *Gateway) compatMode(rc *reqCtx, cand *routing.Candidate) (attemptMode, bool, *ir.Error) {
	if g.Compat == nil || cand.Upstream == "" {
		return attemptMode{}, true, nil
	}
	d := g.Compat.Check(cand.ProviderID, rc.inbound, cand.Upstream)
	switch d.State {
	case compat.HalfOpen:
		return attemptMode{probe: d.Probe}, true, nil
	case compat.Open:
		ok, losses, why := g.passthroughPlan(rc, cand)
		if !ok {
			key, path := compat.Key(cand.ProviderID, rc.inbound, cand.Upstream)
			g.passthroughRefused(key, cand.ProviderID, path, why)
			kind := ir.ErrOverloaded
			if strings.Contains(why, "privacy") || strings.Contains(why, "budget") {
				kind = ir.ErrPolicy
			}
			return attemptMode{}, false, &ir.Error{Kind: kind, Message: "compatibility breaker open for " + string(rc.inbound) + " to " +
				string(cand.Upstream) + " on this provider; " + why}
		}
		return attemptMode{passthrough: true, losses: losses}, true, nil
	}
	return attemptMode{}, true, nil
}

// passthroughPlan decides whether native passthrough is authorized for a
// candidate and which features it loses. Mandatory requirements are never
// weakened silently: required masking or an unenforceable blocking budget
// refuse passthrough unless the policies explicitly allow it.
func (g *Gateway) passthroughPlan(rc *reqCtx, cand *routing.Candidate) (bool, []ir.Loss, string) {
	pol := rc.plan.Published.Document.Compatibility
	if pol.OnBreakerOpen != "passthrough" {
		return false, nil, "the plan does not authorize native passthrough"
	}
	if cand.Upstream != rc.inbound || cand.Upstream == ir.ClaudeCodeBridge || cand.Upstream == ir.CodexBridge {
		return false, nil, "native passthrough needs an upstream speaking " + string(rc.inbound)
	}
	losses := []ir.Loss{
		{Feature: "detailed_usage", Severity: ir.LossApproximate, Detail: "usage is read best-effort from the raw stream"},
		{Feature: "tool_statistics", Severity: ir.LossDrop, Detail: "response tool calls are not inspected"},
	}
	if rc.rec.Privacy != nil {
		required := rc.privacy == nil || rc.privacy.Required
		allowUnmasked := rc.privacy != nil && rc.privacy.AllowUnmasked
		if !slices.Contains(pol.AllowedLosses, "masking") || (required && !allowUnmasked) {
			return false, nil, "required privacy masking cannot run in passthrough"
		}
		losses = append(losses, ir.Loss{Feature: "masking", Severity: ir.LossDrop, Detail: "the client's original payload is forwarded unmasked"})
	}
	if pol.UnenforceableBudget == "block" && g.blockingBudget(rc, cand) {
		return false, nil, "a blocking budget cannot be enforced without detailed usage"
	}
	if rc.trace != nil {
		losses = append(losses, ir.Loss{Feature: "traces", Severity: ir.LossDrop, Detail: "content capture is suspended during passthrough"})
	}
	return true, losses, ""
}

func (g *Gateway) blockingBudget(rc *reqCtx, cand *routing.Candidate) bool {
	for _, b := range rc.snap.Budgets {
		if !b.Spec.Enabled || b.Spec.Enforcement != "block" {
			continue
		}
		switch b.Spec.Scope {
		case cp.BudgetGlobal:
			return true
		case cp.BudgetWorkspace:
			if b.Spec.ScopeID == rc.workspace {
				return true
			}
		case cp.BudgetProject:
			if b.Spec.ScopeID == rc.project {
				return true
			}
		case cp.BudgetBinding:
			if b.Spec.ScopeID == rc.binding.ID {
				return true
			}
		case cp.BudgetAccount:
			if b.Spec.ScopeID == cand.AccountID {
				return true
			}
		}
	}
	return false
}

// recordCompat feeds an attempt outcome to the breaker and Protocol Watch.
func (g *Gateway) recordCompat(rc *reqCtx, cand *routing.Candidate, m attemptMode, err *ir.Error) {
	if g.Compat == nil || m.passthrough || cand.Upstream == "" {
		if m.probe && g.Compat != nil {
			g.Compat.Record(cand.ProviderID, rc.inbound, cand.Upstream, true, &ir.Error{Kind: ir.ErrCanceled})
		}
		return
	}
	g.Compat.Record(cand.ProviderID, rc.inbound, cand.Upstream, m.probe, err)
	if err != nil && g.Watch != nil && compat.Classify(err) == compat.ClassCompat {
		dir := "response"
		if err.Kind != ir.ErrProtocol {
			dir = "request"
		}
		g.Watch.Observe(compat.Signal{Direction: dir, Protocol: cand.Upstream, ProviderID: cand.ProviderID, Harness: rc.harness.Name,
			HarnessVer: rc.harness.Version, RequestID: rc.id, Route: cand.ProviderID + "|" + string(rc.inbound) + ">" + string(cand.Upstream), Err: err})
	}
}

const rawSampleLimit = 256 << 10

// deliverRaw forwards a passthrough response byte-for-byte. Basic
// telemetry is kept: status, bytes, timing and best-effort usage. A bounded
// sample goes to Protocol Watch without ever blocking the client.
func (g *Gateway) deliverRaw(ctx context.Context, w http.ResponseWriter, rc *reqCtx, res *executor.Result, fi *finalInfo, ar *telemetry.AttemptRecord,
	started time.Time, rsvID string) (bool, *executor.AttemptError) {
	defer res.Cancel()
	ct := res.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Request-ID", rc.id)
	w.Header().Set("X-Harbor-Mode", "passthrough")
	g.setUsageHeaders(ctx, w.Header(), rc, &fi.cand)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	var sample bytes.Buffer
	sniff := &usageSniffer{}
	var first bool
	var streamErr *ir.Error
	for chunk := range res.RawStream {
		if !first {
			first = true
			t := g.Clock.Now().Sub(started).Milliseconds()
			fi.ttft = &t
		}
		_ = setWriteDeadline(w, time.Now().Add(g.Limits.SlowClientWrite))
		n, err := w.Write(chunk)
		fi.bytesOut += int64(n)
		if err != nil {
			streamErr = &ir.Error{Kind: ir.ErrCanceled, Message: "client connection failed"}
			res.Cancel()
			for range res.RawStream {
			}
			break
		}
		if flusher != nil {
			flusher.Flush()
		}
		sniff.feed(chunk)
		if sample.Len() < rawSampleLimit {
			sample.Write(chunk[:min(len(chunk), rawSampleLimit-sample.Len())])
		}
	}
	if streamErr == nil && res.Err != nil {
		if err := res.Err(); err != nil {
			streamErr = &ir.Error{Kind: ir.ErrUpstream, Message: "the upstream stream ended with an error"}
		}
	}
	if g.Watch != nil && sample.Len() > 0 {
		g.Watch.Observe(compat.Signal{Direction: "response", Protocol: rc.inbound, ProviderID: fi.cand.ProviderID, Harness: rc.harness.Name,
			HarnessVer: rc.harness.Version, RequestID: rc.id, Route: fi.cand.ProviderID + "|" + string(rc.inbound) + ">" + string(fi.cand.Upstream),
			Raw: sample.Bytes(), Action: map[string]any{"mode": "passthrough"}})
	}
	if streamErr == nil {
		key, path := compat.Key(fi.cand.ProviderID, rc.inbound, fi.cand.Upstream)
		g.passthroughServed(key, fi.cand.ProviderID, path)
	}
	fi.usage = sniff.usage
	fi.stop = ir.StopReason(sniff.stop)
	if !fi.usage.Known() && rc.plan.Published.Document.Compatibility.UnknownUsage == "estimate" && fi.bytesOut > 0 {
		in := int64(inputChars(rc.ir)/4 + 1)
		out := fi.bytesOut / 16
		fi.usage = ir.Usage{InputTokens: &in, OutputTokens: &out}
		fi.usageSource = "estimated"
	}
	g.settleUsage(ctx, rc, fi, ar.ID, rsvID, streamErr == nil)
	now := g.Clock.Now()
	d := now.Sub(started).Milliseconds()
	ar.FinishedAt, ar.DurationMs, ar.TTFTms, ar.Status = &now, &d, fi.ttft, "ok"
	if streamErr != nil {
		ar.Status, ar.ErrorKind, ar.ErrorMessage, ar.Executed = "error", string(streamErr.Kind), streamErr.Message, true
	}
	ar.InputTokens, ar.OutputTokens = fi.usage.UncachedInput(), fi.usage.OutputTokens
	g.Tel.Attempt(*ar)
	status := "ok"
	if streamErr != nil {
		status = "error"
	}
	g.finish(ctx, rc, status, streamErr, fi)
	return true, nil
}

// usageSniffer reads usage figures from raw protocol bytes without
// rewriting or validating them; unknown shapes simply yield no usage.
type usageSniffer struct {
	buf   []byte
	usage ir.Usage
	stop  string
}

func (u *usageSniffer) feed(b []byte) {
	u.buf = append(u.buf, b...)
	for {
		i := bytes.IndexByte(u.buf, '\n')
		if i < 0 {
			if len(u.buf) > 1<<20 {
				u.buf = nil
			}
			break
		}
		line := bytes.TrimSpace(u.buf[:i])
		u.buf = u.buf[i+1:]
		line = bytes.TrimPrefix(line, []byte("data:"))
		u.parse(bytes.TrimSpace(line))
	}
	if len(u.buf) > 0 && u.buf[0] == '{' {
		u.parse(bytes.TrimSpace(u.buf))
	}
}

func (u *usageSniffer) parse(line []byte) {
	if len(line) == 0 || line[0] != '{' || !bytes.Contains(line, []byte("usage")) && !bytes.Contains(line, []byte("stop_reason")) && !bytes.Contains(line, []byte("finish_reason")) {
		return
	}
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return
	}
	pick := func(obj map[string]any) {
		us, _ := obj["usage"].(map[string]any)
		if us == nil {
			us, _ = obj["usageMetadata"].(map[string]any)
		}
		num := func(keys ...string) *int64 {
			for _, k := range keys {
				if f, ok := us[k].(float64); ok {
					v := int64(f)
					return &v
				}
			}
			return nil
		}
		if us != nil {
			u.usage.Merge(ir.Usage{InputTokens: num("input_tokens", "prompt_tokens", "promptTokenCount"),
				OutputTokens:     num("output_tokens", "completion_tokens", "candidatesTokenCount"),
				CacheReadTokens:  num("cache_read_input_tokens"),
				CacheWriteTokens: num("cache_creation_input_tokens")})
			if _, ok := us["prompt_tokens"]; ok {
				u.usage.InputIncludesCache = true
			}
		}
		if s, ok := obj["stop_reason"].(string); ok && s != "" {
			u.stop = s
		}
	}
	pick(m)
	if msg, ok := m["message"].(map[string]any); ok {
		pick(msg)
	}
	if d, ok := m["delta"].(map[string]any); ok {
		if s, ok := d["stop_reason"].(string); ok && s != "" {
			u.stop = s
		}
	}
	if r, ok := m["response"].(map[string]any); ok {
		pick(r)
	}
}

func setWriteDeadline(w http.ResponseWriter, t time.Time) error {
	return http.NewResponseController(w).SetWriteDeadline(t)
}
