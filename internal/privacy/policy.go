package privacy

import (
	"regexp"
	"sort"
	"strings"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

// Finding is a detected span. It never carries the matched value.
type Finding struct {
	Rule   string `json:"rule"`
	Action string `json:"action"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

type allowRule struct {
	name      string
	re        *regexp.Regexp
	detectors map[string]bool
}

// Policy is a compiled privacy policy.
type Policy struct {
	ID       string
	Required bool
	// AllowUnmasked permits dispatching unmasked when masking cannot run.
	AllowUnmasked bool
	Restore       bool
	ToolArgs      cp.ToolArgRestore
	detectors     []*detector
	allow         []allowRule
	secrets       []string
}

var precedence = map[string]int{cp.PrivacyBlock: 4, cp.PrivacyActionAllow: 3, cp.PrivacyMask: 2, cp.PrivacyWarn: 1}

// Compile builds a policy. secretValues are configured values to mask
// exactly (values shorter than cp.MinSecretValueLen are ignored; the
// control plane rejects them when they are stored).
func Compile(id string, s cp.PrivacyPolicySpec, secretValues []string) (*Policy, error) {
	p := &Policy{ID: id, Required: s.Required, AllowUnmasked: s.AllowUnmaskedFallback, Restore: s.RestoreInResponses, ToolArgs: s.ToolArgRestore}
	all := builtin()
	enabled := append(append(append([]string{}, s.Detectors...), s.PersonalData...), s.Supplementary...)
	for _, name := range enabled {
		d, ok := all[name]
		if !ok {
			continue
		}
		dd := *d
		dd.action = cp.PrivacyMask
		if name == "high_entropy" {
			dd.action = cp.PrivacyWarn
		}
		if a, ok := s.DetectorActions[name]; ok {
			dd.action = a
		}
		p.detectors = append(p.detectors, &dd)
	}
	for _, r := range s.Rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, err
		}
		p.detectors = append(p.detectors, &detector{name: r.Name, re: re, group: r.Group, action: r.Action})
	}
	for _, a := range s.Allow {
		re, err := regexp.Compile("^(?:" + a.Pattern + ")$")
		if err != nil {
			return nil, err
		}
		ar := allowRule{name: a.Name, re: re, detectors: map[string]bool{}}
		for _, d := range a.Detectors {
			ar.detectors[d] = true
		}
		p.allow = append(p.allow, ar)
	}
	configured := false
	for _, d := range s.Detectors {
		configured = configured || d == "configured_secrets"
	}
	if configured {
		for _, v := range secretValues {
			if v = strings.TrimSpace(v); len(v) >= cp.MinSecretValueLen {
				p.secrets = append(p.secrets, v)
			}
		}
		// Longest first so overlapping values mask the full value.
		sort.Slice(p.secrets, func(i, j int) bool { return len(p.secrets[i]) > len(p.secrets[j]) })
	}
	return p, nil
}

// HasConfiguredSecrets reports whether the compiled policy holds configured
// secret values (write-only values an operator stored or referenced).
func (p *Policy) HasConfiguredSecrets() bool { return p != nil && len(p.secrets) > 0 }

// DefaultRedaction is the policy used for trace redaction when none is
// configured: every built-in credential detector, masking.
func DefaultRedaction() *Policy {
	p, _ := Compile("", cp.PrivacyPolicySpec{Detectors: cp.BuiltinDetectors}, nil)
	return p
}

// Scan returns non-overlapping findings in text with precedence applied:
// overlapping detections merge into one span carrying the strongest
// action; an allow rule matching a whole span exempts it from mask and
// warn but never from block.
func (p *Policy) Scan(text string) []Finding {
	if text == "" {
		return nil
	}
	var raw []Finding
	for _, d := range p.detectors {
		for _, m := range d.re.FindAllStringSubmatchIndex(text, -1) {
			s, e := m[0], m[1]
			if d.group > 0 && 2*d.group+1 < len(m) && m[2*d.group] >= 0 {
				s, e = m[2*d.group], m[2*d.group+1]
			}
			if e <= s {
				continue
			}
			if d.valid != nil && !d.valid(text[s:e]) {
				continue
			}
			raw = append(raw, Finding{Rule: d.name, Action: d.action, Start: s, End: e})
		}
	}
	for _, v := range p.secrets {
		off := 0
		for {
			i := strings.Index(text[off:], v)
			if i < 0 {
				break
			}
			raw = append(raw, Finding{Rule: "configured_secrets", Action: cp.PrivacyMask, Start: off + i, End: off + i + len(v)})
			off += i + len(v)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	raw = preferCredentials(raw)
	sort.Slice(raw, func(i, j int) bool {
		if raw[i].Start != raw[j].Start {
			return raw[i].Start < raw[j].Start
		}
		return raw[i].End > raw[j].End
	})
	var out []Finding
	cur := raw[0]
	for _, f := range raw[1:] {
		if f.Start < cur.End {
			if f.End > cur.End {
				cur.End = f.End
			}
			if precedence[f.Action] > precedence[cur.Action] {
				cur.Action, cur.Rule = f.Action, f.Rule
			}
			continue
		}
		out = append(out, cur)
		cur = f
	}
	out = append(out, cur)
	for i := range out {
		if out[i].Action == cp.PrivacyBlock {
			continue
		}
		span := text[out[i].Start:out[i].End]
		for _, a := range p.allow {
			if (len(a.detectors) == 0 || a.detectors[out[i].Rule]) && a.re.MatchString(span) {
				out[i].Action, out[i].Rule = cp.PrivacyActionAllow, out[i].Rule+"/"+a.name
				break
			}
		}
	}
	return out
}

// personal names the personal-data detectors.
var personal = func() map[string]bool {
	m := map[string]bool{}
	for _, d := range cp.PersonalDataDetectors {
		m[d] = true
	}
	return m
}()

// preferCredentials drops personal-data findings that overlap a credential
// finding with at least as strong an action. "user:password@host" also
// looks like an e-mail address; reporting it as one would mask the host
// too and attribute a credential leak to personal data.
func preferCredentials(raw []Finding) []Finding {
	out := raw[:0:0]
	for _, f := range raw {
		if personal[f.Rule] && overlapsCredential(raw, f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func overlapsCredential(raw []Finding, f Finding) bool {
	for _, c := range raw {
		if personal[c.Rule] || c.Rule == "high_entropy" {
			continue
		}
		if c.Start < f.End && f.Start < c.End && precedence[c.Action] >= precedence[f.Action] {
			return true
		}
	}
	return false
}

// Redact replaces masked and blocked spans with irreversible markers
// (trace redaction; independent of outbound placeholders).
func (p *Policy) Redact(text string) string {
	fs := p.Scan(text)
	if len(fs) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, f := range fs {
		if f.Action != cp.PrivacyMask && f.Action != cp.PrivacyBlock {
			continue
		}
		b.WriteString(text[last:f.Start])
		b.WriteString("[redacted:" + strings.SplitN(f.Rule, "/", 2)[0] + "]")
		last = f.End
	}
	b.WriteString(text[last:])
	return b.String()
}
