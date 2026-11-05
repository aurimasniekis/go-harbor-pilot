// Package demo seeds the deterministic, clearly synthetic demo dataset. Demo
// mode uses its own database files and never touches production data or
// credentials; every seeded identity uses the reserved example.test domain.
package demo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Demo credentials are public by design: they only ever exist in demo.db.
const (
	AdminEmail    = "admin@example.test"
	AdminPassword = "harbor-pilot-demo" //nolint:gosec // public synthetic demo credential, only valid in demo.db
	ViewerEmail   = "viewer@example.test"
)

// Seed creates the synthetic identities when the demo database is empty.
// It is idempotent.
func Seed(ctx context.Context, db *store.DB, svc *auth.Service) error {
	n, err := svc.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := svc.CreateUser(ctx, tx, auth.NewUser{Email: AdminEmail, DisplayName: "Demo Admin", Role: auth.RoleAdmin, Password: AdminPassword}); err != nil {
			return fmt.Errorf("seed demo admin: %w", err)
		}
		if _, err := svc.CreateUser(ctx, tx, auth.NewUser{Email: ViewerEmail, DisplayName: "Demo Viewer", Role: auth.RoleViewer, Password: AdminPassword}); err != nil {
			return fmt.Errorf("seed demo viewer: %w", err)
		}
		if _, err := svc.CreateServicePrincipal(ctx, tx, auth.SPInput{Name: "demo-terraform", Description: "Synthetic automation identity",
			Permissions: []auth.Permission{auth.PermConfigRead, auth.PermConfigWrite}, ManagedBy: "terraform"}, "system"); err != nil {
			return fmt.Errorf("seed demo principal: %w", err)
		}
		return nil
	})
}
