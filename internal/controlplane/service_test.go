package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

type fx struct {
	t   *testing.T
	svc *Service
	db  *store.DB
	ctx context.Context
	clk *clock.Fake
}

func newFx(t *testing.T) *fx {
	t.Helper()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Kind: auth.KindUser, ID: "usr_test"})
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "h.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	k, _ := crypto.ParseKEK(crypto.GenerateKEKString())
	kr, _ := crypto.NewKeyring(k)
	clk := clock.NewFake(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	svc, err := New(ctx, Options{DB: db, Keyring: kr, Keys: store.NewServerKeys(db, kr), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	return &fx{t: t, svc: svc, db: db, ctx: ctx, clk: clk}
}

func js(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (f *fx) create(kind Kind, name string, spec any, secrets SecretInput) *Object {
	f.t.Helper()
	o, err := f.svc.Create(f.ctx, kind, name, js(spec), secrets, Mutation{ManagedBy: ManagedUI})
	if err != nil {
		f.t.Fatalf("create %s %s: %v", kind.KindName(), name, err)
	}
	return o
}

// seed builds provider -> account -> pool -> alias -> plan (published) -> binding.
func (f *fx) seed() (prov, acc, pool, alias, plan, binding *Object) {
	key := "sk-test-secret-value"
	prov = f.create(ProviderDef, "anthropic", ProviderSpec{Type: ProviderAnthropic, DisplayName: "Anthropic",
		Models: []ModelSpec{{ID: "claude-sonnet-4-5", Capabilities: []string{"tools", "streaming"}}}}, nil)
	acc = f.create(AccountDef, "anthropic-main", AccountSpec{ProviderID: prov.ID, Billing: BillingMetered, Enabled: true}, SecretInput{"credential": &key})
	pool = f.create(PoolDef, "sonnet", PoolSpec{Members: []PoolMember{{AccountID: acc.ID, Model: "claude-sonnet-4-5"}}}, nil)
	alias = f.create(AliasDef, "coding/default", AliasSpec{PoolID: pool.ID}, nil)
	plan = f.create(PlanDef, "default", PlanSpec{DisplayName: "Default"}, nil)
	v, err := f.svc.SaveDraft(f.ctx, plan.ID, "", js(PlanDocument{ModelMappings: []ModelMapping{{Match: "claude-*", AliasID: alias.ID}}}), "first", Mutation{})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.svc.Publish(f.ctx, plan.ID, v.ID, Mutation{}); err != nil {
		f.t.Fatal(err)
	}
	binding = f.create(BindingDef, "laptop", BindingSpec{PlanID: plan.ID, Enabled: true, ExpectedHarness: "claude_code"}, nil)
	return
}

func TestCreateCanonicalizesAndSnapshots(t *testing.T) {
	f := newFx(t)
	prov, acc, pool, alias, plan, binding := f.seed()
	var spec ProviderSpec
	_ = json.Unmarshal(prov.Spec, &spec)
	if spec.BaseURL != "https://api.anthropic.com" || spec.Classification != "official" {
		t.Fatalf("defaults not applied: %+v", spec)
	}
	snap := f.svc.Snapshot()
	if snap.AliasPool[alias.ID] != pool.ID || snap.Bindings[binding.ID] == nil || snap.Accounts[acc.ID] == nil {
		t.Fatal("snapshot missing objects")
	}
	a, how, err := snap.ResolveModel(snap.Plans[plan.ID], "claude-sonnet-4-5-20251001")
	if err != nil || a.ID != alias.ID || how != "mapping:claude-*" {
		t.Fatalf("resolve: %v %s %v", a, how, err)
	}
	if a, how, _ := snap.ResolveModel(snap.Plans[plan.ID], "coding/default"); a.ID != alias.ID || how != "alias_name" {
		t.Fatalf("alias name resolution: %s", how)
	}
	if _, _, err := snap.ResolveModel(snap.Plans[plan.ID], "gpt-5"); err == nil {
		t.Fatal("unmapped model resolved")
	}
	// Secrets are never part of the spec and are reported as presence only.
	if strings.Contains(string(acc.Spec), "sk-test") || !acc.Secrets["credential"].Set || acc.Secrets["credential"].Source != "stored" {
		t.Fatalf("secret handling: %s %+v", acc.Spec, acc.Secrets)
	}
	got, err := f.svc.ResolveSecret(f.ctx, AccountDef, acc.ID, "credential")
	if err != nil || got.Reveal() != "sk-test-secret-value" {
		t.Fatalf("resolve secret: %v", err)
	}
}

func TestValidationAndIntegrity(t *testing.T) {
	f := newFx(t)
	prov, acc, pool, alias, plan, _ := f.seed()
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "bad name", run: func() error {
			_, err := f.svc.Create(f.ctx, WorkspaceDef, "Bad Name", js(WorkspaceSpec{}), nil, Mutation{})
			return err
		}, want: "body.name"},
		{name: "unknown field", run: func() error {
			_, err := f.svc.Create(f.ctx, WorkspaceDef, "w", json.RawMessage(`{"nope":1}`), nil, Mutation{})
			return err
		}, want: "unknown field"},
		{name: "dangling reference", run: func() error {
			_, err := f.svc.Create(f.ctx, AccountDef, "x", js(AccountSpec{ProviderID: "prv_01m6nfgfe4nh15xk7qm326w92v", Billing: BillingMetered}), nil, Mutation{})
			return err
		}, want: "references unknown"},
		{name: "wrong reference kind", run: func() error {
			_, err := f.svc.Create(f.ctx, AccountDef, "x", js(AccountSpec{ProviderID: pool.ID, Billing: BillingMetered}), nil, Mutation{})
			return err
		}, want: "references unknown"},
		{name: "auth header refused", run: func() error {
			_, err := f.svc.Create(f.ctx, ProviderDef, "p2", js(ProviderSpec{Type: ProviderOpenAI, Headers: map[string]string{"Authorization": "x"}}), nil, Mutation{})
			return err
		}, want: "authentication"},
		{name: "credentials in base url", run: func() error {
			_, err := f.svc.Create(f.ctx, ProviderDef, "p3", js(ProviderSpec{Type: ProviderOpenAICompatible, BaseURL: "https://u:p@example.test/v1"}), nil, Mutation{})
			return err
		}, want: "embed credentials"},
		{name: "pool member model not declared", run: func() error {
			_, err := f.svc.Create(f.ctx, PoolDef, "p", js(PoolSpec{Members: []PoolMember{{AccountID: acc.ID, Model: "gpt-5"}}}), nil, Mutation{})
			return err
		}, want: "not declared by provider"},
		{name: "secret and ref both set", run: func() error {
			v := "abc"
			_, err := f.svc.Create(f.ctx, AccountDef, "both", js(AccountSpec{ProviderID: prov.ID, Billing: BillingMetered, CredentialRef: "env:HP_SECRET_X"}), SecretInput{"credential": &v}, Mutation{})
			return err
		}, want: "not both"},
		{name: "delete referenced provider", run: func() error {
			return f.svc.Delete(f.ctx, ProviderDef, prov.ID, Mutation{})
		}, want: "referenced"},
		{name: "delete alias used by published plan", run: func() error {
			// Published plan documents block the delete like resource refs.
			return f.svc.Delete(f.ctx, AliasDef, alias.ID, Mutation{})
		}, want: "referenced"},
		{name: "duplicate name", run: func() error {
			_, err := f.svc.Create(f.ctx, PlanDef, "default", js(PlanSpec{}), nil, Mutation{})
			return err
		}, want: "already exists"},
	}
	_ = plan
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error()+fieldErrs(err), tt.want) {
				t.Fatalf("err=%v %s want %q", err, fieldErrs(err), tt.want)
			}
		})
	}
	// Failed mutations leave no trace in the snapshot or history.
	if _, ok := f.svc.Snapshot().AliasByName["coding/default"]; !ok {
		t.Fatal("alias was deleted despite failing compile")
	}
}

func fieldErrs(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		b, _ := json.Marshal(ve.Errors)
		return string(b)
	}
	return ""
}

func TestAliasCycleRejected(t *testing.T) {
	f := newFx(t)
	_, _, pool, _, _, _ := f.seed()
	a := f.create(AliasDef, "a", AliasSpec{PoolID: pool.ID}, nil)
	b := f.create(AliasDef, "b", AliasSpec{AliasID: a.ID}, nil)
	_, err := f.svc.Replace(f.ctx, AliasDef, a.ID, "a", js(AliasSpec{AliasID: b.ID}), nil, Mutation{})
	if err == nil || !strings.Contains(err.Error(), "alias cycle: ") || strings.Count(fieldErrs(err), "alias cycle") != 1 {
		t.Fatalf("cycle not rejected: %v %s", err, fieldErrs(err))
	}
	if f.svc.Snapshot().AliasPool[b.ID] != pool.ID {
		t.Fatal("snapshot changed after rejected cycle")
	}
}

func TestRevisionsAndDrift(t *testing.T) {
	f := newFx(t)
	ws, err := f.svc.Create(f.ctx, WorkspaceDef, "eng", js(WorkspaceSpec{DisplayName: "Eng"}), nil, Mutation{ManagedBy: ManagedTerraform})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Replace(f.ctx, WorkspaceDef, ws.ID, "", js(WorkspaceSpec{DisplayName: "X"}), nil, Mutation{IfMatch: `"r7"`}); err == nil {
		t.Fatal("stale If-Match accepted")
	}
	ui, err := f.svc.Replace(f.ctx, WorkspaceDef, ws.ID, "", js(WorkspaceSpec{DisplayName: "Edited in UI"}), nil, Mutation{ManagedBy: ManagedUI, IfMatch: `"r1"`})
	if err != nil {
		t.Fatal(err)
	}
	if ui.ManagedBy != ManagedTerraform || ui.DriftState != "drifted" || ui.Revision != 2 {
		t.Fatalf("drift not recorded: %+v", ui)
	}
	tf, err := f.svc.Replace(f.ctx, WorkspaceDef, ws.ID, "", js(WorkspaceSpec{DisplayName: "Eng"}), nil, Mutation{ManagedBy: ManagedTerraform})
	if err != nil || tf.DriftState != "in_sync" {
		t.Fatalf("terraform apply should restore sync: %+v %v", tf, err)
	}
	h, err := f.svc.History(f.ctx, ws.ID, 10)
	if err != nil || len(h) != 3 || h[0].Revision != 3 {
		t.Fatalf("history: %+v %v", h, err)
	}
	if _, err := f.svc.Replace(f.ctx, WorkspaceDef, ws.ID, "renamed", js(WorkspaceSpec{}), nil, Mutation{}); err == nil {
		t.Fatal("rename accepted")
	}
}

func TestPlanVersionsPublishRollback(t *testing.T) {
	f := newFx(t)
	_, _, _, alias, plan, _ := f.seed()
	snap := f.svc.Snapshot()
	v1 := snap.Plans[plan.ID].Published
	d2, err := f.svc.SaveDraft(f.ctx, plan.ID, "", js(PlanDocument{DefaultAliasID: alias.ID}), "default everything", Mutation{})
	if err != nil {
		t.Fatal(err)
	}
	if f.svc.Snapshot().Plans[plan.ID].Published.ID != v1.ID {
		t.Fatal("draft affected published routing")
	}
	bad, err := f.svc.SaveDraft(f.ctx, plan.ID, "", js(PlanDocument{Fallback: FallbackPolicy{Steps: []string{"stop", "retry"}}}), "", Mutation{})
	if err == nil {
		t.Fatalf("invalid draft saved: %+v", bad)
	}
	if _, err := f.svc.Publish(f.ctx, plan.ID, d2.ID, Mutation{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Snapshot().ResolveModel(f.svc.Snapshot().Plans[plan.ID], "anything"); err != nil {
		t.Fatalf("default alias not active: %v", err)
	}
	if _, err := f.svc.Publish(f.ctx, plan.ID, d2.ID, Mutation{}); err == nil {
		t.Fatal("republishing a published version accepted")
	}
	rb, err := f.svc.Rollback(f.ctx, plan.ID, v1.ID, Mutation{})
	if err != nil {
		t.Fatal(err)
	}
	if rb.RollbackOf != v1.ID || rb.Version != 3 || rb.Status != "published" {
		t.Fatalf("rollback: %+v", rb)
	}
	vs, _ := f.svc.ListVersions(f.ctx, plan.ID)
	statuses := []string{}
	for _, v := range vs {
		statuses = append(statuses, v.Status)
	}
	if strings.Join(statuses, ",") != "published,superseded,superseded" {
		t.Fatalf("statuses %v", statuses)
	}
	if _, _, err := f.svc.Snapshot().ResolveModel(f.svc.Snapshot().Plans[plan.ID], "anything"); err == nil {
		t.Fatal("rollback did not restore v1 semantics")
	}
}

func TestGatewayCredentials(t *testing.T) {
	f := newFx(t)
	_, _, _, _, _, binding := f.seed()
	parent, plain, err := f.svc.IssueCredential(f.ctx, binding.ID, CredentialInput{Name: "orchestrator", Scopes: []string{ScopeInvoke, ScopeMint}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "hpk_"+parent.Prefix+"_") {
		t.Fatalf("token format %q", plain)
	}
	ac, err := f.svc.AuthenticateGateway(f.ctx, plain)
	if err != nil || ac.BindingID != binding.ID || !ac.Has(ScopeMint) {
		t.Fatalf("auth: %+v %v", ac, err)
	}
	child, childPlain, err := f.svc.MintChild(f.ctx, ac, CredentialInput{Name: "run", Scopes: []string{ScopeInvoke},
		TTL: time.Hour, BoundRefs: map[string]string{"spaceman.run": "run_01abc"}})
	if err != nil || child.ParentID != parent.ID || child.BoundRefs["spaceman.run"] != "run_01abc" {
		t.Fatalf("mint child: %+v %v", child, err)
	}
	cac, _ := f.svc.AuthenticateGateway(f.ctx, childPlain)
	if _, _, err := f.svc.MintChild(f.ctx, cac, CredentialInput{Name: "x"}); err == nil {
		t.Fatal("child minted a grandchild")
	}
	if _, _, err := f.svc.MintChild(f.ctx, ac, CredentialInput{Name: "x", Scopes: []string{ScopeUsage}}); err == nil {
		t.Fatal("child widened scopes")
	}
	f.clk.Advance(2 * time.Hour)
	if _, err := f.svc.AuthenticateGateway(f.ctx, childPlain); err == nil {
		t.Fatal("expired child accepted")
	}
	if err := f.svc.RevokeCredential(f.ctx, binding.ID, parent.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthenticateGateway(f.ctx, plain); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked parent accepted: %v", err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	src := newFx(t)
	src.seed()
	b, err := src.svc.Export(src.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "sk-test") || strings.Contains(string(raw), `"acc_`) {
		t.Fatalf("bundle leaks secrets or raw ids: %s", raw)
	}
	dst := newFx(t)
	dry, err := dst.svc.Import(dst.ctx, b, ImportOptions{DryRun: true, PublishPlans: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dst.svc.Snapshot().Providers) != 0 || len(dry.Actions) == 0 {
		t.Fatalf("dry run committed or planned nothing: %+v", dry)
	}
	res, err := dst.svc.Import(dst.ctx, b, ImportOptions{PublishPlans: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected a warning about the non-portable stored credential")
	}
	snap := dst.svc.Snapshot()
	var plan *PlanView
	for _, p := range snap.Plans {
		plan = p
	}
	if a, _, err := snap.ResolveModel(plan, "claude-x"); err != nil || a.Name != "coding/default" {
		t.Fatalf("imported plan does not route: %v", err)
	}
	// Re-import is a no-op.
	again, err := dst.svc.Import(dst.ctx, b, ImportOptions{PublishPlans: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range again.Actions {
		if a.Action != "unchanged" {
			t.Fatalf("re-import not idempotent: %+v", a)
		}
	}
}
