package app

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// AccountCheck is the latest "Test connection" result of a provider account.
type AccountCheck struct {
	AccountID  string    `json:"account_id"`
	CheckedAt  time.Time `json:"checked_at"`
	Status     string    `json:"status" enum:"ok,rejected,unreachable,error,no_credential,not_verified" doc:"ok: the provider accepted the credential; rejected: it refused it; unreachable: no answer; not_verified: CLI bridge accounts are confirmed by their first request."`
	Message    string    `json:"message" doc:"Plain-language explanation; never contains the credential."`
	HTTPStatus int       `json:"http_status,omitempty" required:"false"`
	LatencyMs  int64     `json:"latency_ms"`
}

// checkAccount verifies an account's address and credential without spending
// tokens and stores the result.
func (a *App) checkAccount(ctx context.Context, id string) (*AccountCheck, error) {
	snap := a.CP.Snapshot()
	acc := snap.Accounts[id]
	if acc == nil {
		return nil, api.NotFound("provider account")
	}
	prov := snap.Providers[acc.Spec.ProviderID]
	if prov == nil {
		return nil, api.NotFound("provider")
	}
	out := &AccountCheck{AccountID: id, CheckedAt: a.Clock.Now().UTC()}
	secret, err := a.CP.ResolveSecret(ctx, cp.AccountDef, id, "credential")
	switch {
	case err != nil && prov.Spec.Type != cp.ProviderOllama:
		out.Status, out.Message = "no_credential", "no usable credential: "+executor.ScrubText(err.Error(), secret)
		if acc.Spec.CredentialRef == "" {
			out.Message = "no credential is saved for this account"
		}
	case prov.Spec.Type == cp.ProviderClaudeCode || prov.Spec.Type == cp.ProviderCodex:
		out.Status, out.Message = "not_verified", a.bridgeCheckMessage(prov.Spec.Type)
	default:
		r := a.Gateway.HTTP.Check(ctx, executor.Target{Provider: prov, Account: acc}, secret)
		out.Status, out.Message, out.HTTPStatus, out.LatencyMs = r.Status, r.Message, r.HTTPStatus, r.Latency.Milliseconds()
	}
	var hs any
	if out.HTTPStatus != 0 {
		hs = out.HTTPStatus
	}
	if _, err := a.DB.W.ExecContext(ctx, `INSERT INTO account_checks(account_id, checked_at, status, message, http_status, latency_ms) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET checked_at = excluded.checked_at, status = excluded.status, message = excluded.message,
		http_status = excluded.http_status, latency_ms = excluded.latency_ms`,
		id, out.CheckedAt.UnixMilli(), out.Status, out.Message, hs, out.LatencyMs); err != nil {
		return nil, err
	}
	_ = audit.Record(ctx, a.DB.W, audit.Entry{Action: "provider_account.test", TargetKind: cp.KindAccount, TargetID: id,
		Details: map[string]any{"status": out.Status}})
	return out, nil
}

// bridgeCheckMessage explains what can be checked for a CLI bridge account
// without running a request.
func (a *App) bridgeCheckMessage(kind string) string {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	for _, b := range a.bridgeStatus {
		if b.Kind != kind {
			continue
		}
		if b.Binary == "" || b.LastError != "" {
			return "the CLI for this bridge is not available on the server, so requests through this account fail"
		}
		return "a sign-in token is saved and the CLI is installed; the sign-in itself is confirmed by the first request"
	}
	return "a sign-in token is saved; the sign-in itself is confirmed by the first request"
}

func (a *App) listAccountChecks(ctx context.Context) ([]AccountCheck, error) {
	snap := a.CP.Snapshot()
	rows, err := store.Query(ctx, a.DB.R, func(r *sql.Rows) (AccountCheck, error) {
		var c AccountCheck
		var at int64
		var hs sql.NullInt64
		err := r.Scan(&c.AccountID, &at, &c.Status, &c.Message, &hs, &c.LatencyMs)
		c.CheckedAt, c.HTTPStatus = time.UnixMilli(at).UTC(), int(hs.Int64)
		return c, err
	}, `SELECT account_id, checked_at, status, message, http_status, latency_ms FROM account_checks ORDER BY account_id`)
	if err != nil {
		return nil, err
	}
	out := make([]AccountCheck, 0, len(rows))
	for _, c := range rows {
		if snap.Accounts[c.AccountID] != nil { // deleted accounts drop out
			out = append(out, c)
		}
	}
	return out, nil
}

func (a *App) registerAccountChecks(ap *api.API) {
	type idIn struct {
		ID string `path:"id" maxLength:"64"`
	}
	type checkOut struct{ Body AccountCheck }
	api.Register(ap, api.Op{ID: "test-provider-account", Method: http.MethodPost, Path: "/provider-accounts/{id}/test",
		Summary:     "Test an account's connection and credential",
		Description: "Lists the provider's models with the account's credential (no tokens are spent) and stores the result. CLI bridge accounts report whether a sign-in token is saved and the CLI is installed.",
		Tags:        []string{"Provider accounts"}, Permission: auth.PermConfigWrite}, func(ctx context.Context, in *idIn) (*checkOut, error) {
		c, err := a.checkAccount(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &checkOut{Body: *c}, nil
	})
	type listOut struct {
		Body struct {
			Items []AccountCheck `json:"items"`
		}
	}
	api.Register(ap, api.Op{ID: "list-account-checks", Method: http.MethodGet, Path: "/account-checks", Summary: "Latest connection test of each provider account",
		Tags: []string{"Provider accounts"}, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*listOut, error) {
		items, err := a.listAccountChecks(ctx)
		if err != nil {
			return nil, err
		}
		o := &listOut{}
		o.Body.Items = items
		return o, nil
	})
}
