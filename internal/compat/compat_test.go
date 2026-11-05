package compat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(t.TempDir(), "t.db"), Migrations: migrations.Main()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestClassify(t *testing.T) {
	tests := []struct {
		err  *ir.Error
		want string
	}{
		{&ir.Error{Kind: ir.ErrProtocol, Message: "malformed message_start event"}, ClassCompat},
		{&ir.Error{Kind: ir.ErrInvalidRequest, Message: "tools.0.custom: Extra inputs are not permitted"}, ClassCompat},
		{&ir.Error{Kind: ir.ErrInvalidRequest, Message: "messages: field required"}, ClassClient},
		{&ir.Error{Kind: ir.ErrUnsupported, Message: "the request needs features this upstream cannot provide: vision"}, ClassCapability},
		{&ir.Error{Kind: ir.ErrAuthentication}, ClassAuth},
		{&ir.Error{Kind: ir.ErrRateLimited}, ClassRateLimit},
		{&ir.Error{Kind: ir.ErrOverloaded}, ClassOutage},
	}
	for _, tt := range tests {
		if got := Classify(tt.err); got != tt.want {
			t.Errorf("%s/%q: %s want %s", tt.err.Kind, tt.err.Message, got, tt.want)
		}
	}
}

func TestBreakerLifecycle(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	m, err := NewManager(context.Background(), testDB(t), clk, Options{Threshold: 3, Window: time.Minute, Cooldown: time.Minute, CloseAfter: 2})
	if err != nil {
		t.Fatal(err)
	}
	in, up := ir.AnthropicMessages, ir.AnthropicMessages
	perr := &ir.Error{Kind: ir.ErrProtocol, Message: "bad"}
	// Non-compat failures never open the breaker.
	for range 10 {
		m.Record("prv", in, up, false, &ir.Error{Kind: ir.ErrRateLimited})
	}
	if d := m.Check("prv", in, up); d.State != Closed {
		t.Fatal("rate limits must not open a compatibility breaker")
	}
	// Failures spread beyond the window do not accumulate.
	m.Record("prv", in, up, false, perr)
	clk.Advance(2 * time.Minute)
	m.Record("prv", in, up, false, perr)
	m.Record("prv", in, up, false, perr)
	if d := m.Check("prv", in, up); d.State != Closed {
		t.Fatal("old failures counted")
	}
	m.Record("prv", in, up, false, perr)
	if d := m.Check("prv", in, up); d.State != Open {
		t.Fatalf("threshold reached but %s", d.State)
	}
	// Cooldown, then a single half-open probe at a time.
	clk.Advance(61 * time.Second)
	d1, d2 := m.Check("prv", in, up), m.Check("prv", in, up)
	if d1.State != HalfOpen || !d1.Probe || d2.State != Open {
		t.Fatalf("half-open: %+v %+v", d1, d2)
	}
	// A failed probe re-opens with a longer cooldown.
	m.Record("prv", in, up, true, perr)
	clk.Advance(61 * time.Second)
	if d := m.Check("prv", in, up); d.State != Open {
		t.Fatal("cooldown should have doubled")
	}
	clk.Advance(61 * time.Second)
	for i := range 2 {
		d := m.Check("prv", in, up)
		if !d.Probe {
			t.Fatalf("probe %d not granted: %+v", i, d)
		}
		m.Record("prv", in, up, true, nil)
	}
	if d := m.Check("prv", in, up); d.State != Closed {
		t.Fatalf("two probe successes should close, got %s", d.State)
	}
	// Overrides win over automatic state.
	if _, err := m.SetOverride(context.Background(), "prv", string(in)+">"+string(up), OverrideForceOpen, "drill"); err != nil {
		t.Fatal(err)
	}
	if d := m.Check("prv", in, up); d.State != Open {
		t.Fatal("force_open ignored")
	}
	// State survives a restart.
	m2, _ := NewManager(context.Background(), m.DB, clk, Options{})
	if d := m2.Check("prv", in, up); d.State != Open {
		t.Fatal("override not persisted")
	}
}

func TestFingerprintSanitization(t *testing.T) {
	paths, enums := map[string]string{}, map[string]string{}
	var v map[string]any
	_ = jsonUnmarshal(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01ABCDEF1234567890ab","name":"secret_tool",
		"input":{"customer_password":"hunter2","x":{"y":1}}},"metadata":{"user_8f3a9c2b1d4e5f60718293a4":"v"}}`, &v)
	walk(v, "", 0, paths, enums)
	for p := range paths {
		if p == "content_block.input.customer_password" || p == "content_block.input.x.y" || p == "metadata.user_8f3a9c2b1d4e5f60718293a4" {
			t.Fatalf("user-defined key leaked into structure: %s", p)
		}
	}
	if paths["content_block.input"] != "object" || enums["content_block.type"] != "tool_use" {
		t.Fatalf("%v %v", paths, enums)
	}
	for _, e := range enums {
		if e == "hunter2" || e == "secret_tool" {
			t.Fatal("content value kept")
		}
	}
	if got := normalizeRejection(`tools.3.input_schema: Field "foo_bar" is not a valid enum value 'x'`); got != `tools.<n>.input_schema: Field <v> is not a valid enum value <v>` {
		t.Fatalf("%q", got)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
