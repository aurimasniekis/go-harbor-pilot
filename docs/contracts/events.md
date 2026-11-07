# Event catalog

Generated from `internal/events/catalog.go` (`go test ./internal/events -update`).

Every event is published as a JSON envelope `{schema_version, id, type, name, occurred_at, severity, correlation_id, subjects, data}`. Subjects hold identifiers only; `data` holds allowlisted, redacted fields and never prompts or secrets. Delivery is at-least-once: consumers deduplicate by `id` (Pulse `Nats-Msg-Id`, webhook `X-Harbor-Event-Id`).

Webhooks carry `X-Harbor-Signature: t=<unix>,v1=<hex>[,v1=<hex>]`, an HMAC-SHA256 over `<t>.<body>` with the current (and, during rotation, the previous) signing secret. Receivers should reject timestamps more than five minutes away and deduplicate by event id.

| Name | Type | Severity | Subjects | Data | Resolved by | Meaning |
|---|---|---|---|---|---|---|
| `routing.account.switched` | `harborpilot.routing.account_switched.v1` | info | request_id, binding_id, pool_id, from_account_id, to_account_id | pool, from_account_id, to, reason |  | A pool's serving account changed. |
| `routing.fallback.selected` | `harborpilot.routing.fallback_selected.v1` | warning | request_id, binding_id, plan_id, pool_id | from, to, step, reason |  | A request was served by a different model or provider than its first choice. |
| `access.request.blocked` | `harborpilot.access.request_blocked.v1` | warning | request_id, binding_id | reason, requested_model, plan_id, excluded |  | A request was refused by policy (routing restriction, privacy, compatibility, blocking budget). |
| `access.credential.issued` | `harborpilot.access.credential_issued.v1` | info | credential_id, binding_id | prefix, scopes, child |  | A gateway credential was issued (child is true for a minted run token). |
| `access.credential.revoked` | `harborpilot.access.credential_revoked.v1` | info | credential_id, binding_id | reason |  | A gateway credential was revoked. |
| `quota.window.exhausted` | `harborpilot.quota.window_exhausted.v1` | warning | monitor_id, account_id | monitor, bucket, label, reset_at, source | quota.window.recovered | A monitored global quota window reached its exhaustion threshold. |
| `quota.window.recovered` | `harborpilot.quota.window_recovered.v1` | info | monitor_id, account_id | monitor, bucket, label |  | A previously exhausted quota window has capacity again. |
| `quota.monitor.auth_failed` | `harborpilot.quota.monitor_auth_failed.v1` | critical | monitor_id, account_id | monitor, auth_state, error_kind |  | A quota monitor's credential was rejected. |
| `budget.threshold.warning` | `harborpilot.budget.threshold_warning.v1` | warning | budget_id, workspace_id, project_id, binding_id, account_id | budget, scope, period, period_start, spent, limit, currency, fraction, enforcement |  | A budget crossed its warning fraction in the current period. |
| `budget.threshold.exceeded` | `harborpilot.budget.threshold_exceeded.v1` | critical | budget_id, workspace_id, project_id, binding_id, account_id | budget, scope, period, period_start, spent, limit, currency, fraction, enforcement |  | A budget was exhausted in the current period. |
| `provider.account.cooldown_started` | `harborpilot.provider.account_cooldown_started.v1` | warning | account_id, provider_id | reason, until | provider.account.recovered | An account entered a cooldown after a rate limit or authentication failure. |
| `provider.account.recovered` | `harborpilot.provider.account_recovered.v1` | info | account_id, provider_id |  |  | An account's cooldown ended. |
| `policy.plan.published` | `harborpilot.policy.plan_published.v1` | info | plan_id, version_id | version, rollback |  | A routing plan version was published or rolled back. |
| `compat.breaker.opened` | `harborpilot.compat.breaker_opened.v1` | warning | provider_id | path, from, to, reason, override, retry_at | compat.breaker.closed | Repeated compatibility failures opened a breaker. |
| `compat.breaker.half_opened` | `harborpilot.compat.breaker_half_opened.v1` | warning | provider_id | path, from, to, reason, override, retry_at |  | A breaker started probing for recovery. |
| `compat.breaker.closed` | `harborpilot.compat.breaker_closed.v1` | info | provider_id | path, from, to, reason, override |  | A breaker closed: compatibility restored. |
| `compat.passthrough.activated` | `harborpilot.compat.passthrough_activated.v1` | warning | provider_id | path | compat.passthrough.deactivated | Native passthrough started serving a path. |
| `compat.passthrough.blocked` | `harborpilot.compat.passthrough_blocked.v1` | warning | provider_id | path, reason |  | Passthrough was refused on a path (plan, privacy or budget policy). |
| `compat.passthrough.deactivated` | `harborpilot.compat.passthrough_deactivated.v1` | info | provider_id | path |  | Passthrough stopped because the breaker closed. |
| `protocol.change.detected` | `harborpilot.protocol.change_detected.v1` | info | change_id, provider_id | kind, direction, protocol, path, impact, confidence |  | Protocol Watch recorded a new structural change. |
| `config.<kind>.created|updated|deleted` | `harborpilot.config.<kind>_created|updated|deleted.v1` | info | resource_id | name, managed_by, config_revision |  | A managed configuration resource changed. |
