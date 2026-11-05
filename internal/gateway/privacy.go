package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy"
)

// applyPrivacy masks the request per the plan's privacy policy. It writes
// the error response and returns false when the request must not be sent.
func (g *Gateway) applyPrivacy(ctx context.Context, w http.ResponseWriter, rc *reqCtx) bool {
	if g.Privacy == nil || rc.plan == nil || rc.plan.Published == nil {
		return true
	}
	id := rc.plan.Published.Document.Privacy.PolicyID
	if id == "" {
		return true
	}
	pol, err := g.Privacy.Policy(ctx, rc.snap, id)
	scope := privacy.Scope(rc.binding.ID, rc.session.ID, rc.cred.ID)
	var sum *privacy.Summary
	if err == nil {
		masked := privacy.CloneRequest(rc.ir)
		sum, err = pol.MaskRequest(masked, g.Privacy.Vault, scope)
		if err == nil {
			rc.rec.Privacy = sum
			if sum.Blocked {
				e := &ir.Error{Kind: ir.ErrPolicy, Message: "the request contains content blocked by the privacy policy (" + strings.Join(sum.BlockedRules(), ", ") + ")"}
				rc.outcome.Blocked, rc.outcome.BlockReason = true, e.Message
				g.finish(ctx, rc, "blocked", e, nil)
				g.writeError(w, rc.inbound, e, rc.id)
				return false
			}
			rc.unmasked, rc.ir = rc.ir, masked
			rc.privacy, rc.privScope = pol, scope
			return true
		}
	}
	// Masking could not run. A required policy blocks unless it explicitly
	// permits availability-first unmasked dispatch; an unreadable policy is
	// treated as required.
	required := pol == nil || pol.Required
	allowUnmasked := pol != nil && pol.AllowUnmasked
	if required && !allowUnmasked {
		e := &ir.Error{Kind: ir.ErrPolicy, Message: "required privacy masking could not run, so the request was not sent"}
		rc.rec.Privacy = &privacy.Summary{PolicyID: id, Blocked: true}
		rc.outcome.Blocked, rc.outcome.BlockReason = true, e.Message
		g.finish(ctx, rc, "blocked", e, nil)
		g.writeError(w, rc.inbound, e, rc.id)
		g.Log.Error("privacy.masking_failed", "request_id", rc.id, "policy_id", id, "error", err.Error())
		return false
	}
	rc.rec.Privacy = &privacy.Summary{PolicyID: id, Unmasked: true}
	g.Log.Warn("privacy.unmasked_fallback", "request_id", rc.id, "policy_id", id)
	return true
}

// restore wraps an attempt result so placeholders the model echoes are
// restored for this client.
func (g *Gateway) restore(ctx context.Context, rc *reqCtx, res *executor.Result) {
	if rc.privacy == nil || res == nil {
		return
	}
	r := rc.privacy.NewRestorer(g.Privacy.Vault, rc.privScope)
	if res.Response != nil {
		r.Response(res.Response)
	}
	if res.Stream != nil {
		res.Stream = r.Stream(ctx, res.Stream)
	}
}
