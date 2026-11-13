package gateway

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
)

// SeatWindow is one rolling subscription window of a provider account, as
// the quota board last saw it.
type SeatWindow struct {
	// Name is "five_hour" or "seven_day".
	Name string
	// Utilization is the used fraction, 0 to 1.
	Utilization float64
	ResetAt     *time.Time
	// Exhausted is true when the window blocks the account.
	Exhausted bool
}

// Usage header names. Anthropic Messages clients read their usage standing
// from these on every successful response (D-048).
const (
	hdrUnified       = "anthropic-ratelimit-unified-"
	hdrStatus        = hdrUnified + "status"
	hdrReset         = hdrUnified + "reset"
	hdrClaim         = hdrUnified + "representative-claim"
	hdrOverage       = hdrUnified + "overage-"
	usageWarnAt      = 0.75
	usageWarnAgainAt = 0.95
)

// Window statuses, from least to most severe.
const (
	usageAllowed  = "allowed"
	usageWarning  = "allowed_warning"
	usageRejected = "rejected"
)

// seatHeaderPrefix maps window names to their header infix.
var seatHeaderPrefix = map[string]string{"five_hour": "5h", "seven_day": "7d"}

// setUsageHeaders adds the anthropic-ratelimit-unified-* headers for the
// attempt that delivers the response, before any byte is written (D-048).
// They are only sent when enabled and the client speaks Anthropic
// Messages. A subscription account reports its seat windows from the quota
// board; any other account reports the tightest budget that applies to the
// request. With nothing to report, no header is set.
func (g *Gateway) setUsageHeaders(ctx context.Context, h http.Header, rc *reqCtx, cand *routing.Candidate) {
	if !g.UsageHeaders || rc.inbound != ir.AnthropicMessages {
		return
	}
	acc := rc.snap.Accounts[cand.AccountID]
	prov := rc.snap.Providers[cand.ProviderID]
	if acc == nil || prov == nil {
		return
	}
	if seatAccount(prov.Spec.Type, acc.Spec.Billing) {
		if g.SeatWindows != nil {
			seatUsageHeaders(h, g.SeatWindows(acc.ID))
		}
		return
	}
	if g.Budgets == nil || g.DB == nil {
		return
	}
	u, err := g.Budgets.Tightest(ctx, g.DB.R, accounting.ReserveInput{AccountID: acc.ID, BindingID: rc.binding.ID, ProjectID: rc.project,
		WorkspaceID: rc.workspace, Billing: acc.Spec.Billing}, g.Clock.Now())
	if err != nil {
		g.Log.Warn("usage_headers.budget_read_failed", "request_id", rc.id, "error", err)
		return
	}
	budgetUsageHeaders(h, u)
}

// seatAccount reports whether an account is a Claude subscription seat,
// whose rolling windows the quota board tracks.
func seatAccount(providerType, billing string) bool {
	return billing == cp.BillingSubscription && (providerType == cp.ProviderClaudeCode || providerType == cp.ProviderAnthropic)
}

// seatUsageHeaders describes a seat's 5-hour and 7-day windows. The
// top-level status is the worst window's; the representative claim and
// reset name that window (the fuller one on a tie).
func seatUsageHeaders(h http.Header, ws []SeatWindow) {
	var rep *SeatWindow
	repStatus := ""
	for i := range ws {
		w := &ws[i]
		infix, ok := seatHeaderPrefix[w.Name]
		if !ok {
			continue
		}
		p := hdrUnified + infix + "-"
		u := clientUtilization(w.Utilization, w.Exhausted)
		status := windowStatus(u, w.Exhausted)
		h.Set(p+"status", status)
		h.Set(p+"utilization", formatFraction(u))
		if t := surpassed(u, w.Exhausted); t != "" {
			h.Set(p+"surpassed-threshold", t)
		}
		if w.ResetAt != nil {
			h.Set(p+"reset", unixSeconds(*w.ResetAt))
		}
		if rep == nil || severity(status) > severity(repStatus) || (status == repStatus && w.Utilization > rep.Utilization) {
			rep, repStatus = w, status
		}
	}
	if rep == nil {
		return
	}
	h.Set(hdrStatus, repStatus)
	h.Set(hdrClaim, rep.Name)
	if rep.ResetAt != nil {
		h.Set(hdrReset, unixSeconds(*rep.ResetAt))
	}
}

// budgetUsageHeaders describes a budget as the client's usage cap: the
// window-agnostic "overage" claim, since a budget follows the calendar, not
// a rolling window. A response is only written while requests are
// allowed, so utilization stays below 1. Nil writes nothing.
func budgetUsageHeaders(h http.Header, u *accounting.BudgetUsage) {
	if u == nil || u.Limit <= 0 {
		return
	}
	f := clientUtilization(u.Fraction, false)
	status := windowStatus(f, false)
	h.Set(hdrStatus, status)
	h.Set(hdrClaim, "overage")
	h.Set(hdrOverage+"status", status)
	h.Set(hdrOverage+"utilization", formatFraction(f))
	if t := surpassed(f, false); t != "" {
		h.Set(hdrOverage+"surpassed-threshold", t)
	}
	if u.PeriodEnd != nil {
		r := unixSeconds(*u.PeriodEnd)
		h.Set(hdrReset, r)
		h.Set(hdrOverage+"reset", r)
	}
	if p := budgetPeriod(u.Period); p != "" {
		h.Set(hdrOverage+"period", p)
	}
}

func budgetPeriod(p string) string {
	switch p {
	case "day":
		return "daily"
	case "week":
		return "weekly"
	case "month":
		return "monthly"
	}
	return ""
}

func windowStatus(u float64, exhausted bool) string {
	switch {
	case exhausted:
		return usageRejected
	case u >= usageWarnAt:
		return usageWarning
	}
	return usageAllowed
}

func severity(s string) int {
	switch s {
	case usageRejected:
		return 2
	case usageWarning:
		return 1
	case usageAllowed:
		return 0
	}
	return -1
}

// clientUtilization rounds to two decimals and keeps a window that still
// allows requests below 1, so the client does not treat it as used up.
func clientUtilization(u float64, exhausted bool) float64 {
	if exhausted {
		return 1
	}
	u = math.Round(min(max(u, 0), 1)*100) / 100
	return min(u, 0.99)
}

// surpassed names the highest client notice threshold u has reached; empty
// below the first.
func surpassed(u float64, exhausted bool) string {
	switch {
	case exhausted:
		return "1"
	case u >= usageWarnAgainAt:
		return "0.95"
	case u >= usageWarnAt:
		return "0.75"
	}
	return ""
}

func formatFraction(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func unixSeconds(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
