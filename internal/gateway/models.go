package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ratelimit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// authFailuresPerMinute bounds failed credential checks per address; valid
// credentials are never limited by this.
const authFailuresPerMinute, authFailureBurst = 60, 60

// authenticate checks the gateway credential, refusing addresses with too
// many recent failures. Only failures consume the address's budget.
func (g *Gateway) authenticate(r *http.Request) (*cp.AuthenticatedCredential, *ir.Error) {
	g.authFailsOnce.Do(func() { g.authFails = ratelimit.New(authFailuresPerMinute, authFailureBurst, time.Now) })
	key := "gw:" + reqmeta.RateKey(reqmeta.From(r.Context()).IP)
	if ok, retry := g.authFails.Peek(key); !ok {
		return nil, &ir.Error{Kind: ir.ErrRateLimited, Message: "too many failed authentication attempts from this address",
			RetryAfterSeconds: int(retry/time.Second) + 1}
	}
	cred, err := g.CP.AuthenticateGateway(r.Context(), credentialFrom(r))
	if err != nil {
		g.authFails.Allow(key)
		return nil, &ir.Error{Kind: ir.ErrAuthentication, Message: "invalid, expired or revoked gateway credential"}
	}
	return cred, nil
}

func (g *Gateway) authed(w http.ResponseWriter, r *http.Request, p ir.Protocol, scope string) (*cp.AuthenticatedCredential, *cp.Res[cp.BindingSpec], *cp.Snapshot, bool) {
	cred, aerr := g.authenticate(r)
	if aerr != nil {
		g.writeError(w, p, aerr, "")
		return nil, nil, nil, false
	}
	if !cred.Has(scope) {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrPermission, Message: "credential lacks the " + scope + " scope"}, "")
		return nil, nil, nil, false
	}
	snap := g.CP.Snapshot()
	b := snap.Bindings[cred.BindingID]
	if b == nil || !b.Spec.Enabled {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrPermission, Message: "this client is disabled"}, "")
		return nil, nil, nil, false
	}
	return cred, b, snap, true
}

// visibleModels lists model names a client may request.
func visibleModels(snap *cp.Snapshot, b *cp.Res[cp.BindingSpec]) []string {
	names := snap.MappedModelNames(snap.Plans[b.Spec.PlanID])
	if len(b.Spec.AllowedModels) == 0 {
		return names
	}
	out := []string{}
	for _, n := range names {
		for _, a := range b.Spec.AllowedModels {
			if a == n {
				out = append(out, n)
			}
		}
	}
	return out
}

func (g *Gateway) listModelsAuto(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("anthropic-version") != "" || r.Header.Get("x-api-key") != "" {
		g.listModels(ir.AnthropicMessages)(w, r)
		return
	}
	g.listModels(ir.OpenAIChat)(w, r)
}

func (g *Gateway) listModels(p ir.Protocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, b, snap, ok := g.authed(w, r, p, cp.ScopeModels)
		if !ok {
			return
		}
		names := visibleModels(snap, b)
		created := time.Unix(0, 0).UTC()
		var body any
		switch p {
		case ir.AnthropicMessages:
			data := []any{}
			for _, n := range names {
				data = append(data, map[string]any{"type": "model", "id": n, "display_name": n, "created_at": created.Format(time.RFC3339)})
			}
			first, last := "", ""
			if len(names) > 0 {
				first, last = names[0], names[len(names)-1]
			}
			body = map[string]any{"data": data, "has_more": false, "first_id": first, "last_id": last}
		case ir.Gemini:
			ms := []any{}
			for _, n := range names {
				ms = append(ms, map[string]any{"name": "models/" + n, "displayName": n, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent", "countTokens"}})
			}
			body = map[string]any{"models": ms}
		default:
			data := []any{}
			for _, n := range names {
				data = append(data, map[string]any{"id": n, "object": "model", "created": 0, "owned_by": "harbor-pilot"})
			}
			body = map[string]any{"object": "list", "data": data}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(wire.MustMarshal(body))
	}
}

// countTokens answers Anthropic token-count requests. Harbor Pilot does not
// hold provider tokenizers, so the figure is a labelled character-based
// estimate (X-HP-Token-Count: estimated); it is never recorded as usage.
func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request) {
	p := ir.AnthropicMessages
	if _, _, _, ok := g.authed(w, r, p, cp.ScopeInvoke); !ok {
		return
	}
	body, ok := g.readCountBody(w, r, p)
	if !ok {
		return
	}
	req, err := anthropic.ParseRequest(body, anthropic.DefaultLimits)
	if err != nil {
		req = nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-HP-Token-Count", "estimated")
	_, _ = w.Write(wire.MustMarshal(map[string]any{"input_tokens": estimateTokens(req, body)}))
}

// geminiCountTokens answers models/{model}:countTokens in Gemini's own
// shape ({"totalTokens": N}), with the same labelled estimate.
func (g *Gateway) geminiCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	p := ir.Gemini
	if _, _, _, ok := g.authed(w, r, p, cp.ScopeInvoke); !ok {
		return
	}
	body, ok := g.readCountBody(w, r, p)
	if !ok {
		return
	}
	// Either {"contents": …} or {"generateContentRequest": {…}}.
	var wrapped struct {
		GenerateContentRequest json.RawMessage `json:"generateContentRequest"`
	}
	inner := body
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped.GenerateContentRequest) > 0 {
		inner = wrapped.GenerateContentRequest
	}
	req, err := gemini.ParseRequest(inner, model, false)
	if err != nil {
		req = nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-HP-Token-Count", "estimated")
	_, _ = w.Write(wire.MustMarshal(map[string]any{"totalTokens": estimateTokens(req, inner)}))
}

func (g *Gateway) readCountBody(w http.ResponseWriter, r *http.Request, p ir.Protocol) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, g.Limits.MaxRequestBytes+1))
	if err != nil || int64(len(body)) > g.Limits.MaxRequestBytes {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInvalidRequest, Message: "request body too large"}, "")
		return nil, false
	}
	return body, true
}

// estimateTokens is about four characters per token over the text the
// model would read; an unparsable body counts as a whole.
func estimateTokens(req *ir.Request, body []byte) int {
	if req == nil {
		return (len(body) + 3) / 4
	}
	chars := 0
	for _, s := range req.System {
		chars += len(s.Text)
	}
	for _, m := range req.Messages {
		for _, p := range m.Parts {
			chars += len(p.Text)
			if p.ToolCall != nil {
				chars += len(p.ToolCall.Arguments) + len(p.ToolCall.Name)
			}
			if p.ToolResult != nil {
				for _, c := range p.ToolResult.Content {
					chars += len(c.Text)
				}
			}
		}
	}
	for _, t := range req.Tools {
		chars += len(t.Name) + len(t.Description) + len(t.Parameters)
	}
	return (chars + 3) / 4
}

// mintChild issues a short-lived child credential (tokens:mint scope).
func (g *Gateway) mintChild(w http.ResponseWriter, r *http.Request) {
	p := ir.OpenAIChat
	cred, aerr := g.authenticate(r)
	if aerr != nil {
		g.writeError(w, p, aerr, "")
		return
	}
	var in struct {
		Name       string            `json:"name"`
		Scopes     []string          `json:"scopes"`
		TTLSeconds int64             `json:"ttl_seconds"`
		BoundRefs  map[string]string `json:"bound_refs"`
		// CheckBudget refuses the mint when a blocking budget covering the
		// child's scope is already used up (D-043).
		CheckBudget bool `json:"check_budget"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInvalidRequest, Message: "invalid body: " + err.Error()}, "")
		return
	}
	ci := cp.CredentialInput{Name: in.Name, Scopes: in.Scopes, TTL: time.Duration(in.TTLSeconds) * time.Second,
		BoundRefs: in.BoundRefs, ManagedBy: cp.ManagedAPI}
	if in.CheckBudget {
		ci.BudgetCheck = g.mintBudgetCheck
	}
	c, tok, err := g.CP.MintChild(r.Context(), cred, ci)
	if err != nil {
		var be *accounting.BudgetExceeded
		if errors.As(err, &be) {
			writeMintBudgetError(w, be)
			return
		}
		var re *mintBudgetReadError
		if errors.As(err, &re) {
			g.Log.Error("mint.budget_check.failed", "credential_id", cred.ID, "error", re.err.Error())
			g.writeError(w, p, &ir.Error{Kind: ir.ErrInternal, Message: "could not check budgets"}, "")
			return
		}
		var ve *cp.ValidationError
		kind := ir.ErrPermission
		msg := err.Error()
		if ok := asValidation(err, &ve); ok {
			kind = ir.ErrInvalidRequest
		}
		g.writeError(w, p, &ir.Error{Kind: kind, Message: msg}, "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(wire.MustMarshal(map[string]any{"credential": c, "token": tok}))
}

// mintBudgetCheck refuses a child mint when a blocking budget covering the
// child's scope is used up. It reads budgets as GET /budgets/status and the
// request-path reservation do. A refusal writes nothing: no audit entry and
// no event (D-043).
func (g *Gateway) mintBudgetCheck(ctx context.Context, sc cp.ChildScope) error {
	if g.Budgets == nil {
		return nil
	}
	be, err := g.Budgets.Exhausted(ctx, g.DB.R, accounting.Scope{BindingID: sc.BindingID, ProjectID: sc.ProjectID, WorkspaceID: sc.WorkspaceID}, g.Clock.Now())
	if err != nil {
		return &mintBudgetReadError{err: err}
	}
	if be != nil {
		return be
	}
	return nil
}

// mintBudgetReadError is a budget check that could not read usage. The mint
// answers a generic internal error (502 in the gateway envelope); the cause
// is only logged.
type mintBudgetReadError struct{ err error }

func (e *mintBudgetReadError) Error() string { return "budget check: " + e.err.Error() }
func (e *mintBudgetReadError) Unwrap() error { return e.err }

// writeMintBudgetError answers a mint refused by check_budget: 429 in the
// gateway's OpenAI-style error envelope, with code budget_exceeded and the
// budget's id.
func writeMintBudgetError(w http.ResponseWriter, be *accounting.BudgetExceeded) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write(wire.MustMarshal(map[string]any{"error": map[string]any{
		"message": be.Detail + " (" + be.BudgetID + ")", "type": "insufficient_quota", "param": nil,
		"code": string(ir.ErrBudget), "budget_id": be.BudgetID}}))
}

func asValidation(err error, target **cp.ValidationError) bool {
	ve, ok := err.(*cp.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

// revokeChild lets a run owner (the minting parent) or the child itself
// revoke a child credential, e.g. when an agent run ends.
func (g *Gateway) revokeChild(w http.ResponseWriter, r *http.Request) {
	p := ir.OpenAIChat
	cred, aerr := g.authenticate(r)
	if aerr != nil {
		g.writeError(w, p, aerr, "")
		return
	}
	id := chi.URLParam(r, "id")
	target, err := g.CP.GetCredential(r.Context(), id)
	if err != nil || target.ParentID == "" || (target.ParentID != cred.ID && target.ID != cred.ID) {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrNotFound, Message: "no child credential with this id is owned by the caller"}, "")
		return
	}
	if err := g.CP.RevokeCredential(r.Context(), target.BindingID, target.ID, "revoked by its run owner"); err != nil {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInternal, Message: "revocation failed"}, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
