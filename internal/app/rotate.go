package app

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

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
	{Table: "quota_oauth_tokens", IDColumn: "monitor_id", Column: "envelope", AAD: func(id string) string { return "quota-oauth-token:" + id }},
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

// RotateTraceKeys re-wraps every trace bucket DEK in traces.db under the
// current KEK. Trace ciphertexts are unchanged.
func RotateTraceKeys(ctx context.Context, traces *store.DB, kr *crypto.Keyring) (int, error) {
	type row struct {
		id, bucket, kek string
		wrapped         []byte
	}
	rows, err := store.Query(ctx, traces.W, func(r *sql.Rows) (row, error) {
		var x row
		err := r.Scan(&x.id, &x.bucket, &x.kek, &x.wrapped)
		return x, err
	}, `SELECT id, bucket, kek_id, wrapped_dek FROM trace_keys`)
	if err != nil {
		return 0, err
	}
	n := 0
	err = traces.Tx(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			if r.kek == kr.CurrentID() {
				continue
			}
			aad := "trace-key:" + r.bucket
			dek, err := kr.UnwrapDEK(r.kek, r.wrapped, aad)
			if err != nil {
				return fmt.Errorf("trace key %s: %w", r.id, err)
			}
			kek, wrapped, err := kr.WrapDEK(dek, aad)
			clear(dek)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE trace_keys SET kek_id = ?, wrapped_dek = ? WHERE id = ?`, kek, wrapped, r.id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// StaleKeyIDs lists master key ids other than the current one that still
// seal something in either database. Empty means previous keys can be
// removed.
func StaleKeyIDs(ctx context.Context, db, traces *store.DB, kr *crypto.Keyring) ([]string, error) {
	seen := map[string]bool{}
	cols := append([]SealedColumn{{Table: "server_keys", IDColumn: "purpose", Column: "envelope"}}, SealedColumns...)
	for _, c := range cols {
		envs, err := store.Query(ctx, db.R, func(r *sql.Rows) (string, error) {
			var s string
			err := r.Scan(&s)
			return s, err
		}, `SELECT `+c.Column+` FROM `+c.Table+` WHERE `+c.Column+` IS NOT NULL`) //nolint:gosec // identifiers are a static allowlist
		if err != nil {
			return nil, err
		}
		for _, env := range envs {
			if id, err := crypto.EnvelopeKEK(env); err == nil && id != kr.CurrentID() {
				seen[id] = true
			}
		}
	}
	if traces != nil {
		ids, err := store.Query(ctx, traces.R, func(r *sql.Rows) (string, error) {
			var s string
			err := r.Scan(&s)
			return s, err
		}, `SELECT DISTINCT kek_id FROM trace_keys WHERE kek_id <> ?`, kr.CurrentID())
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
