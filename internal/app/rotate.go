package app

import (
	"context"
	"database/sql"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// SealedColumn names a table column holding crypto.Seal envelopes and how to
// derive each row's associated data. Feature packages append to
// SealedColumns so `keys rotate` re-wraps every stored secret.
type SealedColumn struct {
	Table, IDColumn, Column string
	AAD                     func(id string) string
}

// SealedColumns lists every envelope-encrypted column.
var SealedColumns = []SealedColumn{
	{Table: "oidc_providers", IDColumn: "id", Column: "client_secret", AAD: func(id string) string { return "oidc-client-secret|" + id }},
	{Table: "resource_secrets", IDColumn: "resource_id || '|' || field", Column: "envelope", AAD: func(id string) string { return "resource-secret|" + id }},
}

// RotateSecrets re-wraps server keys and all sealed columns under the
// keyring's current KEK. Ciphertexts are unchanged; only DEK wrapping moves.
func RotateSecrets(ctx context.Context, db *store.DB, kr *crypto.Keyring) (int, error) {
	n, err := store.NewServerKeys(db, kr).RewrapAll(ctx)
	if err != nil {
		return n, err
	}
	for _, c := range SealedColumns {
		type row struct{ id, env string }
		// Table and column names come from the static SealedColumns list.
		rows, err := store.Query(ctx, db.W, func(r *sql.Rows) (row, error) {
			var x row
			err := r.Scan(&x.id, &x.env)
			return x, err
		}, `SELECT `+c.IDColumn+`, `+c.Column+` FROM `+c.Table+` WHERE `+c.Column+` IS NOT NULL`) //nolint:gosec // identifiers are a static allowlist
		if err != nil {
			return n, err
		}
		err = db.Tx(ctx, func(tx *sql.Tx) error {
			for _, r := range rows {
				re, err := kr.Rewrap(r.env, c.AAD(r.id))
				if err != nil {
					return err
				}
				if re == r.env {
					continue
				}
				if _, err := tx.ExecContext(ctx, `UPDATE `+c.Table+` SET `+c.Column+` = ? WHERE `+c.IDColumn+` = ?`, re, r.id); err != nil { //nolint:gosec // identifiers are a static allowlist
					return err
				}
				n++
			}
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
