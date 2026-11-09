package claudecode

import (
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
)

// The CLI reports upstream API errors with a status and sometimes a type.
// A 400 without a type is the upstream refusing the request itself: not
// retried on another account. Capacity and server errors stay retryable.
func TestCLIErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		status int
		msg    string
		want   ir.ErrorKind
	}{
		{"", 400, "API Error: 400 messages.4.content.0.cache_control.ttl: invalid order", ir.ErrInvalidRequest},
		{"", 400, "API Error: 400 Your credit balance is too low to access the API.", ir.ErrQuotaExhausted},
		{"", 400, "API Error: 400 This organization has been disabled.", ir.ErrPermission},
		{"invalid_request", 0, "", ir.ErrInvalidRequest},
		{"", 429, "", ir.ErrRateLimited},
		{"rate_limit", 0, "", ir.ErrRateLimited},
		{"", 500, "", ir.ErrOverloaded},
		{"", 529, "", ir.ErrOverloaded},
		{"overloaded", 0, "", ir.ErrOverloaded},
		{"", 401, "", ir.ErrAuthentication},
		{"", 403, "", ir.ErrPermission},
		{"", 0, "", ir.ErrUpstream},
		{"", 404, "", ir.ErrUpstream},
	} {
		if got := cliError(tc.kind, tc.status, tc.msg, 0, time.Now()).Kind; got != tc.want {
			t.Errorf("kind=%q status=%d: %s, want %s", tc.kind, tc.status, got, tc.want)
		}
	}
}
