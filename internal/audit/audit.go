// Package audit records security, configuration and content-access
// operations. Entries are append-only; details are redacted and bounded.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Outcomes.
const (
	Success = "success"
	Denied  = "denied"
	Failed  = "error"
)

// Entry is one audit record.
type Entry struct {
	Action     string
	TargetKind string
	TargetID   string
	Outcome    string
	Details    map[string]any
}

const maxDetailsBytes = 8 << 10

// Record writes an entry using the principal and request metadata on ctx.
// Pass a transaction to make the audit row atomic with the change.
func Record(ctx context.Context, ex store.Execer, e Entry) error {
	p := auth.FromContext(ctx)
	m := reqmeta.From(ctx)
	if e.Outcome == "" {
		e.Outcome = Success
	}
	details := "{}"
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err == nil {
			details = observability.RedactString(string(b))
		}
		if len(details) > maxDetailsBytes {
			details = `{"truncated":true}`
		}
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO audit_log(id, at, actor_id, actor_kind, action, target_kind, target_id,
		outcome, ip, user_agent, request_id, details) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ids.New(ids.Audit), time.Now().UnixMilli(), p.ID, string(p.Kind), e.Action, nullable(e.TargetKind), nullable(e.TargetID),
		e.Outcome, nullable(m.IP), nullable(m.UserAgent), nullable(observability.RequestID(ctx)), details)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
