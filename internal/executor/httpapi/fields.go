package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// requestFieldSet is what a model contributes to its upstream body: the
// free-form fields plus the typed provider pin rendered as "provider".
type requestFieldSet struct {
	fields map[string]json.RawMessage
	policy string
}

// configuredFields collects the fields a model configures. It does not fail:
// control-plane validation already refused bad values, so a field that no
// longer fits is skipped rather than breaking a request.
func configuredFields(spec *controlplane.ModelSpec) (requestFieldSet, bool) {
	if spec == nil {
		return requestFieldSet{}, false
	}
	fs := requestFieldSet{fields: map[string]json.RawMessage{}, policy: spec.RequestFieldPolicy}
	for k, v := range spec.RequestFields {
		if len(v) == 0 || controlplane.IsStructuralRequestField(k) {
			continue
		}
		fs.fields[k] = v
	}
	if raw, ok := spec.ProviderRouting.Wire(); ok {
		fs.fields["provider"] = raw
	}
	if fs.policy == "" {
		fs.policy = controlplane.FieldPolicyConfigured
	}
	return fs, len(fs.fields) > 0
}

// applyRequestFields merges a model's configured fields into an outgoing body
// and reports what it did as translation losses, so an override is never
// silent. A body is left byte-for-byte unchanged when the model configures
// nothing, which is every model today.
func applyRequestFields(body []byte, spec *controlplane.ModelSpec, losses *ir.Losses) ([]byte, error) {
	fs, ok := configuredFields(spec)
	if !ok {
		return body, nil
	}
	o, err := wire.ParseObject(body)
	if err != nil {
		return nil, err
	}
	if err := mergeFields(o, fs, losses); err != nil {
		return nil, err
	}
	return wire.Marshal(o)
}

// mergeFields applies one model's fields to a decoded body. It is shared by
// the translated path (on the body the gateway built) and the passthrough
// path (on the client's own bytes).
func mergeFields(o wire.Object, fs requestFieldSet, losses *ir.Losses) error {
	// Deterministic order, so an operator and a test see the same first error.
	names := make([]string, 0, len(fs.fields))
	for k := range fs.fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := fs.fields[name]
		// JSON keys are case sensitive but upstream routers are not always;
		// an existing "Provider" must not become a second "provider".
		existing := ""
		for k := range o {
			if k == name || (len(k) == len(name) && equalFold(k, name)) {
				existing = k
				break
			}
		}
		if existing == "" {
			o[name] = raw
			continue
		}
		switch fs.policy {
		case controlplane.FieldPolicyReject:
			return &rejectedFieldError{name: name}
		case controlplane.FieldPolicyClient:
			if losses != nil {
				losses.Add("request_field:"+name, "the client's own "+name+" value was kept; the model's configured value was not used", ir.LossApproximate)
			}
		default: // configured: the operator's pin wins
			if existing != name {
				delete(o, existing)
			}
			o[name] = raw
			if losses != nil {
				losses.Add("request_field:"+name, "the client's own "+name+" value was replaced by the model's configured value", ir.LossApproximate)
			}
		}
	}
	return nil
}

// rejectedFieldError is a conflict the model's policy refuses to resolve. It is
// the caller's fault, so it becomes a 400 and is never failed over.
type rejectedFieldError struct{ name string }

func (e *rejectedFieldError) Error() string {
	return fmt.Sprintf("the request sets %q, which this model configures and the gateway refuses to override", e.name)
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

// equalFold reports whether a and b differ only by ASCII letter case.
func equalFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
