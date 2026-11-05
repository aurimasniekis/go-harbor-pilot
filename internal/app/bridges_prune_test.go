package app

import (
	"context"
	"testing"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/gateway"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

type fakePruner struct{ got map[string]bool }

func (f *fakePruner) Execute(context.Context, executor.Target, credentials.Secret, executor.Request, bool) (*executor.Result, *executor.AttemptError) {
	return nil, nil
}
func (f *fakePruner) PruneAccounts(active map[string]bool) { f.got = active }

// Bridges are told which provider accounts still exist, so files of deleted
// accounts (sign-in data, transcripts) are removed.
func TestPruneBridgeAccounts(t *testing.T) {
	f := &fakePruner{}
	a := &App{Gateway: &gateway.Gateway{Bridges: map[string]executor.Executor{cp.ProviderCodex: f}}}
	a.pruneBridgeAccounts(&cp.Snapshot{Accounts: map[string]*cp.Res[cp.AccountSpec]{"acc_1": {ID: "acc_1"}}})
	if len(f.got) != 1 || !f.got["acc_1"] {
		t.Fatalf("active accounts %v", f.got)
	}
}
