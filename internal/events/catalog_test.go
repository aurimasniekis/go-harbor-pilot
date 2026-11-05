package events

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/contracts/events.md")

// TestCatalogDoc keeps docs/contracts/events.md in sync with the catalog.
func TestCatalogDoc(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Event catalog\n\nGenerated from `internal/events/catalog.go` (`go test ./internal/events -update`).\n\n")
	b.WriteString("Every event is published as a JSON envelope `{schema_version, id, type, name, occurred_at, severity, correlation_id, subjects, data}`. ")
	b.WriteString("Subjects hold identifiers only; `data` holds allowlisted, redacted fields and never prompts or secrets. ")
	b.WriteString("Delivery is at-least-once: consumers deduplicate by `id` (Pulse `Nats-Msg-Id`, webhook `X-Harbor-Event-Id`).\n\n")
	b.WriteString("Webhooks carry `X-Harbor-Signature: t=<unix>,v1=<hex>[,v1=<hex>]`, an HMAC-SHA256 over `<t>.<body>` with the current (and, during rotation, the previous) signing secret. ")
	b.WriteString("Receivers should reject timestamps more than five minutes away and deduplicate by event id.\n\n")
	b.WriteString("| Name | Type | Severity | Subjects | Data | Resolved by | Meaning |\n|---|---|---|---|---|---|---|\n")
	for _, k := range Catalog {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s | %s | %s |\n", k.Name, k.Type, k.Severity, strings.Join(k.Subjects, ", "), strings.Join(k.Data, ", "), k.Pair, k.Description)
	}
	path := "../../docs/contracts/events.md"
	if *update {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil { //nolint:gosec // documentation file
			t.Fatal(err)
		}
		return
	}
	cur, err := os.ReadFile(path)
	if err != nil || string(cur) != b.String() {
		t.Fatal("docs/contracts/events.md is stale; run go test ./internal/events -update")
	}
}
