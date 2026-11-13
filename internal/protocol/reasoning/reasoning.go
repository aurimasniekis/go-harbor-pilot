// Package reasoning holds the translation methods that turn a request's
// reasoning settings into an upstream's wire fields: one method per wire
// shape (Anthropic thinking, Chat reasoning_effort, Responses reasoning,
// Gemini thinkingConfig, …), registered by name. Protocol builders call
// Apply, the single entry point; with no model policy each protocol keeps
// its historical (legacy) translation byte for byte.
//
// The package is a leaf: it imports only the IR.
package reasoning

import (
	"fmt"
	"sort"
	"sync"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// Effort levels, lowest first.
const (
	EffortNone    = "none"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

// Ladder lists every effort level in order.
var Ladder = []string{EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// Rank is an effort level's position on the ladder, or -1 when unknown.
func Rank(effort string) int {
	for i, e := range Ladder {
		if e == effort {
			return i
		}
	}
	return -1
}

// Plan is a resolved reasoning decision in method-neutral terms: what the
// upstream should be asked for.
type Plan struct {
	// Off asks the upstream not to reason.
	Off bool
	// Effort is the level to request ("" when only a budget, or nothing,
	// is known).
	Effort string
	// BudgetTokens is an explicit thinking budget (0 = none).
	BudgetTokens int
	// Adaptive lets the model decide how much to think (no level).
	Adaptive bool
	// Summary and IncludeEncrypted pass the client's Responses options on.
	Summary          string
	IncludeEncrypted bool
}

// Target is the upstream body a method writes into.
type Target struct {
	// Body is the object the method's fields belong to (for Gemini, the
	// generationConfig object).
	Body map[string]any
	// Losses receives what the translation could not carry.
	Losses *ir.Losses
	// MaxTokens is the output limit of the built request (0 = unknown).
	MaxTokens int
	// Include is the Responses include list (nil for other protocols).
	Include *[]string
}

// Result reports what a method did.
type Result struct {
	// Consumed lists the request's Ext keys the method translated: the
	// builder neither restores them nor reports them as dropped.
	Consumed []string
	// Active reports that a reasoning configuration was in play.
	Active bool
}

// Method is one way of writing reasoning settings on the wire.
type Method interface {
	// Name is the method's registry name (the model option's style).
	Name() string
	// Protocol is the wire protocol whose bodies the method writes into
	// ("" for a method that writes nothing).
	Protocol() ir.Protocol
	// DefaultEfforts are the levels the wire accepts when the model does
	// not list its own; others are clamped to them.
	DefaultEfforts() []string
	// DefaultHistoryField names the assistant message field that carries
	// earlier reasoning text back ("" when the wire has none).
	DefaultHistoryField() string
	// Write renders a resolved plan.
	Write(p Plan, t *Target) Result
}

// legacyWriter is implemented by the protocol defaults: the translation
// each builder used before methods existed, kept byte for byte.
type legacyWriter interface {
	WriteLegacy(rc *ir.ReasoningConfig, t *Target) Result
}

var (
	mu       sync.RWMutex
	registry = map[string]Method{}
	defaults = map[ir.Protocol]string{}
)

// Register adds a method. It panics on a duplicate name (a programming
// error caught at init).
func Register(m Method) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[m.Name()]; dup {
		panic(fmt.Sprintf("reasoning: method %q registered twice", m.Name()))
	}
	registry[m.Name()] = m
}

// setDefault names a protocol's legacy method.
func setDefault(p ir.Protocol, name string) {
	mu.Lock()
	defer mu.Unlock()
	defaults[p] = name
}

// Lookup returns a registered method.
func Lookup(name string) (Method, bool) {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := registry[name]
	return m, ok
}

// Names lists the registered methods, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DefaultFor is a protocol's legacy method.
func DefaultFor(p ir.Protocol) Method {
	mu.RLock()
	defer mu.RUnlock()
	return registry[defaults[p]]
}

// ForProtocol lists the methods that write into a protocol's bodies,
// sorted by name.
func ForProtocol(p ir.Protocol) []Method {
	var out []Method
	for _, n := range Names() {
		if m, _ := Lookup(n); m.Protocol() == p {
			out = append(out, m)
		}
	}
	return out
}

// Apply writes a request's reasoning settings for protocol p. It is what
// every builder calls.
func Apply(r *ir.Request, p ir.Protocol, t *Target) Result {
	m, ok := DefaultFor(p).(legacyWriter)
	if !ok {
		return Result{}
	}
	return m.WriteLegacy(r.Reasoning.Legacy(), t)
}
