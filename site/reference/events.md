# Event catalog

Harbor Pilot records events when something worth reacting to happens: a quota window runs out, an account enters a
cooldown, a plan is published, a request is blocked. Events are written in the same transaction as the change that
caused them, then relayed to each event destination (Pulse or a signed webhook) and used by alert rules. How to set
up a destination is in [Events (Pulse and webhooks)](/operators/events).

<Shot name="event-stream" alt="Settings, Event stream tab, filtered to problems: a breaker opened by a manual override, a fallback, two account cooldowns and a rejected quota tracker credential" caption="The event stream; its Catalog tab lists every event below" />

## Envelope

Every event has the same JSON envelope:

```json
{
  "schema_version": 1,
  "id": "evt_01J…",
  "type": "harborpilot.quota.window_exhausted.v1",
  "name": "quota.window.exhausted",
  "occurred_at": "2026-11-06T09:30:00Z",
  "severity": "warning",
  "correlation_id": "req_01J…",
  "subjects": { "monitor_id": "qmn_01J…", "account_id": "acc_01J…" },
  "data": { "monitor": "Claude Max", "bucket": "five_hour", "label": "5-hour window", "reset_at": "…", "source": "…" }
}
```

- `subjects` hold identifiers only, so you can filter and join without reading `data`.
- `data` holds an allowlist of fields for each event. It never carries prompts, responses or secrets.
- `severity` is `info`, `warning` or `critical`.

## Delivery

Delivery is **at least once**: an event can arrive more than once, so deduplicate by `id`.

**Pulse** destinations publish each event to the subject `evt.harborpilot.<area>.<entity>.<action>`, for example
`evt.harborpilot.quota.window.exhausted`, with the envelope as the payload and the event id as the message id, so
JetStream drops duplicates within its window. The Pulse client needs publish rights on `evt.harborpilot.>`.

**Webhook** destinations `POST` the envelope with these headers:

| Header | Value |
|---|---|
| `X-Harbor-Event-Id` | The event id, for deduplication. |
| `X-Harbor-Event-Type` | The versioned type, for example `harborpilot.quota.window_exhausted.v1`. |
| `X-Harbor-Signature` | `t=<unix seconds>,v1=<hex>`; during secret rotation a second `v1=` value is signed with the previous secret. |

Any `2xx` answer counts as delivered. Failures are retried with exponential backoff (1 second doubling up to 5
minutes, with jitter, honouring `Retry-After` up to an hour), and an event is dead-lettered after 50 attempts or 24
hours by default (both adjustable per destination). Dead-lettered and delivered events can be replayed from the UI or
API.

### Verifying a webhook signature

The signature is an HMAC-SHA256, keyed with the destination's signing secret, over `<t>.<raw body>`:

```python
import hashlib, hmac, time

def verify(header: str, body: bytes, secret: str, tolerance: int = 300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(",") if p.startswith("t="))
    sigs = [p[3:] for p in header.split(",") if p.startswith("v1=")]
    ts = parts.get("t", "")
    if not ts.isdigit() or abs(time.time() - int(ts)) > tolerance:
        return False
    want = hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256).hexdigest()
    return any(hmac.compare_digest(want, s) for s in sigs)
```

Reject timestamps more than five minutes away, accept any matching `v1` value, and deduplicate by event id.

Alert channels of type webhook use the same signature, with `X-Harbor-Notification-Id` instead of the event headers.

## Filters

A destination can narrow what it receives:

- `events`: name patterns such as `quota.*` or `compat.breaker.*` (empty means all);
- `min_severity`: `info` (default), `warning` or `critical`;
- `subjects`: exact identifier matches, for example only events about one account.

## Events

| Name | Severity | Subjects | Data | Meaning |
|---|---|---|---|---|
| `routing.account.switched` | info | request_id, binding_id, pool_id, from_account_id, to_account_id | pool, from_account_id, to, reason | A group's serving account changed. |
| `routing.fallback.selected` | warning | request_id, binding_id, plan_id, pool_id | from, to, step, reason | A request was served by a different model or provider than its first choice. |
| `access.request.blocked` | warning | request_id, binding_id | reason, requested_model, plan_id, excluded | A request was refused by policy (a routing restriction, privacy, compatibility or a blocking budget). |
| `access.credential.issued` | info | credential_id, binding_id | prefix, scopes, child | A gateway token was issued. `prefix` is the token's non-secret prefix; `child` is true for a run token. |
| `access.credential.revoked` | info | credential_id, binding_id | reason | A gateway token was revoked. |
| `quota.window.exhausted` | warning | monitor_id, account_id | monitor, bucket, label, reset_at, source | A tracked quota window reached its exhaustion threshold. Resolved by `quota.window.recovered`. |
| `quota.window.recovered` | info | monitor_id, account_id | monitor, bucket, label | An exhausted quota window has capacity again. |
| `quota.monitor.auth_failed` | critical | monitor_id, account_id | monitor, auth_state, error_kind | A quota tracker's credential was rejected. |
| `budget.threshold.warning` | warning | budget_id, workspace_id, project_id, binding_id, account_id | budget, scope, period, period_start, spent, limit, currency, fraction, enforcement | A budget crossed its warning fraction this period. |
| `budget.threshold.exceeded` | critical | budget_id, workspace_id, project_id, binding_id, account_id | budget, scope, period, period_start, spent, limit, currency, fraction, enforcement | A budget was used up this period. |
| `provider.account.cooldown_started` | warning | account_id, provider_id | reason, until | An account entered a cooldown after a rate limit or a sign-in failure. Resolved by `provider.account.recovered`. |
| `provider.account.recovered` | info | account_id, provider_id | | An account's cooldown ended. |
| `policy.plan.published` | info | plan_id, version_id | version, rollback | A routing plan version was published or rolled back. |
| `compat.breaker.opened` | warning | provider_id | path, from, to, reason, override, retry_at | Repeated translation failures opened a compatibility breaker. Resolved by `compat.breaker.closed`. |
| `compat.breaker.half_opened` | warning | provider_id | path, from, to, reason, override, retry_at | A breaker started probing for recovery. |
| `compat.breaker.closed` | info | provider_id | path, from, to, reason, override | A breaker closed: translation works again. |
| `compat.passthrough.activated` | warning | provider_id | path | Native passthrough started serving a path. Resolved by `compat.passthrough.deactivated`. |
| `compat.passthrough.blocked` | warning | provider_id | path, reason | Passthrough was refused on a path (by plan, privacy or budget policy). |
| `compat.passthrough.deactivated` | info | provider_id | path | Passthrough stopped because the breaker closed. |
| `protocol.change.detected` | info | change_id, provider_id | kind, direction, protocol, path, impact, confidence | Protocol Watch recorded a new structural change. |
| `config.<kind>.created`, `.updated`, `.deleted` | info | resource_id | name, managed_by, config_revision | A managed configuration resource changed. |

Each event's `type` is `harborpilot.<area>.<entity>_<action>.v1`, for example
`harborpilot.routing.fallback_selected.v1`. The catalog is generated from the code and checked by a test; the
generated copy is
[docs/contracts/events.md](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/docs/contracts/events.md).
