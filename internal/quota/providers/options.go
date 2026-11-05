package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// DecodeOptions decodes adapter options strictly (unknown keys are errors).
func DecodeOptions(in map[string]any, out any) error {
	if len(in) == 0 {
		return nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return NewError(domain.ErrConfiguration, "options are not serializable")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		msg := err.Error()
		if i := strings.Index(msg, "unknown field"); i >= 0 {
			msg = "options: " + msg[i:]
		} else {
			msg = "options have an invalid type or value"
		}
		return NewError(domain.ErrConfiguration, msg)
	}
	return nil
}

// ValidateBaseURL requires HTTPS, except plain HTTP to loopback hosts (local
// development and tests). It strips trailing slashes.
func ValidateBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", NewError(domain.ErrConfiguration, "base_url must be an absolute URL")
	}
	if u.User != nil {
		return "", NewError(domain.ErrConfiguration, "base_url must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", NewError(domain.ErrConfiguration, "base_url must not contain a query or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", NewError(domain.ErrConfiguration, "base_url must use https (http is only allowed for loopback hosts)")
		}
	default:
		return "", NewError(domain.ErrConfiguration, "base_url must use https")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// RequireSecret fetches a named credential or returns a configuration error.
func RequireSecret(c AccountConfig, name string) (string, error) {
	s, ok := c.Credentials.Get(name)
	if !ok {
		return "", NewError(domain.ErrConfiguration, fmt.Sprintf("credential %q is required", name))
	}
	return s.Reveal(), nil
}

// NumFloat parses a json.Number (or decimal string) into a float for
// display-only values.
func NumFloat(n json.Number) (*float64, bool) {
	if n == "" {
		return nil, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return nil, false
	}
	return &f, true
}

// Float returns a pointer to v.
func Float(v float64) *float64 { return &v }

// Int64 returns a pointer to v.
func Int64(v int64) *int64 { return &v }

// String returns a pointer to v.
func String(v string) *string { return &v }

// MaskIdentifier masks an email or identifier for display, e.g.
// "jane.doe@example.com" → "j***@example.com".
func MaskIdentifier(s string) string {
	if s == "" {
		return ""
	}
	if at := strings.IndexByte(s, '@'); at > 0 {
		return s[:1] + "***" + s[at:]
	}
	if len(s) <= 4 {
		return "***"
	}
	return s[:2] + "***" + s[len(s)-2:]
}
