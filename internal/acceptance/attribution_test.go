package acceptance_test

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// failedRow is the attribution recorded for the latest request.
func failedRow(t *testing.T, e *apptest.Env) (status, account, provider, model, mode string) {
	t.Helper()
	e.Flush()
	q := `SELECT status || '|' || COALESCE(final_account_id,'') || '|' || COALESCE(final_provider_id,'') || '|' ||
		COALESCE(final_model,'') || '|' || COALESCE(mode,'') FROM requests ORDER BY id DESC LIMIT 1`
	p := strings.Split(e.QueryString(q), "|")
	return p[0], p[1], p[2], p[3], p[4]
}

// A request that fails at the upstream is attributed to the last account
// it reached (account, provider, model, mode), in the record and in the
// completion log line, as a successful one is.
func TestFailedRequestIsAttributedToLastAttempt(t *testing.T) {
	logs := &syncBuffer{}
	e := apptest.New(t, apptest.WithLog(slog.New(slog.NewJSONHandler(logs, nil))))
	up1, up2 := apptest.NewUpstream(t, "anthropic"), apptest.NewUpstream(t, "anthropic")
	p1 := e.Provider("first", "anthropic", up1.Srv.URL, apptest.Model{ID: "m1", MaxOutput: 1024})
	p2 := e.Provider("second", "anthropic", up2.Srv.URL, apptest.Model{ID: "m2", MaxOutput: 1024})
	a1 := e.Account("a1", p1, apptest.AccountOpts{Secret: "sk-one"})
	a2 := e.Account("a2", p2, apptest.AccountOpts{Secret: "sk-two"})
	alias := e.Alias("chat", e.Pool("p", "ordered", apptest.Member{Account: a1, Model: "m1"}, apptest.Member{Account: a2, Model: "m2", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))

	// The first account is rate limited (failover), the second rejects the
	// request: the error belongs to the second.
	up1.Push(apptest.Reply{Status: 429})
	up2.Push(apptest.Reply{Status: 400})
	r := e.Gateway("POST", "/v1/messages", tok, msg("chat"))
	if b := apptest.ReadAll(r); r.StatusCode != 400 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	status, acc, prov, model, mode := failedRow(t, e)
	if status != "error" || acc != a2 || prov != p2 || model != "m2" || mode != "native" {
		t.Fatalf("record: status=%s account=%s provider=%s model=%s mode=%s", status, acc, prov, model, mode)
	}
	var line map[string]any
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, `"gateway.request.completed"`) {
			_ = json.Unmarshal([]byte(l), &line)
		}
	}
	if line["account_id"] != a2 || line["provider_id"] != p2 || line["final_model"] != "m2" || line["mode"] != "native" || line["status"] != "error" {
		t.Fatalf("log line: %v", line)
	}
}

// The bridge's CLI reports an upstream 400: the request is the client's
// error, so it is not failed over to the next subscription (one attempt,
// the client gets a 400), and it is attributed to the account it reached,
// with the request's losses.
func TestFailedBridgeRequestIsAttributed(t *testing.T) {
	claudeOrSkip(t)
	up := apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t, apptest.WithBridgeEnv("ANTHROPIC_BASE_URL="+up.Srv.URL))
	prov := e.Provider("claude-sub", "claude_code", "", apptest.Model{ID: "claude-test-model", MaxOutput: 8192, Capabilities: []string{"streaming"}})
	a1 := e.Account("max-1", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-one"})
	a2 := e.Account("max-2", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-two"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: a1, Model: "claude-test-model"},
		apptest.Member{Account: a2, Model: "claude-test-model", Priority: 1}))
	tok := e.Token(e.Binding("laptop", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	// The CLI may send side requests of its own; every request fails.
	for range 8 {
		up.Push(apptest.Reply{Status: 400})
	}
	for _, stream := range []bool{true, false} {
		body := msg("claude-x")
		body["stream"] = stream
		r := e.Gateway("POST", "/v1/messages", tok, body)
		if b := apptest.ReadAll(r); r.StatusCode != 400 || !strings.Contains(b, "invalid_request_error") {
			t.Fatalf("stream=%v: %d %s", stream, r.StatusCode, b)
		}
		status, a, p, model, mode := failedRow(t, e)
		if status != "error" || a != a1 || p != prov || model != "claude-test-model" || mode != "translated" {
			t.Fatalf("stream=%v record: status=%s account=%s provider=%s model=%s mode=%s", stream, status, a, p, model, mode)
		}
		id := e.QueryString(`SELECT id FROM requests ORDER BY id DESC LIMIT 1`)
		if n := e.QueryString(`SELECT COUNT(*) FROM attempts WHERE request_id = ?`, id); n != "1" {
			t.Fatalf("stream=%v: %s attempts, want 1 (no failover on invalid_request)", stream, n)
		}
		if l := e.QueryString(`SELECT COALESCE(losses,'') FROM requests WHERE id = ?`, id); !strings.Contains(l, "system_prompt") {
			t.Fatalf("stream=%v: losses not recorded on failure: %q", stream, l)
		}
	}
}

// The synthetic Anthropic upstream enforces the prompt-cache rules (TTL
// order, at most four breakpoints), so a body that breaks them fails here
// the way it does upstream. Requests the gateway reshapes still pass.
func TestAnthropicCacheControlReachesUpstreamValid(t *testing.T) {
	up := apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t)
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("a", prov, apptest.AccountOpts{Secret: "sk-a"})
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: e.Alias("chat", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))}), apptest.BindingOpts{}))
	cc := func(ttl string) map[string]any {
		if ttl == "" {
			return map[string]any{"type": "ephemeral"}
		}
		return map[string]any{"type": "ephemeral", "ttl": ttl}
	}
	text := func(s, ttl string) map[string]any {
		return map[string]any{"type": "text", "text": s, "cache_control": cc(ttl)}
	}
	user := func(blocks ...any) map[string]any { return map[string]any{"role": "user", "content": blocks} }

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"5m system then 1h message", map[string]any{"model": "chat", "max_tokens": 16, "system": []any{text("sys", "")},
			"messages": []any{user(text("q", "1h"))}}},
		{"six breakpoints", map[string]any{"model": "chat", "max_tokens": 16, "system": []any{text("s1", "1h"), text("s2", "1h")},
			"messages": []any{user(text("q1", ""), text("q2", ""), text("q3", ""), text("q4", ""))}}},
		{"consecutive user messages merged", map[string]any{"model": "chat", "max_tokens": 16,
			"messages": []any{user(text("q1", "")), user(text("q2", "1h"))}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up.Push(apptest.Reply{Text: "ok"})
			r := e.Gateway("POST", "/v1/messages", tok, tc.body)
			if b := apptest.ReadAll(r); r.StatusCode != 200 {
				t.Fatalf("%d %s (upstream body %s)", r.StatusCode, b, up.Last().Raw)
			}
		})
	}

	// The check itself: a body sent as is (passthrough would) is refused.
	bad := `{"model":"m","max_tokens":16,"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"q","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`
	res, err := up.Srv.Client().Post(up.Srv.URL+"/v1/messages", "application/json", strings.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("synthetic upstream accepted a 1h breakpoint after a 5m one: %d", res.StatusCode)
	}
}

// The CLI's prompt-cache usage (cache reads and writes reported by the
// upstream) reaches the request record and the ledger, so the effect of
// the CLI's own breakpoints can be checked per request.
func TestClaudeCodeBridgeRecordsCacheTokens(t *testing.T) {
	claudeOrSkip(t)
	up := apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t, apptest.WithBridgeEnv("ANTHROPIC_BASE_URL="+up.Srv.URL))
	prov := e.Provider("claude-sub", "claude_code", "", apptest.Model{ID: "claude-test-model", MaxOutput: 8192, Capabilities: []string{"streaming"}})
	acc := e.Account("max-1", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-synthetic-setup-token"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: acc, Model: "claude-test-model"}))
	tok := e.Token(e.Binding("laptop", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	up.Push(apptest.Reply{Text: "cached answer", InputTok: 12, OutputTok: 3, CacheRead: 4000, CacheWrite: 900})
	body := msg("claude-x")
	body["stream"] = true
	r := e.Gateway("POST", "/v1/messages", tok, body)
	if b := apptest.ReadAll(r); r.StatusCode != 200 || !strings.Contains(b, `"stop_reason":"end_turn"`) {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	e.Flush()
	if got := e.QueryString(`SELECT COALESCE(cache_read_tokens,-1) || '/' || COALESCE(cache_write_tokens,-1) FROM requests ORDER BY id DESC LIMIT 1`); got != "4000/900" {
		t.Fatalf("request cache tokens %s", got)
	}
	if got := e.QueryString(`SELECT COALESCE(cache_read_tokens,-1) || '/' || COALESCE(cache_write_tokens,-1) FROM ledger ORDER BY at DESC LIMIT 1`); got != "4000/900" {
		t.Fatalf("ledger cache tokens %s", got)
	}
	if got := e.QueryString(`SELECT json_extract(provenance, '$.bridge_session') FROM ledger ORDER BY at DESC LIMIT 1`); got != "new_session" {
		t.Fatalf("ledger bridge_session %q", got)
	}
}

// A busy seat (its concurrency limit reached) is skipped, not tried: the
// request served by the next seat has one attempt, numbered 1, with no
// failover reason, and the skip is listed with the decision's exclusions.
func TestBusyCandidateIsSkippedNotFailedOver(t *testing.T) {
	up1, up2 := apptest.NewUpstream(t, "anthropic"), apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t)
	p1 := e.Provider("first", "anthropic", up1.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	p2 := e.Provider("second", "anthropic", up2.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	a1 := e.Account("seat-a", p1, apptest.AccountOpts{Secret: "sk-a", MaxConcurrency: 1})
	a2 := e.Account("seat-b", p2, apptest.AccountOpts{Secret: "sk-b"})
	alias := e.Alias("chat", e.Pool("p", "ordered", apptest.Member{Account: a1, Model: "m"}, apptest.Member{Account: a2, Model: "m", Priority: 1}))
	tok := e.Token(e.Binding("b", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))

	// Hold seat a with a slow request.
	up1.Push(apptest.Reply{Text: "slow", Delay: 3 * time.Second})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := e.Gateway("POST", "/v1/messages", tok, msg("chat"))
		apptest.ReadAll(r)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for up1.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	up2.Push(apptest.Reply{Text: "served by b"})
	r := e.Gateway("POST", "/v1/messages", tok, msg("chat"))
	if b := apptest.ReadAll(r); r.StatusCode != 200 || !strings.Contains(b, "served by b") {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	<-done
	e.Flush()
	id := e.QueryString(`SELECT id FROM requests WHERE final_account_id = ? ORDER BY id DESC LIMIT 1`, a2)
	if got := e.QueryString(`SELECT COUNT(*) || ' ' || MIN(n) || ' ' || COALESCE(MAX(failover_reason),'') FROM attempts WHERE request_id = ?`, id); got != "1 1 " {
		t.Fatalf("attempts (count, first n, failover reason): %q", got)
	}
	var d struct {
		Decision struct {
			Excluded []struct {
				AccountID string `json:"account_id"`
				Stage     string `json:"stage"`
				Reason    string `json:"reason"`
			} `json:"excluded"`
		} `json:"decision"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/activity/requests/"+id, nil, &d)
	found := false
	for _, x := range d.Decision.Excluded {
		found = found || (x.AccountID == a1 && x.Stage == "dispatch" && x.Reason == "account_concurrency")
	}
	if !found {
		t.Fatalf("busy seat not listed: %+v", d.Decision.Excluded)
	}
}
