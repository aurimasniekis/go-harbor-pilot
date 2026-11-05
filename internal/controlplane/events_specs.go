package controlplane

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
)

// Kinds for event delivery and notifications.
const (
	KindDestination      = "event_destination"
	KindChannel          = "notification_channel"
	KindNotificationRule = "notification_rule"
)

// Severities usable in filters.
var Severities = []string{"info", "warning", "critical"}

// EventFilter selects events. Patterns are area.entity.action globs.
type EventFilter struct {
	Events      []string `json:"events" required:"false" doc:"Event-name globs such as quota.* or routing.account.switched; empty = all."`
	MinSeverity string   `json:"min_severity" required:"false" enum:"info,warning,critical,"`
	// Subjects narrows to events about these identities (e.g. provider_id).
	Subjects map[string]string `json:"subjects" required:"false"`
}

func normalizeFilter(f *EventFilter) {
	f.Events = SortedUnique(f.Events)
	f.Subjects = EmptyMap(f.Subjects)
	if f.MinSeverity == "" {
		f.MinSeverity = "info"
	}
}

var eventGlobRE = regexp.MustCompile(`^[a-z0-9_*-]+(\.[a-z0-9_*-]+){0,2}$`)

func validateFilter(v *Validator, loc string, f EventFilter) {
	for _, e := range f.Events {
		v.Check(eventGlobRE.MatchString(e), loc+".events", "invalid event pattern "+e)
	}
	v.Check(slices.Contains(Severities, f.MinSeverity), loc+".min_severity", "must be info, warning or critical")
}

// Matches reports whether an event passes the filter.
func (f EventFilter) Matches(name, severity string, subjects map[string]string) bool {
	if slices.Index(Severities, severity) < slices.Index(Severities, f.MinSeverity) {
		return false
	}
	for k, want := range f.Subjects {
		if subjects[k] != want {
			return false
		}
	}
	if len(f.Events) == 0 {
		return true
	}
	for _, p := range f.Events {
		if ok, _ := path.Match(strings.ReplaceAll(p, ".", "/"), strings.ReplaceAll(name, ".", "/")); ok {
			return true
		}
		if strings.HasSuffix(p, ".*") && strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

// DestinationSpec relays events to an external system.
type DestinationSpec struct {
	Type    string      `json:"type" enum:"pulse,webhook"`
	Enabled bool        `json:"enabled"`
	Filter  EventFilter `json:"filter" required:"false"`
	// Pulse (NATS JetStream).
	PulseURL    string `json:"pulse_url" required:"false" doc:"nats:// or tls:// URL of the Pulse server."`
	PulseSource string `json:"pulse_source" required:"false" doc:"ce-source value; default harbor-pilot."`
	CredsRef    string `json:"creds_ref" required:"false" doc:"file:/path to the Pulse .creds file (alternative to the write-only creds secret)."`
	CARef       string `json:"ca_ref" required:"false" doc:"file:/path to a CA bundle for TLS."`
	// Webhook.
	WebhookURL          string            `json:"webhook_url" required:"false"`
	AllowPrivateNetwork bool              `json:"allow_private_network" required:"false" doc:"Permit private or loopback addresses (metadata endpoints stay blocked)."`
	TimeoutSeconds      int               `json:"timeout_seconds" required:"false" minimum:"0" maximum:"60"`
	Headers             map[string]string `json:"headers" required:"false" doc:"Extra non-secret headers."`
	// Retry policy.
	MaxAttempts int `json:"max_attempts" required:"false" minimum:"0" maximum:"1000" doc:"Dead-letter after this many attempts (default 50)."`
	MaxAgeHours int `json:"max_age_hours" required:"false" minimum:"0" maximum:"720" doc:"Dead-letter events older than this (default 24)."`
}

func normalizeDestination(s *DestinationSpec) {
	normalizeFilter(&s.Filter)
	s.Headers = EmptyMap(s.Headers)
	if s.MaxAttempts == 0 {
		s.MaxAttempts = 50
	}
	if s.MaxAgeHours == 0 {
		s.MaxAgeHours = 24
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = 10
	}
	if s.Type == "pulse" && s.PulseSource == "" {
		s.PulseSource = "harbor-pilot"
	}
}

func validateDestination(v *Validator, s *DestinationSpec) {
	validateFilter(v, "spec.filter", s.Filter)
	switch s.Type {
	case "pulse":
		u, err := url.Parse(s.PulseURL)
		v.Check(err == nil && (u.Scheme == "nats" || u.Scheme == "tls") && u.Host != "", "spec.pulse_url", "must be a nats:// or tls:// URL")
		for loc, ref := range map[string]string{"spec.creds_ref": s.CredsRef, "spec.ca_ref": s.CARef} {
			if ref != "" {
				v.Check(strings.HasPrefix(ref, "file:") && len(ref) > 5, loc, "must be file:/path")
			}
		}
	case "webhook":
		checkHTTPURL(v, "spec.webhook_url", s.WebhookURL, s.AllowPrivateNetwork)
		for k := range s.Headers {
			v.Check(!strings.ContainsAny(k, " :\r\n") && k != "", "spec.headers", "invalid header name")
			lk := strings.ToLower(k)
			v.Check(lk != "authorization" && !strings.HasPrefix(lk, "x-harbor-"), "spec.headers", "put credentials in the signing secret, not headers")
		}
	default:
		v.Fail("spec.type", "must be pulse or webhook")
	}
}

func checkHTTPURL(v *Validator, loc, raw string, private bool) {
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "":
		v.Fail(loc, "must be an absolute URL")
	case u.User != nil:
		v.Fail(loc, "must not embed credentials")
	case u.Scheme == "http" && !private:
		v.Fail(loc, "must use https (http needs allow_private_network)")
	case u.Scheme != "https" && u.Scheme != "http":
		v.Fail(loc, "must use https")
	}
}

// QuietHours defers non-critical notifications to a digest.
type QuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start" required:"false" doc:"HH:MM"`
	End     string `json:"end" required:"false" doc:"HH:MM"`
	// CriticalBypass sends critical notifications even in quiet hours.
	CriticalBypass bool `json:"critical_bypass" required:"false"`
}

var hhmmRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ChannelSpec is a notification destination.
type ChannelSpec struct {
	Type     string `json:"type" enum:"telegram,slack,discord,webhook"`
	Enabled  bool   `json:"enabled"`
	Timezone string `json:"timezone" required:"false" doc:"IANA timezone for quiet hours and digests; empty = UTC."`
	// Telegram chat id (the bot token is the write-only secret).
	TelegramChatID string `json:"telegram_chat_id" required:"false"`
	// APIBaseURL overrides the provider API base (self-hosted relays, tests).
	APIBaseURL          string     `json:"api_base_url" required:"false"`
	WebhookURL          string     `json:"webhook_url" required:"false" doc:"Generic webhook URL (Slack and Discord URLs are secrets)."`
	AllowPrivateNetwork bool       `json:"allow_private_network" required:"false"`
	QuietHours          QuietHours `json:"quiet_hours" required:"false"`
	// DigestMinutes batches deferred notifications (0 = send at quiet-hours end).
	DigestMinutes int `json:"digest_minutes" required:"false" minimum:"0" maximum:"1440"`
	// RatePerMinute caps sends to avoid storms (default 20).
	RatePerMinute int `json:"rate_per_minute" required:"false" minimum:"0" maximum:"600"`
	// SecretRef is an alternative to the write-only secret (env:/file:).
	SecretRef string `json:"secret_ref" required:"false"`
}

func normalizeChannel(s *ChannelSpec) {
	if s.RatePerMinute == 0 {
		s.RatePerMinute = 20
	}
}

func validateChannel(v *Validator, s *ChannelSpec) {
	if s.Timezone != "" {
		_, err := time.LoadLocation(s.Timezone)
		v.Check(err == nil, "spec.timezone", "unknown timezone")
	}
	if s.QuietHours.Enabled {
		v.Check(hhmmRE.MatchString(s.QuietHours.Start), "spec.quiet_hours.start", "must be HH:MM")
		v.Check(hhmmRE.MatchString(s.QuietHours.End), "spec.quiet_hours.end", "must be HH:MM")
	}
	if s.APIBaseURL != "" {
		checkHTTPURL(v, "spec.api_base_url", s.APIBaseURL, s.AllowPrivateNetwork)
	}
	switch s.Type {
	case "telegram":
		v.Check(s.TelegramChatID != "", "spec.telegram_chat_id", "is required")
	case "slack", "discord":
	case "webhook":
		checkHTTPURL(v, "spec.webhook_url", s.WebhookURL, s.AllowPrivateNetwork)
	default:
		v.Fail("spec.type", "must be telegram, slack, discord or webhook")
	}
	if s.SecretRef != "" {
		kind, val, ok := strings.Cut(s.SecretRef, ":")
		v.Check(ok && val != "" && (kind == "env" || kind == "file"), "spec.secret_ref", "must be env:NAME or file:/path")
	}
}

// NotificationRuleSpec routes events to channels.
type NotificationRuleSpec struct {
	Enabled    bool        `json:"enabled"`
	Filter     EventFilter `json:"filter"`
	ChannelIDs []string    `json:"channel_ids"`
	// GroupSeconds groups repeats of the same transition (dedupe key)
	// after the first notification; CooldownSeconds is the hysteresis
	// before the same transition notifies again.
	GroupSeconds    int  `json:"group_seconds" required:"false" minimum:"0" maximum:"86400"`
	CooldownSeconds int  `json:"cooldown_seconds" required:"false" minimum:"0" maximum:"604800"`
	Recovery        bool `json:"recovery" required:"false" doc:"Notify when a notified condition recovers (e.g. quota window recovered, breaker closed)."`
	// Template overrides the message (Go text/template over event fields;
	// output is escaped per channel and truncated).
	Template string `json:"template" required:"false" maxLength:"2000"`
}

func normalizeRule(s *NotificationRuleSpec) {
	normalizeFilter(&s.Filter)
	s.ChannelIDs = SortedUnique(s.ChannelIDs)
	if s.CooldownSeconds == 0 {
		s.CooldownSeconds = 900
	}
}

// ValidateTemplate is installed by the notification module.
var ValidateTemplate func(string) error

func validateRule(v *Validator, s *NotificationRuleSpec) {
	validateFilter(v, "spec.filter", s.Filter)
	v.Check(len(s.ChannelIDs) > 0, "spec.channel_ids", "at least one channel")
	for _, e := range s.Filter.Events {
		v.Check(!strings.HasPrefix(e, "notification.") && !strings.HasPrefix(e, "event."), "spec.filter.events",
			"notification and delivery events cannot trigger notifications (prevents failure loops)")
	}
	if s.Template != "" && ValidateTemplate != nil {
		if err := ValidateTemplate(s.Template); err != nil {
			v.Fail("spec.template", fmt.Sprintf("invalid template: %v", err))
		}
	}
}

var (
	DestinationDef = &Def[DestinationSpec]{Name: KindDestination, Plural: "event-destinations", Prefix: ids.Destination, Title: "Event destination",
		Secrets: []SecretField{
			{Name: "signing_secret", Description: "Webhook HMAC signing secret. Write-only.", RefField: ""},
			{Name: "previous_signing_secret", Description: "Previous webhook signing secret, also signed with during rotation. Write-only.", RefField: ""},
			{Name: "creds", Description: "Pulse .creds file content. Write-only.", RefField: ""},
		},
		Normalize: normalizeDestination, Validate: validateDestination}
	ChannelDef = &Def[ChannelSpec]{Name: KindChannel, Plural: "notification-channels", Prefix: ids.Channel, Title: "Notification channel",
		Secrets:   []SecretField{{Name: "secret", Description: "Telegram bot token, Slack or Discord webhook URL, or webhook signing secret. Write-only.", RefField: "secret_ref"}},
		Normalize: normalizeChannel, Validate: validateChannel}
	RuleDef = &Def[NotificationRuleSpec]{Name: KindNotificationRule, Plural: "notification-rules", Prefix: ids.NotificationRule, Title: "Notification rule",
		Normalize: normalizeRule, Validate: validateRule,
		Refs: func(s *NotificationRuleSpec) []Ref {
			out := make([]Ref, 0, len(s.ChannelIDs))
			for i, c := range s.ChannelIDs {
				out = append(out, Ref{fmt.Sprintf("spec.channel_ids[%d]", i), c, []string{KindChannel}})
			}
			return out
		}}
)
