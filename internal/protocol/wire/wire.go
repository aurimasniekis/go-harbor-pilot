// Package wire contains helpers shared by protocol adapters: field-preserving
// JSON object decoding, bounded server-sent-event reading and writing.
package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Object is a JSON object decoded into raw fields. Adapters Take the fields
// they model; whatever remains is preserved (and reported as unknown).
type Object map[string]json.RawMessage

// ParseObject decodes raw JSON into an Object.
func ParseObject(raw []byte) (Object, error) {
	var o Object
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&o); err != nil {
		return nil, err
	}
	if o == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return o, nil
}

// Take decodes and removes key into v. Missing or null keys leave v unchanged
// and return false.
func (o Object) Take(key string, v any) (bool, error) {
	raw, ok := o[key]
	delete(o, key)
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, fmt.Errorf("field %q: %w", key, err)
	}
	return true, nil
}

// TakeRaw removes and returns a raw field.
func (o Object) TakeRaw(key string) (json.RawMessage, bool) {
	raw, ok := o[key]
	delete(o, key)
	if ok && string(bytes.TrimSpace(raw)) == "null" {
		return nil, false
	}
	return raw, ok
}

// Keys returns the remaining (unmodelled) keys sorted.
func (o Object) Keys() []string {
	out := make([]string, 0, len(o))
	for k := range o {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Ext returns remaining fields keyed with a protocol prefix ("<p>:<k>").
func (o Object) Ext(prefix string, into map[string]json.RawMessage) map[string]json.RawMessage {
	if len(o) == 0 {
		return into
	}
	if into == nil {
		into = map[string]json.RawMessage{}
	}
	for k, v := range o {
		into[prefix+":"+k] = v
	}
	return into
}

// Restore copies ext fields with prefix back into a JSON object map,
// without overriding keys already set by the builder.
func Restore(dst map[string]any, ext map[string]json.RawMessage, prefix string) {
	for k, v := range ext {
		if len(k) > len(prefix)+1 && k[:len(prefix)+1] == prefix+":" {
			name := k[len(prefix)+1:]
			if _, set := dst[name]; !set {
				dst[name] = v
			}
		}
	}
}

// ForeignExtKeys lists ext keys that belong to other protocols (they are
// dropped when building for prefix and reported as losses).
func ForeignExtKeys(ext map[string]json.RawMessage, prefix string) []string {
	var out []string
	for k := range ext {
		if len(k) <= len(prefix)+1 || k[:len(prefix)+1] != prefix+":" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Marshal encodes v without HTML escaping (prompts contain <, >, &).
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// MustMarshal is Marshal for values that cannot fail.
func MustMarshal(v any) []byte {
	b, err := Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// RawOrEmptyObject returns raw, or {} when empty (tool input schemas, args).
func RawOrEmptyObject(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

// ArgsObject converts tool argument text into a JSON value for protocols that
// carry arguments as objects. Invalid JSON is passed as a string value under
// the reserved key so it is never silently "repaired".
func ArgsObject(args string) json.RawMessage {
	trimmed := bytes.TrimSpace([]byte(args))
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`)
	}
	if json.Valid(trimmed) && trimmed[0] == '{' {
		return json.RawMessage(trimmed)
	}
	return MustMarshal(map[string]string{"_harbor_pilot_unparsed_arguments": args})
}
