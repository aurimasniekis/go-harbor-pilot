package anthropic

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// cacheMarkers lists the cache_control objects of a Messages body as raw
// JSON, in the order the API processes them: tools, system, messages
// (with blocks nested in tool results right after their tool_result).
func cacheMarkers(t *testing.T, body []byte) []string {
	t.Helper()
	var b struct {
		Tools    []map[string]json.RawMessage `json:"tools"`
		System   json.RawMessage              `json:"system"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	var out []string
	var blocks func(raw json.RawMessage)
	blocks = func(raw json.RawMessage) {
		var bs []map[string]json.RawMessage
		if json.Unmarshal(raw, &bs) != nil {
			return
		}
		for _, x := range bs {
			if cc, ok := x["cache_control"]; ok {
				out = append(out, string(cc))
			}
			if string(x["type"]) == `"tool_result"` {
				blocks(x["content"])
			}
		}
	}
	for _, tl := range b.Tools {
		if cc, ok := tl["cache_control"]; ok {
			out = append(out, string(cc))
		}
	}
	blocks(b.System)
	for _, m := range b.Messages {
		blocks(m.Content)
	}
	return out
}

func hasLoss(ls []ir.Loss, feature string) bool {
	return slices.ContainsFunc(ls, func(l ir.Loss) bool { return l.Feature == feature })
}

const (
	cc5m = `{"type":"ephemeral"}`
	cc1h = `{"type":"ephemeral","ttl":"1h"}`
)

func TestBuildRequestNormalizesCacheControl(t *testing.T) {
	text := func(s, cc string) string {
		if cc == "" {
			return `{"type":"text","text":"` + s + `"}`
		}
		return `{"type":"text","text":"` + s + `","cache_control":` + cc + `}`
	}
	user := func(blocks ...string) string { return `{"role":"user","content":[` + strings.Join(blocks, ",") + `]}` }
	asst := func(blocks ...string) string {
		return `{"role":"assistant","content":[` + strings.Join(blocks, ",") + `]}`
	}
	req := func(extra string, system []string, msgs ...string) string {
		s := `{"model":"m","max_tokens":64` + extra
		if len(system) > 0 {
			s += `,"system":[` + strings.Join(system, ",") + `]`
		}
		return s + `,"messages":[` + strings.Join(msgs, ",") + `]}`
	}
	toolResult := func(cc string) string {
		return `{"type":"tool_result","tool_use_id":"toolu_1","content":[` + text("result", cc) + `]}`
	}
	toolUse := `{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}`
	tool := func(cc string) string {
		s := `,"tools":[{"name":"lookup","input_schema":{"type":"object"}`
		if cc != "" {
			s += `,"cache_control":` + cc
		}
		return s + `}]`
	}

	for _, tc := range []struct {
		name     string
		body     string
		want     []string // nil: the input's markers, unchanged
		wantLoss []string
		auto     string // the top-level marker expected, when set
	}{
		{
			name: "well-formed 1h then 5m is unchanged",
			body: req(tool(cc1h), []string{text("sys", cc1h)},
				user(text("q1", "")), asst(toolUse), user(toolResult(cc5m), text("q2", cc5m))),
		},
		{
			name: "no breakpoints",
			body: req("", []string{text("sys", "")}, user(text("q", ""))),
		},
		{
			name: "5m then 1h lowers the later one",
			body: req("", []string{text("sys", cc5m)}, user(text("q", cc1h))),
			want: []string{cc5m, cc5m}, wantLoss: []string{LossCacheTTLOrder},
		},
		{
			name: "tool 5m lowers system 1h",
			body: req(tool(cc5m), []string{text("sys", cc1h)}, user(text("q", ""))),
			want: []string{cc5m, cc5m}, wantLoss: []string{LossCacheTTLOrder},
		},
		{
			name: "1h inside a tool result after a 5m message",
			body: req("", nil, user(text("q", cc5m)), asst(toolUse), user(toolResult(cc1h))),
			want: []string{cc5m, cc5m}, wantLoss: []string{LossCacheTTLOrder},
		},
		{
			name: "explicit 5m ttl counts as 5m",
			body: req("", []string{text("sys", `{"type":"ephemeral","ttl":"5m"}`)}, user(text("q", cc1h))),
			want: []string{`{"type":"ephemeral","ttl":"5m"}`, cc5m}, wantLoss: []string{LossCacheTTLOrder},
		},
		{
			name: "more than four keeps the last four",
			body: req(tool(cc1h), []string{text("s1", cc1h), text("s2", cc5m)},
				user(text("q1", cc5m)), asst(text("a1", "")), user(text("q2", cc5m), text("q3", cc5m))),
			want: []string{cc5m, cc5m, cc5m, cc5m}, wantLoss: []string{LossCacheLimit},
		},
		{
			name: "cap applies before ttl order",
			// Dropping the early 5m leaves 1h first, so nothing is lowered.
			body: req(tool(cc5m), []string{text("s1", cc1h), text("s2", cc1h)},
				user(text("q1", cc1h), text("q2", cc5m))),
			want: []string{cc1h, cc1h, cc1h, cc5m}, wantLoss: []string{LossCacheLimit},
		},
		{
			name: "automatic 1h after an explicit 5m",
			body: req(`,"cache_control":{"type":"ephemeral","ttl":"1h"}`, []string{text("s1", cc5m)}, user(text("q1", ""))),
			want: []string{cc5m}, wantLoss: []string{LossCacheTTLOrder},
			auto: cc5m,
		},
		{
			name: "automatic 1h after explicit 1h is unchanged",
			body: req(`,"cache_control":{"type":"ephemeral","ttl":"1h"}`, []string{text("s1", cc1h)}, user(text("q1", ""))),
			auto: cc1h,
		},
		{
			name: "automatic breakpoint takes a slot",
			body: req(`,"cache_control":{"type":"ephemeral"}`, []string{text("s1", cc5m), text("s2", cc5m)},
				user(text("q1", cc5m), text("q2", cc5m))),
			want: []string{cc5m, cc5m, cc5m}, wantLoss: []string{LossCacheLimit},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ParseRequest([]byte(tc.body), DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			out, losses, err := BuildRequest(r, BuildOptions{Model: "m"})
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if want == nil {
				want = cacheMarkers(t, []byte(tc.body))
			}
			if got := cacheMarkers(t, out); !slices.Equal(got, want) {
				t.Errorf("markers\n got %v\nwant %v\nbody %s", got, want, out)
			}
			if tc.auto != "" {
				var top struct {
					CC json.RawMessage `json:"cache_control"`
				}
				_ = json.Unmarshal(out, &top)
				var got, want any
				_ = json.Unmarshal(top.CC, &got)
				_ = json.Unmarshal([]byte(tc.auto), &want)
				if g, w := fmt.Sprint(got), fmt.Sprint(want); g != w {
					t.Errorf("top-level marker %s, want %s", top.CC, tc.auto)
				}
			}
			for _, f := range []string{LossCacheTTLOrder, LossCacheLimit} {
				if hasLoss(losses, f) != slices.Contains(tc.wantLoss, f) {
					t.Errorf("loss %s recorded=%v, want %v (%+v)", f, hasLoss(losses, f), slices.Contains(tc.wantLoss, f), losses)
				}
			}
		})
	}
}

// Lowering a TTL changes the outgoing body only: the parsed request (shared
// across attempts and failover) keeps the client's marker.
func TestBuildRequestCacheControlLeavesIRUntouched(t *testing.T) {
	body := `{"model":"m","max_tokens":8,"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"q","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`
	r, err := ParseRequest([]byte(body), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildRequest(r, BuildOptions{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if cc := r.Messages[0].Parts[0].CacheControl; cc == nil || cc.TTL != "1h" {
		t.Fatalf("IR marker changed: %+v", cc)
	}
}

// Provider tools are forwarded as raw JSON; their markers are counted and
// adjusted like any other.
func TestBuildRequestCacheControlOnProviderTool(t *testing.T) {
	body := `{"model":"m","max_tokens":8,"tools":[{"type":"web_search_20260209","name":"web_search","cache_control":{"type":"ephemeral"}}],
		"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages":[{"role":"user","content":"q"}]}`
	r, err := ParseRequest([]byte(body), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	out, losses, err := BuildRequest(r, BuildOptions{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cacheMarkers(t, out); !slices.Equal(got, []string{cc5m, cc5m}) {
		t.Fatalf("markers %v: %s", got, out)
	}
	if !hasLoss(losses, LossCacheTTLOrder) {
		t.Fatalf("losses %+v", losses)
	}
}
