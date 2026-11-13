package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// configuredField is one field a model adds to its upstream body, resolved
// against the provider type's allowlist.
type configuredField struct {
	rule controlplane.RequestFieldRule
	raw  json.RawMessage
}

// requestFieldSet is what a model contributes to its upstream body: the
// allowlisted free-form fields plus the typed provider pin sent as
// "provider".
type requestFieldSet struct {
	fields []configuredField // sorted by rule name
	policy string
}

// configuredFields collects the fields a model configures for a provider
// type. It does not fail: control-plane validation already refused bad names
// and values, so a field that is not (or no longer) on the allowlist is
// skipped rather than sent or allowed to break a request.
func configuredFields(providerType string, spec *controlplane.ModelSpec) (requestFieldSet, bool) {
	if spec == nil {
		return requestFieldSet{}, false
	}
	fs := requestFieldSet{policy: spec.RequestFieldPolicy}
	seen := map[string]bool{}
	for k, v := range spec.RequestFields {
		rule, ok := controlplane.LookupRequestField(providerType, k)
		if !ok || len(bytes.TrimSpace(v)) == 0 || seen[rule.Name] {
			continue
		}
		seen[rule.Name] = true
		fs.fields = append(fs.fields, configuredField{rule: rule, raw: v})
	}
	if raw, ok := spec.ProviderRouting.Wire(); ok && providerType == controlplane.ProviderOpenAICompatible && !seen["provider"] {
		fs.fields = append(fs.fields, configuredField{rule: controlplane.RequestFieldRule{Name: "provider"}, raw: raw})
	}
	sort.Slice(fs.fields, func(i, j int) bool { return fs.fields[i].rule.Name < fs.fields[j].rule.Name })
	if fs.policy == "" {
		fs.policy = controlplane.FieldPolicyConfigured
	}
	return fs, len(fs.fields) > 0
}

// ClientSettings is the set of settings the client's own request carries, by
// controlplane.FieldConcept. It is read from the client's inbound body and
// from the canonical request it was parsed into, never from the body the
// gateway built, so a field the gateway fills by default is not mistaken for
// the client's.
type ClientSettings map[string]bool

// ClientSettingsOf derives the settings a client request sets. raw is the
// client's body as received (nil for requests the gateway makes itself).
func ClientSettingsOf(r *ir.Request, raw []byte) ClientSettings {
	cs := ClientSettings{}
	if r != nil {
		set := func(ok bool, name string) {
			if ok {
				cs[controlplane.FieldConcept(name)] = true
			}
		}
		set(r.Temperature != nil, "temperature")
		set(r.TopP != nil, "top_p")
		set(r.TopK != nil, "top_k")
		set(len(r.Stop) > 0, "stop")
		set(r.Seed != nil, "seed")
		set(len(r.Metadata) > 0, "metadata")
		set(r.User != "", "user")
	}
	if len(raw) == 0 {
		return cs
	}
	o, err := wire.ParseObject(raw)
	if err != nil {
		return cs
	}
	for k, v := range o {
		c := controlplane.FieldConcept(k)
		cs[c] = true
		// Nested sampling keys (Gemini generationConfig) count as the
		// client's own settings too.
		if c == "generationconfig" {
			if sub, err := wire.ParseObject(v); err == nil {
				for sk := range sub {
					cs[controlplane.FieldConcept(sk)] = true
				}
			}
		}
	}
	return cs
}

func (cs ClientSettings) sets(rule controlplane.RequestFieldRule) bool {
	for _, c := range rule.Concepts() {
		if cs[c] {
			return true
		}
	}
	return false
}

// RefusedRequestField reports the first configured field a model with the
// reject policy would refuse for this client request. The gateway asks it for
// every routing candidate before dispatch, so the answer does not depend on
// which candidate is tried first.
func RefusedRequestField(providerType string, spec *controlplane.ModelSpec, cs ClientSettings) (string, bool) {
	fs, ok := configuredFields(providerType, spec)
	if !ok || fs.policy != controlplane.FieldPolicyReject {
		return "", false
	}
	for _, f := range fs.fields {
		if len(f.rule.Sub) == 0 {
			if cs.sets(f.rule) {
				return f.rule.Name, true
			}
			continue
		}
		for _, sk := range subKeys(f) {
			if cs[controlplane.FieldConcept(sk)] {
				return f.rule.Name + "." + sk, true
			}
		}
	}
	return "", false
}

// applyRequestFields merges a model's configured fields into an outgoing body
// and reports what it did as translation losses, so an override is never
// silent. A body is left byte-for-byte unchanged when the model configures
// nothing, which is every model that does not opt in.
func applyRequestFields(body []byte, providerType string, spec *controlplane.ModelSpec, cs ClientSettings, losses *ir.Losses) ([]byte, error) {
	fs, ok := configuredFields(providerType, spec)
	if !ok {
		return body, nil
	}
	o, err := wire.ParseObject(body)
	if err != nil {
		return nil, err
	}
	if err := mergeFields(o, fs, cs, losses); err != nil {
		return nil, err
	}
	return wire.Marshal(o)
}

// mergeFields applies one model's fields to a decoded body. It is shared by
// the translated path (on the body the gateway built) and the passthrough
// path (on the client's own bytes). A conflict exists only when the client's
// request set the field and the body still carries it.
func mergeFields(o wire.Object, fs requestFieldSet, cs ClientSettings, losses *ir.Losses) error {
	for _, f := range fs.fields {
		if len(f.rule.Sub) > 0 {
			if err := mergeObjectField(o, f, fs.policy, cs, losses); err != nil {
				return err
			}
			continue
		}
		variants := spellings(o, f.rule.Name)
		conflict := len(variants) > 0 && cs.sets(f.rule)
		if err := resolve(o, variants, f.rule.Name, f.rule.Name, f.raw, conflict, fs.policy, losses); err != nil {
			return err
		}
	}
	return nil
}

// mergeObjectField merges an object field key by key (Gemini
// generationConfig): only the configured sub-keys are set, every other key
// of the request's own object is kept.
func mergeObjectField(o wire.Object, f configuredField, policy string, cs ClientSettings, losses *ir.Losses) error {
	// Validated at save time; a value that is not an object sets no keys
	// rather than breaking the request.
	var conf map[string]json.RawMessage
	_ = json.Unmarshal(f.raw, &conf)
	// Only the canonical key is the merge base: it is the object the
	// gateway parsed (and budgeted) or built itself. Another spelling was
	// never read into the canonical request, so letting it merge in would
	// let a client override what the budget saw (its output cap
	// included). Such spellings are dropped and recorded as a loss.
	merged := wire.Object{}
	if raw, ok := o[f.rule.Name]; ok {
		sub, err := wire.ParseObject(raw)
		if err != nil {
			return &rejectedFieldError{name: f.rule.Name, msg: fmt.Sprintf("the request's %q must be a JSON object", f.rule.Name)}
		}
		merged = sub
	}
	for _, k := range spellings(o, f.rule.Name) {
		if k == f.rule.Name {
			continue
		}
		delete(o, k)
		if losses != nil {
			losses.Add("request_field:"+f.rule.Name, fmt.Sprintf("the request's %q was dropped; only %q is merged with the model's configured value", k, f.rule.Name), ir.LossDrop)
		}
	}
	for _, sk := range subKeys(f) {
		var raw json.RawMessage
		for ck, cv := range conf {
			if name, ok := f.rule.SubName(ck); ok && name == sk {
				raw = cv
			}
		}
		variants := spellings(merged, sk)
		conflict := len(variants) > 0 && cs[controlplane.FieldConcept(sk)]
		if err := resolve(merged, variants, sk, f.rule.Name+"."+sk, raw, conflict, policy, losses); err != nil {
			return err
		}
	}
	b, err := wire.Marshal(merged)
	if err != nil {
		return err
	}
	o[f.rule.Name] = b
	return nil
}

// subKeys lists the canonical sub-keys a configured object field sets.
func subKeys(f configuredField) []string {
	var conf map[string]json.RawMessage
	if err := json.Unmarshal(f.raw, &conf); err != nil {
		return nil
	}
	var out []string
	for k := range conf {
		if name, ok := f.rule.SubName(k); ok && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// resolve sets name to raw in o under the conflict policy. When the
// configured value is used, every spelling of the key is removed first, so
// the upstream never sees two (L1).
// label names the field in losses and errors (generationConfig.topP for a
// sub-key).
func resolve(o wire.Object, variants []string, name, label string, raw json.RawMessage, conflict bool, policy string, losses *ir.Losses) error {
	if conflict {
		switch policy {
		case controlplane.FieldPolicyReject:
			// The gateway refuses these before dispatch; this is the
			// executor's own guard for callers that do not.
			return &rejectedFieldError{name: label}
		case controlplane.FieldPolicyClient:
			if losses != nil {
				losses.Add("request_field:"+label, "the client's own "+label+" value was kept; the model's configured value was not used", ir.LossApproximate)
			}
			return nil
		}
	}
	for _, k := range variants {
		delete(o, k)
	}
	o[name] = raw
	if conflict && losses != nil {
		losses.Add("request_field:"+label, "the client's own "+label+" value was replaced by the model's configured value", ir.LossApproximate)
	}
	return nil
}

// spellings returns the keys of o that are the same field as name in any
// letter case or snake/camel spelling.
func spellings(o wire.Object, name string) []string {
	n := controlplane.NormalizeFieldName(name)
	var out []string
	for k := range o {
		if controlplane.NormalizeFieldName(k) == n {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// rejectedFieldError is a conflict the model's policy refuses to resolve. It is
// the caller's fault, so it becomes a 400 and is never failed over.
type rejectedFieldError struct{ name, msg string }

func (e *rejectedFieldError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return RefusedFieldMessage(e.name)
}

// RefusedFieldMessage is the client-facing text for a refused field.
func RefusedFieldMessage(name string) string {
	return fmt.Sprintf("the request sets %q, which this model configures and the gateway refuses to override", name)
}

// requestFieldError maps a merge failure to a client-facing error. A refusal is
// the caller's problem; anything else means the gateway built a body it cannot
// parse, which is internal and must not echo the body.
func requestFieldError(err error) *ir.Error {
	var rej *rejectedFieldError
	if errors.As(err, &rej) {
		return &ir.Error{Kind: ir.ErrInvalidRequest, Message: rej.Error()}
	}
	return &ir.Error{Kind: ir.ErrInternal, Message: "could not apply the model's upstream request fields"}
}
