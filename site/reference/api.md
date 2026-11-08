# Control-plane API

Everything the web UI does goes through the control-plane API under `/api/v1`, so anything you can do in the UI you
can also script. The Terraform provider and the CLI use the same API.

The full description, with every operation, request and response, is the OpenAPI 3.1 document
[api/openapi.yaml](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/api/openapi.yaml). It is generated from
the code (`make gen`), so it always matches the server. A running server prints its own copy with
`harbor-pilot openapi`.

## Authenticating

**Automation** uses an API token (`hpa_…`) issued to a service principal, sent as a bearer token:

```sh
curl -sS https://harbor-pilot.example.com/api/v1/providers \
  -H "Authorization: Bearer $(cat ~/.config/harbor-pilot/api.token)"
```

Create an automation account (a service principal) under **Settings → API tokens** with **New automation account**,
give it only the permissions it needs, then issue it a token; the token is shown once. See [People, API tokens and sign-in](/operators/access).

**The browser** uses a session cookie. Every change made with a cookie must also carry the session's CSRF token in
`X-CSRF-Token` (from `GET /api/v1/session`) and come from the server's own origin. A failed check answers `403` with
`csrf_invalid` or `origin_mismatch`.

Gateway tokens (`hpk_…`) are for the [gateway](/reference/gateway) only and are refused here.

<Shot name="api-tokens" alt="Settings, API tokens tab, listing one automation account managed by Terraform with two permissions" caption="Automation accounts and their API tokens" />

## Permissions

Every operation declares the one permission it needs, as `x-permission` in the OpenAPI document. Roles bundle them:

| Permission | Viewer | Operator | Admin | Allows |
|---|:-:|:-:|:-:|---|
| `config.read` | ✓ | ✓ | ✓ | Read configuration, health and status. |
| `usage.read` | ✓ | ✓ | ✓ | Read usage, spend and statistics. |
| `activity.read` | ✓ | ✓ | ✓ | Read requests and their routing decisions. |
| `traces.metadata.read` | ✓ | ✓ | ✓ | See that captured content exists, without reading it. |
| `config.write` | | ✓ | ✓ | Change providers, accounts, clients, policies and the rest. |
| `policy.publish` | | ✓ | ✓ | Publish routing plan versions and import configuration. |
| `secrets.write` | | ✓ | ✓ | Set or change secrets and secret references. |
| `tokens.manage` | | ✓ | ✓ | Issue and revoke gateway tokens. |
| `events.manage` | | ✓ | ✓ | Manage event destinations and replays. |
| `notifications.manage` | | ✓ | ✓ | Manage alert channels and rules. |
| `compat.manage` | | ✓ | ✓ | Manage compatibility passthrough and Protocol Watch. |
| `audit.read` | | ✓ | ✓ | Read the audit log. |
| `traces.delete` | | ✓ | ✓ | Delete captured content. |
| `users.manage` | | | ✓ | Manage people and service principals. |
| `auth.settings` | | | ✓ | Change sign-in settings and single sign-on providers. |
| `system.manage` | | | ✓ | Backups and system status. |
| `traces.content.read` | | | | Read captured content, redacted. |
| `traces.content.unredacted` | | | | Read captured content unredacted. |
| `traces.export` | | | | Export captured content. |

The three content permissions are never part of a role. They are granted to a person or service principal
explicitly, and every use is audited. Reading unredacted content and exporting it also need a sign-in within the
last 10 minutes (`POST /api/v1/session/reauth` confirms the password), so API tokens cannot do either; such a request
answers `403` with `reauth_required`.

Operations marked `public` need no sign-in (signing in, the setup and recovery links, `GET /api/v1/meta`), and
`session` operations need any signed-in caller.

## Conventions

### Errors

Every error has the same envelope:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "the request is invalid",
    "request_id": "3f9c1a7e5b2d8c4f6a0e9b1d",
    "details": { "fields": [{ "location": "body.name", "message": "is required" }] }
  }
}
```

`code` is stable and meant for programs; `message` is for people and never contains a secret. Common codes:

| Status | Code |
|---|---|
| 400 | `bad_request` |
| 401 | `unauthenticated` |
| 403 | `forbidden`, `csrf_invalid`, `origin_mismatch`, `reauth_required` |
| 404 | `not_found` |
| 405 | `method_not_allowed` |
| 409 | `conflict` |
| 412 | `revision_mismatch` (with `details.current_etag`) |
| 413 | `too_large` |
| 422 | `validation_failed` (with `details.fields`) |
| 428 | `precondition_required` |
| 429 | `rate_limited` |
| 500 | `internal` |

### Revisions and `If-Match`

Every object carries a `revision`, and single-object responses set `ETag: "r<revision>"`. Send it back in
`If-Match` when you change or delete the object; if someone changed it in the meantime the request fails with `412
revision_mismatch` and the current ETag, instead of silently overwriting their change. `If-Match: *` matches any
revision.

### Pagination

Lists return a page:

```json
{ "items": [ … ], "next_cursor": "…" }
```

Pass `?limit=` (1 to 500, default 50) and, for the next page, `?cursor=` with the previous page's `next_cursor`. A
page without `next_cursor` is the last.

### Change source

Changes record where they came from (`ui`, `api`, `terraform`, `cli` or `import`) in the audit log. Send
`X-HP-Managed-By` to set it; the Terraform provider sends `terraform`.

### Identifiers and values

- Identifiers are prefixed ULIDs, for example `prv_…` (provider), `acc_…` (account), `cbn_…` (client), `pln_…`
  (routing plan), `req_…` (request).
- Timestamps are Unix milliseconds, UTC.
- Money is a decimal string plus a currency, kept exact to ten decimal places.

### Secrets

Secret fields are write-only. Responses show only whether a secret is set, where it comes from (`stored`,
`reference` or `none`), a short fingerprint and when it changed. See [Secrets and the master key](/security/secrets).

## What is in the API

| Area | Covers |
|---|---|
| Session and sign-in | Sign in and out, sessions, password, re-authentication, setup and recovery links, single sign-on. |
| People | Users, invitations, roles, content grants, service principals and their API tokens. |
| Providers | Providers, accounts, subscription entitlements, test connection. |
| Routing | Model aliases, pools, routing plans and their versions (validate, publish, roll back), simulate and replay. |
| Clients | Clients, their gateway tokens, workspaces and projects. |
| Limits | Budgets, quota monitors and quota readings. |
| Privacy | Masking policies, capture rules and captured content (with the content permissions). |
| Activity | Requests, attempts, sessions, usage, spend and statistics. |
| Events and alerts | Events, event destinations and deliveries, alert channels and rules, notifications. |
| Compatibility | Passthrough settings and Protocol Watch. |
| Configuration | The effective configuration, revisions, export and atomic import (with dry run). |
| System | Build and schema version, attention items, CLI bridge status, storage and key status, backups. |

## Usage and activity by reference

Requests made with a token that carries **trusted references** (a run token's `bound_refs`, plus the references set on
its client) can be filtered and grouped by those references. Headers such as `X-HP-External-Ref` are claims and are
never filterable. These endpoints need the same permissions as without a filter: `usage.read` for usage and
`activity.read` for activity.

`ref=<key>:<value>` filters by one reference. The value is everything after the first `:`, so it may contain colons.
The key uses the same rules as `bound_refs` keys (lowercase, for example `spaceman.run`), and the value is 1 to 256
bytes. Repeat `ref` up to 4 times to require all of them. A malformed filter returns `422 validation_failed` with
`location: "query.ref"`.

| Endpoint | Parameters |
|---|---|
| `GET /api/v1/usage/summary` | `ref`, `binding_id`, and `group_by=ref:<key>` next to `account`, `provider`, `model`, `binding`, `project`, `workspace` and `billing`. |
| `GET /api/v1/usage/series` | `ref` and `binding_id`, combined with `filter`/`value` and `by`. Over the same range, the buckets add up to the summary's totals under the same filters. |
| `GET /api/v1/activity/requests` | `ref`, combined with the other filters, `total` and pagination. |
| `GET /api/v1/activity/sessions` | `ref`: sessions with a request that carries the references. `ref=session:<value>` finds the session id of a bound session. |

```sh
# Per-run totals inside one space
GET /api/v1/usage/summary?ref=spaceman.space:spc_alpha&group_by=ref:spaceman.run

# One run's spend, split by model
GET /api/v1/usage/summary?ref=spaceman.run:run_a&group_by=model

# One run's spend per hour
GET /api/v1/usage/series?ref=spaceman.run:run_a&step=hour

# One run's failed requests
GET /api/v1/activity/requests?ref=spaceman.run:run_a&status=error

# The session id of a run's bound session
GET /api/v1/activity/sessions?ref=session:run_a
```

```json
{
  "group_by": "ref:spaceman.run",
  "filters": { "refs": { "spaceman.space": "spc_alpha" } },
  "groups": [
    { "key": "run_a", "label": "run_a", "attempts": 2, "tokens": { "input": 300, "output": 30 }, "costs": [ … ], "allocated": [ … ] },
    { "key": "run_b", "label": "run_b", "attempts": 1, "tokens": { "input": 1000, "output": 100 }, "costs": [ … ], "allocated": [ … ] }
  ],
  "next_cursor": "…",
  "totals": [ … ],
  "subscription_fees": [ … ]
}
```

- With `group_by=ref:<key>`, usage without that reference forms the group with key `""`. Empty reference values are
  not indexed, so usage whose value is empty is in that group too.
- Reference groups are ordered by key and paged like lists: `limit` (1 to 500, default 50), then `cursor` with the
  previous `next_cursor`. A cursor only works with the same `group_by`, `ref`, `binding_id`, `from` and `to` as the
  page it came from; otherwise the response is 422 at `query.cursor`. Other groupings return all their groups, and
  ignore `cursor` and `limit`.
- `totals` cover all matching usage in the range, on every page.
- `subscription_fees` and the `subscription_fee` total are the whole range's fees and ignore filters. Each group's
  `allocated` share is its part of the account's tokens, so a filtered slice never gets the whole fee.
- Usage totals include every usage entry. Request and session lists come from request telemetry, which can drop rows
  when the writer is busy (see [Known limitations](/security/limitations)).
- A token that can create run tokens chooses their references. When several clients use the same keys, add
  `binding_id` to stay within one client.

## Budgets and their status

`GET /api/v1/budgets/status` (`usage.read`) returns each budget's current window: `period_start`, `period_end` (`null`
for a one-time budget without an end), `limit_e10`, `spent_e10`, `reserved_e10`, `remaining_e10` (the limit minus spent
and reserved, never below 0), `fraction`, `projected_spend_e10` (`null` without an end or early in the window) and
`state`: `ok`, `warning`, `exceeded`, `disabled`, `scheduled` (a one-time budget before `starts_at`) or `expired`
(after `ends_at`; it no longer applies). The list isn't paginated: it is bounded by the number of budgets.

| Parameter | Returns |
|---|---|
| `project_id` | Budgets that cover requests charged to that project: global, the project's workspace and the project itself. Disabled, scheduled and expired ones are included; read `state`. |
| `binding_id` | With `project_id` only. Also the client's own budgets, the scope a run token's `check_budget` counts. |
| `scope`, `scope_id` | Budgets defined on exactly that object, for example `scope=account&scope_id=acc_…`, or `scope=global` with no id. |

Combining `project_id` with `scope` is a `400 invalid_request`, and so is `binding_id` without `project_id`. An unknown
project or client is a `404`. Provider account budgets never match `project_id`, because routing picks the account
per request.

```sh
# What can stop a run charged to this project and client?
GET /api/v1/budgets/status?project_id=prj_…&binding_id=cbd_…
```

`POST /api/v1/budgets/{id}/reset` (`config.write`; send `If-Match`) starts a one-time budget over: it sets
`spec.starts_at` to the server's current time, truncated to the second (spend recorded earlier in that second still
counts), and returns the budget with its new `ETag`. Spend before that stays in
usage reports. A recurring budget, or one whose `ends_at` has passed, answers `422`.

## System information

`GET /api/v1/system` (`config.read`, so a service-principal token with read access works) identifies the running
build for automation:

```json
{ "version": "0.1.4", "commit": "2e957c7…", "started_at": "2026-11-08T06:00:00Z", "schema_version": 20261108100000000 }
```

`commit` is empty when the build didn't record it. The response holds no host names, data directory, key ids or
secrets; `GET /api/v1/system/status` (`system.manage`) has the storage details.
