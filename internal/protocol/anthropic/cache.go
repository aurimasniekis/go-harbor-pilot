package anthropic

import (
	"bytes"
	"encoding/json"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// MaxCacheBreakpoints is the number of cache_control markers the Messages
// API accepts in one request (a top-level automatic marker counts too).
const MaxCacheBreakpoints = 4

// Loss features recorded when cache markers are adjusted.
const (
	LossCacheTTLOrder = "cache_control_ttl"
	LossCacheLimit    = "cache_control_limit"
)

// cacheSlot is one cache_control marker in the outgoing body, in the order
// the API processes them (tools, system, messages).
type cacheSlot struct {
	block map[string]any
	// tools[i] when the tool was kept as raw JSON (a provider tool); the
	// block is decoded only when it has to change.
	rawTools []any
	rawIndex int
}

func (s cacheSlot) ttl() string {
	switch v := s.block["cache_control"].(type) {
	case *ir.CacheControl:
		return v.TTL
	case map[string]any:
		t, _ := v["ttl"].(string)
		return t
	case json.RawMessage:
		var cc ir.CacheControl
		_ = json.Unmarshal(v, &cc)
		return cc.TTL
	}
	return ""
}

// commit writes a decoded raw tool back into the tools slice.
func (s cacheSlot) commit() {
	if s.rawTools != nil {
		s.rawTools[s.rawIndex] = s.block
	}
}

func (s cacheSlot) drop() {
	delete(s.block, "cache_control")
	s.commit()
}

// lower replaces a 1h marker with the default (5m) one, keeping its type.
// The IR's own marker is never mutated.
func (s cacheSlot) lower() {
	switch v := s.block["cache_control"].(type) {
	case *ir.CacheControl:
		s.block["cache_control"] = &ir.CacheControl{Type: v.Type}
	case map[string]any:
		cp := make(map[string]any, len(v))
		for k, x := range v {
			if k != "ttl" {
				cp[k] = x
			}
		}
		s.block["cache_control"] = cp
	case json.RawMessage:
		var m map[string]any
		if json.Unmarshal(v, &m) == nil {
			delete(m, "ttl")
			s.block["cache_control"] = m
		}
	}
	s.commit()
}

// isFiveMinute reports whether a ttl is the default (5m) one.
func isFiveMinute(ttl string) bool { return ttl == "" || ttl == "5m" }

// normalizeCacheControl enforces the Messages API's prompt-cache rules on a
// built body, after the gateway may have merged, inlined or dropped blocks:
//
//  1. At most four breakpoints. When there are more, the earliest are
//     removed and the last ones kept: the last breakpoint caches the longest
//     prefix (it covers everything before it) and is where a growing
//     conversation writes its next entry, so it is the one worth keeping.
//  2. TTLs must not increase in processing order (tools, system, messages,
//     including blocks nested in tool results): once a 5m breakpoint has
//     appeared, a later 1h one is lowered to 5m.
//
// A request that already follows both rules is left untouched.
func normalizeCacheControl(body map[string]any, losses *ir.Losses) {
	slots := collectCacheSlots(body)
	limit := MaxCacheBreakpoints
	if _, ok := body["cache_control"]; ok {
		limit-- // the automatic breakpoint takes a slot
	}
	if n := len(slots) - max(limit, 0); n > 0 {
		for _, s := range slots[:n] {
			s.drop()
		}
		slots = slots[n:]
		losses.Add(LossCacheLimit, "more than 4 cache breakpoints; the earliest were removed and the last 4 kept", ir.LossDrop)
	}
	seenShort := false
	for _, s := range slots {
		t := s.ttl()
		if isFiveMinute(t) {
			seenShort = true
			continue
		}
		if t == "1h" && seenShort {
			s.lower()
			losses.Add(LossCacheTTLOrder, "a 1h cache breakpoint followed a 5m one; it was lowered to 5m (TTLs must not increase across tools, system and messages)", ir.LossApproximate)
		}
	}
}

func collectCacheSlots(body map[string]any) []cacheSlot {
	var out []cacheSlot
	add := func(b map[string]any) {
		if _, ok := b["cache_control"]; ok {
			out = append(out, cacheSlot{block: b})
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for i, t := range tools {
			switch v := t.(type) {
			case map[string]any:
				add(v)
			case json.RawMessage:
				if !bytes.Contains(v, []byte(`"cache_control"`)) {
					continue
				}
				var m map[string]any
				d := json.NewDecoder(bytes.NewReader(v))
				d.UseNumber()
				if d.Decode(&m) == nil {
					if _, ok := m["cache_control"]; ok {
						out = append(out, cacheSlot{block: m, rawTools: tools, rawIndex: i})
					}
				}
			}
		}
	}
	if system, ok := body["system"].([]any); ok {
		for _, b := range system {
			if m, ok := b.(map[string]any); ok {
				add(m)
			}
		}
	}
	if msgs, ok := body["messages"].([]any); ok {
		for _, msg := range msgs {
			m, ok := msg.(map[string]any)
			if !ok {
				continue
			}
			for _, b := range contentBlocks(m["content"]) {
				add(b)
				if b["type"] == "tool_result" {
					for _, nb := range contentBlocks(b["content"]) {
						add(nb)
					}
				}
			}
		}
	}
	return out
}

func contentBlocks(v any) []map[string]any {
	var out []map[string]any
	switch c := v.(type) {
	case []any:
		for _, x := range c {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = c
	}
	return out
}
