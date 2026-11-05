package routing

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// probeHarnesses and probeStreams span the request facts rules most often
// test; each probe model is tried with every combination.
var probeHarnesses = []string{"claude_code", "codex", "opencode", "pi", "generic"}

// ProbePlan runs a fixed set of sample requests through a plan version (all
// accounts healthy) and warns when its rules block all or most of them, so
// a draft that would refuse traffic is flagged before it is published. It
// complements static validation: a table whose rows look harmless can still
// match everything.
func ProbePlan(s *cp.Snapshot, p *cp.PlanView, now time.Time) []cp.FieldError {
	if p == nil || p.Published == nil {
		return nil
	}
	if p.Published.Compiled == nil {
		c, errs := CompileEngine(p.Published.Document.Engine)
		if len(errs) > 0 {
			return nil // reported as validation errors
		}
		trial := *p
		pub := *p.Published
		pub.Compiled = c
		trial.Published = &pub
		p = &trial
	}
	binding := &cp.Res[cp.BindingSpec]{ID: "probe", Name: "probe"}
	healthy := func(string) Health { return Health{Available: true} }
	same := func(_ string, in ir.Protocol) ir.Protocol { return in }
	var total, blocked int
	sources := map[string]bool{}
	for _, model := range probeModels(s, &p.Published.Document) {
		for _, h := range probeHarnesses {
			for _, stream := range []bool{true, false} {
				d, err := Decide(Input{Snapshot: s, Binding: binding, Plan: p, RequestedModel: model, Inbound: ir.AnthropicMessages,
					RequestSeed: "probe", Now: now, HealthOf: healthy, UpstreamFor: same, Harness: h, HarnessConfidence: "simulated",
					Stream: stream, SessionConfidence: "unknown"})
				var re *Error
				if err != nil && errors.As(err, &re) && re.Kind == ir.ErrNotFound {
					continue // unmapped model: reported by document warnings
				}
				total++
				if err != nil && errors.As(err, &re) && re.Kind == ir.ErrPolicy && d != nil {
					if src := blockingSource(d); src != "" {
						blocked++
						sources[src] = true
					}
				}
			}
		}
	}
	if total == 0 || blocked == 0 {
		return nil
	}
	names := make([]string, 0, len(sources))
	for src := range sources {
		names = append(names, src)
	}
	slices.Sort(names)
	by := strings.Join(names, ", ")
	switch {
	case blocked == total:
		return []cp.FieldError{{Location: "body.document.engine", Message: fmt.Sprintf(
			"blocks every request: all %d test requests (each mapped model, every tool, streaming or not) are refused by %s", total, by)}}
	case blocked*2 >= total:
		return []cp.FieldError{{Location: "body.document.engine", Message: fmt.Sprintf(
			"blocks most requests: %d of %d test requests are refused by %s", blocked, total, by)}}
	}
	return nil
}

// probeModels lists one requested model per way into the plan: each
// mapping (wildcards filled in), each mapped model name, and an unmapped
// model when a default exists.
func probeModels(s *cp.Snapshot, d *cp.PlanDocument) []string {
	var out []string
	add := func(m string) {
		if m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	for _, m := range d.ModelMappings {
		add(strings.NewReplacer("*", "probe", "?", "x").Replace(m.Match))
		if a := s.Aliases[m.AliasID]; a != nil {
			add(a.Name)
		}
	}
	if d.DefaultAliasID != "" {
		add("probe-unmapped-model")
	}
	return out
}

// blockingSource names the rule or table row that blocked a decision.
func blockingSource(d *Decision) string {
	for _, t := range d.Rules {
		if !t.Matched || !strings.HasPrefix(t.Effect, "block") {
			continue
		}
		if t.Kind == "table" && t.Row != nil {
			return "decision table " + t.Name + " row " + strconv.Itoa(*t.Row+1)
		}
		return t.Kind + " " + t.Name
	}
	return ""
}

// HumanSource turns an effect source ("table:t1#0", "rule:r") into words.
func HumanSource(src string) string {
	kind, rest, ok := strings.Cut(src, ":")
	if !ok {
		return src
	}
	if kind == "table" {
		name, row, ok := strings.Cut(rest, "#")
		if n, err := strconv.Atoi(row); ok && err == nil {
			return "decision table " + name + ", row " + strconv.Itoa(n+1)
		}
		return "decision table " + rest
	}
	return "rule " + rest
}
