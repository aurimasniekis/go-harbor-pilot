package acceptance_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
)

const unified = "anthropic-ratelimit-unified-"

// usageRig serves a native Anthropic account and a translated
// OpenAI-compatible one, both metered at 1 USD per million input tokens,
// to a client with a monthly project budget and to one without.
type usageRig struct {
	e        *apptest.Env
	ant, oai *apptest.Upstream
	budgeted string // token of the client with a budget
	free     string // token of the client without one
}

func newUsageRig(t *testing.T, mode string) *usageRig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
	e := apptest.New(t, apptest.WithClock(clk), apptest.WithConfig(func(c *config.Config) { c.Gateway.UsageHeaders = mode }))
	r := &usageRig{e: e, ant: apptest.NewUpstream(t, "anthropic"), oai: apptest.NewUpstream(t, "openai")}
	price := [2]string{"1", "10000"}
	native := e.Account("api", e.Provider("anthropic", "anthropic", r.ant.Srv.URL, apptest.Model{ID: "m", MaxOutput: 1024, Price: &price}),
		apptest.AccountOpts{Billing: "metered_api"})
	router := e.Account("router", e.Provider("router", "openai_compatible", r.oai.Srv.URL, apptest.Model{ID: "r", MaxOutput: 1024, Price: &price}),
		apptest.AccountOpts{Billing: "metered_api", Secret: "sk-router"})
	nativeAlias := e.Alias("native", e.Pool("native", "ordered", apptest.Member{Account: native, Model: "m"}))
	routerAlias := e.Alias("translated", e.Pool("router", "ordered", apptest.Member{Account: router, Model: "r"}))
	plan := e.Plan("default", apptest.PlanOpts{DefaultAlias: nativeAlias, Mappings: map[string]string{"translated": routerAlias}})
	create := func(path, name string, spec map[string]any) string {
		var out struct {
			ID string `json:"id"`
		}
		e.Admin.MustDo(201, "POST", path, map[string]any{"name": name, "spec": spec}, &out)
		return out.ID
	}
	ws := create("/api/v1/workspaces", "ws", map[string]any{})
	proj := create("/api/v1/projects", "checkout", map[string]any{"workspace_id": ws})
	other := create("/api/v1/projects", "billing", map[string]any{"workspace_id": ws})
	create("/api/v1/budgets", "checkout-monthly", map[string]any{"scope": "project", "scope_id": proj, "period": "month", "limit": "10",
		"currency": "USD", "enforcement": "block", "warn_at": 0.8, "enabled": true})
	r.budgeted = e.Token(e.Binding("laptop", plan, apptest.BindingOpts{ProjectID: proj}))
	r.free = e.Token(e.Binding("ci", plan, apptest.BindingOpts{ProjectID: other}))
	return r
}

// upstreamWindows are rate-limit headers an upstream might send; the
// gateway never passes them on.
var upstreamWindows = map[string]string{unified + "status": "allowed", unified + "5h-utilization": "0.5", unified + "5h-status": "allowed",
	unified + "representative-claim": "five_hour", "anthropic-ratelimit-requests-limit": "50"}

func anthropicBody(model string, stream bool) map[string]any {
	return map[string]any{"model": model, "max_tokens": 16, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "x"}}}
}

func usageHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "anthropic-ratelimit-") {
			out[strings.ToLower(k)] = h.Get(k)
		}
	}
	return out
}

// With usage_headers: auto, metered traffic reports the tightest budget as
// the client's cap, natively and translated, streaming or not; a client
// without a budget, and OpenAI clients, get none, and upstream rate-limit
// headers are never passed on (D-048).
func TestUsageHeadersReportBudget(t *testing.T) {
	r := newUsageRig(t, config.UsageHeadersAuto)
	e := r.e
	// 4 USD of the 10 USD monthly budget.
	r.ant.Push(apptest.Reply{Text: "ok", InputTok: 4_000_000})
	if resp := e.Gateway("POST", "/v1/messages", r.budgeted, anthropicBody("native", false)); resp.StatusCode != 200 {
		t.Fatalf("seed: %d %s", resp.StatusCode, apptest.ReadAll(resp))
	} else {
		apptest.ReadAll(resp)
	}
	reset := itoa(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).Unix())
	// The figures include this request's own reservation (16 output tokens
	// at 0.01 USD): (4 + 0.16) / 10.
	want := map[string]string{unified + "status": "allowed", unified + "representative-claim": "overage", unified + "overage-status": "allowed",
		unified + "overage-utilization": "0.42", unified + "reset": reset, unified + "overage-reset": reset, unified + "overage-period": "monthly"}
	t.Run("native", func(t *testing.T) {
		r.ant.Push(apptest.Reply{Text: "ok", InputTok: 10, Headers: upstreamWindows})
		resp := e.Gateway("POST", "/v1/messages", r.budgeted, anthropicBody("native", false))
		apptest.ReadAll(resp)
		if got := usageHeaders(resp.Header); resp.StatusCode != 200 || !mapsEqual(got, want) {
			t.Fatalf("%d headers %v, want %v", resp.StatusCode, got, want)
		}
	})
	t.Run("translated stream", func(t *testing.T) {
		r.oai.Push(apptest.Reply{Text: "ok", InputTok: 10})
		resp := e.Gateway("POST", "/v1/messages", r.budgeted, anthropicBody("translated", true))
		body := apptest.ReadAll(resp)
		if got := usageHeaders(resp.Header); resp.StatusCode != 200 || !strings.Contains(body, "message_stop") || !mapsEqual(got, want) {
			t.Fatalf("%d headers %v, want %v\n%s", resp.StatusCode, got, want, body)
		}
	})
	t.Run("no budget, no headers", func(t *testing.T) {
		r.ant.Push(apptest.Reply{Text: "ok", InputTok: 10, Headers: upstreamWindows})
		resp := e.Gateway("POST", "/v1/messages", r.free, anthropicBody("native", true))
		apptest.ReadAll(resp)
		if got := usageHeaders(resp.Header); resp.StatusCode != 200 || len(got) != 0 {
			t.Fatalf("%d headers %v", resp.StatusCode, got)
		}
	})
	t.Run("openai clients get none", func(t *testing.T) {
		r.ant.Push(apptest.Reply{Text: "ok", InputTok: 10})
		resp := e.Gateway("POST", "/v1/chat/completions", r.budgeted, map[string]any{"model": "native", "max_tokens": 16,
			"messages": []any{map[string]any{"role": "user", "content": "x"}}})
		apptest.ReadAll(resp)
		if got := usageHeaders(resp.Header); resp.StatusCode != 200 || len(got) != 0 {
			t.Fatalf("%d headers %v", resp.StatusCode, got)
		}
	})
}

// With usage_headers off (the default), responses carry exactly the
// headers they did before the setting existed.
func TestUsageHeadersOffLeavesResponsesUnchanged(t *testing.T) {
	names := func(mode string) map[string][]string {
		r := newUsageRig(t, mode)
		out := map[string][]string{}
		for _, c := range []struct {
			name, model string
			stream      bool
		}{{"native", "native", false}, {"native stream", "native", true}, {"translated", "translated", false}, {"translated stream", "translated", true}} {
			r.ant.Push(apptest.Reply{Text: "ok", InputTok: 10, Headers: upstreamWindows})
			r.oai.Push(apptest.Reply{Text: "ok", InputTok: 10})
			resp := r.e.Gateway("POST", "/v1/messages", r.budgeted, anthropicBody(c.model, c.stream))
			apptest.ReadAll(resp)
			if resp.StatusCode != 200 {
				t.Fatalf("%s %s: %d", mode, c.name, resp.StatusCode)
			}
			var ks []string
			for k := range resp.Header {
				if k != "Date" {
					ks = append(ks, strings.ToLower(k))
				}
			}
			slices.Sort(ks)
			out[c.name] = ks
		}
		return out
	}
	off, auto := names(config.UsageHeadersOff), names(config.UsageHeadersAuto)
	for name, ks := range off {
		for _, k := range ks {
			if strings.HasPrefix(k, "anthropic-ratelimit-") {
				t.Fatalf("%s: %s sent with usage headers off", name, k)
			}
		}
		var stripped []string
		for _, k := range auto[name] {
			if !strings.HasPrefix(k, unified) {
				stripped = append(stripped, k)
			}
		}
		if !slices.Equal(ks, stripped) {
			t.Fatalf("%s: off %v, auto without usage headers %v", name, ks, stripped)
		}
		if len(auto[name]) == len(ks) {
			t.Fatalf("%s: auto added no usage headers", name)
		}
	}
}

// The OAuth beta flag a client adds when it signs in with a bearer token is
// never forwarded to an upstream; other flags are.
func TestOAuthBetaNotForwarded(t *testing.T) {
	r := newUsageRig(t, config.UsageHeadersOff)
	r.ant.Push(apptest.Reply{Text: "ok"})
	resp := r.e.Gateway("POST", "/v1/messages", r.free, anthropicBody("native", false),
		"anthropic-beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14")
	if b := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if got := r.ant.Last().Header.Values("anthropic-beta"); !slices.Equal(got, []string{"claude-code-20250219,interleaved-thinking-2025-05-14"}) {
		t.Fatalf("upstream anthropic-beta %q", got)
	}
}

// A bridged subscription seat reports its 5-hour and 7-day windows from
// the quota board; after a failover the headers describe the account that
// served the request, never the one that failed (D-048).
func TestUsageHeadersReportSeatWindows(t *testing.T) {
	claudeOrSkip(t)
	up := apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t, apptest.WithBridgeEnv("ANTHROPIC_BASE_URL="+up.Srv.URL),
		apptest.WithConfig(func(c *config.Config) { c.Gateway.UsageHeaders = config.UsageHeadersAuto }))
	prov := e.Provider("claude-sub", "claude_code", "", apptest.Model{ID: "claude-test-model", MaxOutput: 8192, Capabilities: []string{"streaming"}})
	a1 := e.Account("max-1", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-one"})
	a2 := e.Account("max-2", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-two"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: a1, Model: "claude-test-model"},
		apptest.Member{Account: a2, Model: "claude-test-model", Priority: 1}))
	tok := e.Token(e.Binding("laptop", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))

	r5, r7 := time.Now().Add(3*time.Hour).Unix(), time.Now().Add(72*time.Hour).Unix()
	windows := func(u5, s5 string) map[string]string {
		return map[string]string{unified + "status": s5, unified + "representative-claim": "five_hour", unified + "reset": itoa(r5),
			unified + "5h-utilization": u5, unified + "5h-reset": itoa(r5), unified + "5h-status": s5,
			unified + "7d-utilization": "0.4", unified + "7d-reset": itoa(r7), unified + "7d-status": "allowed"}
	}
	send := func(stream bool) *http.Response {
		t.Helper()
		resp := e.Gateway("POST", "/v1/messages", tok, anthropicBody("claude-x", stream))
		if b := apptest.ReadAll(resp); resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		return resp
	}
	// The first turn puts the seat's windows on the board (the CLI reports
	// them while it runs); the next turn's headers carry them.
	up.Push(apptest.Reply{Text: "one", Headers: windows("0.82", "allowed_warning")})
	send(true)
	up.Push(apptest.Reply{Text: "two", Headers: windows("0.82", "allowed_warning")})
	want := map[string]string{unified + "status": "allowed_warning", unified + "representative-claim": "five_hour", unified + "reset": itoa(r5),
		unified + "5h-status": "allowed_warning", unified + "5h-utilization": "0.82", unified + "5h-reset": itoa(r5), unified + "5h-surpassed-threshold": "0.75",
		unified + "7d-status": "allowed", unified + "7d-utilization": "0.4", unified + "7d-reset": itoa(r7)}
	if got := usageHeaders(send(true).Header); !mapsEqual(got, want) {
		t.Fatalf("seat headers %v, want %v", got, want)
	}
	up.Push(apptest.Reply{Text: "three", Headers: windows("0.82", "allowed_warning")})
	if got := usageHeaders(send(false).Header); !mapsEqual(got, want) {
		t.Fatalf("non-streaming seat headers %v, want %v", got, want)
	}

	// max-1 runs out and the request fails over to max-2.
	up.Push(apptest.Reply{Status: 429, Headers: windows("1.0", "rejected")}, apptest.Reply{Text: "four", Headers: windows("0.3", "allowed")})
	got := usageHeaders(send(true).Header)
	if got[unified+"status"] == "rejected" || got[unified+"5h-utilization"] == "1" {
		t.Fatalf("failover reported the failed account: %v", got)
	}
	if v, ok := got[unified+"5h-utilization"]; ok && v != "0.3" {
		t.Fatalf("failover 5h utilization %q, want 0.3 (max-2) or none yet", v)
	}
	up.Push(apptest.Reply{Text: "five", Headers: windows("0.3", "allowed")})
	if got := usageHeaders(send(true).Header); got[unified+"5h-utilization"] != "0.3" || got[unified+"status"] != "allowed" {
		t.Fatalf("max-2 headers %v", got)
	}
	if auth := up.Last().Header.Get("Authorization"); auth != "Bearer sk-ant-oat01-two" {
		t.Fatalf("last turn used %q", auth)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
