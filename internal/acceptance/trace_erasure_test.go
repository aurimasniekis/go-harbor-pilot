package acceptance_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

// Values the masking policy keeps from the provider are not stored in the
// search index or in masked trace content, and expired trace text leaves
// the database files (including the write-ahead log) after a sweep.
func TestTraceIndexHoldsNoMaskedValuesAndErases(t *testing.T) {
	const (
		dbName = "corp-db-4242"
		email  = "ops@example.test"
		marker = "zebracanaryword"
	)
	clk := clock.NewFake(time.Now().UTC())
	r := newPrivacyRig(t, map[string]any{"required": true, "personal_data": []string{"email"},
		"rules": []any{map[string]any{"name": "db", "pattern": `corp-db-[0-9]+`, "action": "mask"}}},
		map[string]any{"mode": "masked_full", "retention": "custom", "custom_ttl_seconds": 3600}, apptest.WithClock(clk))
	r.up.Push(apptest.Reply{Text: "ok"})
	resp := r.e.Gateway("POST", "/v1/messages", r.tok, map[string]any{"model": "x", "max_tokens": 16,
		"messages": []any{map[string]any{"role": "user", "content": marker + " connect " + dbName + " for " + email}}})
	if b := apptest.ReadAll(resp); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	r.waitTraces(1)
	ts := r.e.App.TraceSvc

	var hits struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet string `json:"snippet"`
			Parts   []struct {
				Text  string `json:"text"`
				Match bool   `json:"match"`
			} `json:"snippet_parts"`
		} `json:"items"`
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/traces/search?q="+dbName, nil, &hits)
	if len(hits.Items) != 0 {
		t.Fatalf("a masked value is searchable: %+v", hits.Items)
	}
	r.e.Admin.MustDo(200, "GET", "/api/v1/traces/search?q="+marker, nil, &hits)
	if len(hits.Items) != 1 {
		t.Fatalf("live trace not searchable by ordinary text: %+v", hits.Items)
	}
	if s := hits.Items[0].Snippet; strings.Contains(s, email) || strings.Contains(s, dbName) {
		t.Fatalf("snippet shows a masked value: %q", s)
	}
	matched := false
	for _, p := range hits.Items[0].Parts {
		matched = matched || (p.Match && strings.EqualFold(p.Text, marker))
	}
	if !matched || strings.ContainsAny(hits.Items[0].Snippet, "\x02\x03") {
		t.Fatalf("search hit does not mark the match: %+v", hits.Items[0])
	}
	var tr json.RawMessage
	r.e.Admin.MustDo(200, "GET", "/api/v1/traces/"+hits.Items[0].ID, nil, &tr)
	if strings.Contains(string(tr), dbName) || strings.Contains(string(tr), email) {
		t.Fatalf("masked trace content holds a value the policy masked: %s", tr)
	}

	clk.Advance(50 * time.Hour) // past retention and the bucket's day
	ctx := context.Background()
	if res, err := ts.Sweep(ctx); err != nil || res.Traces != 1 {
		t.Fatalf("sweep %+v %v", res, err)
	}
	if l, _ := ts.List(ctx, traces.Filter{}); len(l) != 0 {
		t.Fatal("expired trace still listed")
	}
	for _, needle := range []string{marker, dbName, email} {
		if hits := fileContains(t, r.e.DataDir, needle); len(hits) > 0 {
			t.Errorf("%q still present in %v after the sweep", needle, hits)
		}
	}
}
