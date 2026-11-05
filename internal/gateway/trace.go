package gateway

import (
	"context"
	"hash/fnv"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

const maxTracedEvents = 5000

// startTrace decides whether this request's content is captured.
func (g *Gateway) startTrace(ctx context.Context, rc *reqCtx) {
	if g.Traces == nil || rc.plan == nil || rc.plan.Published == nil {
		return
	}
	id := rc.plan.Published.Document.Privacy.TracePolicyID
	pol := rc.snap.TracePols[id]
	if pol == nil || pol.Spec.EffectiveMode(rc.start) == cp.TraceOff {
		return
	}
	if r := pol.Spec.SampleRate; r > 0 && r < 1 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(rc.id))
		if float64(h.Sum32()%10000)/10000 >= r {
			return
		}
	}
	c := &traces.Capture{RequestID: rc.id, BindingID: rc.binding.ID, SessionID: rc.session.ID, PlanID: rc.plan.ID, PolicyID: pol.ID,
		Policy: pol.Spec, Time: rc.start}
	if rid := pol.Spec.RedactionPolicyID; rid != "" && g.Privacy != nil {
		if p, err := g.Privacy.Policy(ctx, rc.snap, rid); err == nil {
			c.Redaction = p
		}
	}
	rc.trace = c
}

// teeTrace records the attempt's output for the trace.
func (g *Gateway) teeTrace(ctx context.Context, rc *reqCtx, res *executor.Result) {
	if rc.trace == nil || res == nil {
		return
	}
	rc.traceEvents = nil
	rc.traceResp = res.Response
	if res.Stream == nil {
		return
	}
	in := res.Stream
	out := make(chan ir.Event, cap(in)+1)
	res.Stream = out
	go func() {
		defer close(out)
		for e := range in {
			if len(rc.traceEvents) < maxTracedEvents {
				rc.traceEvents = append(rc.traceEvents, e)
			}
			select {
			case out <- e:
			case <-ctx.Done():
				for range in {
				}
				return
			}
		}
	}()
}

// finishTrace queues the capture once the request has completed.
func (g *Gateway) finishTrace(rc *reqCtx, usage any) {
	c := rc.trace
	if c == nil {
		return
	}
	c.Sent, c.Original = rc.ir, rc.unmasked
	c.Masking = rc.privacy
	if c.Original == nil {
		c.Original = rc.ir
	}
	if rc.traceResp != nil {
		c.Response = rc.traceResp
	} else if len(rc.traceEvents) > 0 {
		ch := make(chan ir.Event, len(rc.traceEvents))
		for _, e := range rc.traceEvents {
			ch <- e
		}
		close(ch)
		if r, err := ir.Collect(ch); err == nil {
			c.Response = r
		}
		c.Events = rc.traceEvents
	}
	type attempt struct {
		Account  string `json:"account_id"`
		Provider string `json:"provider_id"`
		Model    string `json:"model"`
		Step     string `json:"step"`
		Error    string `json:"error,omitempty"`
	}
	atts := []attempt{}
	for _, a := range rc.outcome.Attempts {
		x := attempt{Account: a.Candidate.AccountID, Provider: a.Candidate.ProviderID, Model: a.Candidate.Model, Step: a.Step}
		if a.Err != nil {
			x.Error = string(a.Err.Kind)
		}
		atts = append(atts, x)
	}
	c.Attempts, c.Routing, c.Usage = atts, rc.outcome.Decision, usage
	g.Traces.Enqueue(c)
}
