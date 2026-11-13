package controlplane

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// RequestFieldRule is one upstream request field a model may configure
// (D-046). Name is the spelling sent upstream. Sub, when set, makes the field
// an object merged key by key: only those sub-keys may be configured, and the
// client's other keys in the object are kept. Also lists further client
// settings that the field overlaps (by normalized name), so a conflict is
// still detected when the client spelled the setting another way.
type RequestFieldRule struct {
	Name string
	Sub  []string
	Also []string
}

// requestFieldAllowlist is the single list of configurable request fields per
// provider type. Every entry is an option the provider's public API documents
// that changes sampling, routing between upstream hosts, or tagging, and that
// cannot raise the output size, add tools, keep state upstream or change the
// response's structure. Extending it is a one-line, reviewed change here plus
// the table in docs/contracts/config.md.
var requestFieldAllowlist = map[string][]RequestFieldRule{
	// OpenAI Chat Completions as served by OpenAI-compatible routers and
	// servers: the Chat sampling parameters, plus the router options for
	// prompt transforms and the upstream-provider preference block.
	ProviderOpenAICompatible: {
		{Name: "transforms"}, {Name: "provider"},
		{Name: "temperature"}, {Name: "top_p"}, {Name: "top_k"}, {Name: "min_p"}, {Name: "top_a"},
		{Name: "frequency_penalty"}, {Name: "presence_penalty"}, {Name: "repetition_penalty"},
		{Name: "seed"}, {Name: "stop"}, {Name: "logit_bias"}, {Name: "user"}, {Name: "metadata"},
	},
	// The official OpenAI provider speaks Chat or Responses depending on the
	// client, so only fields both APIs accept are allowed.
	ProviderOpenAI: {{Name: "temperature"}, {Name: "top_p"}, {Name: "user"}, {Name: "metadata"}},
	// Local OpenAI-compatible endpoints: the Chat sampling parameters their
	// compatibility layer documents.
	ProviderOllama: {
		{Name: "temperature"}, {Name: "top_p"}, {Name: "seed"}, {Name: "stop"},
		{Name: "frequency_penalty"}, {Name: "presence_penalty"},
	},
	// Messages API. Its metadata carries the user id the gateway also builds
	// from a client's "user".
	ProviderAnthropic: {
		{Name: "metadata", Also: []string{"user"}}, {Name: "temperature"}, {Name: "top_p"}, {Name: "top_k"},
		{Name: "stop_sequences"},
	},
	// generateContent: safety thresholds, and only the sampling keys of
	// generationConfig, merged into the request's own generationConfig.
	ProviderGemini: {
		{Name: "safetySettings"},
		{Name: "generationConfig", Sub: []string{"temperature", "topP", "topK", "stopSequences"}},
	},
}

// forbiddenRequestFields are never configurable, whatever a future allowlist
// says, keyed by normalized name. TestAllowlistHasNoForbiddenField keeps the
// two lists apart.
var forbiddenRequestFields = func() map[string]string {
	groups := []struct {
		why   string
		names []string
	}{
		{"output limits bypass the budget reservation and the model's output cap", []string{
			"max_tokens", "max_completion_tokens", "max_output_tokens", "max_new_tokens", "max_tokens_to_sample", "num_predict"}},
		{"extra candidates multiply the cost", []string{"n", "candidate_count", "best_of"}},
		{"the model and the router's model list are chosen by routing", []string{"model", "models", "route"}},
		{"tools are the client's", []string{
			"tools", "tool_choice", "tool_config", "parallel_tool_calls", "functions", "function_call",
			"mcp_servers", "plugins", "web_search_options"}},
		{"stored or server-side state is not allowed", []string{
			"store", "previous_response_id", "conversation", "prompt", "background", "cached_content",
			"prompt_cache_key", "context_management", "container"}},
		{"prompt caching is set by the gateway", []string{"cache_control"}},
		{"streaming and the request's structure belong to the client", []string{
			"stream", "stream_options", "messages", "input", "contents", "system", "system_instruction", "instructions",
			"include", "response_format", "text", "response_mime_type", "response_schema", "response_modalities",
			"modalities", "audio", "prediction"}},
		{"it changes what the request costs", []string{
			"reasoning", "reasoning_effort", "thinking", "thinking_config", "service_tier", "usage", "verbosity"}},
	}
	out := map[string]string{}
	for _, g := range groups {
		for _, n := range g.names {
			out[NormalizeFieldName(n)] = g.why
		}
	}
	return out
}()

// NormalizeFieldName folds the spellings of one request field together:
// letter case is ignored and snake_case equals camelCase, so
// "system_instruction", "systemInstruction" and "SYSTEM-INSTRUCTION" are the
// same name.
func NormalizeFieldName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_' || c == '-':
			continue
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// fieldConceptAliases maps normalized names that mean the same setting under
// another protocol's name.
var fieldConceptAliases = map[string]string{"stopsequences": "stop"}

// FieldConcept is the setting a field name stands for, used to tell whether
// a client already sent it: the normalized name, with cross-protocol
// synonyms folded together (stop_sequences and stopSequences are stop).
func FieldConcept(name string) string {
	n := NormalizeFieldName(name)
	if a, ok := fieldConceptAliases[n]; ok {
		return a
	}
	return n
}

// Concepts lists the client settings a rule's field overlaps.
func (r RequestFieldRule) Concepts() []string {
	out := []string{FieldConcept(r.Name)}
	for _, a := range r.Also {
		out = append(out, FieldConcept(a))
	}
	return out
}

// SubName returns the canonical spelling of an allowed sub-key.
func (r RequestFieldRule) SubName(name string) (string, bool) {
	n := NormalizeFieldName(name)
	for _, s := range r.Sub {
		if NormalizeFieldName(s) == n {
			return s, true
		}
	}
	return "", false
}

// RequestFieldRules returns the fields a provider type allows, in order.
func RequestFieldRules(providerType string) []RequestFieldRule {
	return requestFieldAllowlist[providerType]
}

// LookupRequestField finds the allowlist entry a configured name refers to,
// in any spelling.
func LookupRequestField(providerType, name string) (RequestFieldRule, bool) {
	n := NormalizeFieldName(name)
	for _, r := range requestFieldAllowlist[providerType] {
		if NormalizeFieldName(r.Name) == n {
			return r, true
		}
	}
	return RequestFieldRule{}, false
}

func allowedFieldNames(providerType string) string {
	var names []string
	for _, r := range requestFieldAllowlist[providerType] {
		if len(r.Sub) > 0 {
			names = append(names, r.Name+" ("+strings.Join(r.Sub, ", ")+")")
			continue
		}
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

var requestFieldNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

// maxRequestFieldBytes bounds one configured field, so a spec cannot smuggle a
// request body of its own into the configuration.
const maxRequestFieldBytes = 8 << 10

// maxRequestFields bounds the number of configured fields per model.
const maxRequestFields = 32

func validateModelRequestFields(v *Validator, loc string, t ProviderType, m ModelSpec) {
	if t.Execution == "cli_bridge" {
		v.Check(len(m.RequestFields) == 0 && m.ProviderRouting == nil, loc+".request_fields",
			"CLI bridge providers run the vendor CLI and cannot carry upstream request fields")
		return
	}
	v.Check(len(m.RequestFields) <= maxRequestFields, loc+".request_fields", "at most 32 fields")
	names := make([]string, 0, len(m.RequestFields))
	for k := range m.RequestFields {
		names = append(names, k)
	}
	sort.Strings(names)
	seen := map[string]string{}
	for _, k := range names {
		raw := m.RequestFields[k]
		floc := loc + ".request_fields." + k
		if !requestFieldNameRE.MatchString(k) {
			v.Fail(floc, "invalid field name")
			continue
		}
		n := NormalizeFieldName(k)
		if prev, dup := seen[n]; dup {
			v.Fail(floc, "the same field as "+prev+" under another spelling")
			continue
		}
		seen[n] = k
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || len(trimmed) > maxRequestFieldBytes {
			v.Fail(floc, "must be between 1 byte and 8 KiB of JSON")
			continue
		}
		if !json.Valid(trimmed) {
			v.Fail(floc, "must be valid JSON")
			continue
		}
		if why, bad := forbiddenRequestFields[n]; bad {
			v.Fail(floc, "cannot be configured: "+why)
			continue
		}
		rule, ok := LookupRequestField(t.Type, k)
		if !ok {
			v.Fail(floc, "not configurable on "+t.Type+" providers; allowed: "+allowedFieldNames(t.Type))
			continue
		}
		if len(rule.Sub) > 0 {
			validateSubFields(v, floc, rule, trimmed)
		}
	}
	if p := m.ProviderRouting; p != nil {
		ploc := loc + ".provider_routing"
		v.Check(t.Type == ProviderOpenAICompatible, ploc, "upstream provider routing applies to openai_compatible providers only")
		v.Check(seen[NormalizeFieldName("provider")] == "", ploc,
			"set either provider_routing or a request_fields.provider entry, not both")
		v.Check(len(p.Order) <= 32 && len(p.Only) <= 32 && len(p.Ignore) <= 32 && len(p.Quantizations) <= 16, ploc, "too many entries")
		v.Check(p.Sort == "" || slices.Contains([]string{"price", "throughput", "latency"}, p.Sort), ploc+".sort", "must be price, throughput or latency")
		v.Check(p.DataCollection == "" || slices.Contains([]string{"allow", "deny"}, p.DataCollection), ploc+".data_collection", "must be allow or deny")
		if mp := p.MaxPrice; mp != nil {
			for f, val := range map[string]*float64{"prompt": mp.Prompt, "completion": mp.Completion, "request": mp.Request, "image": mp.Image} {
				if val != nil {
					v.Check(*val >= 0, ploc+".max_price."+f, "must be non-negative")
				}
			}
		}
	}
}

// validateSubFields checks an object field whose keys are merged one by one,
// such as Gemini's generationConfig: only the allowed keys, once each.
func validateSubFields(v *Validator, floc string, rule RequestFieldRule, raw []byte) {
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil || o == nil {
		v.Fail(floc, "must be a JSON object")
		return
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sloc := floc + "." + k
		n := NormalizeFieldName(k)
		if why, bad := forbiddenRequestFields[n]; bad {
			v.Fail(sloc, "cannot be configured: "+why)
			continue
		}
		if _, ok := rule.SubName(k); !ok {
			v.Fail(sloc, "not configurable; allowed: "+strings.Join(rule.Sub, ", "))
			continue
		}
		v.Check(!seen[n], sloc, "the same key under another spelling")
		seen[n] = true
	}
}
