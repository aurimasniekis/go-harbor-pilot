package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

// Child credentials may bind only the client's own or allowed projects and
// cannot replace references the operator configured on the client.
func TestMintChildConfinesBoundRefs(t *testing.T) {
	f := newFx(t)
	_, _, _, _, plan, _ := f.seed()
	ws := f.create(WorkspaceDef, "acme", WorkspaceSpec{DisplayName: "Acme"}, nil)
	own := f.create(ProjectDef, "web", ProjectSpec{WorkspaceID: ws.ID, DisplayName: "Web"}, nil)
	allowed := f.create(ProjectDef, "docs", ProjectSpec{WorkspaceID: ws.ID, DisplayName: "Docs"}, nil)
	other := f.create(ProjectDef, "data", ProjectSpec{WorkspaceID: ws.ID, DisplayName: "Data"}, nil)
	b := f.create(BindingDef, "runs", BindingSpec{PlanID: plan.ID, Enabled: true, ProjectID: own.ID, AllowedProjectIDs: []string{own.ID, allowed.ID},
		ExternalRefs: map[string]string{"spaceman.space": "spc_a"}}, nil)
	_, plain, err := f.svc.IssueCredential(f.ctx, b.ID, CredentialInput{Name: "orchestrator", Scopes: []string{ScopeInvoke, ScopeMint}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := f.svc.AuthenticateGateway(f.ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		refs map[string]string
		ok   bool
	}{
		{"own project", map[string]string{"project_id": own.ID}, true},
		{"allowed project", map[string]string{"project_id": allowed.ID}, true},
		{"another team's project", map[string]string{"project_id": other.ID}, false},
		{"unknown project", map[string]string{"project_id": "prj_unknown"}, false},
		{"run reference", map[string]string{"spaceman.run": "run_1"}, true},
		{"same configured reference", map[string]string{"spaceman.space": "spc_a"}, true},
		{"overrides configured reference", map[string]string{"spaceman.space": "spc_other"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := f.svc.MintChild(f.ctx, parent, CredentialInput{Name: "run", Scopes: []string{ScopeInvoke}, TTL: time.Hour, BoundRefs: tt.refs})
			var ve *ValidationError
			if tt.ok && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !tt.ok && !errors.As(err, &ve) {
				t.Fatalf("accepted or wrong error: %v", err)
			}
		})
	}
}

// A minted child records its parent credential as the creator and as the
// audit actor, not the anonymous gateway caller.
func TestMintChildRecordsParentAsCreator(t *testing.T) {
	f := newFx(t)
	_, _, _, _, plan, _ := f.seed()
	b := f.create(BindingDef, "runs", BindingSpec{PlanID: plan.ID, Enabled: true}, nil)
	_, plain, err := f.svc.IssueCredential(f.ctx, b.ID, CredentialInput{Name: "orchestrator", Scopes: []string{ScopeInvoke, ScopeMint}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := f.svc.AuthenticateGateway(f.ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	// The gateway mint endpoint runs without a console principal.
	ctx := auth.WithPrincipal(context.Background(), auth.Anonymous)
	child, _, err := f.svc.MintChild(ctx, parent, CredentialInput{Name: "run", Scopes: []string{ScopeInvoke}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if child.CreatedBy != parent.ID {
		t.Errorf("created_by = %q, want parent %q", child.CreatedBy, parent.ID)
	}
	var actor, kind string
	if err := f.db.R.QueryRowContext(f.ctx, `SELECT actor_id, actor_kind FROM audit_log WHERE action = 'gateway_credential.issue'
		AND json_extract(details, '$.credential_id') = ?`, child.ID).Scan(&actor, &kind); err != nil {
		t.Fatal(err)
	}
	if actor != parent.ID || kind != string(auth.KindCredential) {
		t.Errorf("audit actor = %s/%s, want %s/%s", kind, actor, auth.KindCredential, parent.ID)
	}
}
