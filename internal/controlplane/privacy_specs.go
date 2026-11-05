package controlplane

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
)

// Privacy actions in precedence order: block beats allow, allow beats mask,
// mask beats warn.
const (
	PrivacyBlock       = "block"
	PrivacyActionAllow = "allow"
	PrivacyMask        = "mask"
	PrivacyWarn        = "warn"
)

// BuiltinDetectors are the deterministic credential detectors.
var BuiltinDetectors = []string{"private_key", "aws_access_key", "aws_secret_key", "github_token", "anthropic_key", "openai_key",
	"slack_token", "google_api_key", "stripe_key", "jwt", "secret_assignment", "credential_url", "gateway_token", "configured_secrets"}

// PersonalDataDetectors are optional personal-data detectors.
var PersonalDataDetectors = []string{"email", "phone", "credit_card", "iban", "ip_address"}

// SupplementaryDetectors flag likely secrets heuristically (warn only).
var SupplementaryDetectors = []string{"high_entropy"}

// PrivacyRule is a custom detector.
type PrivacyRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern" doc:"RE2 regular expression."`
	// Group selects the capture group to act on (0 = whole match).
	Group  int    `json:"group" required:"false" minimum:"0" maximum:"9"`
	Action string `json:"action" enum:"mask,block,warn"`
}

// PrivacyAllowRule narrowly exempts matches from masking or warning. It never
// overrides block.
type PrivacyAllowRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern" doc:"RE2; a detection whose text matches this pattern entirely is allowed."`
	// Detectors limits the exemption to these detectors/rules (empty = all).
	Detectors []string `json:"detectors" required:"false"`
}

// ToolArgRestore authorizes restoring originals into tool-call arguments.
type ToolArgRestore struct {
	Enabled bool `json:"enabled"`
	// Tools lists tool names whose arguments may receive originals.
	Tools []string `json:"tools" required:"false"`
	// Fields limits restoration to these top-level argument keys (empty = any).
	Fields []string `json:"fields" required:"false"`
	// AllowedHosts permits restoring inside URLs only for these hosts.
	AllowedHosts []string `json:"allowed_hosts" required:"false"`
}

// PrivacyPolicySpec configures outbound masking.
type PrivacyPolicySpec struct {
	Description string `json:"description" required:"false" maxLength:"1000"`
	// Required makes masking mandatory: if it cannot run safely the request
	// is blocked, unless AllowUnmaskedFallback is set.
	Required              bool `json:"required"`
	AllowUnmaskedFallback bool `json:"allow_unmasked_fallback" required:"false" doc:"Availability-first: dispatch unmasked when masking cannot run (recorded on the request)."`
	// Detectors enables built-in detectors; empty means all built-ins.
	Detectors          []string           `json:"detectors" required:"false"`
	PersonalData       []string           `json:"personal_data" required:"false" doc:"Optional personal-data detectors: email, phone, credit_card, iban, ip_address."`
	Supplementary      []string           `json:"supplementary" required:"false" doc:"Heuristic detectors (warn only): high_entropy."`
	DetectorActions    map[string]string  `json:"detector_actions" required:"false" doc:"Per-detector action override (mask, block, warn). Built-ins default to mask."`
	Rules              []PrivacyRule      `json:"rules" required:"false"`
	Allow              []PrivacyAllowRule `json:"allow" required:"false"`
	RestoreInResponses bool               `json:"restore_in_responses" required:"false" doc:"Replace placeholders in text returned to the same client."`
	ToolArgRestore     ToolArgRestore     `json:"tool_arg_restore" required:"false"`
	// SecretValuesRef points at configured secret values (one per line) as
	// an alternative to the write-only secret_values secret.
	SecretValuesRef string `json:"secret_values_ref" required:"false"`
}

func normalizePrivacy(s *PrivacyPolicySpec) {
	if len(s.Detectors) == 0 {
		s.Detectors = append([]string{}, BuiltinDetectors...)
	}
	s.Detectors = SortedUnique(s.Detectors)
	s.PersonalData = SortedUnique(s.PersonalData)
	s.Supplementary = SortedUnique(s.Supplementary)
	s.DetectorActions = EmptyMap(s.DetectorActions)
	if s.Rules == nil {
		s.Rules = []PrivacyRule{}
	}
	if s.Allow == nil {
		s.Allow = []PrivacyAllowRule{}
	}
	for i := range s.Allow {
		s.Allow[i].Detectors = SortedUnique(s.Allow[i].Detectors)
	}
	t := &s.ToolArgRestore
	t.Tools, t.Fields, t.AllowedHosts = SortedUnique(t.Tools), SortedUnique(t.Fields), SortedUnique(t.AllowedHosts)
}

func validatePrivacy(v *Validator, s *PrivacyPolicySpec) {
	checkText(v, "spec.description", s.Description, 1000)
	for _, d := range s.Detectors {
		v.Check(slices.Contains(BuiltinDetectors, d), "spec.detectors", "unknown detector "+d)
	}
	for _, d := range s.PersonalData {
		v.Check(slices.Contains(PersonalDataDetectors, d), "spec.personal_data", "unknown personal-data detector "+d)
	}
	for _, d := range s.Supplementary {
		v.Check(slices.Contains(SupplementaryDetectors, d), "spec.supplementary", "unknown supplementary detector "+d)
	}
	names := map[string]bool{}
	for _, d := range append(append(append([]string{}, BuiltinDetectors...), PersonalDataDetectors...), SupplementaryDetectors...) {
		names[d] = true
	}
	for d, a := range s.DetectorActions {
		v.Check(names[d], "spec.detector_actions."+d, "unknown detector")
		v.Check(a == PrivacyMask || a == PrivacyBlock || a == PrivacyWarn, "spec.detector_actions."+d, "must be mask, block or warn")
	}
	if len(s.Rules) > 100 {
		v.Fail("spec.rules", "at most 100 rules")
	}
	for i, r := range s.Rules {
		loc := fmt.Sprintf("spec.rules[%d]", i)
		v.Check(refKeyRE.MatchString(r.Name), loc+".name", "must be a lowercase identifier")
		v.Check(!names[r.Name], loc+".name", "must not shadow a built-in detector")
		names[r.Name] = true
		re, err := regexp.Compile(r.Pattern)
		switch {
		case err != nil || r.Pattern == "":
			v.Fail(loc+".pattern", "invalid RE2 pattern")
		case r.Group > re.NumSubexp():
			v.Fail(loc+".group", "the pattern has fewer capture groups")
		case re.MatchString(""):
			v.Fail(loc+".pattern", "must not match the empty string")
		}
		v.Check(r.Action == PrivacyMask || r.Action == PrivacyBlock || r.Action == PrivacyWarn, loc+".action", "must be mask, block or warn")
	}
	for i, a := range s.Allow {
		loc := fmt.Sprintf("spec.allow[%d]", i)
		v.Check(a.Name != "", loc+".name", "is required")
		if _, err := regexp.Compile(a.Pattern); err != nil || a.Pattern == "" {
			v.Fail(loc+".pattern", "invalid RE2 pattern")
		}
		for _, d := range a.Detectors {
			v.Check(names[d], loc+".detectors", "unknown detector or rule "+d)
		}
	}
	if s.ToolArgRestore.Enabled {
		v.Check(len(s.ToolArgRestore.Tools) > 0, "spec.tool_arg_restore.tools", "list the tools allowed to receive originals")
	}
	if s.AllowUnmaskedFallback {
		v.Check(s.Required, "spec.allow_unmasked_fallback", "only meaningful when masking is required")
	}
	if s.SecretValuesRef != "" {
		kind, val, ok := strings.Cut(s.SecretValuesRef, ":")
		v.Check(ok && val != "" && (kind == "env" || kind == "file"), "spec.secret_values_ref", "must be env:NAME or file:/path")
	}
}

// Trace content modes.
const (
	TraceOff              = "off"
	TraceMaskedDiagnostic = "masked_diagnostic"
	TraceMaskedFull       = "masked_full"
	TraceUnredacted       = "unredacted"
)

// TraceSections lists capturable trace sections.
var TraceSections = []string{"inbound_request", "upstream_request", "response", "stream_events", "attempts", "routing", "usage"}

// MaxTraceTTL bounds custom retention.
const MaxTraceTTL = 30 * 24 * time.Hour

// MaxUnredactedWindow bounds how far ahead unredacted capture may run.
const MaxUnredactedWindow = 7 * 24 * time.Hour

// TracePolicySpec configures short-lived content capture.
type TracePolicySpec struct {
	Mode string `json:"mode" enum:"off,masked_diagnostic,masked_full,unredacted"`
	// Retention is a preset (24h, 3d, 7d) or custom.
	Retention        string `json:"retention" enum:"24h,3d,7d,custom"`
	CustomTTLSeconds int    `json:"custom_ttl_seconds" required:"false" minimum:"0" doc:"Used when retention is custom; at most 30 days."`
	// Sections limits what is captured (empty = all).
	Sections []string `json:"sections" required:"false"`
	// MaxTraceBytes truncates individual traces; MaxTotalBytes is the
	// per-plan quota across live traces.
	MaxTraceBytes int     `json:"max_trace_bytes" required:"false" minimum:"0" maximum:"16777216"`
	MaxTotalBytes int64   `json:"max_total_bytes" required:"false" minimum:"0"`
	SampleRate    float64 `json:"sample_rate" required:"false" minimum:"0" maximum:"1" doc:"Fraction of requests captured; 0 = all."`
	AllowExport   bool    `json:"allow_export" required:"false" doc:"Permit users with traces.export to download trace content."`
	// UnredactedUntil time-boxes unredacted capture (RFC 3339, at most seven
	// days after the policy is saved); afterwards capture is masked_full.
	UnredactedUntil string `json:"unredacted_until" required:"false"`
	// RedactionPolicyID is the privacy policy used to redact masked traces
	// (empty = built-in credential detectors).
	RedactionPolicyID string `json:"redaction_policy_id" required:"false"`
}

func normalizeTrace(s *TracePolicySpec) {
	if s.Mode == "" {
		s.Mode = TraceOff
	}
	if s.Retention == "" {
		s.Retention = "3d"
	}
	if len(s.Sections) == 0 {
		s.Sections = append([]string{}, TraceSections...)
	}
	s.Sections = SortedUnique(s.Sections)
	if s.MaxTraceBytes == 0 {
		s.MaxTraceBytes = 1 << 20
	}
	if s.MaxTotalBytes == 0 {
		s.MaxTotalBytes = 512 << 20
	}
}

// TraceNow is the clock used to validate unredacted windows.
var TraceNow = time.Now

func validateTrace(v *Validator, s *TracePolicySpec) {
	v.Check(slices.Contains([]string{TraceOff, TraceMaskedDiagnostic, TraceMaskedFull, TraceUnredacted}, s.Mode), "spec.mode", "unknown mode")
	v.Check(slices.Contains([]string{"24h", "3d", "7d", "custom"}, s.Retention), "spec.retention", "must be 24h, 3d, 7d or custom")
	if s.Retention == "custom" {
		ttl := time.Duration(s.CustomTTLSeconds) * time.Second
		v.Check(ttl >= time.Hour && ttl <= MaxTraceTTL, "spec.custom_ttl_seconds", "must be between one hour and 30 days")
	}
	for _, sec := range s.Sections {
		v.Check(slices.Contains(TraceSections, sec), "spec.sections", "unknown section "+sec)
	}
	if s.Mode == TraceUnredacted {
		t, err := time.Parse(time.RFC3339, s.UnredactedUntil)
		switch {
		case err != nil:
			v.Fail("spec.unredacted_until", "unredacted capture must be time-boxed with an RFC 3339 end time")
		case t.After(TraceNow().Add(MaxUnredactedWindow)):
			v.Fail("spec.unredacted_until", "unredacted capture may run for at most seven days")
		}
	}
}

// TTL returns the retention duration.
func (s TracePolicySpec) TTL() time.Duration {
	switch s.Retention {
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "custom":
		return time.Duration(s.CustomTTLSeconds) * time.Second
	}
	return 3 * 24 * time.Hour
}

// EffectiveMode returns the mode in force at t (unredacted capture ends at
// its deadline).
func (s TracePolicySpec) EffectiveMode(t time.Time) string {
	if s.Mode == TraceUnredacted {
		if until, err := time.Parse(time.RFC3339, s.UnredactedUntil); err != nil || !t.Before(until) {
			return TraceMaskedFull
		}
	}
	return s.Mode
}

// PlanPrivacy links a plan to its masking and trace policies.
type PlanPrivacy struct {
	PolicyID      string `json:"policy_id" required:"false" doc:"Privacy (masking) policy; empty = no outbound masking."`
	TracePolicyID string `json:"trace_policy_id" required:"false" doc:"Trace policy; empty = no content capture."`
}

var (
	PrivacyPolicyDef = &Def[PrivacyPolicySpec]{Name: KindPrivacyPolicy, Plural: "privacy-policies", Prefix: ids.PrivacyPolicy, Title: "Privacy policy",
		Secrets:   []SecretField{{Name: "secret_values", Description: "Configured secret values to mask, one per line. Write-only.", RefField: "secret_values_ref"}},
		Normalize: normalizePrivacy, Validate: validatePrivacy}
	TracePolicyDef = &Def[TracePolicySpec]{Name: KindTracePolicy, Plural: "trace-policies", Prefix: ids.TracePolicy, Title: "Trace policy",
		Normalize: normalizeTrace, Validate: validateTrace,
		Refs: func(s *TracePolicySpec) []Ref {
			return []Ref{{"spec.redaction_policy_id", s.RedactionPolicyID, []string{KindPrivacyPolicy}}}
		}}
)
