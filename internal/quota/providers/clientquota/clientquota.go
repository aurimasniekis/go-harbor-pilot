// Package clientquota implements the push-only "claude_code_statusline"
// adapter. Claude Code documents a `rate_limits` object in the JSON it passes
// to statusline scripts (https://code.claude.com/docs/en/statusline). A small
// script on the user's machine forwards those values to the ingestion API;
// this adapter only describes the resulting buckets and is never polled.
package clientquota

import (
	"context"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID is the adapter ID.
const ProviderID domain.ProviderID = "claude_code_statusline"

// Adapter is the push-only adapter.
type Adapter struct{}

// New returns the adapter.
func New() *Adapter { return &Adapter{} }

// Info implements providers.Adapter.
func (Adapter) Info() domain.ProviderInfo {
	s, p, u := providers.Sup, providers.Part, providers.Unsup
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "Claude Code (statusline)", Family: "claude", AdapterVersion: "1",
		Tier: domain.TierClientReported, CredentialTypes: []string{"ingestion token (client side)"}, PushOnly: true,
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining: s("5-hour and 7-day used percentage from Claude Code"),
			domain.CapQuotaUsed:      s(""),
			domain.CapQuotaLimit:     p("normalized percentage only; absolute capacity is not exposed"),
			domain.CapFixedReset:     s("resets_at per window"),
			domain.CapModelQuota:     u("not part of the documented statusline schema"),
		}),
		Limitations: []string{
			"Client-reported: values arrive only while Claude Code runs and after its first API response in a session.",
			"Only claude.ai Pro/Max subscribers (or a Claude apps gateway) receive rate_limits.",
			"Freshness depends on the statusline refresh cadence on the user's machine.",
		},
		DocsURLs: []string{"https://code.claude.com/docs/en/statusline"},
	}
}

// Validate implements providers.Adapter.
func (Adapter) Validate(providers.AccountConfig) error { return nil }

// Fetch is never called for push-only adapters.
func (Adapter) Fetch(context.Context, providers.FetchRequest) (*providers.FetchResult, error) {
	return nil, providers.NewError(domain.ErrUnsupported, "push-only adapter; data arrives through the ingestion API")
}

// SpecFor maps a window key from the statusline payload to a bucket spec.
func SpecFor(key string) (domain.BucketSpec, bool) {
	switch key {
	case "five_hour":
		return domain.BucketSpec{Key: key, Label: "5-hour session", Scope: domain.ScopeAccount, Kind: domain.KindFixedWindow,
			Window: 5 * time.Hour, Unit: domain.UnitPercent, ResetSemantics: domain.ResetProviderTimestamp, Global: true, Official: true}, true
	case "seven_day":
		return domain.BucketSpec{Key: key, Label: "7-day weekly", Scope: domain.ScopeAccount, Kind: domain.KindFixedWindow,
			Window: 7 * 24 * time.Hour, Unit: domain.UnitPercent, ResetSemantics: domain.ResetProviderTimestamp, Global: true, Official: true}, true
	case "spend_limit":
		return domain.BucketSpec{Key: key, Label: "Gateway spend limit", Scope: domain.ScopeAccount, Kind: domain.KindBudget,
			Unit: domain.UnitPercent, ResetSemantics: domain.ResetProviderTimestamp, Global: true, Official: true}, true
	}
	return domain.BucketSpec{}, false
}
