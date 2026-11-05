// Package privacy masks secrets and personal data in outbound content.
//
// Detectors are deterministic (known credential formats, private keys,
// secret assignments, credential-bearing URLs, configured secret values,
// custom rules, optional personal data); an entropy heuristic only warns.
// Masked values are replaced with unpredictable placeholders that are
// stable within a session scope, and the mapping lives only in memory.
// Opaque provider state is never inspected or rewritten.
package privacy

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// detector finds candidate spans in text.
type detector struct {
	name   string
	re     *regexp.Regexp
	group  int
	action string
	// valid filters candidates (checksums); nil accepts all.
	valid func(s string) bool
}

func mustRE(s string) *regexp.Regexp { return regexp.MustCompile(s) }

// builtin returns the built-in detectors by name.
func builtin() map[string]*detector {
	ds := []*detector{
		{name: "private_key", re: mustRE(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----[\s\S]*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)},
		{name: "aws_access_key", re: mustRE(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
		{name: "aws_secret_key", re: mustRE(`(?i)aws_?secret_?access_?key["']?\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})`), group: 1},
		{name: "github_token", re: mustRE(`\b(?:gh[pousr]_[A-Za-z0-9]{36,255}|github_pat_[A-Za-z0-9_]{22,255})\b`)},
		{name: "anthropic_key", re: mustRE(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
		{name: "openai_key", re: mustRE(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{20,}`)},
		{name: "slack_token", re: mustRE(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
		{name: "google_api_key", re: mustRE(`\bAIza[0-9A-Za-z_-]{35}\b`)},
		{name: "stripe_key", re: mustRE(`\b(?:sk|rk)_(?:live|test)_[0-9A-Za-z]{16,}\b`)},
		{name: "jwt", re: mustRE(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
		{name: "secret_assignment", re: mustRE(`(?i)(?:^|[^A-Za-z0-9])[A-Za-z0-9_]*(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?key|auth[_-]?token|token)["']?\s*[:=]\s*["']?([^\s"'` + "`" + `,;]{8,})`), group: 1},
		{name: "credential_url", re: mustRE(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+:([^\s/@]+)@`), group: 1},
		{name: "gateway_token", re: mustRE(`\bhp[a-z]_[a-z0-9]{8}_[A-Za-z0-9_-]{43}\b`)},
		{name: "email", re: mustRE(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)},
		{name: "phone", re: mustRE(`(?:\+\d{1,3}[\s.-]?)?\(?\d{2,4}\)?[\s.-]?\d{3,4}[\s.-]?\d{3,4}\b`), valid: func(s string) bool { return digits(s) >= 9 }},
		{name: "credit_card", re: mustRE(`\b(?:\d[ -]?){13,19}\b`), valid: luhn},
		{name: "iban", re: mustRE(`\b[A-Z]{2}\d{2}(?:[ ]?[A-Z0-9]{4}){2,7}(?:[ ]?[A-Z0-9]{1,4})?\b`), valid: ibanValid},
		{name: "ip_address", re: mustRE(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`)},
		{name: "high_entropy", re: mustRE(`[A-Za-z0-9+/=_-]{32,}`), valid: highEntropy},
	}
	out := map[string]*detector{}
	for _, d := range ds {
		out[d.name] = d
	}
	return out
}

func digits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

func luhn(s string) bool {
	var ds []int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			ds = append(ds, int(r-'0'))
		}
	}
	if len(ds) < 13 || len(ds) > 19 {
		return false
	}
	sum := 0
	for i := len(ds) - 1; i >= 0; i-- {
		d := ds[i]
		if (len(ds)-1-i)%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

func ibanValid(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	r := s[4:] + s[:4]
	rem := 0
	for _, c := range r {
		var v int
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
			rem = (rem*10 + v) % 97
		case c >= 'A' && c <= 'Z':
			v = int(c-'A') + 10
			rem = (rem*100 + v) % 97
		default:
			return false
		}
	}
	return rem == 1
}

// highEntropy flags long tokens with high Shannon entropy that mix
// character classes (identifiers and hex hashes are excluded).
func highEntropy(s string) bool {
	if len(s) < 32 {
		return false
	}
	var lower, upper, digit bool
	freq := map[rune]int{}
	for _, r := range s {
		freq[r]++
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !lower || !upper || !digit {
		return false
	}
	h := 0.0
	for _, c := range freq {
		p := float64(c) / float64(len(s))
		h -= p * math.Log2(p)
	}
	return h >= 4.2
}
