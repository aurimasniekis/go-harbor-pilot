package api

import (
	"strings"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// MaxRefFilters is how many ref filters one request may combine.
const MaxRefFilters = 4

// MaxRefValue is the longest ref value a filter accepts, in bytes.
const MaxRefValue = 256

// RefFilter is one trusted-reference condition: key = value.
type RefFilter struct {
	Key   string
	Value string
}

// ParseRefFilters parses repeated ref=<key>:<value> query values (D-041).
// The value is split at the first colon, so values may contain colons. The
// key follows the bound-ref key rules. Filters are ANDed; repeating a key
// with the same value is harmless, with different values it matches nothing.
func ParseRefFilters(raw []string) ([]RefFilter, error) {
	if len(raw) > MaxRefFilters {
		return nil, Invalid("too many ref filters", FieldError{Location: "query.ref", Message: "at most 4 ref filters"})
	}
	out := make([]RefFilter, 0, len(raw))
	for _, r := range raw {
		k, v, ok := strings.Cut(r, ":")
		switch {
		case !ok:
			return nil, Invalid("invalid ref filter", FieldError{Location: "query.ref", Message: "must be <key>:<value>"})
		case !cp.ValidRefKey(k):
			return nil, Invalid("invalid ref filter", FieldError{Location: "query.ref", Message: "key must be a lowercase identifier such as spaceman.run"})
		case v == "" || len(v) > MaxRefValue:
			return nil, Invalid("invalid ref filter", FieldError{Location: "query.ref", Message: "value must be 1 to 256 bytes"})
		}
		out = append(out, RefFilter{Key: k, Value: v})
	}
	return out, nil
}

// RefMap returns the filters as a map for echoing in responses (nil when
// empty).
func RefMap(fs []RefFilter) map[string]string {
	if len(fs) == 0 {
		return nil
	}
	m := make(map[string]string, len(fs))
	for _, f := range fs {
		m[f.Key] = f.Value
	}
	return m
}
