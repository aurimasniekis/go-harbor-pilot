// Package gateway serves the model data plane: it authenticates gateway
// credentials, parses client requests in their native protocol, routes them
// through the client's plan, executes attempts with policy-controlled
// failover, streams results back, and records accounting and activity.
//
// The hot path only waits on: authentication, the immutable configuration
// snapshot, one small reservation transaction per attempt and the upstream.
// Telemetry is queued; events are written to the outbox after the response.
package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	"github.com/aurimasniekis/go-harbor-pilot/internal/compat"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/httpapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ratelimit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

// Limits bound gateway work.
type Limits struct {
	MaxRequestBytes int64
	// HeaderCommit is how long a stream may wait for the first upstream event
	// before response headers are committed (to send keepalives).
	HeaderCommit time.Duration
	// Keepalive is the keepalive interval while a stream is idle.
	Keepalive time.Duration
	// SlowClientWrite bounds a single write to a slow client.
	SlowClientWrite time.Duration
}

// DefaultLimits are suitable for coding agents.
var DefaultLimits = Limits{MaxRequestBytes: 32 << 20, HeaderCommit: 10 * time.Second, Keepalive: 15 * time.Second, SlowClientWrite: 60 * time.Second}

// Gateway is the data-plane handler.
type Gateway struct {
	CP       *cp.Service
	Acct     *accounting.Service
	Tel      *telemetry.Writer
	Sessions *telemetry.Sessions
	HTTP     *httpapi.Executor
	// Bridges maps provider types to CLI bridge executors.
	Bridges map[string]executor.Executor
	DB      *store.DB
	Clock   clock.Clock
	Log     *slog.Logger
	Metrics *observability.Metrics
	Limits  Limits
	// HealthOf reports account availability (quota board, cooldowns). When
	// nil, only gateway-observed cooldowns apply.
	HealthOf func(accountID string) routing.Health
	// Observe receives unknown upstream events and translation losses for
	// Protocol Watch (must not block).
	Observe func(o Observation)
	// Privacy masks outbound content per plan policy (nil = disabled).
	Privacy *privacy.Service
	// Traces stores short-lived content captures (nil = disabled).
	Traces *traces.Service
	// Compat holds compatibility breakers; Watch records protocol changes
	// (both optional).
	Compat *compat.Manager
	Watch  *compat.Watch
	// ObserveResponse receives upstream response headers for quota
	// observations (must not block).
	ObserveResponse func(accountID, providerType, model string, h http.Header)
	// AfterRequest runs after the response has been written (events).
	AfterRequest func(ctx context.Context, r *Outcome)

	cooldowns    sync.Map // account id -> time.Time
	cooldownMeta sync.Map // account id -> cooldownInfo
	ptActive     sync.Map // breaker key -> passthrough served
	ptBlocked    sync.Map // breaker key -> passthrough refused
	limMu        sync.Mutex
	limiters     map[string]*ratelimit.Limiter
}

// Observation is a compatibility signal for Protocol Watch.
type Observation struct {
	At         time.Time
	Direction  string // request | response
	Protocol   ir.Protocol
	ProviderID string
	Mode       executor.Mode
	Unknown    []string
	Losses     []ir.Loss
	Harness    string
	HarnessVer string
}

// Routes mounts the data-plane endpoints.
func (g *Gateway) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		for _, base := range []string{"", "/anthropic"} {
			r.Post(base+"/v1/messages", g.handler(ir.AnthropicMessages))
			r.Post(base+"/v1/messages/count_tokens", g.countTokens)
		}
		r.Get("/anthropic/v1/models", g.listModels(ir.AnthropicMessages))
		for _, base := range []string{"", "/openai"} {
			r.Post(base+"/v1/chat/completions", g.handler(ir.OpenAIChat))
			r.Post(base+"/v1/responses", g.handler(ir.OpenAIResponses))
		}
		r.Get("/openai/v1/models", g.listModels(ir.OpenAIChat))
		r.Get("/v1/models", g.listModelsAuto)
		for _, base := range []string{"", "/gemini"} {
			r.Post(base+"/v1beta/models/{target}", g.geminiHandler)
			r.Get(base+"/v1beta/models", g.listModels(ir.Gemini))
		}
		r.Post("/v1/harbor/credentials", g.mintChild)
	})
}

// credentialFrom extracts a gateway token from protocol-appropriate places.
func credentialFrom(r *http.Request) string {
	if v := r.Header.Get("Authorization"); v != "" {
		if t, ok := strings.CutPrefix(v, "Bearer "); ok {
			return strings.TrimSpace(t)
		}
	}
	for _, h := range []string{"x-api-key", "x-goog-api-key"} {
		if v := r.Header.Get(h); v != "" {
			return strings.TrimSpace(v)
		}
	}
	if v := r.URL.Query().Get("key"); v != "" {
		return v
	}
	return ""
}

func (g *Gateway) writeError(w http.ResponseWriter, p ir.Protocol, e *ir.Error, reqID string) {
	w.Header().Set("Content-Type", "application/json")
	if reqID != "" {
		w.Header().Set("X-Request-ID", reqID)
	}
	if e.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", itoa(e.RetryAfterSeconds))
	}
	status := e.HTTPStatus
	if status == 0 || status < 400 {
		status = ir.StatusFor(e.Kind)
	}
	if status == 499 {
		status = 400
	}
	w.WriteHeader(status)
	_, _ = w.Write(protocol.EncodeError(p, e))
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (g *Gateway) limiter(key string, perMinute int) *ratelimit.Limiter {
	g.limMu.Lock()
	defer g.limMu.Unlock()
	if g.limiters == nil {
		g.limiters = map[string]*ratelimit.Limiter{}
	}
	k := key + "|" + itoa(perMinute)
	if l, ok := g.limiters[k]; ok {
		return l
	}
	l := ratelimit.New(perMinute, max(perMinute/6, 1), g.Clock.Now)
	g.limiters[k] = l
	return l
}

// cooldown marks an account unavailable until t.
func (g *Gateway) cooldown(accountID string, until time.Time) {
	if cur, ok := g.cooldowns.Load(accountID); ok && cur.(time.Time).After(until) {
		return
	}
	g.cooldowns.Store(accountID, until)
}

func (g *Gateway) health(accountID string) routing.Health {
	h := routing.Health{Available: true}
	if g.HealthOf != nil {
		h = g.HealthOf(accountID)
	}
	if v, ok := g.cooldowns.Load(accountID); ok {
		until := v.(time.Time)
		if g.Clock.Now().Before(until) {
			// Keep the quota view (remaining, reset) alongside the cooldown.
			h.Available, h.Reason, h.Until = false, "cooling down after upstream rate limit or auth failure", &until
			return h
		}
		g.cooldowns.Delete(accountID)
		g.noteCooldownEnded(accountID)
	}
	return h
}

// upstreamFor returns the upstream protocol for a provider type, including
// CLI bridges when installed.
func (g *Gateway) upstreamFor(providerType string, inbound ir.Protocol) ir.Protocol {
	switch providerType {
	case cp.ProviderClaudeCode:
		if g.Bridges[providerType] != nil {
			return ir.ClaudeCodeBridge
		}
		return ""
	case cp.ProviderCodex:
		if g.Bridges[providerType] != nil {
			return ir.CodexBridge
		}
		return ""
	}
	return httpapi.UpstreamProtocol(providerType, inbound)
}

func (g *Gateway) executorFor(providerType string) executor.Executor {
	if b := g.Bridges[providerType]; b != nil {
		return b
	}
	return g.HTTP
}

// AccountHealth reports current account eligibility (for simulation).
func (g *Gateway) AccountHealth(accountID string) routing.Health { return g.health(accountID) }

// UpstreamProtocol reports which protocol a provider type would use.
func (g *Gateway) UpstreamProtocol(providerType string, inbound ir.Protocol) ir.Protocol {
	return g.upstreamFor(providerType, inbound)
}
