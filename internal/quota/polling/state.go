package polling

import (
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// AuthState summarizes credential health.
type AuthState string

// Auth states.
const (
	AuthUnknown AuthState = "unknown"
	AuthOK      AuthState = "ok"
	AuthFailed  AuthState = "failed"
	AuthDenied  AuthState = "permission_denied"
)

// PollState is persisted scheduling and health state of a monitored account.
type PollState struct {
	AccountID           domain.AccountID
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	ConsecutiveFailures int
	NextAttemptAt       *time.Time
	LastError           *domain.FetchError
	AuthState           AuthState
	CooldownUntil       *time.Time
	LastLatencyMS       *int64
}
