package acceptance_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// A stream whose first event changed shape: "message" is now a string.
const driftedStream = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":\"drifted-shape-token\"}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello from a drifted upstream\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

type compatRig struct {
	e   *apptest.Env
	up  *apptest.Upstream
	tok string
}

func newCompatRig(t *testing.T, compat map[string]any, privacy map[string]any) *compatRig {
	t.Helper()
	e := apptest.New(t)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("api", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	var pp struct{ ID string }
	if privacy != nil {
		e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": "mask", "spec": privacy}, &pp)
	}
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias, Compatibility: compat, PrivacyPolicy: pp.ID})
	return &compatRig{e: e, up: up, tok: e.Token(e.Binding("b", plan, apptest.BindingOpts{}))}
}

func (r *compatRig) send(t *testing.T) (int, string) {
	t.Helper()
	resp := r.e.Gateway("POST", "/v1/messages", r.tok, map[string]any{"model": "x", "max_tokens": 64, "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "summarize the release notes"}}})
	return resp.StatusCode, apptest.ReadAll(resp)
}

var passthroughPlan = map[string]any{"on_breaker_open": "passthrough", "allowed_losses": []string{"detailed_usage", "tool_statistics", "masking"}}

// Scenario H: protocol drift opens the breaker and the plan's approved
// native passthrough serves later requests, without splicing streams.
func TestScenarioH_DriftActivatesApprovedPassthrough(t *testing.T) {
	r := newCompatRig(t, passthroughPlan, nil)
	for i := range 3 {
		r.up.Push(apptest.Reply{Raw: driftedStream})
		code, body := r.send(t)
		if code < 400 || strings.Contains(body, "hello from a drifted upstream") {
			t.Fatalf("drifted request %d should fail cleanly before output: %d %s", i, code, body)
		}
	}
	r.e.Eventually(func() bool {
		return r.e.QueryInt(`SELECT count(*) FROM event_outbox WHERE type = 'harborpilot.compat.breaker_opened.v1'`) == 1
	}, "breaker opened event")
	r.up.Push(apptest.Reply{Raw: driftedStream})
	code, body := r.send(t)
	if code != 200 || body != driftedStream {
		t.Fatalf("passthrough must forward the upstream stream unchanged: %d %q", code, body)
	}
	if r.up.Count() != 4 {
		t.Fatalf("upstream calls %d", r.up.Count())
	}
	r.e.Flush()
	var mode, losses string
	var out int64
	_ = r.e.App.DB.R.QueryRow(`SELECT mode, losses, COALESCE(output_tokens, -1) FROM requests ORDER BY id DESC LIMIT 1`).Scan(&mode, &losses, &out)
	if mode != "passthrough" || !strings.Contains(losses, "detailed_usage") || out != 5 {
		t.Fatalf("passthrough request recorded as mode=%s losses=%s output=%d", mode, losses, out)
	}
	var deg struct {
		Items []struct {
			Plans []struct {
				Action string   `json:"action"`
				Losses []string `json:"losses"`
			} `json:"plans"`
		} `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/compat/degradations", nil, &deg)
	if len(deg.Items) != 1 || deg.Items[0].Plans[0].Action != "passthrough" {
		t.Fatalf("degradation %+v", deg)
	}

	// Scenario I: Protocol Watch recorded sanitized, deduplicated changes.
	r.e.App.Watch.Drain(context.Background())
	r.up.Push(apptest.Reply{Raw: driftedStream})
	_, _ = r.send(t)
	r.e.App.Watch.Drain(context.Background())
	var ch struct {
		Items []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Count int    `json:"count"`
			Path  string `json:"path"`
		} `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/protocol-watch/changes", nil, &ch)
	kinds := map[string]int{}
	for _, c := range ch.Items {
		kinds[c.Kind] += c.Count
		if c.Kind == "type_change" && !strings.Contains(c.Path, "message_start.message") {
			t.Fatalf("unexpected type change path %q", c.Path)
		}
	}
	if kinds["parse_failure"] != 3 || kinds["type_change"] != 2 {
		t.Fatalf("changes %+v", ch.Items)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM work_items`); int(n) != len(ch.Items) {
		t.Fatalf("work items %d for %d changes (must be deduplicated)", n, len(ch.Items))
	}
	for _, table := range []string{"protocol_changes", "work_items"} {
		if n := r.e.QueryInt(`SELECT count(*) FROM ` + table + ` WHERE ` + allText(table) + ` LIKE '%drifted upstream%' OR ` + allText(table) + ` LIKE '%drifted-shape-token%'`); n != 0 {
			t.Fatalf("%s stored content values", table)
		}
	}
	var wi struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/protocol-watch/work-items", nil, &wi)
	var fx struct {
		Fixture  map[string]any `json:"fixture"`
		Revision int64          `json:"revision"`
	}
	r.e.Admin.MustDo(200, "POST", "/api/v1/protocol-watch/work-items/"+wi.Items[0].ID+"/fixture", nil, &fx)
	if fx.Fixture["synthetic"] != true {
		t.Fatalf("fixture %+v", fx)
	}
	r.e.Admin.MustDo(200, "PATCH", "/api/v1/protocol-watch/work-items/"+wi.Items[0].ID, map[string]any{"status": "acknowledged", "fixture_reviewed": true}, nil,
		"If-Match", `"r`+itoa(fx.Revision)+`"`)
}

func allText(table string) string {
	if table == "protocol_changes" {
		return "(path || detail)"
	}
	return "(title || diff || COALESCE(fixture, ''))"
}

// Scenario J: required privacy masking prevents an unauthorized raw
// fallback even when the plan approves passthrough.
func TestScenarioJ_RequiredPrivacyBlocksRawFallback(t *testing.T) {
	r := newCompatRig(t, passthroughPlan, map[string]any{"required": true})
	for range 3 {
		r.up.Push(apptest.Reply{Raw: driftedStream})
		_, _ = r.send(t)
	}
	code, body := r.send(t)
	if code != 403 || !strings.Contains(body, "privacy masking cannot run in passthrough") {
		t.Fatalf("expected a policy block: %d %s", code, body)
	}
	if r.up.Count() != 3 {
		t.Fatalf("raw request reached the upstream (calls %d)", r.up.Count())
	}
	// Forcing the breaker closed restores normal (masked) routing.
	var br struct {
		Items []struct {
			ProviderID string `json:"provider_id"`
			Path       string `json:"path"`
		} `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/compat/breakers", nil, &br)
	r.e.Admin.MustDo(200, "POST", "/api/v1/compat/breakers/override", map[string]any{"provider_id": br.Items[0].ProviderID, "path": br.Items[0].Path,
		"override": "force_closed", "reason": "upstream fixed"}, nil)
	r.up.Push(apptest.Reply{Text: "ok"})
	if code, _ := r.send(t); code != 200 {
		t.Fatalf("after override: %d", code)
	}
}
