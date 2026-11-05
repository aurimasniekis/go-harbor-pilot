package privacy

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// PlaceholderPrefix starts every placeholder.
const PlaceholderPrefix = "HPSEC_"

const placeholderBody = 20 // base32 characters (100 bits)

// PlaceholderRE matches complete placeholders.
var PlaceholderRE = regexp.MustCompile(`HPSEC_[A-Z2-7]{20}`)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// ---------- vault ----------

type scopeVault struct {
	fwd  map[string]string // original -> placeholder
	rev  map[string]string // placeholder -> original
	used time.Time
}

// Vault keeps placeholder mappings in memory only. Mappings are scoped
// (binding plus session), expire after an idle TTL and are bounded in
// total size; after a restart or expiry earlier placeholders can no longer
// be restored.
type Vault struct {
	mu      sync.Mutex
	scopes  map[string]*scopeVault
	entries int
	TTL     time.Duration
	Max     int
	Now     func() time.Time
}

// NewVault creates a vault (TTL 24h, at most 200k mappings).
func NewVault(now func() time.Time) *Vault {
	if now == nil {
		now = time.Now
	}
	return &Vault{scopes: map[string]*scopeVault{}, TTL: 24 * time.Hour, Max: 200_000, Now: now}
}

func (v *Vault) scope(name string) *scopeVault {
	s := v.scopes[name]
	if s == nil {
		s = &scopeVault{fwd: map[string]string{}, rev: map[string]string{}}
		v.scopes[name] = s
	}
	s.used = v.Now()
	return s
}

// evict drops expired scopes, then the least recently used, until under
// the size bound.
func (v *Vault) evict() {
	now := v.Now()
	for k, s := range v.scopes {
		if now.Sub(s.used) > v.TTL {
			v.entries -= len(s.fwd)
			delete(v.scopes, k)
		}
	}
	for v.entries > v.Max && len(v.scopes) > 0 {
		var oldest string
		var at time.Time
		for k, s := range v.scopes {
			if oldest == "" || s.used.Before(at) {
				oldest, at = k, s.used
			}
		}
		v.entries -= len(v.scopes[oldest].fwd)
		delete(v.scopes, oldest)
	}
}

// placeholder returns the scope's placeholder for value, creating a fresh
// unpredictable one when needed. avoid is text the placeholder must not
// already occur in (collision check).
func (v *Vault) placeholder(scope, value, avoid string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := v.scope(scope)
	if p, ok := s.fwd[value]; ok {
		return p, nil
	}
	for range 8 {
		b := make([]byte, 13)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		p := PlaceholderPrefix + b32.EncodeToString(b)[:placeholderBody]
		if _, taken := s.rev[p]; taken || strings.Contains(avoid, p) {
			continue
		}
		s.fwd[value], s.rev[p] = p, value
		v.entries++
		if v.entries > v.Max {
			v.evict()
		}
		return p, nil
	}
	return "", errors.New("privacy: could not allocate a unique placeholder")
}

func (v *Vault) original(scope, placeholder string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := v.scopes[scope]
	if s == nil {
		return "", false
	}
	o, ok := s.rev[placeholder]
	if ok {
		s.used = v.Now()
	}
	return o, ok
}

// Sweep removes expired scopes (call periodically).
func (v *Vault) Sweep() {
	v.mu.Lock()
	v.evict()
	v.mu.Unlock()
}

// ---------- summaries ----------

// RuleCount counts detections per rule and action.
type RuleCount struct {
	Rule   string `json:"rule"`
	Action string `json:"action"`
	Count  int    `json:"count"`
}

// Summary describes masking of one request without revealing values.
type Summary struct {
	PolicyID    string      `json:"policy_id"`
	Masked      int         `json:"masked"`
	Warned      int         `json:"warned"`
	Allowed     int         `json:"allowed"`
	Blocked     bool        `json:"blocked"`
	Uninspected int         `json:"uninspected_parts" doc:"Binary parts (images, documents) the detectors cannot read."`
	Unmasked    bool        `json:"unmasked_fallback" doc:"Masking could not run and the policy permitted unmasked dispatch."`
	Rules       []RuleCount `json:"rules"`
	counts      map[[2]string]int
}

func (s *Summary) add(f Finding) {
	if s.counts == nil {
		s.counts = map[[2]string]int{}
	}
	s.counts[[2]string{f.Rule, f.Action}]++
	switch f.Action {
	case cp.PrivacyMask:
		s.Masked++
	case cp.PrivacyWarn:
		s.Warned++
	case cp.PrivacyActionAllow:
		s.Allowed++
	case cp.PrivacyBlock:
		s.Blocked = true
	}
}

func (s *Summary) finish() {
	s.Rules = []RuleCount{}
	for k, n := range s.counts {
		s.Rules = append(s.Rules, RuleCount{Rule: k[0], Action: k[1], Count: n})
	}
	sort.Slice(s.Rules, func(i, j int) bool {
		if s.Rules[i].Rule != s.Rules[j].Rule {
			return s.Rules[i].Rule < s.Rules[j].Rule
		}
		return s.Rules[i].Action < s.Rules[j].Action
	})
}

// BlockedRules names the rules that blocked the request.
func (s *Summary) BlockedRules() []string {
	var out []string
	for _, r := range s.Rules {
		if r.Action == cp.PrivacyBlock {
			out = append(out, r.Rule)
		}
	}
	return out
}

// ---------- masking ----------

// maskText applies findings to text.
func (p *Policy) maskText(text string, v *Vault, scope string, sum *Summary) (string, error) {
	fs := p.Scan(text)
	if len(fs) == 0 {
		return text, nil
	}
	var b strings.Builder
	last := 0
	for _, f := range fs {
		sum.add(f)
		if f.Action != cp.PrivacyMask {
			continue
		}
		ph, err := v.placeholder(scope, text[f.Start:f.End], text)
		if err != nil {
			return "", err
		}
		b.WriteString(text[last:f.Start])
		b.WriteString(ph)
		last = f.End
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

// maskJSON masks string values inside JSON text without re-serializing it
// (keys and layout are preserved; values are decoded, masked and
// re-encoded). Invalid JSON is masked as plain text.
func (p *Policy) maskJSON(raw string, v *Vault, scope string, sum *Summary) (string, error) {
	if !json.Valid([]byte(raw)) {
		return p.maskText(raw, v, scope, sum)
	}
	var b strings.Builder
	i := 0
	for i < len(raw) {
		c := raw[i]
		if c != '"' {
			b.WriteByte(c)
			i++
			continue
		}
		j := i + 1
		for j < len(raw) {
			if raw[j] == '\\' {
				j += 2
				continue
			}
			if raw[j] == '"' {
				break
			}
			j++
		}
		lit := raw[i : j+1]
		var s string
		if err := json.Unmarshal([]byte(lit), &s); err != nil {
			b.WriteString(lit)
			i = j + 1
			continue
		}
		m, err := p.maskText(s, v, scope, sum)
		if err != nil {
			return "", err
		}
		if m == s {
			b.WriteString(lit)
		} else {
			enc, _ := json.Marshal(m)
			b.Write(enc)
		}
		i = j + 1
	}
	return b.String(), nil
}

func (p *Policy) maskParts(parts []ir.Part, v *Vault, scope string, sum *Summary) error {
	for i := range parts {
		pt := &parts[i]
		var err error
		switch pt.Kind {
		case ir.PartText, ir.PartRefusal:
			pt.Text, err = p.maskText(pt.Text, v, scope, sum)
		case ir.PartReasoning:
			// Exposed reasoning text may be masked; a signature over it
			// (sealed) must then not be replayed, so signed reasoning is
			// left untouched.
			if pt.Reasoning != nil && pt.Sealed == nil {
				pt.Reasoning.Text, err = p.maskText(pt.Reasoning.Text, v, scope, sum)
			}
		case ir.PartToolCall:
			if pt.ToolCall != nil {
				pt.ToolCall.Arguments, err = p.maskJSON(pt.ToolCall.Arguments, v, scope, sum)
			}
		case ir.PartToolResult:
			if pt.ToolResult != nil {
				err = p.maskParts(pt.ToolResult.Content, v, scope, sum)
			}
		case ir.PartImage, ir.PartDocument:
			sum.Uninspected++
		case ir.PartSealed:
			// Opaque provider state is never inspected or rewritten.
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// MaskRequest masks a copy of the request's content in place of the
// original and returns a summary. The caller must check Summary.Blocked.
func (p *Policy) MaskRequest(r *ir.Request, v *Vault, scope string) (*Summary, error) {
	sum := &Summary{PolicyID: p.ID}
	if err := p.maskParts(r.System, v, scope, sum); err != nil {
		return sum, err
	}
	for i := range r.Messages {
		if err := p.maskParts(r.Messages[i].Parts, v, scope, sum); err != nil {
			return sum, err
		}
	}
	sum.finish()
	return sum, nil
}

// CloneRequest deep-copies the parts of a request that masking rewrites.
func CloneRequest(r *ir.Request) *ir.Request {
	c := *r
	c.System = cloneParts(r.System)
	c.Messages = make([]ir.Message, len(r.Messages))
	for i, m := range r.Messages {
		c.Messages[i] = m
		c.Messages[i].Parts = cloneParts(m.Parts)
	}
	return &c
}

func cloneParts(ps []ir.Part) []ir.Part {
	if ps == nil {
		return nil
	}
	out := make([]ir.Part, len(ps))
	for i, p := range ps {
		out[i] = p
		if p.ToolCall != nil {
			tc := *p.ToolCall
			out[i].ToolCall = &tc
		}
		if p.ToolResult != nil {
			tr := *p.ToolResult
			tr.Content = cloneParts(p.ToolResult.Content)
			out[i].ToolResult = &tr
		}
		if p.Reasoning != nil {
			rs := *p.Reasoning
			out[i].Reasoning = &rs
		}
	}
	return out
}

// Test scans text for the sandbox: findings and the masked preview, using
// a throwaway vault (nothing is persisted or retained).
func (p *Policy) Test(text string) ([]Finding, string, *Summary) {
	v := NewVault(nil)
	sum := &Summary{PolicyID: p.ID}
	masked, _ := p.maskText(text, v, "sandbox", sum)
	sum.finish()
	return p.Scan(text), masked, sum
}

// ---------- restoration ----------

// Restorer replaces placeholders in model output with originals for the
// client that sent them. Text is restored across stream chunk boundaries.
// Tool-call arguments are restored only for authorized tools and fields,
// never into URLs to hosts outside the allowlist.
type Restorer struct {
	p     *Policy
	v     *Vault
	scope string
	carry map[int]string
	args  map[int]*argBuf
}

type argBuf struct {
	name string
	b    strings.Builder
}

// NewRestorer creates a restorer for one response.
func (p *Policy) NewRestorer(v *Vault, scope string) *Restorer {
	return &Restorer{p: p, v: v, scope: scope, carry: map[int]string{}, args: map[int]*argBuf{}}
}

func (r *Restorer) restoreText(s string) string {
	return PlaceholderRE.ReplaceAllStringFunc(s, func(ph string) string {
		if o, ok := r.v.original(r.scope, ph); ok {
			return o
		}
		return ph
	})
}

// heldSuffix returns how many trailing bytes could start a placeholder.
func heldSuffix(s string) int {
	max := len(PlaceholderPrefix) + placeholderBody - 1
	start := len(s) - max
	if start < 0 {
		start = 0
	}
	for i := start; i < len(s); i++ {
		if s[i] != 'H' {
			continue
		}
		tail := s[i:]
		if isPlaceholderPrefix(tail) {
			return len(tail)
		}
	}
	return 0
}

func isPlaceholderPrefix(t string) bool {
	if len(t) <= len(PlaceholderPrefix) {
		return strings.HasPrefix(PlaceholderPrefix, t)
	}
	if !strings.HasPrefix(t, PlaceholderPrefix) {
		return false
	}
	body := t[len(PlaceholderPrefix):]
	if len(body) >= placeholderBody {
		return false
	}
	for _, c := range body {
		if (c < 'A' || c > 'Z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

func (r *Restorer) allowedTool(name string) bool {
	t := r.p.ToolArgs
	if !t.Enabled {
		return false
	}
	for _, n := range t.Tools {
		if n == name {
			return true
		}
	}
	return false
}

// restoreArgs restores placeholders inside authorized JSON argument fields.
func (r *Restorer) restoreArgs(raw string) string {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &top) != nil {
		return raw
	}
	fields := map[string]bool{}
	for _, f := range r.p.ToolArgs.Fields {
		fields[f] = true
	}
	changed := false
	for k, v := range top {
		if len(fields) > 0 && !fields[k] {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) != nil || !PlaceholderRE.MatchString(s) {
			continue
		}
		if strings.Contains(s, "://") && !r.hostAllowed(s) {
			continue
		}
		out := r.restoreText(s)
		if out != s {
			enc, _ := json.Marshal(out)
			top[k] = enc
			changed = true
		}
	}
	if !changed {
		return raw
	}
	b, _ := json.Marshal(top)
	return string(b)
}

var urlHostRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://([^/\s:?#@]+)`)

func (r *Restorer) hostAllowed(s string) bool {
	hosts := urlHostRE.FindAllStringSubmatch(s, -1)
	if len(hosts) == 0 {
		return false
	}
	for _, h := range hosts {
		ok := false
		for _, a := range r.p.ToolArgs.AllowedHosts {
			if strings.EqualFold(h[1], a) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Event rewrites one stream event, possibly into several.
func (r *Restorer) Event(e ir.Event) []ir.Event {
	switch e.Kind {
	case ir.EvPartStart:
		if e.Part != nil && e.Part.ToolCall != nil && r.allowedTool(e.Part.ToolCall.Name) {
			r.args[e.Index] = &argBuf{name: e.Part.ToolCall.Name}
		}
		return []ir.Event{e}
	case ir.EvDelta:
		if ab := r.args[e.Index]; ab != nil {
			ab.b.WriteString(e.Delta)
			return nil
		}
		if !r.p.Restore {
			return []ir.Event{e}
		}
		s := r.carry[e.Index] + e.Delta
		hold := heldSuffix(s)
		r.carry[e.Index] = s[len(s)-hold:]
		out := r.restoreText(s[:len(s)-hold])
		if out == "" {
			return nil
		}
		e.Delta = out
		return []ir.Event{e}
	case ir.EvPartStop:
		var out []ir.Event
		if ab := r.args[e.Index]; ab != nil {
			out = append(out, ir.Event{Kind: ir.EvDelta, Index: e.Index, Delta: r.restoreArgs(ab.b.String())})
			delete(r.args, e.Index)
		}
		if c := r.carry[e.Index]; c != "" {
			out = append(out, ir.Event{Kind: ir.EvDelta, Index: e.Index, Delta: r.restoreText(c)})
			delete(r.carry, e.Index)
		}
		return append(out, e)
	}
	return []ir.Event{e}
}

// Response restores a complete response.
func (r *Restorer) Response(resp *ir.Response) {
	for i := range resp.Parts {
		p := &resp.Parts[i]
		switch {
		case p.Kind == ir.PartText && r.p.Restore:
			p.Text = r.restoreText(p.Text)
		case p.ToolCall != nil && r.allowedTool(p.ToolCall.Name):
			p.ToolCall.Arguments = r.restoreArgs(p.ToolCall.Arguments)
		}
	}
}

// Stream wraps an event channel with restoration. It stops forwarding when
// ctx ends and drains the input so the producer is never blocked.
func (r *Restorer) Stream(ctx context.Context, in <-chan ir.Event) <-chan ir.Event {
	out := make(chan ir.Event, cap(in)+1)
	go func() {
		defer close(out)
		for e := range in {
			for _, x := range r.Event(e) {
				select {
				case out <- x:
				case <-ctx.Done():
					for range in {
					}
					return
				}
			}
		}
	}()
	return out
}
