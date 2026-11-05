// Package codingplan implements UNOFFICIAL adapters for coding-plan quotas
// whose endpoints are not publicly documented: Z.ai GLM Coding Plan and
// MiniMax Coding/Token Plan. The endpoints and field semantics were observed
// in two independent open-source clients (onWatch: github.com/onllm-dev/onwatch
// and CodexBar: github.com/steipete/CodexBar), which disagree on some details;
// decoding is therefore tolerant and ambiguous values are left unknown.
// Accounts must opt in with unofficial.acknowledge_terms_risk (enforced at
// configuration load for every TierUnofficial adapter).
package codingplan

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Provider IDs.
const (
	ZaiID     domain.ProviderID = "zai_coding_plan"
	MiniMaxID domain.ProviderID = "minimax_coding_plan"
)

const communityNote = "Community-observed endpoint (not documented by the provider); field semantics inferred from open-source clients and may change."

// options shared by both adapters.
type options struct {
	// Region selects the host: global (default) or china.
	Region string `json:"region"`
	// BaseURL overrides the host (tests, proxies).
	BaseURL string `json:"base_url"`
}

func parseOptions(cfg providers.AccountConfig, global, china string) (string, error) {
	var o options
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return "", err
	}
	if o.BaseURL != "" {
		return providers.ValidateBaseURL(o.BaseURL)
	}
	switch o.Region {
	case "", "global":
		return global, nil
	case "china":
		return china, nil
	}
	return "", providers.NewError(domain.ErrConfiguration, "options.region must be global or china")
}

// flexNum decodes a JSON number or numeric string; null, "" and garbage
// decode as unknown (nil), never zero.
type flexNum struct{ v *float64 }

func (f *flexNum) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	s := string(b)
	if b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		f.v = &v
	}
	return nil
}

func (f flexNum) known() bool { return f.v != nil }

func (f flexNum) val() float64 {
	if f.v == nil {
		return 0
	}
	return *f.v
}

// epoch converts an epoch (milliseconds or seconds) to a time.
func (f flexNum) epoch() *time.Time {
	if f.v == nil || *f.v <= 0 {
		return nil
	}
	n := int64(*f.v)
	var t time.Time
	if n > 1e12 {
		t = time.UnixMilli(n).UTC()
	} else {
		t = time.Unix(n, 0).UTC()
	}
	return &t
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

func windowKey(win time.Duration, prefix string) (string, string) {
	switch win {
	case 5 * time.Hour:
		return "five_hour", "5-hour"
	case 7 * 24 * time.Hour:
		return "seven_day", "Weekly"
	case 24 * time.Hour:
		return prefix + ":1d", "Daily"
	}
	if win > 0 {
		return prefix + ":" + win.String(), win.String() + " window"
	}
	return prefix, "Window"
}
