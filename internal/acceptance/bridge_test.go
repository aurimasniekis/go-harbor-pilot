package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

func claudeOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err == nil {
		return
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(home + "/.local/bin/claude"); err == nil {
		t.Setenv("PATH", home+"/.local/bin:"+os.Getenv("PATH"))
		return
	}
	t.Skip("claude CLI not installed")
}

// The real claude CLI runs as a subscription bridge against a synthetic
// Anthropic upstream: no real credentials, no network.
func TestClaudeCodeBridgeThroughGateway(t *testing.T) {
	claudeOrSkip(t)
	up := apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t, apptest.WithBridgeEnv("ANTHROPIC_BASE_URL="+up.Srv.URL))
	prov := e.Provider("claude-sub", "claude_code", "", apptest.Model{ID: "claude-test-model", MaxOutput: 8192, Capabilities: []string{"streaming", "tools"}})
	acc := e.Account("max-1", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-synthetic-setup-token"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: acc, Model: "claude-test-model"}))
	tok := e.Token(e.Binding("laptop", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))

	reset := time.Now().Add(3 * time.Hour).Unix()
	rl := map[string]string{"anthropic-ratelimit-unified-status": "allowed_warning", "anthropic-ratelimit-unified-representative-claim": "five_hour",
		"anthropic-ratelimit-unified-reset": itoa(reset), "anthropic-ratelimit-unified-5h-utilization": "0.82",
		"anthropic-ratelimit-unified-5h-reset": itoa(reset), "anthropic-ratelimit-unified-5h-status": "allowed_warning"}
	up.Push(apptest.Reply{ToolName: "mcp__client__read_file", ToolArgs: `{"path":"main.go"}`, InputTok: 40, OutputTok: 12, Headers: rl},
		apptest.Reply{Text: "The file defines main.", InputTok: 60, OutputTok: 8, Headers: rl})

	tools := []any{map[string]any{"name": "read_file", "description": "Read a file", "input_schema": map[string]any{"type": "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}}
	body := map[string]any{"model": "claude-x", "max_tokens": 1024, "stream": true, "system": "You are a coding agent.", "tools": tools,
		"messages": []any{map[string]any{"role": "user", "content": "What does main.go do?"}}}
	r := e.Gateway("POST", "/v1/messages", tok, body)
	sse := apptest.ReadAll(r)
	if r.StatusCode != 200 || !strings.Contains(sse, `"name":"read_file"`) || !strings.Contains(sse, `"stop_reason":"tool_use"`) {
		t.Fatalf("tool call turn: %d %s", r.StatusCode, sse)
	}
	id := between(sse, `"id":"toolu`, `"`)
	if id == "" {
		t.Fatalf("no tool_use id in %s", sse)
	}
	id = "toolu" + id

	body["messages"] = []any{map[string]any{"role": "user", "content": "What does main.go do?"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "read_file", "input": map[string]any{"path": "main.go"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "package main\nfunc main() {}"}}}}
	r = e.Gateway("POST", "/v1/messages", tok, body)
	sse = apptest.ReadAll(r)
	if r.StatusCode != 200 || !strings.Contains(sse, `"text":" defines"`) || !strings.Contains(sse, `"stop_reason":"end_turn"`) {
		t.Fatalf("continuation: %d %s", r.StatusCode, sse)
	}
	if b, _ := json.Marshal(up.Last().Body); !strings.Contains(string(b), "func main() {}") {
		t.Fatal("tool result did not reach the model")
	}

	e.Flush()
	if src := e.QueryString(`SELECT usage_source FROM requests ORDER BY id DESC LIMIT 1`); src != "upstream_reported" {
		t.Fatalf("usage source %q", src)
	}
	if kind := e.QueryString(`SELECT cost_kind FROM ledger ORDER BY at DESC LIMIT 1`); kind == "estimated_api_charge" {
		t.Fatal("subscription usage must not be recorded as an API charge")
	}
	var board struct {
		Observed []struct {
			AccountID string `json:"account_id"`
			Buckets   []struct {
				Key               string   `json:"key"`
				Source            string   `json:"source"`
				RemainingFraction *float64 `json:"remaining_fraction"`
			} `json:"buckets"`
		} `json:"observed_accounts"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/quota/board", nil, &board)
	found := false
	for _, o := range board.Observed {
		for _, b := range o.Buckets {
			if o.AccountID == acc && b.Key == "cli:five_hour" && b.Source == "cli_event" && b.RemainingFraction != nil && *b.RemainingFraction > 0.17 && *b.RemainingFraction < 0.19 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("CLI subscription window missing from the board: %+v", board)
	}
	var br struct {
		Items []struct {
			Kind    string `json:"kind"`
			Enabled bool   `json:"enabled"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/system/bridges", nil, &br)
	if len(br.Items) == 0 || !br.Items[0].Enabled {
		t.Fatalf("bridge status %+v", br)
	}
}

// A rejected subscription window fails over to the next account and keeps
// the exhausted one out of routing until its reset.
func TestClaudeCodeBridgeExhaustionFailsOver(t *testing.T) {
	claudeOrSkip(t)
	up := apptest.NewUpstream(t, "anthropic")
	e := apptest.New(t, apptest.WithBridgeEnv("ANTHROPIC_BASE_URL="+up.Srv.URL))
	prov := e.Provider("claude-sub", "claude_code", "", apptest.Model{ID: "claude-test-model", MaxOutput: 8192, Capabilities: []string{"streaming"}})
	a1 := e.Account("max-1", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-one"})
	a2 := e.Account("max-2", prov, apptest.AccountOpts{Billing: "subscription", Secret: "sk-ant-oat01-two"})
	alias := e.Alias("coding/default", e.Pool("subs", "ordered", apptest.Member{Account: a1, Model: "claude-test-model"}, apptest.Member{Account: a2, Model: "claude-test-model", Priority: 1}))
	tok := e.Token(e.Binding("laptop", e.Plan("default", apptest.PlanOpts{DefaultAlias: alias}), apptest.BindingOpts{}))
	reset := time.Now().Add(2 * time.Hour).Unix()
	up.Push(apptest.Reply{Status: 429, Headers: map[string]string{"anthropic-ratelimit-unified-status": "rejected",
		"anthropic-ratelimit-unified-representative-claim": "five_hour", "anthropic-ratelimit-unified-reset": itoa(reset),
		"anthropic-ratelimit-unified-5h-utilization": "1.0", "anthropic-ratelimit-unified-5h-reset": itoa(reset), "anthropic-ratelimit-unified-5h-status": "rejected"}},
		apptest.Reply{Text: "served by the second subscription"})
	r := e.Gateway("POST", "/v1/messages", tok, msg("x"))
	if b := apptest.ReadAll(r); r.StatusCode != 200 || !strings.Contains(b, "served by the second subscription") {
		t.Fatalf("failover: %d %s", r.StatusCode, b)
	}
	if auth := up.Last().Header.Get("Authorization"); auth != "Bearer sk-ant-oat01-two" {
		t.Fatalf("second attempt used %q", auth)
	}
	var h struct {
		Health []struct {
			AccountID string `json:"account_id"`
			Available bool   `json:"available"`
		} `json:"account_health"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/quota/board", nil, &h)
	for _, x := range h.Health {
		if x.AccountID == a1 && x.Available {
			t.Fatal("exhausted subscription still routable")
		}
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}
