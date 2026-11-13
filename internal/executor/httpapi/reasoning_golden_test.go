package httpapi

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/protocol/reasoning/golden")

// reasoningDir holds synthetic request bodies with reasoning settings, one
// per source protocol and shape (see testdata/protocol/README.md).
var reasoningDir = filepath.Join("..", "..", "..", "testdata", "protocol", "reasoning")

// goldenProviders are the HTTP provider types a request can be built for.
var goldenProviders = []string{
	controlplane.ProviderAnthropic,
	controlplane.ProviderOpenAI,
	controlplane.ProviderGemini,
	controlplane.ProviderOpenAICompatible,
	controlplane.ProviderOllama,
}

type goldenTarget struct {
	Protocol ir.Protocol     `json:"protocol"`
	Losses   []string        `json:"losses"`
	Body     json.RawMessage `json:"body"`
}

type goldenFile struct {
	Requirements []string                 `json:"requirements"`
	Targets      map[string]*goldenTarget `json:"targets"`
}

func parseFixture(t *testing.T, name string, body []byte) *ir.Request {
	t.Helper()
	var (
		r   *ir.Request
		err error
	)
	switch {
	case strings.HasPrefix(name, "anthropic_"):
		r, err = anthropic.ParseRequest(body, anthropic.DefaultLimits)
	case strings.HasPrefix(name, "chat_"):
		r, err = openaichat.ParseRequest(body, openaichat.DefaultLimits)
	case strings.HasPrefix(name, "responses_"):
		r, err = openairesponses.ParseRequest(body, openairesponses.DefaultLimits)
	case strings.HasPrefix(name, "gemini_"):
		r, err = gemini.ParseRequest(body, "client-model", false)
	default:
		t.Fatalf("fixture %s: unknown source protocol prefix", name)
	}
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return r
}

func goldenTargetFor(providerType string) executor.Target {
	return executor.Target{
		Provider:  &controlplane.Res[controlplane.ProviderSpec]{ID: "prv_golden", Spec: controlplane.ProviderSpec{Type: providerType, BaseURL: "https://upstream.example.com"}},
		Model:     "upstream-model",
		ModelSpec: &controlplane.ModelSpec{ID: "upstream-model", MaxOutputTokens: 64000},
	}
}

func lossStrings(ls []ir.Loss) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Feature+" | "+string(l.Severity)+" | "+l.Detail)
	}
	return out
}

func encodeGolden(t *testing.T, g *goldenFile) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func compactJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return b.Bytes()
}

// TestReasoningGoldens pins the upstream body and losses every reasoning
// shape produces for every HTTP provider type, byte for byte. A model with
// no reasoning option must keep producing exactly these bodies; a change
// here is a behaviour change and needs `go test ./internal/executor/httpapi
// -run TestReasoningGoldens -update` plus a reviewed diff.
func TestReasoningGoldens(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join(reasoningDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no reasoning fixtures found")
	}
	for _, path := range fixtures {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := &goldenFile{Requirements: []string{}, Targets: map[string]*goldenTarget{}}
			req := ir.Requirements(parseFixture(t, name, src))
			sort.Strings(req)
			got.Requirements = append(got.Requirements, req...)
			for _, pt := range goldenProviders {
				// Each build gets its own parse: builders must not depend on
				// state left behind by another build.
				r := parseFixture(t, name, src)
				up := UpstreamProtocol(pt, r.Source)
				body, losses, err := build(r, goldenTargetFor(pt), up, r.Stream)
				if err != nil {
					t.Fatalf("%s: build: %v", pt, err)
				}
				got.Targets[pt] = &goldenTarget{Protocol: up, Losses: lossStrings(losses), Body: body}
			}
			goldenPath := filepath.Join(reasoningDir, "golden", name+".golden.json")
			if *updateGolden {
				if err := os.WriteFile(goldenPath, encodeGolden(t, got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			var want goldenFile
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("golden: %v", err)
			}
			if strings.Join(got.Requirements, ",") != strings.Join(want.Requirements, ",") {
				t.Errorf("requirements = %v, want %v", got.Requirements, want.Requirements)
			}
			for _, pt := range goldenProviders {
				g, w := got.Targets[pt], want.Targets[pt]
				if w == nil {
					t.Errorf("%s: no golden entry", pt)
					continue
				}
				if g.Protocol != w.Protocol {
					t.Errorf("%s: protocol %s, want %s", pt, g.Protocol, w.Protocol)
				}
				if gb, wb := g.Body, compactJSON(t, w.Body); !bytes.Equal(gb, wb) {
					t.Errorf("%s: body changed\n got %s\nwant %s", pt, gb, wb)
				}
				if gl, wl := strings.Join(g.Losses, "\n"), strings.Join(w.Losses, "\n"); gl != wl {
					t.Errorf("%s: losses changed\n got %s\nwant %s", pt, gl, wl)
				}
			}
		})
	}
}
