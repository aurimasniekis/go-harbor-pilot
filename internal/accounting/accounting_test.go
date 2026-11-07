package accounting

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

func newService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(t.TempDir(), "h.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, clock.NewFake(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)), nil), db
}

func ledgerRefs(t *testing.T, db *store.DB, id string) map[string]string {
	t.Helper()
	rows, err := db.R.Query(`SELECT key, value FROM ledger_refs WHERE ledger_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRecordIndexesTrustedRefs(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	refs := map[string]string{"spaceman.run": "run_a", "session": "run_a"}

	t.Run("refs are written with the row", func(t *testing.T) {
		id, err := s.Record(ctx, Entry{Kind: "usage", RequestID: "req_1", UsageSource: "upstream_reported", IdempotencyKey: "usage:att_1", Refs: refs})
		if err != nil {
			t.Fatal(err)
		}
		if got := ledgerRefs(t, db, id); !reflect.DeepEqual(got, refs) {
			t.Fatalf("refs %v", got)
		}
	})
	t.Run("an idempotent repeat adds nothing", func(t *testing.T) {
		id, err := s.Record(ctx, Entry{Kind: "usage", RequestID: "req_1", UsageSource: "upstream_reported", IdempotencyKey: "usage:att_1",
			Refs: map[string]string{"spaceman.run": "run_other"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := ledgerRefs(t, db, id); !reflect.DeepEqual(got, refs) {
			t.Fatalf("refs after repeat %v", got)
		}
		var n int
		_ = db.R.QueryRow(`SELECT count(*) FROM ledger_refs`).Scan(&n)
		if n != 2 {
			t.Fatalf("ledger_refs rows %d", n)
		}
	})
	t.Run("a correction inherits the corrected row's refs", func(t *testing.T) {
		var orig string
		_ = db.R.QueryRow(`SELECT id FROM ledger WHERE idempotency_key = 'usage:att_1'`).Scan(&orig)
		id, err := s.Record(ctx, Entry{Kind: "correction", RequestID: "req_1", UsageSource: "provider_reported", CorrectsID: orig, IdempotencyKey: "fix:1"})
		if err != nil {
			t.Fatal(err)
		}
		if got := ledgerRefs(t, db, id); !reflect.DeepEqual(got, refs) {
			t.Fatalf("correction refs %v", got)
		}
	})
	t.Run("empty values are not indexed", func(t *testing.T) {
		for i, in := range []map[string]string{{"spaceman.run": "run_e", "label": ""}, {"label": ""}} {
			id, err := s.Record(ctx, Entry{Kind: "usage", UsageSource: "upstream_reported", IdempotencyKey: "empty:" + string(rune('a'+i)), Refs: in})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{}
			if in["spaceman.run"] != "" {
				want["spaceman.run"] = in["spaceman.run"]
			}
			if got := ledgerRefs(t, db, id); !reflect.DeepEqual(got, want) {
				t.Fatalf("refs %v, want %v", got, want)
			}
		}
	})
	t.Run("an entry without refs has none", func(t *testing.T) {
		id, err := s.Record(ctx, Entry{Kind: "usage", UsageSource: "upstream_reported", IdempotencyKey: "probe:1"})
		if err != nil {
			t.Fatal(err)
		}
		if got := ledgerRefs(t, db, id); len(got) != 0 {
			t.Fatalf("refs %v", got)
		}
	})
}

func TestRecoverStartupCopiesRequestRefs(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	for _, q := range []string{
		`INSERT INTO requests(id, created_at, trusted_refs, inbound_protocol, stream, status) VALUES ('req_1', 1, '{"spaceman.run":"run_a"}', 'anthropic', 0, 'in_progress')`,
		`INSERT INTO reservations(id, request_id, account_id, binding_id, status, created_at, lease_until) VALUES ('rsv_1', 'req_1', 'acc_1', 'cbd_1', 'active', 1, 2)`,
		// The request row of this one was never written (dropped telemetry).
		`INSERT INTO reservations(id, request_id, account_id, binding_id, status, created_at, lease_until) VALUES ('rsv_2', 'req_2', 'acc_1', 'cbd_1', 'active', 1, 2)`,
	} {
		if _, err := db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RecoverStartup(ctx); err != nil || n != 2 {
		t.Fatalf("recovered %d %v", n, err)
	}
	for req, want := range map[string]map[string]string{"req_1": {"spaceman.run": "run_a"}, "req_2": {}} {
		var id string
		if err := db.R.QueryRow(`SELECT id FROM ledger WHERE request_id = ? AND kind = 'unknown_usage'`, req).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if got := ledgerRefs(t, db, id); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s refs %v, want %v", req, got, want)
		}
	}
}
