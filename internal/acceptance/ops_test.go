package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ops"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
)

type snapshot struct {
	resources, ledger, requests int64
	cost                        int64
	revision                    int64
}

func takeSnapshot(e *apptest.Env) snapshot {
	var s snapshot
	_ = e.App.DB.R.QueryRow(`SELECT (SELECT COUNT(*) FROM resources), (SELECT COUNT(*) FROM ledger), (SELECT COUNT(*) FROM requests),
		(SELECT COALESCE(SUM(cost_e10),0) FROM ledger), (SELECT COALESCE(MAX(revision),0) FROM config_revisions)`).Scan(&s.resources, &s.ledger, &s.requests, &s.cost, &s.revision)
	return s
}

// Scenario M: restart and restore preserve configuration and accounting
// integrity.
func TestScenarioM_RestartAndRestorePreserveIntegrity(t *testing.T) {
	up := apptest.NewUpstream(t, "anthropic")
	up.Default = apptest.Reply{Text: "ok", InputTok: 100, OutputTok: 50}
	e := apptest.New(t)
	price := [2]string{"3", "15"}
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price})
	acc := e.Account("a", prov, apptest.AccountOpts{Secret: "sk-test-restore-secret"})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	bind := e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{})
	tok := e.Token(bind)
	send := func(env *apptest.Env, n int) {
		for range n {
			r := env.Gateway("POST", "/v1/messages", tok, msg("x"))
			if b := apptest.ReadAll(r); r.StatusCode != 200 {
				t.Fatalf("%d %s", r.StatusCode, b)
			}
		}
		env.Flush()
	}
	send(e, 3)
	atBackup := takeSnapshot(e)
	var bk struct {
		Path string `json:"path"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/system/backups", map[string]any{}, &bk)
	send(e, 2)
	afterMore := takeSnapshot(e)
	dbPath, tracesPath := e.App.Cfg.DBPath(), e.App.Cfg.TracesDBPath()
	key, dir := e.Key, e.DataDir
	e.Stop()

	// 1. A restart keeps everything, including stored secrets.
	e2 := apptest.New(t, apptest.WithDataDir(dir), apptest.WithMasterKey(key))
	if got := takeSnapshot(e2); got != afterMore {
		t.Fatalf("restart changed state: %+v vs %+v", got, afterMore)
	}
	send(e2, 1)
	// Simulate a crash with an in-flight reservation.
	if _, err := e2.App.DB.W.Exec(`INSERT INTO reservations(id, request_id, account_id, binding_id, status, est_cost_e10, created_at, lease_until)
		VALUES ('rsv_crash', 'req_crash', ?, ?, 'active', 0, ?, ?)`, acc, bind, time.Now().UnixMilli(), time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	e2.Stop()
	e3 := apptest.New(t, apptest.WithDataDir(dir), apptest.WithMasterKey(key))
	if st := e3.QueryString(`SELECT status FROM reservations WHERE id = 'rsv_crash'`); st == "active" {
		t.Fatal("abandoned reservation was not recovered at startup")
	}
	e3.Stop()

	// 2. Restore with the wrong master key is refused.
	wrong, _ := crypto.ParseKEK(crypto.GenerateKEKString())
	wkr, _ := crypto.NewKeyring(wrong)
	if _, err := ops.Restore(bk.Path, ops.Sources{DB: dbPath, Traces: tracesPath}, wkr); err == nil || !strings.Contains(err.Error(), "master key") {
		t.Fatalf("restore under a wrong key: %v", err)
	}
	// 3. Restore returns to the backup point.
	k, _ := crypto.ParseKEK(key)
	kr, _ := crypto.NewKeyring(k)
	res, err := ops.Restore(bk.Path, ops.Sources{DB: dbPath, Traces: tracesPath}, kr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.MovedAside) == 0 {
		t.Fatal("previous files were not kept")
	}
	e4 := apptest.New(t, apptest.WithDataDir(dir), apptest.WithMasterKey(key))
	got := takeSnapshot(e4)
	if got != atBackup {
		t.Fatalf("restored state %+v, backup point %+v", got, atBackup)
	}
	if dup := e4.QueryInt(`SELECT COUNT(*) - COUNT(DISTINCT idempotency_key) FROM ledger`); dup != 0 {
		t.Fatalf("ledger has %d duplicate idempotency keys", dup)
	}
	// The restored account secret still decrypts: traffic works.
	send(e4, 1)
	if n := e4.QueryInt(`SELECT COUNT(*) FROM requests`); n != atBackup.requests+1 {
		t.Fatalf("requests after restore %d", n)
	}
	// A tampered backup fails verification.
	if _, err := ops.Verify(bk.Path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(bk.Path, "harbor.db"))
	b[len(b)/2] ^= 0xff
	_ = os.WriteFile(filepath.Join(bk.Path, "harbor.db"), b, 0o600)
	if _, err := ops.Verify(bk.Path); err == nil {
		t.Fatal("tampered backup verified")
	}
}
