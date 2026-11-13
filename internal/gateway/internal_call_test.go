package gateway

import (
	"testing"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// The classifier and the probe never carry a model's reasoning option:
// with max_tokens 8, a fixed level or default effort would only add billed
// reasoning tokens (D-047).
func TestInternalCallSpecDropsReasoning(t *testing.T) {
	m := &cp.ModelSpec{ID: "m", MaxOutputTokens: 100, Reasoning: &cp.ModelReasoningSpec{Mode: "fixed", Effort: "high"}}
	got := internalCallSpec(m)
	if got.Reasoning != nil || got.ID != "m" || got.MaxOutputTokens != 100 {
		t.Errorf("internal spec %+v", got)
	}
	if m.Reasoning == nil {
		t.Error("the snapshot's spec must not be modified")
	}
	plain := &cp.ModelSpec{ID: "p"}
	if internalCallSpec(plain) != plain || internalCallSpec(nil) != nil {
		t.Error("a spec without the option is used as is")
	}
}
