package codingplan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// MiniMax hosts and paths. CodexBar tries the token-plan path first and falls
// back to the older coding-plan path that onWatch uses.
const (
	MiniMaxGlobalBase  = "https://api.minimax.io"
	MiniMaxChinaBase   = "https://api.minimaxi.com"
	miniMaxTokenPlan   = "/v1/token_plan/remains"
	miniMaxCodingPlan  = "/v1/api/openplatform/coding_plan/remains"
	miniMaxStatusNoSub = 3
	miniMaxStatusEmpty = 2
)

// MiniMax is the MiniMax Coding/Token Plan adapter (unofficial).
type MiniMax struct{ http *providers.HTTPClient }

// NewMiniMax creates the adapter.
func NewMiniMax(d providers.Deps) *MiniMax { return &MiniMax{http: d.HTTP} }

// Info implements providers.Adapter.
func (a *MiniMax) Info() domain.ProviderInfo {
	s, p := providers.Sup, providers.Part
	return domain.ProviderInfo{
		ID: MiniMaxID, DisplayName: "MiniMax Coding Plan (unofficial)", Family: "minimax", AdapterVersion: "1",
		Tier: domain.TierUnofficial, DefaultPollInterval: 10 * time.Minute,
		CredentialTypes: []string{"api_key (coding/token plan key, sk-cp-…)"},
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaRemaining: s("*_remaining_percent of the interval and weekly windows. " + communityNote),
			domain.CapQuotaUsed:      p("derived as 100 − remaining percent; raw counts are ambiguous and not used"),
			domain.CapFixedReset:     s("interval end_time and weekly_end_time"),
			domain.CapModelQuota:     p("per model_name entries when the plan reports several"),
		}),
		Limitations: []string{
			communityNote,
			"The open-source clients disagree on the endpoint version, response envelope and value types; both shapes are accepted.",
			"onWatch reports that current_interval_usage_count actually holds REMAINING requests; counts are therefore not used, only remaining percentages.",
			"Weekly windows exist only for plans purchased from 2026-03-23.",
		},
		DocsURLs: []string{"https://platform.minimax.io/docs/coding-plan/intro", "https://github.com/onllm-dev/onwatch", "https://github.com/steipete/CodexBar"},
	}
}

// Validate implements providers.Adapter.
func (a *MiniMax) Validate(cfg providers.AccountConfig) error {
	if _, err := parseOptions(cfg, MiniMaxGlobalBase, MiniMaxChinaBase); err != nil {
		return err
	}
	_, err := providers.RequireSecret(cfg, "api_key")
	return err
}

type miniMaxModel struct {
	ModelName                       string  `json:"model_name"`
	StartTime                       flexNum `json:"start_time"`
	EndTime                         flexNum `json:"end_time"`
	CurrentIntervalRemainingPercent flexNum `json:"current_interval_remaining_percent"`
	CurrentIntervalStatus           flexNum `json:"current_interval_status"`
	WeeklyStartTime                 flexNum `json:"weekly_start_time"`
	WeeklyEndTime                   flexNum `json:"weekly_end_time"`
	CurrentWeeklyRemainingPercent   flexNum `json:"current_weekly_remaining_percent"`
	CurrentWeeklyStatus             flexNum `json:"current_weekly_status"`
}

type miniMaxPayload struct {
	CurrentSubscribeTitle string         `json:"current_subscribe_title"`
	ModelRemains          []miniMaxModel `json:"model_remains"`
}

type miniMaxResponse struct {
	BaseResp *struct {
		StatusCode flexNum `json:"status_code"`
	} `json:"base_resp"`
	miniMaxPayload
	Data *miniMaxPayload `json:"data"`
}

func (r miniMaxResponse) payload() miniMaxPayload {
	if r.Data != nil && len(r.Data.ModelRemains) > 0 {
		return *r.Data
	}
	return r.miniMaxPayload
}

func miniMaxStatusError(code int) error {
	switch code {
	case 1004, 2049: // authentication failure / invalid API key
		return &providers.Error{Kind: domain.ErrAuthentication, Message: "provider rejected the credentials"}
	case 1002: // rate limited
		return providers.NewError(domain.ErrRateLimited, "provider rate limit reached")
	}
	return providers.NewError(domain.ErrInvalidResponse, fmt.Sprintf("provider returned status code %d", code))
}

func (a *MiniMax) get(ctx context.Context, u, key string) (*miniMaxResponse, error) {
	resp, err := a.http.Do(ctx, providers.Request{URL: u, Header: providers.BearerHeader(key), NotFoundIsUnsupported: true})
	if err != nil {
		return nil, err
	}
	var body miniMaxResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, providers.NewError(domain.ErrInvalidResponse, "response is not valid JSON for the expected schema")
	}
	if body.BaseResp != nil && body.BaseResp.StatusCode.known() && body.BaseResp.StatusCode.val() != 0 {
		return nil, miniMaxStatusError(int(body.BaseResp.StatusCode.val()))
	}
	return &body, nil
}

func pctObs(key, label, model string, win time.Duration, remaining flexNum, status flexNum, end *time.Time, now time.Time) domain.BucketObservation {
	scope, global := domain.ScopeAccount, true
	if model != "" {
		scope, global = domain.ScopeModel, false
		key += ":model:" + model
		label += " · " + model
	}
	ob := domain.BucketObservation{
		Spec: domain.BucketSpec{Key: key, Label: label, Scope: scope, Kind: domain.KindFixedWindow, Window: win, Unit: domain.UnitPercent,
			Model: model, ResetSemantics: domain.ResetUnknown, Global: global},
		Quality: domain.QualityProviderReported, Source: "minimax:remains", SourceTime: &now,
	}
	switch {
	case remaining.known():
		r := clampPct(remaining.val())
		ob.Remaining, ob.Used, ob.Limit = providers.Float(r), providers.Float(100-r), providers.Float(100)
	case int(status.val()) == miniMaxStatusEmpty:
		// Status 2 = exhausted (onWatch).
		ob.Remaining, ob.Used, ob.Limit = providers.Float(0), providers.Float(100), providers.Float(100)
	}
	if end != nil && end.After(now) {
		ob.ResetAt = end
		ob.Spec.ResetSemantics = domain.ResetProviderTimestamp
	}
	return ob
}

// Fetch implements providers.Adapter.
func (a *MiniMax) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	base, err := parseOptions(req.Config, MiniMaxGlobalBase, MiniMaxChinaBase)
	if err != nil {
		return nil, err
	}
	key, err := providers.RequireSecret(req.Config, "api_key")
	if err != nil {
		return nil, err
	}
	body, err := a.get(ctx, base+miniMaxTokenPlan, key)
	if pe := providers.AsError(err); err != nil && pe.Kind != domain.ErrUnsupported {
		return nil, err
	}
	if err != nil || len(body.payload().ModelRemains) == 0 {
		if body, err = a.get(ctx, base+miniMaxCodingPlan, key); err != nil {
			return nil, err
		}
	}
	p := body.payload()
	now := req.Now.UTC()
	res := &providers.FetchResult{SourceTime: &now}
	if p.CurrentSubscribeTitle != "" {
		id := "plan: " + p.CurrentSubscribeTitle
		res.MaskedIdentifier = &id
	}
	models := 0
	for _, m := range p.ModelRemains {
		if m.ModelName != "" && m.ModelName != "general" {
			models++
		}
	}
	notSubscribed := len(p.ModelRemains) > 0
	missingPct := false
	for _, m := range p.ModelRemains {
		if int(m.CurrentIntervalStatus.val()) != miniMaxStatusNoSub || !m.CurrentIntervalStatus.known() {
			notSubscribed = false
		}
		if int(m.CurrentIntervalStatus.val()) == miniMaxStatusNoSub && m.CurrentIntervalStatus.known() {
			continue
		}
		model := ""
		if models > 1 || (m.ModelName != "" && m.ModelName != "general") {
			model = m.ModelName
		}
		start, end := m.StartTime.epoch(), m.EndTime.epoch()
		var win time.Duration
		if start != nil && end != nil && end.After(*start) {
			win = end.Sub(*start).Round(time.Minute)
		}
		k, label := windowKey(win, "interval")
		res.Buckets = append(res.Buckets, pctObs(k, label, model, win, m.CurrentIntervalRemainingPercent, m.CurrentIntervalStatus, end, now))
		if !m.CurrentIntervalRemainingPercent.known() {
			missingPct = true
		}
		ws, we := m.WeeklyStartTime.epoch(), m.WeeklyEndTime.epoch()
		if we != nil && (m.CurrentWeeklyRemainingPercent.known() || m.CurrentWeeklyStatus.known()) {
			ww := 7 * 24 * time.Hour
			if ws != nil && we.After(*ws) {
				ww = we.Sub(*ws).Round(time.Minute)
			}
			wk, wl := windowKey(ww, "weekly")
			if wk != "seven_day" {
				wk, wl = "weekly:"+ww.String(), "Weekly"
			}
			res.Buckets = append(res.Buckets, pctObs(wk, wl, model, ww, m.CurrentWeeklyRemainingPercent, m.CurrentWeeklyStatus, we, now))
		}
	}
	if notSubscribed {
		return nil, providers.NewError(domain.ErrUnsupported, "no active MiniMax coding plan on this key")
	}
	if missingPct {
		res.Notes = append(res.Notes, "remaining percentage not reported; usage left unknown because raw counts are ambiguous")
	}
	if len(res.Buckets) == 0 {
		res.Notes = append(res.Notes, "no plan windows in the response")
	}
	return res, nil
}
