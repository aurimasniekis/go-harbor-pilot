package events

// KindInfo documents one event kind.
type KindInfo struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Subject     string   `json:"subject"`
	Severity    string   `json:"severity"`
	Description string   `json:"description"`
	Subjects    []string `json:"subjects"`
	Data        []string `json:"data"`
	// Pair names the event that resolves this one (same dedupe key).
	Pair string `json:"pair,omitempty"`
}

func info(area, entity, action, sev, desc string, subjects, data []string, pair string) KindInfo {
	k := Kind{Area: area, Entity: entity, Action: action}
	return KindInfo{Name: k.Name(), Type: k.Type(), Subject: k.Subject(), Severity: sev, Description: desc, Subjects: subjects, Data: data, Pair: pair}
}

// Catalog lists every event kind Harbor Pilot emits. Events are
// transitions, never per-candidate evaluations. Data fields are
// allowlisted and never contain prompts or secrets.
var Catalog = []KindInfo{
	info("routing", "account", "switched", SeverityInfo, "A pool's serving account changed.", []string{"request_id", "binding_id", "pool_id", "from_account_id", "to_account_id"}, []string{"pool", "from_account_id", "to", "reason"}, ""),
	info("routing", "fallback", "selected", SeverityWarning, "A request was served by a different model or provider than its first choice.", []string{"request_id", "binding_id", "plan_id", "pool_id"}, []string{"from", "to", "step", "reason"}, ""),
	info("access", "request", "blocked", SeverityWarning, "A request was refused by policy (routing restriction, privacy, compatibility, blocking budget).", []string{"request_id", "binding_id"}, []string{"reason", "requested_model", "plan_id", "excluded"}, ""),
	info("access", "credential", "issued", SeverityInfo, "A gateway credential was issued (child is true for a minted run token).", []string{"credential_id", "binding_id"}, []string{"prefix", "scopes", "child"}, ""),
	info("access", "credential", "revoked", SeverityInfo, "A gateway credential was revoked.", []string{"credential_id", "binding_id"}, []string{"reason"}, ""),
	info("quota", "window", "exhausted", SeverityWarning, "A monitored global quota window reached its exhaustion threshold.", []string{"monitor_id", "account_id"}, []string{"monitor", "bucket", "label", "reset_at", "source"}, "quota.window.recovered"),
	info("quota", "window", "recovered", SeverityInfo, "A previously exhausted quota window has capacity again.", []string{"monitor_id", "account_id"}, []string{"monitor", "bucket", "label"}, ""),
	info("quota", "monitor", "auth_failed", SeverityCritical, "A quota monitor's credential was rejected.", []string{"monitor_id", "account_id"}, []string{"monitor", "auth_state", "error_kind"}, ""),
	info("budget", "threshold", "warning", SeverityWarning, "A budget crossed its warning fraction in the current period.", []string{"budget_id", "workspace_id", "project_id", "binding_id", "account_id"}, []string{"budget", "scope", "period", "period_start", "spent", "limit", "currency", "fraction", "enforcement"}, ""),
	info("budget", "threshold", "exceeded", SeverityCritical, "A budget was exhausted in the current period.", []string{"budget_id", "workspace_id", "project_id", "binding_id", "account_id"}, []string{"budget", "scope", "period", "period_start", "spent", "limit", "currency", "fraction", "enforcement"}, ""),
	info("provider", "account", "cooldown_started", SeverityWarning, "An account entered a cooldown after a rate limit or authentication failure.", []string{"account_id", "provider_id"}, []string{"reason", "until"}, "provider.account.recovered"),
	info("provider", "account", "recovered", SeverityInfo, "An account's cooldown ended.", []string{"account_id", "provider_id"}, []string{}, ""),
	info("policy", "plan", "published", SeverityInfo, "A routing plan version was published or rolled back.", []string{"plan_id", "version_id"}, []string{"version", "rollback"}, ""),
	info("compat", "breaker", "opened", SeverityWarning, "Repeated compatibility failures opened a breaker.", []string{"provider_id"}, []string{"path", "from", "to", "reason", "override", "retry_at"}, "compat.breaker.closed"),
	info("compat", "breaker", "half_opened", SeverityWarning, "A breaker started probing for recovery.", []string{"provider_id"}, []string{"path", "from", "to", "reason", "override", "retry_at"}, ""),
	info("compat", "breaker", "closed", SeverityInfo, "A breaker closed: compatibility restored.", []string{"provider_id"}, []string{"path", "from", "to", "reason", "override"}, ""),
	info("compat", "passthrough", "activated", SeverityWarning, "Native passthrough started serving a path.", []string{"provider_id"}, []string{"path"}, "compat.passthrough.deactivated"),
	info("compat", "passthrough", "blocked", SeverityWarning, "Passthrough was refused on a path (plan, privacy or budget policy).", []string{"provider_id"}, []string{"path", "reason"}, ""),
	info("compat", "passthrough", "deactivated", SeverityInfo, "Passthrough stopped because the breaker closed.", []string{"provider_id"}, []string{"path"}, ""),
	info("protocol", "change", "detected", SeverityInfo, "Protocol Watch recorded a new structural change.", []string{"change_id", "provider_id"}, []string{"kind", "direction", "protocol", "path", "impact", "confidence"}, ""),
	info("config", "<kind>", "created|updated|deleted", SeverityInfo, "A managed configuration resource changed.", []string{"resource_id"}, []string{"name", "managed_by", "config_revision"}, ""),
}
