package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
)

// ErrNoProbeAccount means the provider has no usable account for a canary.
var ErrNoProbeAccount = errors.New("the provider has no enabled account with a credential")

// Probe runs an explicitly authorized live canary on a protocol path: a
// minimal synthetic request through the inspected (non-passthrough) path.
// It never replays client content. Usage is recorded in the ledger.
func (g *Gateway) Probe(ctx context.Context, providerID, path string) (*ir.Error, error) {
	inbound, upstream, ok := strings.Cut(path, ">")
	if !ok {
		return nil, errors.New("invalid path")
	}
	snap := g.CP.Snapshot()
	prov := snap.Providers[providerID]
	if prov == nil {
		return nil, errors.New("unknown provider")
	}
	var acc *cp.Res[cp.AccountSpec]
	for _, a := range snap.Accounts {
		if a.Spec.ProviderID == providerID && a.Spec.Enabled {
			acc = a
			break
		}
	}
	if acc == nil {
		return nil, ErrNoProbeAccount
	}
	model := ""
	if len(prov.Spec.Models) > 0 {
		model = prov.Spec.Models[0].ID
	}
	secret, err := g.CP.ResolveSecret(ctx, cp.AccountDef, acc.ID, "credential")
	if err != nil {
		return nil, ErrNoProbeAccount
	}
	max := 8
	req := &ir.Request{Source: ir.Protocol(inbound), Model: model, Stream: true, MaxOutputTokens: &max,
		Messages: []ir.Message{{Role: ir.RoleUser, Parts: []ir.Part{{Kind: ir.PartText, Text: "Reply with OK."}}}}}
	if g.upstreamFor(prov.Spec.Type, ir.Protocol(inbound)) != ir.Protocol(upstream) {
		return nil, errors.New("the provider does not serve this path")
	}
	t := executor.Target{Provider: prov, Account: acc, Model: model}
	if m, ok := snap.ModelOf(providerID, model); ok {
		t.ModelSpec = m
	}
	reqID := ids.New(ids.Request)
	res, aerr := g.executorFor(prov.Spec.Type).Execute(ctx, t, secret, executor.Request{IR: req, Inbound: ir.Protocol(inbound), RequestID: reqID}, true)
	if aerr != nil {
		return aerr.Err, nil
	}
	defer res.Cancel()
	r, cerr := ir.Collect(res.Stream)
	if cerr == nil && res.Err != nil {
		cerr = res.Err()
	}
	if cerr != nil {
		var ie *ir.Error
		if errors.As(cerr, &ie) {
			return ie, nil
		}
		return &ir.Error{Kind: ir.ErrProtocol, Message: cerr.Error()}, nil
	}
	_, _ = g.Acct.Record(context.WithoutCancel(ctx), accounting.Entry{Kind: "usage", RequestID: reqID, AccountID: acc.ID, ProviderID: providerID, Model: model,
		Billing: acc.Spec.Billing, UsageSource: "upstream_reported", Usage: r.Usage, CostKind: "none",
		IdempotencyKey: "canary:" + reqID, Provenance: map[string]any{"live_canary": true, "path": path}})
	return nil, nil
}
