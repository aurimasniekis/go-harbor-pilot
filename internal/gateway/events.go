package gateway

import (
	"context"
	"database/sql"
	"sync"

	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
)

// Transitions emits domain events for routing transitions. It records
// changes (a pool's serving account switching, a request falling back to a
// different model or provider, a request blocked by policy), never every
// candidate evaluation. Events go to the transactional outbox after the
// response has been written, so delivery never affects the request.
type Transitions struct {
	DB *sql.DB
	mu sync.Mutex
	// serving remembers the account last used per pool (in memory; after a
	// restart the first request re-establishes the baseline silently).
	serving map[string]string
}

// NewTransitions creates the emitter.
func NewTransitions(db *sql.DB) *Transitions {
	return &Transitions{DB: db, serving: map[string]string{}}
}

// After is installed as Gateway.AfterRequest.
func (t *Transitions) After(ctx context.Context, o *Outcome) {
	var evs []events.Event
	subjects := map[string]string{"request_id": o.RequestID, "binding_id": o.BindingID}
	if o.Blocked {
		data := map[string]any{"reason": o.BlockReason}
		if o.Decision != nil {
			data["requested_model"] = o.Decision.RequestedModel
			data["plan_id"] = o.Decision.PlanID
			data["excluded"] = len(o.Decision.Excluded)
		}
		evs = append(evs, events.Event{Kind: events.Kind{Area: "access", Entity: "request", Action: "blocked"}, Severity: events.SeverityWarning,
			Subjects: subjects, Data: data, DedupeKey: "blocked:" + o.BindingID})
	}
	if o.FinalIndex >= 0 && o.Decision != nil {
		final := o.Attempts[o.FinalIndex].Candidate
		first := o.Attempts[0].Candidate
		if o.FinalIndex > 0 && (final.ProviderID != first.ProviderID || final.Model != first.Model) {
			evs = append(evs, events.Event{Kind: events.Kind{Area: "routing", Entity: "fallback", Action: "selected"}, Severity: events.SeverityWarning,
				Subjects:  mergeSubjects(subjects, map[string]string{"plan_id": o.Decision.PlanID, "pool_id": o.Decision.PoolID}),
				DedupeKey: "fallback:" + o.Decision.PoolID + ":" + final.AccountID + ":" + final.Model,
				Data: map[string]any{"from": candidateRef(first), "to": candidateRef(final), "step": o.Attempts[o.FinalIndex].Step,
					"reason": attemptReason(o.Attempts[:o.FinalIndex])}})
		}
		t.mu.Lock()
		prev, had := t.serving[o.Decision.PoolID]
		t.serving[o.Decision.PoolID] = final.AccountID
		t.mu.Unlock()
		if had && prev != final.AccountID {
			evs = append(evs, events.Event{Kind: events.Kind{Area: "routing", Entity: "account", Action: "switched"}, Severity: events.SeverityInfo,
				Subjects:  mergeSubjects(subjects, map[string]string{"pool_id": o.Decision.PoolID, "from_account_id": prev, "to_account_id": final.AccountID}),
				DedupeKey: "switch:" + o.Decision.PoolID,
				Data: map[string]any{"pool": o.Decision.Pool, "from_account_id": prev, "to": candidateRef(final),
					"reason": attemptReason(o.Attempts[:o.FinalIndex])}})
		}
	}
	if len(evs) == 0 {
		return
	}
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	for _, e := range evs {
		if _, err := events.Append(ctx, tx, e); err != nil {
			_ = tx.Rollback()
			return
		}
	}
	_ = tx.Commit()
}

func mergeSubjects(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func candidateRef(c routing.Candidate) map[string]any {
	return map[string]any{"provider_id": c.ProviderID, "account_id": c.AccountID, "account": c.Account, "model": c.Model, "billing": c.Billing}
}

func attemptReason(prev []AttemptOutcome) string {
	for i := len(prev) - 1; i >= 0; i-- {
		if prev[i].Err != nil {
			return string(prev[i].Err.Kind)
		}
	}
	return "unavailable"
}
