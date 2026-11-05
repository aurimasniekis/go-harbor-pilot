package acceptance_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aurimasniekis/go-pulse/pkg/pulse"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/outbound"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/relay"
)

type captured struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
}

func (c *captured) add(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, r)
	c.body = append(c.body, string(b))
	return string(b)
}

func (c *captured) n() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.reqs) }

// switchEvent produces one routing.account.switched event via the gateway.
func switchEvent(t *testing.T, e *apptest.Env) {
	t.Helper()
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("a1", prov, apptest.AccountOpts{})
	a2 := e.Account("a2", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	_ = apptest.ReadAll(e.Gateway("POST", "/v1/messages", tok, msg("x")))
	up.Push(apptest.Reply{Status: 429, RetryAfter: 60})
	_ = apptest.ReadAll(e.Gateway("POST", "/v1/messages", tok, msg("x")))
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.routing.account_switched.v1'`) == 1
	}, "switch event")
}

func TestWebhookRelaySignedRetriedAndReplayed(t *testing.T) {
	var fail atomic.Int32
	fail.Store(1)
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.add(r)
		if fail.Add(-1) >= 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)
	e := apptest.New(t)
	var dst struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/event-destinations", map[string]any{"name": "hook", "spec": map[string]any{"type": "webhook", "enabled": true,
		"webhook_url": srv.URL, "allow_private_network": true, "filter": map[string]any{"events": []string{"routing.*"}}},
		"secrets": map[string]string{"signing_secret": "whsec-test-current", "previous_signing_secret": "whsec-test-old"}}, &dst)
	ctx := context.Background()
	e.App.Relay.Round(ctx) // creates the cursor at the current end
	switchEvent(t, e)
	e.Eventually(func() bool { e.App.Relay.Round(ctx); return cap.n() >= 2 }, "retried webhook delivery")
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'delivered'`) == 1
	}, "delivered")
	cap.mu.Lock()
	last, body := cap.reqs[len(cap.reqs)-1], cap.body[len(cap.body)-1]
	cap.mu.Unlock()
	if last.Header.Get("X-Harbor-Event-Type") != "harborpilot.routing.account_switched.v1" || last.Header.Get("X-Harbor-Event-Id") == "" {
		t.Fatalf("headers %v", last.Header)
	}
	sig := last.Header.Get("X-Harbor-Signature")
	for _, secret := range []string{"whsec-test-current", "whsec-test-old"} {
		if !outbound.Verify(sig, []byte(body), secret, time.Now(), 5*time.Minute) {
			t.Fatalf("signature does not verify with %s: %s", secret, sig)
		}
	}
	if outbound.Verify(sig, []byte(body), "wrong", time.Now(), 5*time.Minute) || outbound.Verify(sig, []byte(body), "whsec-test-current", time.Now().Add(time.Hour), 5*time.Minute) {
		t.Fatal("signature accepted a wrong secret or a stale timestamp")
	}
	if strings.Contains(body, "whsec") {
		t.Fatal("secret in payload")
	}
	// A permanent failure dead-letters; replay re-sends it.
	fail.Store(0)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cap.add(r); w.WriteHeader(400) })
	e.Admin.MustDo(200, "POST", "/api/v1/event-destinations/"+dst.ID+"/replay", map[string]any{"since": time.Now().Add(-time.Hour)}, nil)
	e.Eventually(func() bool {
		e.App.Relay.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'dead'`) == 1
	}, "dead letter")
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cap.add(r); w.WriteHeader(200) })
	var rp struct {
		Queued int64 `json:"queued"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/event-destinations/"+dst.ID+"/replay", map[string]any{}, &rp)
	if rp.Queued != 1 {
		t.Fatalf("replay queued %d", rp.Queued)
	}
	e.Eventually(func() bool {
		e.App.Relay.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'delivered'`) == 1
	}, "replayed delivery")
	if n := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action = 'events.replay'`); n != 2 {
		t.Fatalf("replays audited %d", n)
	}
}

func embeddedJetStream(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := &natsserver.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true}
	s, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(s.Shutdown)
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	if _, err := js.CreateStream(context.Background(), jetstream.StreamConfig{Name: "EVENTS", Subjects: []string{"evt.>"}, Duplicates: 2 * time.Minute,
		MaxMsgSize: 1 << 20, Storage: jetstream.FileStorage}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPulseRelayPublishesWithIdempotencyHeaders(t *testing.T) {
	ns := embeddedJetStream(t)
	conn := func(url string, creds, ca []byte, source string) (relay.Publisher, error) {
		nc, err := nats.Connect(ns.ClientURL())
		if err != nil {
			return nil, err
		}
		c, err := pulse.New(nc, source)
		if err != nil {
			return nil, err
		}
		return relay.WrapPulse(c), nil
	}
	e := apptest.New(t, apptest.WithPulse(conn))
	e.Admin.MustDo(201, "POST", "/api/v1/event-destinations", map[string]any{"name": "pulse", "spec": map[string]any{"type": "pulse", "enabled": true,
		"pulse_url": "nats://pulse.example.test:4222", "filter": map[string]any{"events": []string{"routing.account.*"}}}}, nil)
	ctx := context.Background()
	e.App.Relay.Round(ctx)
	switchEvent(t, e)
	e.Eventually(func() bool {
		e.App.Relay.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'delivered'`) == 1
	}, "pulse delivery")
	nc, _ := nats.Connect(ns.ClientURL())
	defer nc.Close()
	js, _ := jetstream.New(nc)
	cons, err := js.OrderedConsumer(ctx, "EVENTS", jetstream.OrderedConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	id := e.QueryString(`SELECT id FROM event_outbox WHERE type = 'harborpilot.routing.account_switched.v1'`)
	if m.Subject() != "evt.harborpilot.routing.account.switched" || m.Headers().Get("Nats-Msg-Id") != id ||
		m.Headers().Get("ce-type") != "harborpilot.routing.account_switched.v1" {
		t.Fatalf("subject %s headers %v", m.Subject(), m.Headers())
	}
	var env map[string]any
	if json.Unmarshal(m.Data(), &env) != nil || env["id"] != id {
		t.Fatalf("payload %s", m.Data())
	}
	// A replay is deduplicated by JetStream within the window.
	e.Admin.MustDo(200, "POST", "/api/v1/event-destinations/"+e.QueryString(`SELECT id FROM resources WHERE kind = 'event_destination'`)+"/replay",
		map[string]any{"since": time.Now().Add(-time.Hour)}, nil)
	e.Eventually(func() bool {
		e.App.Relay.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'delivered'`) == 1
	}, "replayed pulse delivery")
	info, _ := js.Stream(ctx, "EVENTS")
	if st, _ := info.Info(ctx); st.State.Msgs != 1 {
		t.Fatalf("stream holds %d messages; duplicates must be deduplicated", st.State.Msgs)
	}
}

func TestNotificationsChannelsHysteresisRecoveryAndTest(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]string{}
	var discord429 atomic.Bool
	discord429.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] = append(got[r.URL.Path], string(b))
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/discord") && discord429.CompareAndSwap(true, false) {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"retry_after": 0.01}`))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	e := apptest.New(t)
	mk := func(name string, spec map[string]any, secret string) string {
		var o struct{ ID string }
		spec["enabled"] = true
		spec["allow_private_network"] = true
		e.Admin.MustDo(201, "POST", "/api/v1/notification-channels", map[string]any{"name": name, "spec": spec, "secrets": map[string]string{"secret": secret}}, &o)
		return o.ID
	}
	tg := mk("tg", map[string]any{"type": "telegram", "telegram_chat_id": "-100123", "api_base_url": srv.URL}, "123456:SYNTHETIC-bot-token")
	sl := mk("slack", map[string]any{"type": "slack"}, srv.URL+"/slack/hook")
	dc := mk("discord", map[string]any{"type": "discord"}, srv.URL+"/discord/hook")
	e.Admin.MustDo(201, "POST", "/api/v1/notification-rules", map[string]any{"name": "quota", "spec": map[string]any{"enabled": true, "recovery": true,
		"cooldown_seconds": 3600, "channel_ids": []string{tg, sl, dc}, "filter": map[string]any{"events": []string{"quota.window.exhausted"}}}}, nil)
	ctx := context.Background()
	e.App.Notify.Round(ctx)
	emit := func(action, sev string) {
		_, err := e.App.DB.W.Exec(`INSERT INTO event_outbox(id, type, subject, occurred_at, severity, dedupe_key, payload) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"evt_test_"+action+time.Now().Format("150405.000000"), "harborpilot.quota.window_"+action+".v1", "evt.harborpilot.quota.window."+action,
			time.Now().UnixMilli(), sev, "quota:qmn_1:five_hour",
			`{"name":"quota.window.`+action+`","severity":"`+sev+`","subjects":{"monitor_id":"qmn_1"},"data":{"label":"5-hour <window> & co","monitor":"team"}}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	emit("exhausted", "warning")
	emit("exhausted", "warning") // within cooldown: grouped, not re-sent
	emit("recovered", "info")
	e.Eventually(func() bool {
		e.App.Notify.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM notifications WHERE status = 'sent'`) == 6
	}, "alert + recovery on three channels")
	mu.Lock()
	tgBodies := got["/bot123456:SYNTHETIC-bot-token/sendMessage"]
	slack, discord := got["/slack/hook"], got["/discord/hook"]
	mu.Unlock()
	if len(tgBodies) != 2 || !strings.Contains(tgBodies[0], `5-hour \u0026lt;window\u0026gt; \u0026amp; co`) || !strings.Contains(tgBodies[1], "Recovered") {
		t.Fatalf("telegram %v", tgBodies)
	}
	if len(slack) != 2 || !strings.Contains(slack[0], "lt;window") || strings.Contains(slack[0], "<window>") {
		t.Fatalf("slack %v", slack)
	}
	if len(discord) != 3 || !strings.Contains(discord[0], `"allowed_mentions":{"parse":[]}`) {
		t.Fatalf("discord (expected one 429 retry) %v", discord)
	}
	var test struct {
		Delivered bool `json:"delivered"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/notification-channels/"+sl+"/test", nil, &test)
	if !test.Delivered {
		t.Fatal("test delivery failed")
	}
	// Notification rules cannot subscribe to notification events (no loops).
	if code := e.Admin.Do("POST", "/api/v1/notification-rules", map[string]any{"name": "loop", "spec": map[string]any{"enabled": true,
		"channel_ids": []string{sl}, "filter": map[string]any{"events": []string{"notification.*"}}}}, nil); code != 422 {
		t.Fatalf("loop rule accepted: %d", code)
	}
}

// Scenario K: Pulse and notification outages do not block gateway requests.
func TestScenarioK_DeliveryOutagesDoNotBlockGateway(t *testing.T) {
	done := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(done) })
	e := apptest.New(t)
	e.Admin.MustDo(201, "POST", "/api/v1/event-destinations", map[string]any{"name": "pulse-down", "spec": map[string]any{"type": "pulse", "enabled": true,
		"pulse_url": "nats://127.0.0.1:1"}}, nil)
	e.Admin.MustDo(201, "POST", "/api/v1/event-destinations", map[string]any{"name": "hook-hangs", "spec": map[string]any{"type": "webhook", "enabled": true,
		"webhook_url": hang.URL, "allow_private_network": true, "timeout_seconds": 20}}, nil)
	var ch struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/notification-channels", map[string]any{"name": "slack-hangs", "spec": map[string]any{"type": "slack", "enabled": true,
		"allow_private_network": true}, "secrets": map[string]string{"secret": hang.URL}}, &ch)
	e.Admin.MustDo(201, "POST", "/api/v1/notification-rules", map[string]any{"name": "all", "spec": map[string]any{"enabled": true,
		"channel_ids": []string{ch.ID}, "filter": map[string]any{}}}, nil)
	// Position the consumers before traffic starts (new consumers begin at
	// the end of the outbox).
	e.App.Relay.Round(context.Background())
	e.App.Notify.Round(context.Background())
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("p", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("a1", prov, apptest.AccountOpts{})
	a2 := e.Account("a2", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	// Delivery goroutines are now stuck on the outages; requests that emit
	// events must still complete promptly.
	for i := range 10 {
		if i == 3 {
			up.Push(apptest.Reply{Status: 429, RetryAfter: 1})
		}
		start := time.Now()
		r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
		_ = apptest.ReadAll(r)
		if r.StatusCode != 200 {
			t.Fatalf("request %d: %d", i, r.StatusCode)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("request %d took %v while deliveries were failing", i, d)
		}
	}
	e.Eventually(func() bool {
		return e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.routing.account_switched.v1'`) >= 1
	}, "events still recorded")
	ok := false
	for range 100 {
		if e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status IN ('deferred','sending','pending')`) > 0 {
			ok = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("no pending deliveries: cursors=%d deliveries=%d events=%d", e.QueryInt(`SELECT count(*) FROM event_cursors`),
			e.QueryInt(`SELECT count(*) FROM event_deliveries`), e.QueryInt(`SELECT count(*) FROM event_outbox`))
	}
}
