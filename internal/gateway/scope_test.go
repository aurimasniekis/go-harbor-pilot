package gateway

import (
	"testing"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// CLI bridge processes are scoped to the client binding and the root
// credential, so child tokens of one run share a scope and other clients
// never do.
func TestBridgeScope(t *testing.T) {
	b := &cp.Res[cp.BindingSpec]{ID: "cbn_1"}
	tests := []struct {
		name string
		rc   *reqCtx
		want string
	}{
		{"credential", &reqCtx{binding: b, cred: &cp.AuthenticatedCredential{ID: "crd_a"}}, "cbn_1/crd_a"},
		{"child token uses its parent", &reqCtx{binding: b, cred: &cp.AuthenticatedCredential{ID: "crd_c", ParentID: "crd_a"}}, "cbn_1/crd_a"},
		{"unauthenticated", &reqCtx{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bridgeScope(tt.rc); got != tt.want {
				t.Fatalf("scope %q, want %q", got, tt.want)
			}
		})
	}
}
