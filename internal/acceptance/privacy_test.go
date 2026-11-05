package acceptance_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

// A synthetic, credential-shaped value (not a real secret).
const fakeSecret = "sk-ant-api03-SYNTHETICprivacyTEST000000000000"

type privacyRig struct {
	e   *apptest.Env
	up  *apptest.Upstream
	tok string
}

func newPrivacyRig(t *testing.T, policy map[string]any, trace map[string]any, opts ...apptest.Option) *privacyRig {
	t.Helper()
	e := apptest.New(t, opts...)
	up := apptest.NewUpstream(t, "anthropic")
	prov := e.Provider("anthropic", "anthropic", up.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024})
	acc := e.Account("api", prov, apptest.AccountOpts{})
	alias := e.Alias("coding/default", e.Pool("p", "ordered", apptest.Member{Account: acc, Model: "m"}))
	var pp, tp struct{ ID string }
	if policy != nil {
		e.Admin.MustDo(201, "POST", "/api/v1/privacy-policies", map[string]any{"name": "mask", "spec": policy}, &pp)
	}
	if trace != nil {
		e.Admin.MustDo(201, "POST", "/api/v1/trace-policies", map[string]any{"name": "trace", "spec": trace}, &tp)
	}
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: alias, PrivacyPolicy: pp.ID, TracePolicy: tp.ID})
	e.GrantTraceAccess()
	return &privacyRig{e: e, up: up, tok: e.Token(e.Binding("b", plan, apptest.BindingOpts{}))}
}

func fileContains(t *testing.T, dir, needle string) []string {
	t.Helper()
	var hits []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), needle) {
			hits = append(hits, filepath.Base(p))
		}
		return nil
	})
	return hits
}

// Scenario F: a masked secret never reaches the upstream or persistence,
// and the client still sees its own value in the response.
func TestScenarioF_MaskedSecretAbsentUpstreamAndPersistence(t *testing.T) {
	r := newPrivacyRig(t, map[string]any{"required": true, "restore_in_responses": true},
		map[string]any{"mode": "masked_full", "retention": "3d", "allow_export": true})
	for _, stream := range []bool{false, true} {
		r.up.Push(apptest.Reply{Echo: true})
		body := map[string]any{"model": "x", "max_tokens": 64, "stream": stream, "messages": []any{map[string]any{"role": "user",
			"content": "deploy with ANTHROPIC_API_KEY=" + fakeSecret + " please"}}}
		resp := r.e.Gateway("POST", "/v1/messages", r.tok, body)
		out := apptest.ReadAll(resp)
		if resp.StatusCode != 200 {
			t.Fatalf("stream=%v: %d %s", stream, resp.StatusCode, out)
		}
		sent := r.up.Last().Raw
		if strings.Contains(sent, fakeSecret) || !strings.Contains(sent, "HPSEC_") {
			t.Fatalf("stream=%v: upstream body not masked: %s", stream, sent)
		}
		if !strings.Contains(strings.ReplaceAll(out, `=`, "="), fakeSecret[:20]) {
			t.Fatalf("stream=%v: client did not get its value restored: %s", stream, out)
		}
	}
	r.e.Flush()
	r.e.App.TraceSvc.Flush(context.Background())
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		} `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/traces", nil, &list)
	if len(list.Items) != 2 {
		t.Fatalf("traces %+v", list)
	}
	var content map[string]any
	r.e.Admin.MustDo(200, "GET", "/api/v1/traces/"+list.Items[0].ID, nil, &content)
	b := mustJSONString(content)
	if strings.Contains(b, fakeSecret) || !strings.Contains(b, "[redacted:") {
		t.Fatalf("trace content not redacted: %s", b)
	}
	if code := r.e.Admin.Do("GET", "/api/v1/traces/"+list.Items[0].ID+"/export", nil, nil); code != 200 {
		t.Fatalf("export: %d", code)
	}
	if n := r.e.QueryInt(`SELECT count(*) FROM audit_log WHERE action IN ('trace.read','trace.export')`); n < 2 {
		t.Fatalf("content access not audited (%d)", n)
	}
	var detail struct {
		Privacy struct {
			Masked int `json:"masked"`
		} `json:"privacy"`
	}
	reqID := r.e.QueryString(`SELECT id FROM requests ORDER BY id LIMIT 1`)
	r.e.Admin.MustDo(200, "GET", "/api/v1/activity/requests/"+reqID, nil, &detail)
	if detail.Privacy.Masked != 1 {
		t.Fatalf("masking summary %+v", detail)
	}
	if hits := fileContains(t, r.e.App.Cfg.Server.DataDir, fakeSecret); len(hits) > 0 {
		t.Fatalf("secret persisted in %v", hits)
	}
}

func TestPrivacyBlockRule(t *testing.T) {
	r := newPrivacyRig(t, map[string]any{"required": true, "rules": []any{map[string]any{"name": "prod_db", "pattern": `prod-db-[0-9]+`, "action": "block"}}}, nil)
	resp := r.e.Gateway("POST", "/v1/messages", r.tok, map[string]any{"model": "x", "max_tokens": 16,
		"messages": []any{map[string]any{"role": "user", "content": "connect to prod-db-7"}}})
	body := apptest.ReadAll(resp)
	if resp.StatusCode != 403 || !strings.Contains(body, "prod_db") || strings.Contains(body, "prod-db-7") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if r.up.Count() != 0 {
		t.Fatal("blocked content reached the upstream")
	}
	var sb struct {
		Findings []struct {
			Rule   string `json:"rule"`
			Action string `json:"action"`
		} `json:"findings"`
		Masked string `json:"masked"`
	}
	r.e.Admin.MustDo(200, "POST", "/api/v1/privacy/sandbox", map[string]any{"policy": map[string]any{"required": true}, "text": "token " + fakeSecret}, &sb)
	if len(sb.Findings) != 1 || strings.Contains(sb.Masked, fakeSecret) {
		t.Fatalf("sandbox %+v", sb)
	}
}

// Scenario G: expired traces are unreadable and unsearchable, then
// physically removed and their keys destroyed.
func TestScenarioG_ExpiredTracesUnreadableAndUnsearchable(t *testing.T) {
	clk := clock.NewFake(time.Now().UTC())
	r := newPrivacyRig(t, nil, map[string]any{"mode": "masked_full", "retention": "custom", "custom_ttl_seconds": 3600}, apptest.WithClock(clk))
	r.up.Push(apptest.Reply{Text: "ok"})
	resp := r.e.Gateway("POST", "/v1/messages", r.tok, map[string]any{"model": "x", "max_tokens": 16,
		"messages": []any{map[string]any{"role": "user", "content": "rollout checklist for kubernetes"}}})
	_ = apptest.ReadAll(resp)
	r.e.Flush()
	ts := r.e.App.TraceSvc
	ts.Flush(context.Background())
	ctx := context.Background()
	items, _ := ts.List(ctx, traces.Filter{})
	if len(items) != 1 {
		t.Fatalf("traces %d", len(items))
	}
	id := items[0].ID
	var hits struct {
		Items []any `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/traces/search?q=kubernetes", nil, &hits)
	if len(hits.Items) != 1 {
		t.Fatal("live trace not searchable")
	}

	clk.Advance(2 * time.Hour) // past the one-hour retention
	if _, err := ts.Get(ctx, id); err != traces.ErrNotFound {
		t.Fatalf("expired trace readable: %v", err)
	}
	if h, _ := ts.Search(ctx, "kubernetes", 10); len(h) != 0 {
		t.Fatal("expired trace searchable before sweep")
	}
	if l, _ := ts.List(ctx, traces.Filter{}); len(l) != 0 {
		t.Fatal("expired trace listed")
	}
	clk.Advance(48 * time.Hour) // past the end of the bucket's day
	res, err := ts.Sweep(ctx)
	if err != nil || res.Traces != 1 || res.Keys != 1 {
		t.Fatalf("sweep %+v %v", res, err)
	}
	var rows, fts, keys int
	_ = r.e.App.Traces.R.QueryRow(`SELECT (SELECT count(*) FROM traces), (SELECT count(*) FROM trace_search), (SELECT count(*) FROM trace_keys)`).Scan(&rows, &fts, &keys)
	if rows+fts+keys != 0 {
		t.Fatalf("leftovers: traces=%d search=%d keys=%d", rows, fts, keys)
	}
}

func mustJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
