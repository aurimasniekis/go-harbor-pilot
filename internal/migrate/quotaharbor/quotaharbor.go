// Package quotaharbor imports a Quota Harbor deployment: its monitored
// accounts become quota monitors (same adapter ids and options), its
// Telegram notification settings become a notification channel and rule,
// and its observation history seeds quota history so forecasts work from
// day one. Items that have no equivalent are reported, never silently
// dropped. Secrets are carried as references (env:/file:) only.
package quotaharbor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"

	_ "modernc.org/sqlite" // read the source database
)

// SecretRef is a Quota Harbor secret reference.
type SecretRef struct {
	Type string `yaml:"type"`
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	Key  string `yaml:"key"`
}

// Credentials accepts a single reference (api_key) or a map.
type Credentials map[string]SecretRef

// UnmarshalYAML implements yaml.Unmarshaler.
func (c *Credentials) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return errors.New("credentials must be a mapping")
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == "type" {
			var r SecretRef
			if err := n.Decode(&r); err != nil {
				return err
			}
			*c = Credentials{"api_key": r}
			return nil
		}
	}
	m := map[string]SecretRef{}
	if err := n.Decode(&m); err != nil {
		return err
	}
	*c = m
	return nil
}

// Account is a monitored account.
type Account struct {
	ID           string         `yaml:"id"`
	Provider     string         `yaml:"provider"`
	Alias        string         `yaml:"alias"`
	Enabled      *bool          `yaml:"enabled"`
	Tags         []string       `yaml:"tags"`
	SharedScope  string         `yaml:"shared_scope"`
	PollInterval string         `yaml:"poll_interval"`
	Options      map[string]any `yaml:"options"`
	Credentials  Credentials    `yaml:"credentials"`
	Unofficial   struct {
		AcknowledgeTermsRisk bool `yaml:"acknowledge_terms_risk"`
	} `yaml:"unofficial"`
}

// Config is the subset of Quota Harbor configuration that is imported.
type Config struct {
	Include  []string         `yaml:"include"`
	Accounts []Account        `yaml:"accounts"`
	Pricing  []map[string]any `yaml:"pricing"`
	Budgets  []map[string]any `yaml:"budgets"`
	Alerts   struct {
		Rules []map[string]any `yaml:"rules"`
	} `yaml:"alerts"`
	Notifications struct {
		Telegram struct {
			Enabled    bool      `yaml:"enabled"`
			BotToken   SecretRef `yaml:"bot_token"`
			ChatID     string    `yaml:"chat_id"`
			APIBaseURL string    `yaml:"api_base_url"`
			Severities []string  `yaml:"severities"`
			QuietHours struct {
				Start          string `yaml:"start"`
				End            string `yaml:"end"`
				Timezone       string `yaml:"timezone"`
				CriticalBypass *bool  `yaml:"critical_bypass"`
			} `yaml:"quiet_hours"`
			Cooldown       string `yaml:"cooldown"`
			SendRecoveries *bool  `yaml:"send_recoveries"`
		} `yaml:"telegram"`
	} `yaml:"notifications"`
}

// Load reads a config file and its includes (globs relative to it).
func Load(path string) (*Config, error) {
	var root Config
	if err := readYAML(path, &root); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	for _, pat := range root.Include {
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(dir, pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			return nil, fmt.Errorf("include %q: %w", pat, err)
		}
		sort.Strings(matches)
		for _, m := range matches {
			var inc Config
			if err := readYAML(m, &inc); err != nil {
				return nil, err
			}
			root.Accounts = append(root.Accounts, inc.Accounts...)
			root.Pricing = append(root.Pricing, inc.Pricing...)
			root.Budgets = append(root.Budgets, inc.Budgets...)
			root.Alerts.Rules = append(root.Alerts.Rules, inc.Alerts.Rules...)
		}
	}
	return &root, nil
}

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path) //nolint:gosec // operator-chosen source config
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// Item is one planned import step.
type Item struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action" enum:"create,exists,skip"`
	Reason string `json:"reason,omitempty"`
	// SourceID is the Quota Harbor account id (history import key).
	SourceID string `json:"source_id,omitempty"`
	spec     any
	def      cp.Kind
	existing string // id of the monitor that already has this name
}

// Plan is a reviewable import plan.
type Plan struct {
	Items []Item `json:"items"`
}

var nameRE = regexp.MustCompile(`[^a-z0-9._-]+`)

func slug(s string) string {
	s = nameRE.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		s = "imported"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func refString(r SecretRef) (string, bool) {
	switch {
	case r.Type == "env" && r.Name != "":
		return "env:" + r.Name, true
	case r.Type == "file" && r.Path != "" && r.Key == "":
		return "file:" + r.Path, true
	}
	return "", false
}

// importRef converts a reference and checks it against the reference
// policy; problem explains what the operator must do when it cannot be
// carried over.
func importRef(r SecretRef) (ref, problem string) {
	s, ok := refString(r)
	if !ok {
		return "", fmt.Sprintf("uses an unsupported %q reference", r.Type)
	}
	if err := credentials.CheckRef(s); err != nil {
		if r.Type == "env" {
			return "", fmt.Sprintf("reads %s, which is not an allowed secret variable; rename it to HP_SECRET_%s (or another configured prefix) and set the reference", r.Name, r.Name)
		}
		return "", "points at the data directory or a master key file"
	}
	return s, ""
}

// Build plans the import against the current configuration.
func Build(c *Config, snap *cp.Snapshot) *Plan {
	p := &Plan{}
	existing := map[string]string{}
	for id, m := range snap.Monitors {
		existing[m.Name] = id
	}
	for _, a := range c.Accounts {
		name := slug(a.ID)
		if id := existing[name]; id != "" {
			p.Items = append(p.Items, Item{Kind: cp.KindQuotaMonitor, Name: name, Action: "exists", SourceID: a.ID, existing: id,
				Reason: "a monitor with this name already exists; its configuration is kept and history is added to it"})
			continue
		}
		spec := cp.QuotaMonitorSpec{Adapter: a.Provider, Options: a.Options, Enabled: a.Enabled == nil || *a.Enabled,
			AcknowledgeTermsRisk: a.Unofficial.AcknowledgeTermsRisk, SharedScopeID: slug(a.SharedScope)}
		if a.SharedScope == "" {
			spec.SharedScopeID = ""
		}
		if d, err := time.ParseDuration(a.PollInterval); err == nil && d > 0 {
			spec.PollIntervalSecs = int(d.Seconds())
		}
		var notes []string
		for cname, ref := range a.Credentials {
			r, problem := importRef(ref)
			if problem != "" {
				notes = append(notes, fmt.Sprintf("credential %s %s; set it on the monitor after import", cname, problem))
				continue
			}
			switch cname {
			case "api_key":
				spec.APIKeyRef = r
			case "management_key":
				spec.ManagementKeyRef = r
			case "access_token":
				spec.AccessTokenRef = r
			case "service_account":
				spec.ServiceAccountRef = r
			default:
				notes = append(notes, "credential "+cname+" is not supported by quota monitors")
			}
		}
		p.Items = append(p.Items, Item{Kind: cp.KindQuotaMonitor, Name: name, Action: "create", SourceID: a.ID, Reason: strings.Join(notes, "; "),
			spec: spec, def: cp.QuotaMonitorDef})
	}
	tg := c.Notifications.Telegram
	channelExists, ruleExists := "", false
	for id, ch := range snap.Channels {
		if ch.Name == "telegram" {
			channelExists = id
		}
	}
	for _, r := range snap.Rules {
		ruleExists = ruleExists || r.Name == "telegram-quota-alerts"
	}
	switch {
	case tg.Enabled && channelExists != "":
		p.Items = append(p.Items, Item{Kind: cp.KindChannel, Name: "telegram", Action: "exists", existing: channelExists, Reason: "a channel with this name already exists"})
	case tg.Enabled:
		ref, problem := importRef(tg.BotToken)
		ch := cp.ChannelSpec{Type: "telegram", Enabled: true, TelegramChatID: tg.ChatID, APIBaseURL: tg.APIBaseURL, Timezone: tg.QuietHours.Timezone, SecretRef: ref}
		if tg.QuietHours.Start != "" && tg.QuietHours.End != "" {
			ch.QuietHours = cp.QuietHours{Enabled: true, Start: tg.QuietHours.Start, End: tg.QuietHours.End,
				CriticalBypass: tg.QuietHours.CriticalBypass == nil || *tg.QuietHours.CriticalBypass}
		}
		reason := ""
		if problem != "" {
			reason = "the bot token " + problem + "; set the channel secret after import"
		}
		p.Items = append(p.Items, Item{Kind: cp.KindChannel, Name: "telegram", Action: "create", Reason: reason, spec: ch, def: cp.ChannelDef})
	}
	if tg.Enabled && ruleExists {
		p.Items = append(p.Items, Item{Kind: cp.KindNotificationRule, Name: "telegram-quota-alerts", Action: "exists", Reason: "a rule with this name already exists"})
	} else if tg.Enabled {
		rule := cp.NotificationRuleSpec{Enabled: true, Recovery: tg.SendRecoveries == nil || *tg.SendRecoveries,
			Filter: cp.EventFilter{Events: []string{"quota.*", "budget.*", "provider.account.*", "compat.breaker.*"}, MinSeverity: "warning"}}
		if d, err := time.ParseDuration(tg.Cooldown); err == nil && d > 0 {
			rule.CooldownSeconds = int(d.Seconds())
		}
		p.Items = append(p.Items, Item{Kind: cp.KindNotificationRule, Name: "telegram-quota-alerts", Action: "create", spec: rule, def: cp.RuleDef,
			Reason: "routes quota, budget, account health and compatibility events to the imported Telegram channel"})
	}
	if len(c.Alerts.Rules) > 0 {
		p.Items = append(p.Items, Item{Kind: "alert_rule", Action: "skip", Reason: fmt.Sprintf("%d alert rules: thresholds are replaced by events and notification rules; recreate custom ones as notification rules", len(c.Alerts.Rules))})
	}
	if len(c.Pricing) > 0 {
		p.Items = append(p.Items, Item{Kind: "pricing", Action: "skip", Reason: fmt.Sprintf("%d price entries: Harbor Pilot keeps prices on provider models; add them there", len(c.Pricing))})
	}
	if len(c.Budgets) > 0 {
		p.Items = append(p.Items, Item{Kind: "budget", Action: "skip", Reason: fmt.Sprintf("%d budgets: monitored-account budgets do not map to gateway spend; recreate them as budgets on clients, projects or accounts", len(c.Budgets))})
	}
	// Check specs now so a dry run shows what would be rejected. Rules are
	// checked at apply time, once their channel exists.
	for i := range p.Items {
		it := &p.Items[i]
		if it.Action != "create" || it.Kind == cp.KindNotificationRule {
			continue
		}
		raw, _ := json.Marshal(it.spec)
		v := cp.NewValidator("", nil)
		it.def.Canonicalize(raw, v)
		if !v.OK() {
			msgs := make([]string, 0, len(v.Errors))
			for _, e := range v.Errors {
				msgs = append(msgs, strings.TrimPrefix(e.Location, "body.")+": "+e.Message)
			}
			it.Action, it.Reason = "skip", "invalid: "+strings.Join(msgs, "; ")
		}
	}
	return p
}

// Apply creates the planned resources (managed_by=import) and returns
// source account id → monitor id (new or already existing).
func Apply(ctx context.Context, svc *cp.Service, p *Plan) (map[string]string, error) {
	monitors := map[string]string{}
	channelID := ""
	for i := range p.Items {
		it := &p.Items[i]
		if it.Action == "exists" && it.existing != "" {
			switch it.Kind {
			case cp.KindQuotaMonitor:
				monitors[it.SourceID] = it.existing
			case cp.KindChannel:
				channelID = it.existing
			}
		}
		if it.Action != "create" {
			continue
		}
		if it.Kind == cp.KindNotificationRule {
			if channelID == "" {
				it.Action, it.Reason = "skip", "no channel was created"
				continue
			}
			rs := it.spec.(cp.NotificationRuleSpec)
			rs.ChannelIDs = []string{channelID}
			it.spec = rs
		}
		raw, _ := json.Marshal(it.spec)
		o, err := svc.Create(ctx, it.def, it.Name, raw, nil, cp.Mutation{ManagedBy: cp.ManagedImport})
		if err != nil {
			it.Action, it.Reason = "skip", "rejected: "+err.Error()
			continue
		}
		switch it.Kind {
		case cp.KindQuotaMonitor:
			monitors[it.SourceID] = o.ID
		case cp.KindChannel:
			channelID = o.ID
		}
	}
	return monitors, nil
}

// ImportHistory copies bucket observations newer than since from a Quota
// Harbor database into quota history for the mapped monitors; the newest
// observation of each bucket becomes its current state. It returns the
// number of observations imported; ones already present are skipped, so a
// re-run is safe.
func ImportHistory(ctx context.Context, dst *store.DB, srcPath string, monitors map[string]string, since time.Time) (int64, error) {
	src, err := sql.Open("sqlite", "file:"+srcPath+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer src.Close()
	rows, err := src.QueryContext(ctx, `SELECT b.account_id, b.key, b.label, b.scope, b.kind, b.window_seconds, b.unit, COALESCE(b.currency, ''),
		COALESCE(b.model, ''), COALESCE(b.model_family, ''), COALESCE(b.shared_scope_id, ''), b.reset_semantics, b.period_seconds, b.global, b.official,
		o.observed_at, o.limit_value, o.used_value, o.remaining_value, o.limit_e10, o.used_e10, o.remaining_e10, o.reset_at, o.source_time, o.quality
		FROM bucket_observations o JOIN quota_buckets b ON b.id = o.bucket_id WHERE o.observed_at >= ? ORDER BY o.observed_at, o.id`, since.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("read source history: %w", err)
	}
	defer rows.Close()
	ptrF := func(v sql.NullFloat64) *float64 {
		if !v.Valid {
			return nil
		}
		return &v.Float64
	}
	ptrA := func(v sql.NullInt64) *money.Amount {
		if !v.Valid {
			return nil
		}
		a := money.Amount(v.Int64)
		return &a
	}
	ptrT := func(v sql.NullInt64) *time.Time {
		if !v.Valid {
			return nil
		}
		t := time.UnixMilli(v.Int64).UTC()
		return &t
	}
	var n int64
	err = dst.Tx(ctx, func(tx *sql.Tx) error {
		for rows.Next() {
			var acct, quality string
			var spec domain.BucketSpec
			var win, period sql.NullInt64
			var global, official int
			var at int64
			var lim, used, rem sql.NullFloat64
			var limE, usedE, remE, resetAt, srcT sql.NullInt64
			if err := rows.Scan(&acct, &spec.Key, &spec.Label, &spec.Scope, &spec.Kind, &win, &spec.Unit, &spec.Currency, &spec.Model, &spec.ModelFamily,
				&spec.SharedScopeID, &spec.ResetSemantics, &period, &global, &official, &at, &lim, &used, &rem, &limE, &usedE, &remE, &resetAt, &srcT,
				&quality); err != nil {
				return err
			}
			mon := monitors[acct]
			if mon == "" {
				continue
			}
			spec.Window = time.Duration(win.Int64) * time.Second
			spec.Period = time.Duration(period.Int64) * time.Second
			spec.Global, spec.Official = global == 1, official == 1
			obs := domain.BucketObservation{Spec: spec, Limit: ptrF(lim), Used: ptrF(used), Remaining: ptrF(rem), LimitAmount: ptrA(limE),
				UsedAmount: ptrA(usedE), RemainingAmount: ptrA(remE), ResetAt: ptrT(resetAt), SourceTime: ptrT(srcT), Quality: domain.Quality(quality)}
			added, err := quota.ImportObservation(ctx, tx, mon, obs, time.UnixMilli(at).UTC())
			if err != nil {
				return err
			}
			if added {
				n++
			}
		}
		return rows.Err()
	})
	return n, err
}
