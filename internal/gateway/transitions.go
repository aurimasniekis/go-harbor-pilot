package gateway

import (
	"context"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
)

// emit writes a transition event to the outbox off the request path.
func (g *Gateway) emit(k events.Kind, sev string, subjects map[string]string, data map[string]any, dedupe string) {
	if g.DB == nil {
		return
	}
	at := g.Clock.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = events.Append(ctx, g.DB.W, events.Event{Kind: k, Severity: sev, Subjects: subjects, Data: data, DedupeKey: dedupe, OccurredAt: at})
	}()
}

type cooldownInfo struct {
	until      time.Time
	providerID string
}

// noteCooldown records an account entering cooldown (a transition only
// when it was not already cooling down).
func (g *Gateway) noteCooldown(accountID, providerID, reason string, until time.Time) {
	prev, had := g.cooldowns.Load(accountID)
	active := had && g.Clock.Now().Before(prev.(time.Time))
	g.cooldown(accountID, until)
	g.cooldownMeta.Store(accountID, cooldownInfo{until: until, providerID: providerID})
	if !active {
		g.emit(events.Kind{Area: "provider", Entity: "account", Action: "cooldown_started"}, events.SeverityWarning,
			map[string]string{"account_id": accountID, "provider_id": providerID},
			map[string]any{"reason": reason, "until": until}, "account-health:"+accountID)
	}
}

// noteCooldownEnded is called when an expired cooldown is cleared.
func (g *Gateway) noteCooldownEnded(accountID string) {
	v, ok := g.cooldownMeta.LoadAndDelete(accountID)
	if !ok {
		return
	}
	g.emit(events.Kind{Area: "provider", Entity: "account", Action: "recovered"}, events.SeverityInfo,
		map[string]string{"account_id": accountID, "provider_id": v.(cooldownInfo).providerID}, map[string]any{}, "account-health:"+accountID)
}

// passthroughServed records the first passthrough response on a breaker.
func (g *Gateway) passthroughServed(key, providerID, path string) {
	if _, loaded := g.ptActive.LoadOrStore(key, true); !loaded {
		g.emit(events.Kind{Area: "compat", Entity: "passthrough", Action: "activated"}, events.SeverityWarning,
			map[string]string{"provider_id": providerID}, map[string]any{"path": path}, "passthrough:"+key)
	}
}

// passthroughRefused records that passthrough was refused on a breaker.
func (g *Gateway) passthroughRefused(key, providerID, path, reason string) {
	if _, loaded := g.ptBlocked.LoadOrStore(key, true); !loaded {
		g.emit(events.Kind{Area: "compat", Entity: "passthrough", Action: "blocked"}, events.SeverityWarning,
			map[string]string{"provider_id": providerID}, map[string]any{"path": path, "reason": reason}, "passthrough-blocked:"+key)
	}
}

// BreakerClosed ends passthrough bookkeeping for a breaker key.
func (g *Gateway) BreakerClosed(key, providerID, path string) {
	g.ptBlocked.Delete(key)
	if _, was := g.ptActive.LoadAndDelete(key); was {
		g.emit(events.Kind{Area: "compat", Entity: "passthrough", Action: "deactivated"}, events.SeverityInfo,
			map[string]string{"provider_id": providerID}, map[string]any{"path": path}, "passthrough:"+key)
	}
}
