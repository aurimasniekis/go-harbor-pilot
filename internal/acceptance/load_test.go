package acceptance_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/loadgen"
)

// loadScale is 1 by default (a quick regression guard) and larger with
// HP_LOAD=1 for capacity runs.
func loadScale() int {
	if os.Getenv("HP_LOAD") == "1" {
		return 8
	}
	return 1
}

type loadEnv struct {
	e    *apptest.Env
	up   *apptest.Upstream
	tok  string
	base string
}

func newLoadEnv(t *testing.T, opts ...apptest.Option) loadEnv {
	t.Helper()
	e := apptest.New(t, opts...)
	up := apptest.NewUpstream(t, "anthropic")
	up.Default = apptest.Reply{Text: strings.Repeat("streamed text ", 16), InputTok: 200, OutputTok: 40, Chunks: 8}
	price := [2]string{"3", "15"}
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price})
	acc := e.Account("a", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	tok := e.Token(e.Binding("load", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	return loadEnv{e: e, up: up, tok: tok, base: e.Srv.URL}
}

func (l loadEnv) run(conc, n int, readDelay time.Duration) loadgen.Report {
	return loadgen.Run(context.Background(), loadgen.Config{BaseURL: l.base, Token: l.tok, Model: "coding/default", Concurrency: conc,
		Requests: n, Stream: true, ReadDelay: readDelay})
}

// checkIntegrity verifies that every request was recorded once and settled.
func (l loadEnv) checkIntegrity(t *testing.T, n int64, activityComplete bool) {
	t.Helper()
	l.e.Flush()
	if got := l.e.QueryInt(`SELECT count(*) FROM ledger WHERE kind = 'usage'`); got != n {
		t.Errorf("ledger usage entries %d, want %d (each request billed exactly once)", got, n)
	}
	if got := l.e.QueryInt(`SELECT count(*) FROM reservations WHERE status = 'active'`); got != 0 {
		t.Errorf("%d reservations left active", got)
	}
	if got := l.e.QueryInt(`SELECT count(DISTINCT idempotency_key) FROM ledger WHERE kind = 'usage'`); got != n {
		t.Errorf("distinct usage idempotency keys %d, want %d", got, n)
	}
	if activityComplete {
		if got := l.e.QueryInt(`SELECT count(*) FROM requests`); got != n {
			t.Errorf("activity rows %d, want %d", got, n)
		}
	}
}

func TestLoadStreamingThroughputAndIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	l := newLoadEnv(t)
	n := 400 * loadScale()
	rep := l.run(64, n, 0)
	t.Logf("streaming: %s", rep)
	if rep.OK != n {
		t.Fatalf("%d of %d failed: %v", n-rep.OK, n, rep.Errors)
	}
	l.checkIntegrity(t, int64(n), true)
}

// Slow clients hold streams open; other clients must not wait on them.
func TestLoadSlowClientsDoNotStallOthers(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	l := newLoadEnv(t)
	base := l.run(16, 200, 0)
	slowDone := make(chan loadgen.Report, 1)
	slowN := 32 * loadScale()
	go func() { slowDone <- l.run(slowN, slowN, 40*time.Millisecond) }()
	time.Sleep(100 * time.Millisecond) // let the slow streams open
	fast := l.run(16, 200*loadScale(), 0)
	slow := <-slowDone
	t.Logf("baseline: %s", base)
	t.Logf("fast during slow clients: %s", fast)
	t.Logf("slow clients: %s", slow)
	if fast.OK != fast.Requests || slow.OK != slow.Requests {
		t.Fatalf("failures: fast %v slow %v", fast.Errors, slow.Errors)
	}
	// Each slow stream takes over a second; fast requests must stay far
	// below that, whatever the machine.
	if limit := max(10*base.Latency.P95, 250*time.Millisecond); fast.Latency.P95 > limit {
		t.Fatalf("fast p95 %s while slow clients were connected (baseline p95 %s, limit %s)", fast.Latency.P95, base.Latency.P95, limit)
	}
	if slow.Latency.P50 < time.Second {
		t.Fatalf("slow clients finished in %s; the read delay was not applied", slow.Latency.P50)
	}
	l.checkIntegrity(t, int64(base.Requests+fast.Requests+slow.Requests), true)
}

// With a tiny telemetry queue, activity records are dropped and counted,
// but requests succeed and accounting stays complete.
func TestLoadTelemetryBackPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	l := newLoadEnv(t, apptest.WithApp(func(o *app.Options) { o.TelemetryQueue = 1 }))
	n := 300 * loadScale()
	rep := l.run(64, n, 0)
	t.Logf("tiny telemetry queue: %s; dropped %d", rep, l.e.App.Tel.Dropped())
	if rep.OK != n {
		t.Fatalf("%d of %d failed under telemetry pressure: %v", n-rep.OK, n, rep.Errors)
	}
	if l.e.App.Tel.Dropped() == 0 {
		t.Fatal("expected dropped activity records with a one-slot queue")
	}
	var sum struct {
		Dropped int64 `json:"telemetry_dropped"`
	}
	l.e.Admin.MustDo(200, "GET", "/api/v1/activity/summary", nil, &sum)
	if sum.Dropped == 0 {
		t.Fatal("the activity summary does not report the coverage gap")
	}
	l.checkIntegrity(t, int64(n), false)
}
