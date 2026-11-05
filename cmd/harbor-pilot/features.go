package main

import (
	"context"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// setupFeatures constructs feature modules (control plane, gateway, quota,
// events, notifications, …) and registers their routes, operations and
// background workers on the app.
func setupFeatures(_ context.Context, a *app.App) error {
	for _, reg := range featureAPI(a) {
		a.AddAPI(reg)
	}
	return nil
}

// featureAPI lists feature API registrations (deps captured lazily).
func featureAPI(_ *app.App) []func(*api.API) {
	return nil
}

// specRegistrations returns every registration for OpenAPI rendering.
func specRegistrations(a *app.App) []func(*api.API) {
	return append(a.CoreAPI(), featureAPI(a)...)
}

func rotateAll(ctx context.Context, db *store.DB, kr *crypto.Keyring) (int, error) {
	return app.RotateSecrets(ctx, db, kr)
}
