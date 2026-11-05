package controlplane

import (
	"errors"
	"testing"
	"time"
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
