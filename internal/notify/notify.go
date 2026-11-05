// Package notify turns domain events into human notifications. It is a
// separate outbox consumer from event relays: rules filter events, route
// them to channels, group repeats, apply hysteresis, send recovery notices,
// honour quiet hours with digests, and deliver through Telegram, Slack,
// Discord or signed webhooks with retries, rate limits and dead letters.
//
// Notification bodies are built from the event's allowlisted fields only;
// prompts and secrets never appear. Notification failures do not emit
// events, so they cannot feed back into notifications.
package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/outbound"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/relay"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ratelimit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

const consumerName = "notify"

// Recovery actions close an active transition with the same dedupe key.
var recoveryActions = map[string]bool{"recovered": true, "closed": true, "reset": true, "restored": true, "resolved": true}

// Engine evaluates rules and delivers notifications.
type Engine struct {
	DB       *store.DB
	Clock    clock.Clock
	Log      *slog.Logger
	Snapshot func() *cp.Snapshot
	Secret   func(ctx context.Context, channelID string) (credentials.Secret, error)
	HTTP     *outbound.Client
	Wake     chan struct{}

	mu       sync.Mutex
	limiters map[string]*ratelimit.Limiter
}

// New creates the engine and installs template validation.
func New(db *store.DB, c clock.Clock, log *slog.Logger, snap func() *cp.Snapshot) *Engine {
	if c == nil {
		c = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	cp.ValidateTemplate = func(s string) error { _, err := parseTemplate(s); return err }
	return &Engine{DB: db, Clock: c, Log: log, Snapshot: snap, HTTP: outbound.New(), Wake: make(chan struct{}, 1), limiters: map[string]*ratelimit.Limiter{}}
}

// Run processes events and deliveries until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.Wake:
		}
		e.Round(ctx)
	}
}

// Round evaluates new events and delivers due notifications.
func (e *Engine) Round(ctx context.Context) {
	if err := e.evaluate(ctx); err != nil {
		e.Log.Warn("notify.evaluate_failed", "error", err.Error())
	}
	e.deliver(ctx)
}

// ---------- evaluation ----------

type envelope struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Severity   string            `json:"severity"`
	OccurredAt time.Time         `json:"occurred_at"`
	Subjects   map[string]string `json:"subjects"`
	Data       map[string]any    `json:"data"`
}

func (e *Engine) evaluate(ctx context.Context) error {
	snap := e.Snapshot()
	if snap == nil {
		return nil
	}
	now := e.Clock.Now()
	return e.DB.Tx(ctx, func(tx *sql.Tx) error {
		cur, err := relay.Cursor(ctx, tx, consumerName, now)
		if err != nil {
			return err
		}
		evs, err := relay.LoadEvents(ctx, tx, cur, 500)
		if err != nil || len(evs) == 0 {
			return err
		}
		ruleIDs := make([]string, 0, len(snap.Rules))
		for id := range snap.Rules {
			ruleIDs = append(ruleIDs, id)
		}
		sort.Strings(ruleIDs)
		for _, ev := range evs {
			var env envelope
			_ = json.Unmarshal(ev.Payload, &env)
			action := ev.Name[strings.LastIndex(ev.Name, ".")+1:]
			for _, rid := range ruleIDs {
				r := snap.Rules[rid]
				if !r.Spec.Enabled {
					continue
				}
				recovery := recoveryActions[action] && r.Spec.Recovery
				if !recovery && !r.Spec.Filter.Matches(ev.Name, ev.Severity, ev.Subjects) {
					continue
				}
				for _, chID := range r.Spec.ChannelIDs {
					ch := snap.Channels[chID]
					if ch == nil || !ch.Spec.Enabled {
						continue
					}
					if err := e.consider(ctx, tx, r, ch, ev, env, recovery, now); err != nil {
						return err
					}
				}
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE event_cursors SET last_seq = ?, updated_at = ? WHERE consumer = ?`, evs[len(evs)-1].Seq, now.UnixMilli(), consumerName)
		return err
	})
}

// consider applies hysteresis, grouping and recovery for one rule/channel.
func (e *Engine) consider(ctx context.Context, tx *sql.Tx, r *cp.Res[cp.NotificationRuleSpec], ch *cp.Res[cp.ChannelSpec], ev relay.Event, env envelope, recovery bool, now time.Time) error {
	key := ev.DedupeKey
	if key == "" {
		key = ev.Name
	}
	var state string
	var last sql.NullInt64
	var suppressed int
	err := tx.QueryRowContext(ctx, `SELECT state, last_sent_at, suppressed FROM notification_state WHERE rule_id = ? AND channel_id = ? AND dedupe_key = ?`,
		r.ID, ch.ID, key).Scan(&state, &last, &suppressed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	kind := "alert"
	if recovery {
		if state != "active" {
			return nil // nothing was notified, so nothing recovers
		}
		kind = "recovery"
	} else if state == "active" && last.Valid {
		since := now.Sub(time.UnixMilli(last.Int64))
		if since < time.Duration(r.Spec.CooldownSeconds)*time.Second || since < time.Duration(r.Spec.GroupSeconds)*time.Second {
			_, err := tx.ExecContext(ctx, `UPDATE notification_state SET suppressed = suppressed + 1, updated_at = ? WHERE rule_id = ? AND channel_id = ? AND dedupe_key = ?`,
				now.UnixMilli(), r.ID, ch.ID, key)
			return err
		}
	}
	title, body := render(r.Spec.Template, env, kind, suppressed)
	status := "pending"
	if inQuietHours(ch.Spec, now) && (!ch.Spec.QuietHours.CriticalBypass || ev.Severity != "critical") {
		status = "digest"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notifications(id, rule_id, channel_id, event_seq, event_id, kind, dedupe_key, severity, title, body, status,
		next_attempt_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, ids.NewAt(ids.Notification, now), r.ID, ch.ID, ev.Seq, ev.ID, kind, key, ev.Severity,
		title, body, status, now.UnixMilli(), now.UnixMilli()); err != nil {
		return err
	}
	newState := "active"
	if recovery {
		newState = "resolved"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_state(rule_id, channel_id, dedupe_key, state, last_sent_at, suppressed, updated_at) VALUES (?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(rule_id, channel_id, dedupe_key) DO UPDATE SET state = excluded.state, last_sent_at = excluded.last_sent_at, suppressed = 0, updated_at = excluded.updated_at`,
		r.ID, ch.ID, key, newState, now.UnixMilli(), now.UnixMilli())
	return err
}

func minutes(hhmm string) int {
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	return h*60 + m
}

func location(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return l
}

func inQuietHours(c cp.ChannelSpec, now time.Time) bool {
	q := c.QuietHours
	if !q.Enabled {
		return false
	}
	t := now.In(location(c.Timezone))
	m := t.Hour()*60 + t.Minute()
	s, e := minutes(q.Start), minutes(q.End)
	if s <= e {
		return m >= s && m < e
	}
	return m >= s || m < e // spans midnight
}

// ---------- templates ----------

const maxBody = 3500

func parseTemplate(s string) (*template.Template, error) {
	return template.New("n").Option("missingkey=zero").Funcs(template.FuncMap{"upper": strings.ToUpper}).Parse(s)
}

// render builds a title and body from allowlisted event fields.
func render(tmpl string, env envelope, kind string, suppressed int) (string, string) {
	title := humanize(env.Name)
	switch kind {
	case "recovery":
		title = "Recovered: " + title
	case "test":
		title = "Harbor Pilot test notification"
	}
	var body string
	if tmpl != "" {
		if t, err := parseTemplate(tmpl); err == nil {
			var b bytes.Buffer
			if err := t.Execute(&limitedWriter{w: &b, n: maxBody}, map[string]any{"Name": env.Name, "Severity": env.Severity, "Subjects": env.Subjects,
				"Data": env.Data, "OccurredAt": env.OccurredAt, "Kind": kind}); err == nil {
				body = strings.ReplaceAll(b.String(), "<no value>", "") // missing fields render empty
			}
		}
	}
	if body == "" {
		var lines []string
		keys := make([]string, 0, len(env.Data))
		for k := range env.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := env.Data[k]
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok {
				var b bytes.Buffer
				enc := json.NewEncoder(&b)
				enc.SetEscapeHTML(false)
				_ = enc.Encode(v)
				s = strings.TrimSpace(b.String())
			}
			if len(s) > 200 {
				s = s[:200] + "…"
			}
			lines = append(lines, k+": "+s)
		}
		subj := make([]string, 0, len(env.Subjects))
		for k, v := range env.Subjects {
			subj = append(subj, k+"="+v)
		}
		sort.Strings(subj)
		if len(subj) > 0 {
			lines = append(lines, "subjects: "+strings.Join(subj, ", "))
		}
		body = strings.Join(lines, "\n")
	}
	if suppressed > 0 {
		body += fmt.Sprintf("\n(+%d similar since the last notification)", suppressed)
	}
	if len(body) > maxBody {
		body = body[:maxBody] + "…"
	}
	return title, body
}

func humanize(name string) string {
	s := strings.ReplaceAll(name, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return "event"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type limitedWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		l.w.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// ---------- delivery ----------

type pending struct {
	id, channelID, kind, severity, title, body, eventID string
	attempts                                            int
	created                                             time.Time
}

const (
	maxAttempts = 10
	maxAge      = 24 * time.Hour
)

func (e *Engine) limiter(ch *cp.Res[cp.ChannelSpec]) *ratelimit.Limiter {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := ch.ID + fmt.Sprint(ch.Spec.RatePerMinute)
	l := e.limiters[k]
	if l == nil {
		l = ratelimit.New(ch.Spec.RatePerMinute, max(ch.Spec.RatePerMinute/4, 1), e.Clock.Now)
		e.limiters[k] = l
	}
	return l
}

func (e *Engine) deliver(ctx context.Context) {
	snap := e.Snapshot()
	if snap == nil {
		return
	}
	now := e.Clock.Now()
	e.flushDigests(ctx, snap, now)
	rows, err := store.Query(ctx, e.DB.R, func(r *sql.Rows) (pending, error) {
		var p pending
		var created int64
		var evID sql.NullString
		err := r.Scan(&p.id, &p.channelID, &p.kind, &p.severity, &p.title, &p.body, &p.attempts, &created, &evID)
		p.created, p.eventID = time.UnixMilli(created).UTC(), evID.String
		return p, err
	}, `SELECT id, channel_id, kind, severity, title, body, attempts, created_at, event_id FROM notifications
		WHERE (status = 'pending' AND next_attempt_at <= ?) OR (status = 'sending' AND lease_until < ?) ORDER BY created_at LIMIT 100`, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return
	}
	for _, p := range rows {
		ch := snap.Channels[p.channelID]
		if ch == nil || !ch.Spec.Enabled {
			e.setStatus(ctx, p.id, "dead", p.attempts, 0, "channel removed or disabled", now, 0)
			continue
		}
		if p.attempts >= maxAttempts {
			e.giveUp(ctx, p.id, fmt.Sprintf("gave up after %d attempts", p.attempts))
			continue
		}
		if now.Sub(p.created) > maxAge {
			e.giveUp(ctx, p.id, "gave up: too old to send")
			continue
		}
		if ok, wait := e.limiter(ch).Allow(ch.ID); !ok {
			e.setStatus(ctx, p.id, "pending", p.attempts, 0, "channel rate limit", now, wait)
			continue
		}
		claim, err := e.DB.W.ExecContext(ctx, `UPDATE notifications SET status = 'sending', lease_until = ? WHERE id = ?
			AND ((status = 'pending' AND next_attempt_at <= ?) OR (status = 'sending' AND lease_until < ?))`, now.Add(2*time.Minute).UnixMilli(), p.id, now.UnixMilli(), now.UnixMilli())
		if err != nil {
			continue
		}
		if n, _ := claim.RowsAffected(); n == 0 {
			continue
		}
		res := e.send(ctx, ch, p)
		switch {
		case res.OK:
			e.setStatus(ctx, p.id, "sent", p.attempts+1, res.Status, "", now, 0)
		case res.Retryable:
			wait := relay.Backoff(p.attempts + 1)
			if res.RetryAfter > wait {
				wait = min(res.RetryAfter, time.Hour)
			}
			e.setStatus(ctx, p.id, "pending", p.attempts+1, res.Status, res.Err, now, wait)
		default:
			e.setStatus(ctx, p.id, "dead", p.attempts+1, res.Status, res.Err, now, 0)
		}
	}
}

// credentialProblem explains why a channel's credential could not be used,
// naming the reference (never a value) when one is configured.
func credentialProblem(spec cp.ChannelSpec) string {
	if spec.SecretRef != "" {
		return "secret reference " + spec.SecretRef + " could not be read on the server; check that it is set"
	}
	return "no credential is saved for this channel; edit the channel and add it"
}

// giveUp marks a notification failed for good, keeping the last attempt's
// status and error so the cause stays visible.
func (e *Engine) giveUp(ctx context.Context, id, reason string) {
	_, _ = e.DB.W.ExecContext(ctx, `UPDATE notifications SET status = 'dead',
		last_error = CASE WHEN last_error IS NULL OR last_error = '' THEN ? ELSE last_error || ' (' || ? || ')' END WHERE id = ?`,
		reason, reason, id)
}

func (e *Engine) setStatus(ctx context.Context, id, status string, attempts, code int, errText string, now time.Time, wait time.Duration) {
	var sent any
	if status == "sent" {
		sent = now.UnixMilli()
	}
	var codeV, errV any
	if code != 0 {
		codeV = code
	}
	if errText != "" {
		errV = errText
	}
	_, _ = e.DB.W.ExecContext(ctx, `UPDATE notifications SET status = ?, attempts = ?, last_status = ?, last_error = ?, sent_at = COALESCE(?, sent_at), next_attempt_at = ? WHERE id = ?`,
		status, attempts, codeV, errV, sent, now.Add(wait).UnixMilli(), id)
}

// flushDigests combines deferred notifications into one digest per channel
// once quiet hours end (or every DigestMinutes outside quiet hours).
func (e *Engine) flushDigests(ctx context.Context, snap *cp.Snapshot, now time.Time) {
	for _, ch := range snap.Channels {
		if inQuietHours(ch.Spec, now) {
			continue
		}
		items, err := store.Query(ctx, e.DB.R, func(r *sql.Rows) (pending, error) {
			var p pending
			err := r.Scan(&p.id, &p.title, &p.severity)
			return p, err
		}, `SELECT id, title, severity FROM notifications WHERE channel_id = ? AND status = 'digest' ORDER BY created_at LIMIT 200`, ch.ID)
		if err != nil || len(items) == 0 {
			continue
		}
		var lines []string
		for _, it := range items {
			lines = append(lines, "• ["+it.severity+"] "+it.title)
		}
		body := strings.Join(lines, "\n")
		if len(body) > maxBody {
			body = body[:maxBody] + "…"
		}
		_ = e.DB.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO notifications(id, channel_id, kind, severity, title, body, status, next_attempt_at, created_at)
				VALUES (?, ?, 'digest', 'info', ?, ?, 'pending', ?, ?)`, ids.NewAt(ids.Notification, now), ch.ID, fmt.Sprintf("Digest: %d notifications during quiet hours", len(items)),
				body, now.UnixMilli(), now.UnixMilli()); err != nil {
				return err
			}
			for _, it := range items {
				if _, err := tx.ExecContext(ctx, `UPDATE notifications SET status = 'digested' WHERE id = ?`, it.id); err != nil {
					return err
				}
			}
			return nil
		})
	}
}

// ---------- adapters ----------

func telegramEscape(s string) string { return html.EscapeString(s) }

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func discordEscape(s string) string {
	return strings.NewReplacer("@", "@\u200b", "`", "'").Replace(s)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (e *Engine) send(ctx context.Context, ch *cp.Res[cp.ChannelSpec], p pending) outbound.Result {
	secret, err := e.Secret(ctx, ch.ID)
	if err != nil && ch.Spec.Type != "webhook" {
		return outbound.Result{Err: credentialProblem(ch.Spec)}
	}
	text := p.title + "\n" + p.body
	switch ch.Spec.Type {
	case "telegram":
		base := strings.TrimRight(ch.Spec.APIBaseURL, "/")
		if base == "" {
			base = "https://api.telegram.org"
		}
		msg := "<b>" + telegramEscape(p.title) + "</b>\n" + telegramEscape(p.body)
		body := outbound.JSON(map[string]any{"chat_id": ch.Spec.TelegramChatID, "text": truncateRunes(msg, 4000), "parse_mode": "HTML", "disable_web_page_preview": true})
		res := e.HTTP.Post(ctx, base+"/bot"+secret.Reveal()+"/sendMessage", body, nil, ch.Spec.AllowPrivateNetwork, 10*time.Second)
		if res.Status == 429 {
			var tr struct {
				Parameters struct {
					RetryAfter int `json:"retry_after"`
				} `json:"parameters"`
			}
			if json.Unmarshal(res.Body, &tr) == nil && tr.Parameters.RetryAfter > 0 {
				res.RetryAfter = time.Duration(tr.Parameters.RetryAfter) * time.Second
			}
		}
		res.Body = nil
		return res
	case "slack":
		body := outbound.JSON(map[string]any{"text": truncateRunes("*"+slackEscape(p.title)+"*\n"+slackEscape(p.body), 3000)})
		return e.HTTP.Post(ctx, secret.Reveal(), body, nil, ch.Spec.AllowPrivateNetwork, 10*time.Second)
	case "discord":
		body := outbound.JSON(map[string]any{"content": truncateRunes("**"+discordEscape(p.title)+"**\n"+discordEscape(p.body), 2000),
			"allowed_mentions": map[string]any{"parse": []string{}}})
		res := e.HTTP.Post(ctx, secret.Reveal(), body, nil, ch.Spec.AllowPrivateNetwork, 10*time.Second)
		if res.Status == 429 {
			var dr struct {
				RetryAfter float64 `json:"retry_after"`
			}
			if json.Unmarshal(res.Body, &dr) == nil && dr.RetryAfter > 0 {
				res.RetryAfter = time.Duration(dr.RetryAfter * float64(time.Second))
			}
		}
		return res
	case "webhook":
		body := outbound.JSON(map[string]any{"notification_id": p.id, "kind": p.kind, "severity": p.severity, "title": p.title, "body": p.body,
			"event_id": p.eventID, "text": text})
		h := http.Header{}
		h.Set("X-Harbor-Notification-Id", p.id)
		h.Set("X-Harbor-Signature", outbound.Sign(body, e.Clock.Now(), secret.Reveal()))
		return e.HTTP.Post(ctx, ch.Spec.WebhookURL, body, h, ch.Spec.AllowPrivateNetwork, 10*time.Second)
	}
	return outbound.Result{Err: "unknown channel type"}
}

// Test sends a synthetic test notification immediately and records it.
func (e *Engine) Test(ctx context.Context, channelID string) (outbound.Result, error) {
	snap := e.Snapshot()
	ch := snap.Channels[channelID]
	if ch == nil {
		return outbound.Result{}, errors.New("unknown channel")
	}
	now := e.Clock.Now()
	title, body := render("", envelope{Name: "notification.channel.test", Severity: "info", Data: map[string]any{"channel": ch.Name, "sent_at": now.Format(time.RFC3339)}}, "test", 0)
	id := ids.NewAt(ids.Notification, now)
	res := e.send(ctx, ch, pending{id: id, channelID: ch.ID, kind: "test", severity: "info", title: title, body: body})
	status := "sent"
	if !res.OK {
		status = "dead"
	}
	var errV any
	if res.Err != "" {
		errV = res.Err
	}
	_, _ = e.DB.W.ExecContext(ctx, `INSERT INTO notifications(id, channel_id, kind, severity, title, body, status, attempts, next_attempt_at, last_status, last_error, sent_at, created_at)
		VALUES (?, ?, 'test', 'info', ?, ?, ?, 1, ?, ?, ?, ?, ?)`, id, ch.ID, title, body, status, now.UnixMilli(), nonZero(res.Status), errV, now.UnixMilli(), now.UnixMilli())
	return res, nil
}

func nonZero(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// Notification is a notification row.
type Notification struct {
	ID        string     `json:"id"`
	RuleID    string     `json:"rule_id"`
	ChannelID string     `json:"channel_id"`
	EventID   string     `json:"event_id"`
	Kind      string     `json:"kind" enum:"alert,recovery,digest,test"`
	Severity  string     `json:"severity"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Status    string     `json:"status" enum:"pending,digest,sent,digested,dead"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty" required:"false"`
	SentAt    *time.Time `json:"sent_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// List returns notifications, newest first. beforeID ("" = none)
// continues a previous page.
func (e *Engine) List(ctx context.Context, channelID, status, beforeID string, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, COALESCE(rule_id,''), channel_id, COALESCE(event_id,''), kind, severity, title, body, status, attempts, COALESCE(last_error,''), sent_at, created_at
		FROM notifications WHERE 1=1`
	var args []any
	if channelID != "" {
		q += ` AND channel_id = ?`
		args = append(args, channelID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if beforeID != "" {
		q += ` AND id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	return store.Query(ctx, e.DB.R, func(r *sql.Rows) (Notification, error) {
		var n Notification
		var sent sql.NullInt64
		var created int64
		err := r.Scan(&n.ID, &n.RuleID, &n.ChannelID, &n.EventID, &n.Kind, &n.Severity, &n.Title, &n.Body, &n.Status, &n.Attempts, &n.LastError, &sent, &created)
		if sent.Valid {
			t := time.UnixMilli(sent.Int64).UTC()
			n.SentAt = &t
		}
		n.CreatedAt = time.UnixMilli(created).UTC()
		return n, err
	}, q, args...)
}

// ErrTestNotReplayable is returned when replaying a test notification:
// tests are one-shot, so send a new one instead.
var ErrTestNotReplayable = errors.New("test notifications are not replayed; send a new test")

// Replay re-queues a dead notification.
func (e *Engine) Replay(ctx context.Context, id string) (bool, error) {
	var kind string
	if err := e.DB.R.QueryRowContext(ctx, `SELECT kind FROM notifications WHERE id = ?`, id).Scan(&kind); err == nil && kind == "test" {
		return false, ErrTestNotReplayable
	}
	res, err := e.DB.W.ExecContext(ctx, `UPDATE notifications SET status = 'pending', attempts = 0, last_error = NULL, next_attempt_at = ?, created_at = ?
		WHERE id = ? AND status = 'dead' AND kind != 'test'`, e.Clock.Now().UnixMilli(), e.Clock.Now().UnixMilli(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	select {
	case e.Wake <- struct{}{}:
	default:
	}
	return n == 1, nil
}
