// Package controlplane owns managed configuration: typed resource kinds,
// their validation and canonicalization, the single mutation path (validate,
// check revision, write, history, audit, outbox, recompile snapshot), routing
// plan versions, gateway credentials and export/import.
package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// Ref is a reference from a spec field to another resource.
type Ref struct {
	Field string // dotted path used in validation messages, e.g. "spec.provider_id"
	ID    string
	Kinds []string // acceptable target kinds
}

// SecretField declares a write-only secret attached to a resource.
type SecretField struct {
	Name        string
	Description string
	// RefField names the spec field that may hold a secret reference
	// (env:/file:) instead of a stored value. Empty if refs are not allowed.
	RefField string
}

// Def describes one resource kind. S is its spec type.
type Def[S any] struct {
	Name      string // singular snake name, e.g. "provider_account"
	Plural    string // URL segment, e.g. "provider-accounts"
	Prefix    string // id prefix
	Title     string // human title
	NameRE    *regexp.Regexp
	NameHint  string
	Secrets   []SecretField
	Normalize func(s *S)
	Validate  func(v *Validator, s *S)
	Refs      func(s *S) []Ref
}

// Kind is the type-erased view of a Def used by the service.
type Kind interface {
	KindName() string
	KindPlural() string
	KindPrefix() string
	KindTitle() string
	SecretFields() []SecretField
	ValidName(string) bool
	NameRule() string
	// Canonicalize decodes raw JSON strictly, normalizes, validates and
	// re-encodes canonically. It returns the references for integrity checks.
	Canonicalize(raw json.RawMessage, v *Validator) (json.RawMessage, []Ref)
}

// DefaultNameRE allows lowercase slugs.
var DefaultNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// PathNameRE additionally allows "/" (used by model aliases like coding/default).
var PathNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}(/[a-z0-9][a-z0-9._-]{0,62}){0,3}$`)

func (d *Def[S]) KindName() string            { return d.Name }
func (d *Def[S]) KindPlural() string          { return d.Plural }
func (d *Def[S]) KindPrefix() string          { return d.Prefix }
func (d *Def[S]) KindTitle() string           { return d.Title }
func (d *Def[S]) SecretFields() []SecretField { return d.Secrets }
func (d *Def[S]) ValidName(n string) bool {
	re := d.NameRE
	if re == nil {
		re = DefaultNameRE
	}
	return re.MatchString(n)
}
func (d *Def[S]) NameRule() string {
	if d.NameHint != "" {
		return d.NameHint
	}
	return "lowercase letters, digits, '.', '_' and '-', starting with a letter or digit, at most 63 characters"
}

// Canonicalize implements Kind.
func (d *Def[S]) Canonicalize(raw json.RawMessage, v *Validator) (json.RawMessage, []Ref) {
	var s S
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		v.Fail("spec", "invalid spec: "+err.Error())
		return nil, nil
	}
	if d.Normalize != nil {
		d.Normalize(&s)
	}
	if d.Validate != nil {
		d.Validate(v, &s)
	}
	var refs []Ref
	if d.Refs != nil {
		refs = d.Refs(&s)
	}
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		if !v.Exists(r.ID, r.Kinds...) {
			v.Fail(r.Field, fmt.Sprintf("references unknown %v %q", r.Kinds, r.ID))
		}
	}
	out, err := json.Marshal(s)
	if err != nil {
		v.Fail("spec", "cannot encode spec")
		return nil, nil
	}
	return out, refs
}

// Decode decodes a canonical spec into its typed form.
func Decode[S any](raw json.RawMessage) (S, error) {
	var s S
	err := json.Unmarshal(raw, &s)
	return s, err
}

// ---------- validator ----------

// FieldError is one validation problem.
type FieldError struct {
	Location string `json:"location"`
	Message  string `json:"message"`
}

// Validator accumulates field errors and resolves references.
type Validator struct {
	Errors []FieldError
	lookup func(id string) (kind string, ok bool)
	self   string
}

// NewValidator creates a validator; lookup resolves resource ids to kinds.
func NewValidator(self string, lookup func(id string) (string, bool)) *Validator {
	return &Validator{lookup: lookup, self: self}
}

// Fail records an error at a location (prefixed with body.).
func (v *Validator) Fail(loc, msg string) {
	v.Errors = append(v.Errors, FieldError{Location: "body." + loc, Message: msg})
}

// Check records msg at loc when ok is false.
func (v *Validator) Check(ok bool, loc, msg string) {
	if !ok {
		v.Fail(loc, msg)
	}
}

// OK reports whether no errors were recorded.
func (v *Validator) OK() bool { return len(v.Errors) == 0 }

// Exists reports whether id exists with one of kinds (any kind if empty).
func (v *Validator) Exists(id string, kinds ...string) bool {
	if v.lookup == nil {
		return true
	}
	k, ok := v.lookup(id)
	if !ok {
		return false
	}
	if len(kinds) == 0 {
		return true
	}
	for _, want := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// Self returns the id of the resource being validated (empty on create).
func (v *Validator) Self() string { return v.self }

// ---------- helpers used by specs ----------

// SortedUnique sorts and de-duplicates a string list (nil stays empty list).
func SortedUnique(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	m := map[string]struct{}{}
	for _, s := range in {
		m[s] = struct{}{}
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// EmptyMap returns m or an empty map (so JSON never contains null maps).
func EmptyMap[V any](m map[string]V) map[string]V {
	if m == nil {
		return map[string]V{}
	}
	return m
}
