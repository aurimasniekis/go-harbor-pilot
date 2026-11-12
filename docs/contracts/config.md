# Managed Configuration Contract

## Authority
The database is the single source of truth for every managed object. `harbor-pilot.yaml` only holds process bootstrap settings.

All writes go through one service path, whether they come from the UI, the API, the CLI, Terraform or an import. That path runs these steps inside one SQLite transaction:

1. Validate the change and canonicalize it.
2. Check `If-Match`.
3. Write the change and record history.
4. Write the audit entry.
5. Write the outbox event (`config.<kind>.created|updated|deleted`).
6. Bump `config_revisions`.
7. **Compile the full would-be snapshot.**

If compilation fails, the whole transaction rolls back. Committed configuration therefore always compiles. The compiled snapshot replaces the active one through an atomic swap, and the previous snapshot stays as the last-known-good.

## Resource kinds
| Kind | Path | Id prefix | Secrets | References |
|---|---|---|---|---|
| workspace | `/workspaces` | `wsp_` | — | — |
| project | `/projects` | `prj_` | — | workspace |
| provider | `/providers` | `prv_` | — | — |
| provider_account | `/provider-accounts` | `acc_` | `credential` (or `spec.credential_ref`) | provider |
| subscription_entitlement | `/subscription-entitlements` | `ent_` | — | provider_account |
| pool | `/pools` | `pol_` | — | provider_account (members) |
| model_alias | `/model-aliases` | `als_` | — | pool or model_alias (acyclic, depth ≤ 8) |
| routing_plan | `/routing-plans` | `pln_` | — | (documents reference aliases/providers) |
| client_binding | `/client-bindings` | `cbn_` | — | routing_plan, project(s) |

Every kind supports these operations:

| Method and path | Purpose |
|---|---|
| `GET /{kind}` | Cursor-paginated list |
| `GET /{kind}/{id-or-name}` | Read one object |
| `POST /{kind}` | Create; the body is `{name, spec, secrets?}` |
| `PUT /{kind}/{id}` | Full replace |
| `DELETE /{kind}/{id}` | Delete |
| `GET /{kind}/{id}/history` | Revision history |

## Semantics
- **Names** are unique per kind and immutable. A rename means delete and create; Terraform models this as ForceNew.
- **Specs** are decoded strictly: unknown fields are rejected.
  - Defaults are applied, such as provider base URL and classification, pool strategy and affinity.
  - Lists are sorted and de-duplicated wherever order carries no meaning.
  - Lists and maps are always present, never null.
  - Read-after-write therefore returns the canonical form, and Terraform plans converge.
- **Concurrency.** Each object has a `revision` and an `ETag` of the form `"r<revision>"`. A stale `If-Match` returns `412 revision_mismatch` with the current ETag. Terraform always sends `If-Match`.
- **Secrets** are write-only.
  - `secrets.{field}` sets a value, `""` clears it, and omitting the key keeps the current value.
  - Responses report only `{set, source: stored|reference|none, fingerprint, updated_at}`.
  - A secret reference (`env:NAME`, `file:/path`) and a stored value are mutually exclusive.
- **References.** References use ids and are integrity-checked. Deleting a referenced object returns `409` with `details.referenced_by`. References from published plan documents are checked at compile time, so deleting an alias that a published plan uses is rejected.
- **Ownership and drift.** `managed_by` is taken from `X-HP-Managed-By`: ui, api, terraform, cli or import.
  - When a non-Terraform source changes a Terraform-managed object, the object becomes `drift_state: drifted` and stays Terraform-managed.
  - It is listed in `GET /config/effective` and flagged in the UI.
  - The next Terraform plan shows the difference, and the next Terraform apply sets it back to `in_sync`.

## Per-model upstream request fields
A model in a provider's catalog may carry fields the gateway adds to every request for it, so clients do not have to
send them (D-046). They are declared on `spec.models[]` and apply wherever that model is routed.

| Field | Type | Meaning |
|---|---|---|
| `request_fields` | object of raw JSON values | Extra top-level request body fields, up to 32, each at most 8 KiB of valid JSON. Names must match `^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`. |
| `provider_routing` | object | Typed upstream-provider pin, sent as the body's `provider`. Only on `openai_compatible` providers. |
| `request_field_policy` | `configured` \| `client` \| `reject` | What happens when the client already sends one of these fields. Empty means `configured`. |

`provider_routing` accepts `order`, `only`, `ignore`, `sort` (`price`, `throughput`, `latency`), `allow_fallbacks`,
`require_parameters`, `data_collection` (`allow`, `deny`), `zdr`, `quantizations` and `max_price`
(`prompt`, `completion`, `request`, `image`). An unset field is omitted from the request, so `allow_fallbacks: false`
is sent and an absent `sort` is not. An empty object is dropped when the spec is saved.

Structural fields are refused in `request_fields`, in any letter case: `model`, `messages`, `input`, `contents`,
`system`, `systeminstruction`, `instructions`, `stream`, `stream_options`, `tools`, `tool_choice`,
`parallel_tool_calls` and `n`. CLI bridge providers (`claude_code`, `codex`) accept neither field.

## Routing plan versions
A plan holds metadata only. Routing logic lives in immutable versions under `/routing-plans/{id}/versions`:

| Step | Endpoint | What it does |
|---|---|---|
| Draft | `POST /versions`, `PUT /versions/{vid}` (drafts only) | Create or replace a draft |
| Validate | `POST /versions/{vid}/validate` | Check structure, references and engine compilation against the current configuration, plus warnings |
| Publish | `POST /versions/{vid}/publish` (`policy.publish`) | The previous published version becomes `superseded` and `policy.plan.published` is emitted |
| Rollback | `POST /routing-plans/{id}/rollback {version_id}` | Publish a new version copying an older document (`rollback_of` is recorded); history stays immutable |

## Gateway credentials
- **Issuance.** `POST /client-bindings/{id}/credentials` returns `hpk_<prefix>_<secret>` once, together with the base URLs. Only `HMAC-SHA256(pepper, secret)` is stored.
- **Scopes.** `gateway:invoke`, `gateway:models`, `usage:read` and `tokens:mint`.
- **Child credentials.** A credential with `tokens:mint` can mint children through the data-plane endpoint `POST /v1/harbor/credentials` (see the gateway contract). Children:
  - live at most 7 days;
  - can only narrow the parent's scopes;
  - cannot mint further children;
  - inherit the parent's trusted `bound_refs` and cannot override them;
  - are checked against budgets at mint only when the request sets `check_budget` (D-043).
- **Revocation.** Revoking a parent revokes its children.

## Export and import
- **Export.** `GET /config/export[?kind=…]` returns a bundle (`api_version: harbor-pilot/v1`). Ids inside specs and plan documents are rewritten to portable `ref:<kind>/<name>` references. Secret values are never included, only their presence.
- **Import.** `POST /config/import {bundle, modes, dry_run, publish_plans, allow_terraform}` applies everything in one transaction and one config revision. The resulting configuration must compile.

| Option | Behaviour |
|---|---|
| `merge` (default per kind) | Create missing objects and replace changed ones by name; unchanged objects are reported as `unchanged`. |
| `replace` | `merge`, then delete objects of that kind that are absent from the bundle, in reverse dependency order. |
| `dry_run` | Run everything, including compilation, then roll back and return the planned actions. |
| `allow_terraform` | Without it, Terraform-managed objects are skipped. |
| `publish_plans` | Publish a bundle plan document as a new version when it differs from the published one. |

- **Stored secrets** are reported as warnings: they must be set again after import.
