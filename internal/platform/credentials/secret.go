// Package credentials resolves secret references and holds secret material in
// a type that refuses to be printed, logged or serialized.
package credentials

import (
	"crypto/subtle"
	"log/slog"
)

// Redacted is the placeholder rendered instead of secret material.
const Redacted = "[REDACTED]"

// Secret holds secret bytes. Its String, GoString, MarshalJSON, MarshalText
// and LogValue methods never reveal the value; use Reveal at the exact point
// of use (e.g. when setting an HTTP header).
type Secret struct {
	v []byte
}

// NewSecret wraps a value.
func NewSecret(v string) Secret {
	return Secret{v: []byte(v)}
}

// Reveal returns the secret value. Call it only where the value is sent to
// its intended destination.
func (s Secret) Reveal() string { return string(s.v) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return len(s.v) == 0 }

// Equal compares in constant time.
func (s Secret) Equal(o Secret) bool {
	return subtle.ConstantTimeCompare(s.v, o.v) == 1
}

// String implements fmt.Stringer.
func (s Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer.
func (s Secret) GoString() string { return Redacted }

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler (also covers YAML).
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// Bundle is the resolved set of named secrets for one account. Most adapters
// need a single "api_key"; some need several (e.g. a token plus a key ID).
type Bundle struct {
	items map[string]Secret
}

// NewBundle builds a bundle.
func NewBundle(items map[string]Secret) Bundle {
	cp := make(map[string]Secret, len(items))
	for k, v := range items {
		cp[k] = v
	}
	return Bundle{items: cp}
}

// Get returns a named secret.
func (b Bundle) Get(name string) (Secret, bool) {
	s, ok := b.items[name]
	return s, ok && !s.IsZero()
}

// Names returns the configured secret names (not values).
func (b Bundle) Names() []string {
	out := make([]string, 0, len(b.items))
	for k := range b.items {
		out = append(out, k)
	}
	return out
}

// String never reveals values.
func (b Bundle) String() string { return "credentials.Bundle" + Redacted }

// LogValue never reveals values.
func (b Bundle) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalJSON never reveals values.
func (b Bundle) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }
