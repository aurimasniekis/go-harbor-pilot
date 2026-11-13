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

Left out on purpose: `reasoning` and similar effort settings (they change what a request costs; the model's
`reasoning` option below sets them) and `response_format` (it changes the shape of the response the client parses).

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
- reasoning: `reasoning`, `reasoning_effort`, `thinking`, `thinkingConfig` (use the model's `reasoning` option);
- cost options: `service_tier`, `usage`, `verbosity`.

Any other name not on the allowlist is refused with an error that lists the allowed names for the provider type.

### Conflicts
A conflict exists only when the **client's own request** sets the field: its body as received (any spelling, and Gemini
`generationConfig` keys) or the canonical request it was parsed into, so `stop_sequences` from a Messages client is
the same setting as `stop`. A value the gateway filled in itself is never a client conflict.

- `configured`: the model's value is sent; every spelling of the key is removed from the body first.

Gemini `generationConfig` is merged into the request's own canonical `generationConfig` only. Another spelling of it
in the same request (`generation_config`) is dropped whatever the policy, with a drop loss
`request_field:generationConfig`: the gateway parses only the canonical key, so the other spelling's keys (an output
cap among them) were never budgeted.
- `client`: the client's value is kept; the model's value fills the field only when the request does not set it.
- `reject`: the request is answered 400 `invalid_request`, naming the field. The gateway decides this before trying
  any candidate and checks every candidate: if any candidate's model would refuse, the request is refused, so the
  answer does not depend on candidate order.

An override or a kept client value is recorded as a translation loss `request_field:<name>`.

### Updating a provider
`PUT /providers/{id}` replaces the whole spec. An update that leaves these fields (or `reasoning`, below) out of a
model clears them, so a client that manages providers (Terraform included) must send them back.

## Per-model reasoning translation
A model's optional `reasoning` object (D-047) decides how the client's reasoning settings are written for the model's
upstream: Anthropic `thinking` (enabled, disabled or adaptive) and the top-level `output_config.effort`, Chat
`reasoning_effort`, Responses `reasoning`, Gemini `thinkingConfig`. Leaving it out, or saving only defaults
(`{"mode":"auto"}`, `history: "drop"`), stores no option and keeps the built-in translation byte for byte.

| Field | Values | Meaning |
|---|---|---|
| `mode` | `auto` (default) \| `client` \| `fixed` \| `off` | `auto`: the built-in translation (only `history` applies). `client`: follow the client, written in `style`; `effort` or `budget_tokens` is the default when the client names no level. `fixed`: always ask for `effort` or `budget_tokens`, whatever the client sent. `off`: always ask the upstream not to reason. |
| `style` | see below | The upstream's wire shape. Empty: the upstream protocol's default style. |
| `effort` | `minimal` … `max` | Default (`client`) or fixed (`fixed`) level. Not with `budget_tokens`. |
| `budget_tokens` | integer | Default or fixed thinking budget instead of an effort. Below `max_output_tokens` when that is set. |
| `max_budget_tokens` | integer | A larger client budget is reduced to this (approximate loss `reasoning_budget`). |
| `efforts` | list of levels | The levels the model accepts, stored in ladder order. A level outside it is clamped to the nearest lower one (else the lowest) with an approximate loss `reasoning_effort_clamped`. Empty: the style's defaults. |
| `history` | `drop` (default) \| `field` | Earlier reasoning in a Chat history: dropped (`reasoning_history` loss) or sent back on the assistant message in `history_field`. Chat upstreams only (`openai_compatible`, `ollama`). |
| `history_field` | lowercase identifier | The assistant message field for `history: field`. Empty: the style's default. Standard message fields (`content`, `role`, `tool_calls`, …) and `reasoning_details`, `prefix`, `cache_control` are refused. |

Effort ladder: `none` < `minimal` < `low` < `medium` < `high` < `xhigh` < `max` (`none` is `mode: off`).

| Style | Provider types | Writes | Off writes | Default efforts | Default history field |
|---|---|---|---|---|---|
| `anthropic_thinking` | `anthropic` | `thinking`: the client's adaptive object unchanged, else an enabled budget below `max_tokens` | no `thinking` | minimal … xhigh | none |
| `openai_reasoning_effort` | `openai`, `openai_compatible`, `ollama` | `reasoning_effort` | `reasoning_effort: "none"` | low, medium, high | none |
| `openai_responses_reasoning` | `openai` | `reasoning.effort`, `reasoning.summary` | `reasoning.effort: "none"` | minimal … high | none |
| `openrouter_reasoning` | `openai_compatible` | `reasoning: {effort}`, `{max_tokens}` for a budget, or `{enabled: true}` (adaptive with no level) | `reasoning: {effort: "none"}` | minimal … max | `reasoning` |
| `deepseek_thinking` | `openai_compatible` | `thinking: {type: "enabled"}`, plus `reasoning_effort` when a level is known | `thinking: {type: "disabled"}` | low, high, max | `reasoning_content` |
| `gemini_thinking_config` | `gemini` | `generationConfig.thinkingConfig.thinkingBudget` (adaptive with no level: `-1`) | `thinkingBudget: 0` | minimal … xhigh | none |
| `none` | all HTTP types | nothing (the client's `thinking` is not sent either) | nothing | n/a | none |

A style the upstream protocol does not speak (an `openai` model answering a Responses client with
`openai_reasoning_effort`) falls back to that protocol's default style with the same resolved level.

**Resolution in `client` mode.** A disabled client setting is `off`. Otherwise the level is the client's effort or
budget; Anthropic's top-level `output_config.effort` is the effort when the client's thinking is adaptive or names no
level of its own. With no level, the model's `effort`/`budget_tokens` applies. Adaptive thinking with no level at all
uses the style's "let the model decide" form. A client that sent no reasoning setting gets the model's default level
if one is set, else nothing. `fixed` and `off` record an approximate loss `reasoning_policy` when they override a
client setting.

**What counts as dropped.** With an option in `client`, `fixed` or `off` mode, a Messages request's `thinking` and an
effort-only `output_config` are translated, not reported as dropped. An `output_config` that also carries `format` is
still dropped (`field:anthropic_messages:output_config`). `context_management` (Anthropic server-side context editing)
is always dropped outside Anthropic.

**Not affected:** routing requirements (adaptive thinking or an output effort alone never requires the `reasoning`
capability; an explicit client level or thinking budget still does, so a model in any mode receives those requests
only when it declares the capability), passthrough requests (the client's bytes are forwarded), the gateway's own
intent classifier and connection probe (built without the option), and the CLI bridges (refused: the CLI chooses its
own thinking). On a native Anthropic upstream the option writes only `thinking`; the client's `output_config.effort`
is sent as written in every mode. With an active option the client's own raw reasoning field of the upstream's
protocol (`reasoning`, `reasoning_effort`, `thinking`) is replaced, not sent beside it. A router budget is kept below
the request's `max_tokens`.

Validation: the style must be allowed for the provider type; `auto` takes no style, levels or budgets; `client` and
`fixed` cannot use style `none`; `fixed` needs exactly one of `effort` or `budget_tokens`, a budget only for styles
that take one (not `openai_reasoning_effort`, `openai_responses_reasoning` or `deepseek_thinking`), and an effort the
style sends unless `efforts` lists it; `off` takes no levels; `effort` must be in `efforts` when both are set;
`budget_tokens` ≤ `max_budget_tokens`; `history: field` needs a `history_field` when the style has no default. On
`anthropic` and `gemini` providers, whose styles turn a level into a thinking budget, `effort` and `efforts` must be
among the style's default efforts (minimal … xhigh).

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
