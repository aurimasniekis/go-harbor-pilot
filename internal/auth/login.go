package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
)

// Throttle policy: the first throttleFree failures are free, after which each
// further failure doubles the lockout, capped at throttleMax. Counters decay
// after throttleDecay without failures. There is no permanent lockout.
const (
	throttleFree  = 3
	throttleMax   = 15 * time.Minute
	throttleDecay = 24 * time.Hour
)

// ThrottledError carries the retry delay.
type ThrottledError struct{ RetryAfter time.Duration }

func (e *ThrottledError) Error() string { return ErrThrottled.Error() }
func (e *ThrottledError) Unwrap() error { return ErrThrottled }

// Login verifies an email/password pair and creates a session.
func (s *Service) Login(ctx context.Context, email, password string) (string, *SessionInfo, error) {
	st, err := s.GetSettings(ctx)
	if err != nil {
		return "", nil, err
	}
	if !st.PasswordLogin {
		return "", nil, ErrPasswordLoginOff
	}
	email = strings.ToLower(strings.TrimSpace(email))
	ipKey := "ip:" + reqmeta.RateKey(reqmeta.From(ctx).IP)
	acctKey := "acct:" + email
	if d := s.throttled(ctx, acctKey, ipKey); d > 0 {
		VerifyDummy(password)
		return "", nil, &ThrottledError{RetryAfter: d}
	}
	u, hash, err := s.userByEmail(ctx, s.db.R, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", nil, err
	}
	if u == nil || hash == "" || u.Disabled {
		VerifyDummy(password)
		s.recordFailure(ctx, acctKey, ipKey)
		return "", nil, ErrInvalidCredentials
	}
	ok, rehash := VerifyPassword(hash, password)
	if !ok {
		s.recordFailure(ctx, acctKey, ipKey)
		return "", nil, ErrInvalidCredentials
	}
	s.clearFailures(ctx, acctKey)
	if rehash {
		if h, err := HashPassword(password); err == nil {
			_, _ = s.db.W.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, h, u.ID)
		}
	}
	_, _ = s.db.W.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, s.nowMS(), u.ID)
	return s.CreateSession(ctx, s.db.W, u.ID, "password", "", false)
}

func (s *Service) throttled(ctx context.Context, keys ...string) time.Duration {
	now := s.nowMS()
	var worst time.Duration
	for _, k := range keys {
		var until int64
		err := s.db.R.QueryRowContext(ctx, `SELECT blocked_until FROM login_throttle WHERE key = ?`, k).Scan(&until)
		if err == nil && until > now {
			worst = max(worst, time.Duration(until-now)*time.Millisecond)
		}
	}
	return worst
}

func (s *Service) recordFailure(ctx context.Context, keys ...string) {
	now := s.clock.Now()
	for _, k := range keys {
		var failures, last int64
		err := s.db.W.QueryRowContext(ctx, `SELECT failures, last_failure_at FROM login_throttle WHERE key = ?`, k).Scan(&failures, &last)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && now.Sub(time.UnixMilli(last)) > throttleDecay) {
			failures = 0
		} else if err != nil {
			continue
		}
		failures++
		var until int64
		if failures > throttleFree {
			d := time.Second << min(failures-throttleFree-1, 20)
			until = now.Add(min(d, throttleMax)).UnixMilli()
		}
		_, _ = s.db.W.ExecContext(ctx, `INSERT INTO login_throttle(key, failures, last_failure_at, blocked_until) VALUES (?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET failures = excluded.failures, last_failure_at = excluded.last_failure_at, blocked_until = excluded.blocked_until`,
			k, failures, now.UnixMilli(), until)
	}
}

func (s *Service) clearFailures(ctx context.Context, key string) {
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM login_throttle WHERE key = ?`, key)
}

// ---------- one-time tokens ----------

// One-time token purposes and lifetimes.
const (
	PurposeSetup    = "setup"
	PurposeRecovery = "recovery"
	PurposeInvite   = "invite"
	PurposeReset    = "reset"
)

var oneTimeTTL = map[string]time.Duration{
	PurposeSetup:    30 * time.Minute,
	PurposeRecovery: 30 * time.Minute,
	PurposeInvite:   72 * time.Hour,
	PurposeReset:    time.Hour,
}

// OneTime describes a consumed one-time token.
type OneTime struct {
	Purpose string
	UserID  string
	Email   string
	Role    Role
}

// IssueOneTime creates a single-use token and returns its plaintext.
func (s *Service) IssueOneTime(ctx context.Context, purpose, userID, email string, role Role, createdBy string) (string, time.Time, error) {
	ttl, ok := oneTimeTTL[purpose]
	if !ok {
		return "", time.Time{}, errors.New("unknown token purpose")
	}
	tok := RandomString(32)
	exp := s.clock.Now().Add(ttl)
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO one_time_tokens(token_hash, purpose, user_id, email, role, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, HashOpaque(s.sessionPepper, "one-time", tok), purpose, nullStr(userID), nullStr(email),
		nullStr(string(role)), createdBy, s.nowMS(), exp.UnixMilli())
	return tok, exp, err
}

// PeekOneTime validates a token without consuming it.
func (s *Service) PeekOneTime(ctx context.Context, purpose, tok string) (*OneTime, error) {
	return s.loadOneTime(ctx, s.db.R, purpose, tok)
}

func (s *Service) loadOneTime(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, purpose, tok string) (*OneTime, error) {
	if tok == "" || len(tok) > 128 {
		return nil, ErrTokenInvalid
	}
	var ot OneTime
	var uid, email, role sql.NullString
	var exp int64
	var used sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT purpose, user_id, email, role, expires_at, used_at FROM one_time_tokens WHERE token_hash = ?`,
		HashOpaque(s.sessionPepper, "one-time", tok)).Scan(&ot.Purpose, &uid, &email, &role, &exp, &used)
	if err != nil || ot.Purpose != purpose || used.Valid || exp <= s.nowMS() {
		return nil, ErrTokenInvalid
	}
	ot.UserID, ot.Email, ot.Role = uid.String, email.String, Role(role.String)
	return &ot, nil
}

// ConsumeOneTime marks a token used inside tx and returns its contents.
func (s *Service) ConsumeOneTime(ctx context.Context, tx *sql.Tx, purpose, tok string) (*OneTime, error) {
	ot, err := s.loadOneTime(ctx, tx, purpose, tok)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE one_time_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`,
		s.nowMS(), HashOpaque(s.sessionPepper, "one-time", tok))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrTokenInvalid
	}
	return ot, nil
}

// SetupRequired reports whether no administrator exists yet.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL`).Scan(&n)
	return n == 0, err
}

// Bootstrap consumes a setup token and creates the first administrator.
func (s *Service) Bootstrap(ctx context.Context, tok, email, name, password string) (string, *SessionInfo, *User, error) {
	if required, err := s.SetupRequired(ctx); err != nil {
		return "", nil, nil, err
	} else if !required {
		return "", nil, nil, ErrTokenInvalid
	}
	var sess string
	var info *SessionInfo
	var u *User
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.ConsumeOneTime(ctx, tx, PurposeSetup, tok); err != nil {
			return err
		}
		var err error
		u, err = s.CreateUser(ctx, tx, NewUser{Email: email, DisplayName: name, Role: RoleAdmin, Password: password})
		if err != nil {
			return err
		}
		sess, info, err = s.CreateSession(ctx, tx, u.ID, "password", "", false)
		return err
	})
	return sess, info, u, err
}

// AcceptInvite consumes an invite or reset token, sets the password and
// signs the user in.
func (s *Service) AcceptInvite(ctx context.Context, purpose, tok, password string) (string, *SessionInfo, error) {
	var sess string
	var info *SessionInfo
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		ot, err := s.ConsumeOneTime(ctx, tx, purpose, tok)
		if err != nil {
			return err
		}
		if err := s.SetPassword(ctx, tx, ot.UserID, password, ""); err != nil {
			return err
		}
		sess, info, err = s.CreateSession(ctx, tx, ot.UserID, "password", "", false)
		return err
	})
	return sess, info, err
}

// Recover consumes a break-glass recovery token. It always works, even when
// password login is disabled, and raises a persistent security alert.
func (s *Service) Recover(ctx context.Context, tok string) (string, *SessionInfo, error) {
	var sess string
	var info *SessionInfo
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		ot, err := s.ConsumeOneTime(ctx, tx, PurposeRecovery, tok)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at = NULL, updated_at = ? WHERE id = ?`, s.nowMS(), ot.UserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_alerts(id, kind, message, created_at) VALUES (?, 'breakglass', ?, ?)`,
			ids.New(ids.Audit), "Break-glass recovery login was used. Review the audit log and rotate credentials if this was unexpected.", s.nowMS()); err != nil {
			return err
		}
		sess, info, err = s.CreateSession(ctx, tx, ot.UserID, "breakglass", "", false)
		return err
	})
	return sess, info, err
}

// SecurityAlert is an unacknowledged security notice.
type SecurityAlert struct {
	ID             string
	Kind           string
	Message        string
	CreatedAt      time.Time
	AcknowledgedAt *time.Time
	AcknowledgedBy string
}

// ListSecurityAlerts returns alerts (unacknowledged first).
func (s *Service) ListSecurityAlerts(ctx context.Context, includeAcked bool) ([]SecurityAlert, error) {
	q := `SELECT id, kind, message, created_at, acknowledged_at, acknowledged_by FROM security_alerts`
	if !includeAcked {
		q += ` WHERE acknowledged_at IS NULL`
	}
	q += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := s.db.R.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecurityAlert
	for rows.Next() {
		var a SecurityAlert
		var created int64
		var ack sql.NullInt64
		var by sql.NullString
		if err := rows.Scan(&a.ID, &a.Kind, &a.Message, &created, &ack, &by); err != nil {
			return nil, err
		}
		a.CreatedAt = time.UnixMilli(created).UTC()
		if ack.Valid {
			t := time.UnixMilli(ack.Int64).UTC()
			a.AcknowledgedAt = &t
		}
		a.AcknowledgedBy = by.String
		out = append(out, a)
	}
	return out, rows.Err()
}

// AcknowledgeSecurityAlert marks an alert acknowledged.
func (s *Service) AcknowledgeSecurityAlert(ctx context.Context, tx *sql.Tx, id, by string) error {
	res, err := tx.ExecContext(ctx, `UPDATE security_alerts SET acknowledged_at = ?, acknowledged_by = ? WHERE id = ? AND acknowledged_at IS NULL`,
		s.nowMS(), by, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CheckPassword verifies a user's current password (subject to throttling)
// without creating a session.
func (s *Service) CheckPassword(ctx context.Context, userID, password string) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	key := "acct:" + u.Email
	if d := s.throttled(ctx, key); d > 0 {
		VerifyDummy(password)
		return &ThrottledError{RetryAfter: d}
	}
	_, hash, err := s.userByEmail(ctx, s.db.R, u.Email)
	if err != nil {
		return err
	}
	if ok, _ := VerifyPassword(hash, password); !ok {
		s.recordFailure(ctx, key)
		return ErrInvalidCredentials
	}
	return nil
}
