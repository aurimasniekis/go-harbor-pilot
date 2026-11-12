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
| `request_fields` | object of raw JSON values | Extra request body fields from the provider type's allowlist below, up to 32, each at most 8 KiB of valid JSON. |
| `provider_routing` | object | Typed upstream-provider pin, sent as the body's `provider`. Only on `openai_compatible` providers. |
| `request_field_policy` | `configured` \| `client` \| `reject` | What happens when the client's own request already sets one of these fields. Empty means `configured`. |

`provider_routing` accepts `order`, `only`, `ignore`, `sort` (`price`, `throughput`, `latency`), `allow_fallbacks`,
`require_parameters`, `data_collection` (`allow`, `deny`), `zdr`, `quantizations` and `max_price`
(`prompt`, `completion`, `request`, `image`). An unset field is omitted from the request, so `allow_fallbacks: false`
is sent and an absent `sort` is not. An empty object is dropped when the spec is saved. It is sent only to
`openai_compatible` providers.

### Allowlist
The list lives in one place, `internal/controlplane/request_fields.go`; this table mirrors it. Names are compared
ignoring letter case, and snake_case equals camelCase (`top_p` = `topP`). The name sent upstream is the spelling in
this table.

| Provider type | Allowed fields | Source |
|---|---|---|
| `openai_compatible` | `transforms`, `provider` (only without `provider_routing`), `temperature`, `top_p`, `top_k`, `min_p`, `top_a`, `frequency_penalty`, `presence_penalty`, `repetition_penalty`, `seed`, `stop`, `logit_bias`, `user`, `metadata` | OpenAI Chat Completions request parameters, plus the router parameters documented for OpenAI-compatible routers (prompt transforms, provider preferences, extra samplers) |
| `openai` | `temperature`, `top_p`, `user`, `metadata` | Accepted by both Chat Completions and Responses; the provider speaks either, depending on the client |
| `ollama` | `temperature`, `top_p`, `seed`, `stop`, `frequency_penalty`, `presence_penalty` | The OpenAI-compatible endpoint's documented Chat parameters |
| `anthropic` | `metadata`, `temperature`, `top_p`, `top_k`, `stop_sequences` | Messages API request parameters |
| `gemini` | `safetySettings`, `generationConfig` with only `temperature`, `topP`, `topK`, `stopSequences` | generateContent request body; `generationConfig` is merged key by key, never replaced |
| `claude_code`, `codex` | none | the vendor CLI builds its own request |

Left out on purpose: `reasoning` and similar effort settings (they change what a request costs) and `response_format`
(it changes the shape of the response the client parses).

Never configurable, whatever the provider type, in any spelling (the error gives the reason):
- output limits: `max_tokens`, `max_completion_tokens`, `max_output_tokens`/`maxOutputTokens` and the like (they
  would bypass the budget reservation and the model's output cap);
- extra candidates: `n`, `candidateCount`, `best_of`;
- the model and router model lists: `model`, `models`, `route`;
- tools: `tools`, `tool_choice`, `tool_config`, `parallel_tool_calls`, `functions`, `function_call`, `mcp_servers`,
  `plugins`, `web_search_options`;
- stored or server-side state: `store`, `previous_response_id`, `conversation`, `prompt`, `background`,
  `cachedContent`, `prompt_cache_key`, `context_management`, `container`;
- prompt caching: `cache_control`;
- streaming and structure: `stream`, `stream_options`, `messages`, `input`, `contents`, `system`,
  `systemInstruction`, `instructions`, `include`, `response_format`, `text`, `responseMimeType`, `responseSchema`,
  `responseModalities`, `modalities`, `audio`, `prediction`;
- cost options: `reasoning`, `reasoning_effort`, `thinking`, `thinkingConfig`, `service_tier`, `usage`, `verbosity`.

Any other name not on the allowlist is refused with an error that lists the allowed names for the provider type.

### Conflicts
A conflict exists only when the **client's own request** sets the field: its body as received (any spelling, and Gemini
`generationConfig` keys) or the canonical request it was parsed into, so `stop_sequences` from a Messages client is
the same setting as `stop`. A value the gateway filled in itself is never a client conflict.

- `configured`: the model's value is sent; every spelling of the key is removed from the body first.
- `client`: the client's value is kept; the model's value fills the field only when the request does not set it.
- `reject`: the request is answered 400 `invalid_request`, naming the field. The gateway decides this before trying
  any candidate and checks every candidate: if any candidate's model would refuse, the request is refused, so the
  answer does not depend on candidate order.

An override or a kept client value is recorded as a translation loss `request_field:<name>`.

### Updating a provider
`PUT /providers/{id}` replaces the whole spec. An update that leaves these fields out of a model clears them, so a
client that manages providers (Terraform included) must send them back.

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
