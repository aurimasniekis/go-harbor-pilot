// Package quotaapi exposes quota monitoring, forecasts, provider-reported
// usage and budget status.
package quotaapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ratelimit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/forecasting"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Deps are the API's dependencies.
type Deps struct {
	Quota   func() *quota.Service
	Budgets *accounting.Budgets
	DB      func() *store.DB
	CP      func() *cp.Service
	Clock   clock.Clock
	// Health reports combined routing health (quota and gateway cooldowns).
	Health func(accountID string) routing.Health
	// Origin is the server's public origin (OAuth callbacks).
	Origin func() string
}

// AccountHealthView is the routing view of a provider account.
type AccountHealthView struct {
	AccountID string     `json:"account_id"`
	Name      string     `json:"name"`
	Billing   string     `json:"billing"`
	Available bool       `json:"available"`
	Reason    string     `json:"reason"`
	Until     *time.Time `json:"until"`
	Remaining *float64   `json:"remaining" doc:"Fraction of the tightest known window left; null when unknown."`
	ResetAt   *time.Time `json:"reset_at"`
	Stale     bool       `json:"stale"`
}

// BoardBody is the quota board.
type BoardBody struct {
	Monitors []quota.MonitorView      `json:"monitors"`
	Accounts []quota.AccountQuotaView `json:"observed_accounts" doc:"Rate-limit state derived from gateway responses."`
	Health   []AccountHealthView      `json:"account_health"`
	// DroppedObservations counts response observations dropped under load.
	DroppedObservations int64 `json:"dropped_observations"`
}

// Register mounts the endpoints.
func Register(a *api.API, d Deps) {
	tags := []string{"Quota"}
	type adaptersOut struct {
		Body struct {
			Items []quota.AdapterView `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-quota-adapters", Method: http.MethodGet, Path: "/quota/adapters", Summary: "List quota adapters",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*adaptersOut, error) {
		out := &adaptersOut{}
		out.Body.Items = d.Quota().Adapters()
		return out, nil
	})

	type boardOut struct{ Body BoardBody }
	api.Register(a, api.Op{ID: "quota-board", Method: http.MethodGet, Path: "/quota/board", Summary: "Quota monitors, observed rate limits and account health",
		Tags: tags, Permission: auth.PermUsageRead}, func(ctx context.Context, _ *struct{}) (*boardOut, error) {
		mons, accs := d.Quota().Board()
		body := BoardBody{Monitors: mons, Accounts: accs, Health: []AccountHealthView{}, DroppedObservations: quota.DroppedObservations()}
		if snap := d.CP().Snapshot(); snap != nil {
			for _, id := range sortedAccountIDs(snap) {
				acc := snap.Accounts[id]
				h := d.Health(id)
				body.Health = append(body.Health, AccountHealthView{AccountID: id, Name: acc.Name, Billing: acc.Spec.Billing, Available: h.Available,
					Reason: h.Reason, Until: h.Until, Remaining: h.Remaining, ResetAt: h.ResetAt, Stale: h.Stale})
			}
		}
		return &boardOut{Body: body}, nil
	})

	type refreshIn struct {
		ID string `path:"id"`
	}
	type refreshOut struct {
		Body struct {
			Status string `json:"status" enum:"queued,already_queued,in_flight,rate_limited,unknown"`
		}
	}
	api.Register(a, api.Op{ID: "refresh-quota-monitor", Method: http.MethodPost, Path: "/quota/monitors/{id}/refresh", Summary: "Poll a quota monitor now",
		Tags: tags, Permission: auth.PermConfigWrite}, func(ctx context.Context, in *refreshIn) (*refreshOut, error) {
		snap := d.CP().Snapshot()
		if snap == nil || snap.Monitors[in.ID] == nil {
			return nil, api.NotFound("quota monitor")
		}
		d.Quota().SyncNow(ctx)
		out := &refreshOut{}
		out.Body.Status = d.Quota().Refresh(in.ID)
		return out, nil
	})

	type histIn struct {
		Owner  string `path:"owner" doc:"Quota monitor id, or provider account id for response-derived buckets."`
		Bucket string `query:"bucket" required:"true" maxLength:"200"`
		Hours  int    `query:"hours" minimum:"1" maximum:"2160" default:"168"`
	}
	type histOut struct {
		Body struct {
			Points []quota.HistoryPoint `json:"points"`
		}
	}
	api.Register(a, api.Op{ID: "quota-history", Method: http.MethodGet, Path: "/quota/buckets/{owner}/history", Summary: "Observation history of a quota bucket",
		Tags: tags, Permission: auth.PermUsageRead}, func(ctx context.Context, in *histIn) (*histOut, error) {
		h := in.Hours
		if h <= 0 {
			h = 168
		}
		pts, err := d.Quota().History(ctx, in.Owner, in.Bucket, d.Clock.Now().Add(-time.Duration(h)*time.Hour))
		if err != nil {
			return nil, err
		}
		out := &histOut{}
		out.Body.Points = pts
		return out, nil
	})

	type fcIn struct {
		Owner  string `path:"owner"`
		Bucket string `query:"bucket" required:"true" maxLength:"200"`
	}
	type fcOut struct{ Body forecasting.QuotaForecast }
	api.Register(a, api.Op{ID: "quota-forecast", Method: http.MethodGet, Path: "/quota/buckets/{owner}/forecast", Summary: "Forecast a quota bucket",
		Description: "Projects remaining capacity from recent burn. Forecasts are estimates with a stated confidence; unknown values are never assumed.",
		Tags:        tags, Permission: auth.PermUsageRead}, func(ctx context.Context, in *fcIn) (*fcOut, error) {
		r, err := d.Quota().Forecast(ctx, in.Owner, in.Bucket)
		if errors.Is(err, quota.ErrUnknownBucket) {
			return nil, api.NotFound("quota bucket")
		}
		if err != nil {
			return nil, err
		}
		return &fcOut{Body: r}, nil
	})

	type extIn struct {
		MonitorID string `query:"monitor_id" maxLength:"64"`
		Breakdown string `query:"breakdown" maxLength:"64" doc:"total, model, api_key, … Rows of different breakdowns describe the same usage."`
		Days      int    `query:"days" minimum:"1" maximum:"400" default:"7"`
	}
	type extOut struct {
		Body struct {
			Items []quota.ExternalUsageRow `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "external-usage", Method: http.MethodGet, Path: "/usage/external", Summary: "Usage and charges reported by provider APIs",
		Tags: []string{"Usage"}, Permission: auth.PermUsageRead}, func(ctx context.Context, in *extIn) (*extOut, error) {
		days := in.Days
		if days <= 0 {
			days = 7
		}
		now := d.Clock.Now()
		rows, err := d.Quota().ExternalUsage(ctx, in.MonitorID, in.Breakdown, now.Add(-time.Duration(days)*24*time.Hour), now.Add(time.Hour))
		if err != nil {
			return nil, err
		}
		out := &extOut{}
		out.Body.Items = rows
		return out, nil
	})

	type recIn struct {
		Days int `query:"days" minimum:"1" maximum:"90" default:"14"`
	}
	type recOut struct {
		Body struct {
			Items []quota.ReconciliationRow `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "usage-reconciliation", Method: http.MethodGet, Path: "/usage/reconciliation", Summary: "Provider-reported vs gateway-observed usage",
		Tags: []string{"Usage"}, Permission: auth.PermUsageRead}, func(ctx context.Context, in *recIn) (*recOut, error) {
		days := in.Days
		if days <= 0 {
			days = 14
		}
		rows, err := d.Quota().Reconciliation(ctx, days)
		if err != nil {
			return nil, err
		}
		out := &recOut{}
		out.Body.Items = rows
		return out, nil
	})

	registerLogin(a, d)

	type budOut struct {
		Body struct {
			Items []accounting.BudgetUsage `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "budget-status", Method: http.MethodGet, Path: "/budgets/status", Summary: "Current period spend for every budget",
		Tags: []string{"Budget"}, Permission: auth.PermUsageRead}, func(ctx context.Context, _ *struct{}) (*budOut, error) {
		items, err := d.Budgets.Status(ctx, d.DB().R, d.Clock.Now())
		if err != nil {
			return nil, err
		}
		out := &budOut{}
		out.Body.Items = items
		return out, nil
	})
}

func sortedAccountIDs(s *cp.Snapshot) []string {
	out := make([]string, 0, len(s.Accounts))
	for id := range s.Accounts {
		out = append(out, id)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && s.Accounts[out[j]].Name < s.Accounts[out[j-1]].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

var loginLimiter = ratelimit.New(20, 5, time.Now)

func loginErr(err error) error {
	var le *quota.LoginError
	switch {
	case errors.Is(err, quota.ErrNotLoginMonitor):
		return api.Errorf(http.StatusNotFound, "not_found", "monitor is not configured with token_source login")
	case errors.Is(err, quota.ErrBadCallback):
		return api.Errorf(http.StatusBadRequest, "invalid_callback", err.Error())
	case errors.As(err, &le):
		return api.Errorf(http.StatusUnprocessableEntity, "login_failed", le.Message)
	}
	return err
}

func registerLogin(a *api.API, d Deps) {
	tags := []string{"Quota"}
	type listOut struct {
		Body struct {
			Items []quota.LoginView `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-quota-logins", Method: http.MethodGet, Path: "/quota/logins", Summary: "Monitors that use in-app sign-in",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*listOut, error) {
		out := &listOut{}
		out.Body.Items = d.Quota().Logins()
		return out, nil
	})
	type startIn struct {
		ID   string `path:"id"`
		Body struct {
			CallbackURL string `json:"callback_url,omitempty" maxLength:"2048" doc:"Same-origin URL for providers that redirect back (key exchange)."`
		}
	}
	type startOut struct {
		Body struct {
			AuthorizeURL string `json:"authorize_url"`
			State        string `json:"state"`
			Instructions string `json:"instructions"`
		}
	}
	api.Register(a, api.Op{ID: "start-quota-login", Method: http.MethodPost, Path: "/quota/logins/{id}/start", Summary: "Start provider sign-in for a monitor",
		Tags: tags, Permission: auth.PermSecretsWrite}, func(ctx context.Context, in *startIn) (*startOut, error) {
		if ok, _ := loginLimiter.Allow("start"); !ok {
			return nil, api.Errorf(http.StatusTooManyRequests, "rate_limited", "too many sign-in attempts")
		}
		u, st, ins, err := d.Quota().StartLogin(ctx, in.ID, in.Body.CallbackURL, d.Origin())
		if err != nil {
			return nil, loginErr(err)
		}
		out := &startOut{}
		out.Body.AuthorizeURL, out.Body.State, out.Body.Instructions = u, st, ins
		return out, nil
	})
	type completeIn struct {
		ID   string `path:"id"`
		Body struct {
			State string `json:"state" maxLength:"512"`
			Code  string `json:"code" maxLength:"4096" doc:"The code (or redirected URL) shown after approval."`
		}
	}
	type completeOut struct {
		Body struct {
			Status  string `json:"status"`
			Refresh string `json:"refresh"`
		}
	}
	api.Register(a, api.Op{ID: "complete-quota-login", Method: http.MethodPost, Path: "/quota/logins/{id}/complete", Summary: "Complete provider sign-in",
		Tags: tags, Permission: auth.PermSecretsWrite}, func(ctx context.Context, in *completeIn) (*completeOut, error) {
		if ok, _ := loginLimiter.Allow("complete"); !ok {
			return nil, api.Errorf(http.StatusTooManyRequests, "rate_limited", "too many sign-in attempts")
		}
		st, err := d.Quota().CompleteLogin(ctx, in.ID, in.Body.State, in.Body.Code)
		if err != nil {
			return nil, loginErr(err)
		}
		out := &completeOut{}
		out.Body.Status, out.Body.Refresh = "signed_in", st
		return out, nil
	})
	type delIn struct {
		ID string `path:"id"`
	}
	api.Register(a, api.Op{ID: "delete-quota-login", Method: http.MethodDelete, Path: "/quota/logins/{id}", Summary: "Remove a stored sign-in token",
		Tags: tags, Permission: auth.PermSecretsWrite, Status: http.StatusNoContent}, func(ctx context.Context, in *delIn) (*struct{}, error) {
		if err := d.Quota().Logout(ctx, in.ID); err != nil {
			return nil, loginErr(err)
		}
		return nil, nil
	})
}
