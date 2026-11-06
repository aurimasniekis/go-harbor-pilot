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
| System | Attention items, CLI bridge status, storage and key status, backups. |
