package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

func (g *Gateway) authed(w http.ResponseWriter, r *http.Request, p ir.Protocol, scope string) (*cp.AuthenticatedCredential, *cp.Res[cp.BindingSpec], *cp.Snapshot, bool) {
	cred, err := g.CP.AuthenticateGateway(r.Context(), credentialFrom(r))
	if err != nil {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrAuthentication, Message: "invalid, expired or revoked gateway credential"}, "")
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

// countTokens answers token-count requests. Harbor Pilot does not hold
// provider tokenizers, so the figure is a labelled character-based estimate
// (X-HP-Token-Count: estimated); it is never recorded as usage.
func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request) {
	p := ir.AnthropicMessages
	if _, _, _, ok := g.authed(w, r, p, cp.ScopeInvoke); !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, g.Limits.MaxRequestBytes+1))
	if err != nil || int64(len(body)) > g.Limits.MaxRequestBytes {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInvalidRequest, Message: "request body too large"}, "")
		return
	}
	req, err := anthropic.ParseRequest(body, anthropic.DefaultLimits)
	if err != nil {
		req = nil
	}
	chars := 0
	if req != nil {
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
	} else {
		chars = len(body)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-HP-Token-Count", "estimated")
	_, _ = w.Write(wire.MustMarshal(map[string]any{"input_tokens": (chars + 3) / 4}))
}

// mintChild issues a short-lived child credential (tokens:mint scope).
func (g *Gateway) mintChild(w http.ResponseWriter, r *http.Request) {
	p := ir.OpenAIChat
	cred, err := g.CP.AuthenticateGateway(r.Context(), credentialFrom(r))
	if err != nil {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrAuthentication, Message: "invalid, expired or revoked gateway credential"}, "")
		return
	}
	var in struct {
		Name       string            `json:"name"`
		Scopes     []string          `json:"scopes"`
		TTLSeconds int64             `json:"ttl_seconds"`
		BoundRefs  map[string]string `json:"bound_refs"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		g.writeError(w, p, &ir.Error{Kind: ir.ErrInvalidRequest, Message: "invalid body: " + err.Error()}, "")
		return
	}
	c, tok, err := g.CP.MintChild(r.Context(), cred, cp.CredentialInput{Name: in.Name, Scopes: in.Scopes, TTL: time.Duration(in.TTLSeconds) * time.Second,
		BoundRefs: in.BoundRefs, ManagedBy: cp.ManagedAPI})
	if err != nil {
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

func asValidation(err error, target **cp.ValidationError) bool {
	ve, ok := err.(*cp.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}
