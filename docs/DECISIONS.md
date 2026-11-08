# Decisions

One entry per decision: what was decided and why. A later entry that changes an earlier one names it. Ids are stable
and never reused.

## D-001: Subscriptions run through native CLI bridges; everything else through API keys (owner, 2026-11-05)

- Claude subscriptions run through the vendor `claude` binary, one isolated `CLAUDE_CONFIG_DIR` per account,
  authenticated with a `claude setup-token` token passed as `CLAUDE_CODE_OAUTH_TOKEN`.
- ChatGPT subscriptions run through the vendor `codex` binary, one `CODEX_HOME` per account.
- Harbor Pilot never replays subscription OAuth tokens against vendor HTTP endpoints and never impersonates vendor
  client headers.
- Observing subscription quota executes nothing. The subscription quota adapters stay available as "unofficial,
  opt-in", and each such monitor sets `acknowledge_terms_risk: true`.

**Why:** the vendor binary is the mechanism the vendor ships for a subscription; replaying tokens or spoofing headers
risks the terms of service and the account. The cost is that the bridge host must hold the binaries and the logins.

## D-002: License and code hygiene (owner)

- Apache-2.0 for the application and the Terraform provider.
- Code, comments and identifiers never name reference projects. Reference projects appear only in `docs/` and in the
  named integrations (the Pulse relay, Porch-compatible OIDC, the Quota Harbor importer, the SpaceMan examples).
- Designs observed in MIT-licensed reference code are rewritten from scratch; no code or fixtures are copied. Protocol
  fixtures are synthetic, written from public API documentation.
- Quota Harbor is the owner's own code; the owner approved carrying it over and evolving it.

## D-003: Commits

Commits at logical points, Conventional Commits. The agent never pushes.

## D-010: One binary, one SQLite instance

A single binary serves the API, the gateway and the embedded UI on modernc SQLite in WAL mode, with one writer and a
pool of readers. Multi-writer HA is unsupported (ARCHITECTURE §1).

## D-011: Control-plane API defined in Go types

- huma v2 on chi; OpenAPI 3.1 is generated from Go types and committed as `api/openapi.yaml`. A test fails when the
  committed file differs from the generated one.
- The UI uses types generated with `openapi-typescript`; the Terraform provider uses an `oapi-codegen` client from a
  vendored copy of the spec.
- Data-plane (streaming protocol) handlers are plain chi handlers, documented in `docs/contracts/gateway.md`.

**Why:** a generated spec cannot drift from the code, and both clients get types for free.

## D-012: The database is the only source of managed configuration

The file holds only process bootstrap settings. Import and export bundles are explicit operations, never a live
second source (ARCHITECTURE §6).

## D-013: Separate `traces.db`

Trace content has its own SQLite file so it is left out of ordinary backups, can be vacuumed and deleted on its own,
and can be crypto-erased per key bucket.

## D-014: Secrets

- The master key is a 32-byte KEK from `security.master_key_file` / `HP_MASTER_KEY_FILE` (preferred) or
  `HP_MASTER_KEY`, base64 or hex.
- A provider secret is either a reference (`env:`, `file:`) resolved when used and never stored, or a value sealed
  with AES-256-GCM under a random per-secret DEK that the KEK wraps (stored with `kek_id`).
- `harbor-pilot keys rotate` re-wraps the DEKs under a new KEK.
- The API reports only whether a secret is set, its source and a fingerprint.

## D-015: Gateway credential format

- `hpk_<8-char prefix>_<43-char base64url secret>`: 256 bits of entropy.
- Only the prefix and `HMAC-SHA256(pepper, secret)` are stored. The pepper is derived from the KEK with HKDF, label
  `harbor-pilot/token-pepper/v1`.
- Shown once, at issue.
- Scopes: `gateway:invoke`, `gateway:models`, `usage:read` and `tokens:mint`. A `tokens:mint` holder can mint
  short-lived child tokens with bound external refs (for example orchestrator run tokens).

## D-016: Canonical IR written from scratch

The IR is typed and carries per-protocol `Extensions`. Opaque signed or continuation state is a sealed part: it
returns only to the same protocol family and upstream lineage; otherwise it is dropped under an explicit `degrade`
policy that records the loss, or the request is rejected. It is never turned into text.

## D-017: Failover only before the first byte

- An attempt fails over only when no byte has reached the client and the failure is classified as not executed:
  connect error, 401/403/429 before any body, or an explicit provider "not processed" signal.
- An ambiguous execution (a timeout after sending, a 5xx after the body was sent) is never replayed.

## D-018: Expr for conditions and scores

- `expr-lang/expr` with a typed environment, a node limit (`MaxExprNodes`, 500), a memory budget and pure built-ins
  only.
- Missing values are explicit: `known()`, `stale()`, `fresh_within()`.
- Decision tables declare their hit policy: `first`, `unique` or `collect`.

## D-019: Ledger and reservations

The ledger is append-only and idempotent. Reservations are taken in one `BEGIN IMMEDIATE` transaction; the single
writer serialises them, so concurrent requests cannot over-allocate. A leased reaper settles abandoned reservations,
including at startup.

## D-020: Hot-path isolation

Telemetry, the observer and trace capture use bounded channels that drop rather than block. Events and notifications
leave only through the outbox. A Pulse or notification outage cannot stall a stream.

## D-021: Pulse mapping

| Field | Value |
|---|---|
| Subject | `evt.harborpilot.<area>.<entity>.<action>` |
| Type | `harborpilot.<area>.<entity>_<action>.v1` |
| Message id | the event ULID (JetStream deduplication) |
| `ce-source` | the destination's `pulse_source`, default `harbor-pilot` |
| Client | `github.com/aurimasniekis/go-pulse/pkg/pulse` |

Delivery is at least once.

## D-022: UI stack carried over (superseded by D-034)

The first UI kept the carried-over stack (React, TanStack Router/Query/Table/Virtual, CSS Modules on `tokens.css`,
ECharts, lucide-react, zod, Vite, Vitest, Playwright with axe), changing only the branding.

## D-023: Auth parameters

- **Passwords:** Argon2id (64 MiB, t=3, p=2, 16-byte salt, 32-byte key) in PHC format with versioned parameters;
  NFKC-normalised, 12 to 1024 bytes.
- **Sessions:** server-side, storing the token hash. Cookie `__Host-hp_session` (`hp_session` on plain HTTP),
  `Secure`, `HttpOnly`, `SameSite=Lax`. Default lifetime 30 minutes idle, 12 hours absolute (configurable).
- **CSRF:** same-origin `Origin` (or `Sec-Fetch-Site`) plus an `X-CSRF-Token` header carrying an HMAC of the session's
  CSRF secret.
- **OIDC:** authorization code with PKCE S256, state and nonce kept server-side; ES256, RS256 or EdDSA ID tokens; users
  keyed by iss+sub; groups from a dotted-path claim (`porch.groups` for Porch).

## D-024: Notifications are a separate consumer

Notification rules read domain events from the outbox. Each channel has its own deliveries with a lease, backoff with
jitter honouring `retry_after`, dead letters, quiet hours and digests.

## D-025: Masking placeholders (superseded by D-030)

The first design used `HPSEC_<26 base32>` scoped to principal, binding and session, with a 2-hour TTL. D-030 replaced
the format, scope and lifetime.

## D-026: Bridge isolation

- Each account has its own config directory under `<data>/bridges/<kind>/<account_id>/`, guarded by an advisory lock.
- Each request runs in a fresh working directory with a scrubbed environment and the CLI's built-in tools off (exact
  flags in D-038).
- Client tools are exposed through a per-request loopback MCP endpoint whose bearer token travels in a 0600 file or the
  child's environment, never in arguments.
- The user's global credential file is never shared or rewritten.
- Claude Code's `--bare` is not used: it never reads OAuth or keychain credentials, so it cannot work with
  setup-tokens. Isolation comes from the flags in D-038 and a per-account `CLAUDE_CONFIG_DIR`.
- In `-p` mode Claude Code silently skips settings files that fail validation, so the bridge runs a start-up self-test
  (`docs/contracts/harness-wire-facts.md`).

## D-027: Quota monitors and budgets are managed resources

- A `quota_monitor` binds an adapter, options, write-only credentials and optionally an account. Fresh observations
  feed the linked account's routing health: a global bucket at or below `exhausted_below` makes it unavailable until
  reset. Stale or unknown values never decide.
- A `budget` limits spend for a scope (global, workspace, project, binding, account) per day, week or month in one
  currency, counting only the listed cost categories (by default estimated and actual API charges, never
  subscription list-equivalents).
- Budgets are checked inside the reservation transaction; threshold events are written to the outbox once per period.
  One-time budgets and re-arming on a limit raise: D-044.
- Tokens from in-app sign-in are sealed under the master key in `quota_oauth_tokens`.

## D-028: Claude Code bridge conversation model

Verified against `claude` 2.1.318 with a synthetic upstream (`docs/contracts/harness-wire-facts.md` §1.3):

- Multi-message stream-json input does not reproduce a conversation, so the bridge writes the history as a session
  transcript, starts the CLI with `--resume` and sends the last user turn on stdin.
- After a client tool call the CLI is parked on its blocked MCP `tools/call` (matched by
  `_meta["claudecode/toolUseId"]`); the client's tool result answers it and the same process continues. Plain
  follow-ups go to an idle process's stdin. Processes are matched by account, model, system prompt, tool set and a
  conversation fingerprint.
- With no parked process (restart, eviction, timeout), the history including the tool result is resumed and the CLI is
  asked to continue; this records an approximate `tool_continuation` loss.
- CLI retries are off (`CLAUDE_CODE_MAX_RETRIES=0`) so the gateway's failover decides; compaction, CLAUDE.md loading,
  auto memory and tool search are off because the client owns its context.

## D-029: Trace key partitioning

- One data key per (trace policy, UTC expiry day), wrapped by the master key with `trace-key:<bucket>` as associated
  data and stored in `trace_keys`, expiring at the end of that day.
- Each trace section is sealed separately with AES-GCM, associated data `<trace id>|<section>`.
- Reads check `expires_at` first. The sweep deletes expired traces, parts and search entries in batches of 500, then
  destroys bucket keys with no live trace; ciphertext left in free pages, the WAL or a backup without the key row is
  then undecryptable.
- The FTS5 index receives only text redacted by the trace policy and by the masking that ran (D-037). Deletes use
  `secure_delete` and FTS5 secure-delete, then merge the index and truncate the WAL.

## D-030: Masking precedence and placeholders

- Overlapping detections merge into one span with the strongest action: block > allow > mask > warn. Allow exempts a
  span from mask and warn, never from block.
- Placeholders are `HPSEC_` plus 20 random base32 characters, stable for a value within one scope (binding,
  credential family, and session when known) and different across scopes.
- The mapping is memory-only, expires after 24 hours idle and is bounded (D-037); after a restart echoed placeholders
  cannot be restored.
- Trace redaction is separate and irreversible (`[redacted:<rule>]`).

## D-031: Compatibility breakers and passthrough

- Breakers are keyed by provider and protocol path (`<inbound>><upstream>`) and count only compatibility failures
  (unparseable output, schema rejections), not capability mismatches, auth errors, rate limits or outages.
- Three failures in five minutes open a breaker for two minutes, doubling after each failed probe up to an hour. Half
  open runs one request at a time; two successes close it. Operators can force it open or closed, or run a live
  canary.
- While open, a plan skips the candidate (`on_breaker_open: fail`) or uses native passthrough (`passthrough`), which
  needs a native upstream and losses authorised in the plan. Required masking without `allow_unmasked_fallback`, or a
  blocking budget under `unenforceable_budget: block`, refuses passthrough. Trace capture is suspended.
- A request is served in one mode; streams are never spliced across modes and executed attempts never replayed.

## D-032: Protocol Watch baseline

Seeded from this build's own encoders, then extended by sampling one in twenty healthy upstream streams (64 KiB each);
passthrough samples (256 KiB) are compared with it. Findings are fingerprinted by kind, direction, protocol, provider,
path and adapter version, one work item per fingerprint.

## D-033: Event relays and notifications are separate outbox consumers

- Each destination and the notification engine keep their own cursor over `event_outbox`. A new consumer starts at
  the end; history is sent only by explicit replay.
- Destinations deliver in order and stop a round at the first failure; notification channels are independent.
- Webhook signatures are `t=<unix>,v1=<hex>`; during rotation both secrets sign.
- Notification failures never create events, and rules cannot subscribe to `notification.*` or `event.*`, so a
  failing channel cannot cause a storm.

## D-034: UI rebuilt on Mantine (supersedes D-022)

- **Why:** the owner's review of the first UI (2026-11-05) found nested cards, copy in place of hierarchy, ungrouped
  navigation and tables overflowing on phones, and asked for an established component framework.
- **Stack:** Mantine 9 (`core`, `hooks`, `dates`, `spotlight`, `notifications`), pinned; TanStack Router, Query and
  Table, ECharts, lucide-react, zod, Vitest and Playwright with axe stay. `tokens.css` becomes the Mantine theme.
- **Rules:** one container level; headline figures before detail; help in tooltips and docs, not on the page; grouped
  navigation with a command palette; lists that become cards on narrow screens.

## D-035: Secret references are confined

- **Why:** the 2026-11-05 security review showed an operator could point `credential_ref` at `env:HP_MASTER_KEY` and a
  provider `base_url` at their own host, sending the master key out on the next request; the same held for any process
  variable and for `file:` references under a broad root.
- **Rule:** `env:` references read only variables with an allowed prefix (`security.secret_env_prefixes` /
  `HP_SECRET_ENV_PREFIXES`, default `HP_SECRET_`). Prefixes must be at least three characters, and no `HP_` prefix
  other than `HP_SECRET_` is allowed. `HP_MASTER_KEY`, `HP_MASTER_KEY_FILE` and the previous-key variables are always
  denied. `file:` references may not read the data directory or a master key file, and roots containing them are
  rejected at startup.
- **Enforcement:** when a reference is saved (spec validation, OIDC input) and when it is resolved.
- **Upgrade impact:** references to unprefixed variables stop resolving; rename them (for example
  `ANTHROPIC_API_KEY` to `HP_SECRET_ANTHROPIC_API_KEY`) or add a dedicated prefix.

## D-036: SSO links an existing account only when an administrator asked

- **Why:** in pre-provisioned mode, any verified email matching a local account was linked on first sign-in,
  administrators included, and `allowed_domains` was ignored, a takeover where users can choose their email.
- **Rule:** an existing account is linked only when an administrator marked it for that provider
  (`sso_link_provider`); the mark clears on linking. `allowed_domains` applies in both modes. Just-in-time
  provisioning never takes over an existing email.
- **Also:** post-login redirects accept only a same-origin path without control characters or backslashes; callbacks
  re-check that the provider is still enabled.

## D-037: Data-protection hardening after the security review

- **Restore only into text.** Streamed restoration follows each part's kind: text is restored, tool-call arguments
  only through the allowlisted path, reasoning and unannounced parts never. Tool-argument restoration parses URLs,
  refuses userinfo and backslash authorities, and refuses any value naming a host outside the allowlist, including
  bare host names and IP addresses.
- **Signed reasoning blocks rather than leaks.** Signed thinking blocks must round-trip byte for byte and cannot be
  masked; if one holds a masked or blocked value the request is blocked (rule `signed_reasoning`). Dropping the block
  would break the conversation in harder-to-explain ways.
- **Unresolvable configured values fail closed.** A required policy blocks; an optional one, or one allowing unmasked
  fallback, masks with the detectors that still work. Failures are never cached; references are re-read every minute.
- **Placeholder scope includes the credential.** Sessions narrow a scope but never join two credentials' scopes.
- **Vault budgets.** At most 200,000 mappings or 128 MiB overall and 5,000 or 8 MiB per credential. Over budget a value
  is still masked, but cannot be restored and cannot evict other credentials' mappings.
- **Trace search index** is redacted by the masking that ran as well as the trace policy; deletes use secure delete,
  merge the index and truncate the WAL.
- **Error scrubbing.** Every failed attempt and in-stream error is scrubbed of the attempt's credential (including JSON-
  and URL-escaped forms) and key-shaped strings before it is returned, recorded or observed.
- **Rotation is complete.** `keys rotate` covers quota sign-in tokens and trace bucket keys, and clears previous keys
  only after checking nothing still uses them.
- **Sandbox and stored values.** Testing a saved policy against its configured secret values needs `secrets.write`,
  because match offsets would confirm guesses.

## D-038: Network and process hardening after the security review

**CLI bridges**

- **Codex built-in tools** are turned off with `-c` flags, verified against codex 0.166.0 with
  `codex debug prompt-input`: `features.shell_tool`, `features.unified_exec`, `features.view_image`,
  `features.image_generation`, `features.multi_agent`, `features.apps`, `features.plugins`, the browser and
  computer-use features, `features.skill_search`, `features.tool_suggest`, `features.sleep_tool`, and
  `web_search="disabled"`. What remains (the code-mode wrapper, which has no file system or network of its own, and
  the patch tool) is contained by `--sandbox read-only` and a fail-closed check: any `command_execution`,
  `file_change` or `web_search` item, or a call to an MCP server other than the client's, stops the turn and kills the
  process group.
- **Claude Code built-in tools:** `--tools ""`, `--strict-mcp-config`, `--allowedTools mcp__client`,
  `--setting-sources ""`. A test asserts these arguments.
- **No secrets in arguments.** Claude Code gets its MCP configuration and system prompt as 0600 files
  (`--mcp-config`, `--system-prompt-file`); Codex gets them in a 0600 per-run profile (`-p hp-<random>`). The MCP token
  is a bearer header (Claude) or `HP_MCP_TOKEN` (Codex `bearer_token_env_var`). The MCP server accepts only
  `Authorization: Bearer`, checks `Host`, and has read and idle timeouts.
- **Content does not outlive processes.** Claude transcripts, per-workdir caches and per-run files are removed when the
  process ends. Codex rollouts are removed once older than the thread TTL (24 h), and prompt history is off
  (`history.persistence="none"`). A periodic sweep removes crash leftovers.
- **Processes** run in their own process group and are killed as a group; spawns hold a real slot reservation; parked
  processes and resumable threads are reused only within the same client scope (`executor.Request.Scope`); files of
  deleted accounts are removed.

**Outbound network**

- Quota adapters use the `netpolicy` transport (public destinations, no environment proxy); private endpoints need
  the monitor's `allow_private_network`. Pulse dials through the same policy and reports failures generically.
- Link-local ranges and the Azure WireServer and Oracle Cloud metadata addresses are always blocked. IPv4 embedded in
  NAT64, 6to4, IPv4-compatible and Teredo forms is checked as IPv4.
- Stored secrets do not follow a destination: a channel, event destination or quota monitor moved to another origin
  needs its secrets re-entered; a provider whose base URL moves has its accounts' stored credentials cleared, audited.
  Provider base URLs need https unless `allow_private_network` is set.

## D-039: Step-up re-authentication and throttling hardening

- **Step-up:** reading unredacted trace content, exporting traces and setting a first password on an SSO-only account
  need a sign-in within 10 minutes (`auth.StepUpWindow`). Password sessions use `POST /api/v1/session/reauth`; SSO
  sessions sign in again. API tokens never qualify; automation that needs content uses masked modes.
- **Throttling:** login attempts reserve their failure in the same writer transaction as the lockout check. Gateway
  credential failures are limited per address (60 a minute, burst 60); valid tokens from a blocked address are refused
  too until it refills, an accepted trade-off.
- **Proxies and exposure:** client addresses join every `X-Forwarded-For` line. Token-less `/metrics` is served only to
  direct loopback requests without forwarding headers, never with trusted proxies configured. Demo mode refuses a
  non-loopback address without `--demo-allow-public`.

## D-040: Run token provenance and budget refusals (mint budget check: see D-043)

- **Refs.** A minted child credential stores its parent's bound refs plus the refs sent at mint. The binding's
  `external_refs` are not copied in; the gateway applies them per request, for keys the credential does not bind. A
  mint that sends a parent or binding key with a different value is refused with `400`. Orchestrator bindings should
  pin only what every run shares (for example `spaceman.fleet`) and leave per-run keys (`spaceman.space`,
  `spaceman.run`, `project_id`) to the mint.
- **Creator.** The gateway mint endpoint has no console principal, so the minting credential acts as one (principal
  kind `credential`, id `crd_…`). It is the child's `created_by` and the audit actor of `gateway_credential.issue`.
- **Budget refusals are policy blocks.** When blocking budgets refuse every candidate at reservation, the request is
  recorded as `blocked` (error `budget_exceeded`, HTTP 429) and emits `access.request.blocked` per request, like the
  other policy refusals. `budget.threshold.exceeded` stays a once-per-period transition (D-027). A request that
  reached a provider on an earlier attempt stays `error`.
- **Mint does not check budgets.** A `check_budget` mint option (429 when a covering blocking budget is exhausted) is
  deferred: account-scoped budgets can't be resolved before routing picks an account, and the option would be new API
  surface for the mint contract. Orchestrators can read `GET /api/v1/budgets/status` before minting.

## D-041: Usage and activity filter and group by trusted references

- **Shape.** `GET /api/v1/usage/summary`, `/activity/requests` and `/activity/sessions` accept `ref=<key>:<value>`,
  repeatable (at most 4, ANDed). The value is split at the first `:`, the key follows the bound-ref key rules, and the
  value is 1 to 256 bytes. `usage/summary` also accepts `group_by=ref:<key>`, where rows without the key form the `""`
  group, and a `binding_id` filter. Permissions stay `usage.read` and `activity.read`.
- **Paged groups, whole totals.** `group_by=ref:<key>` groups are ordered by key and paged with the usual `cursor`,
  `limit` (default 50, at most 500) and `next_cursor`. There is no group cap. `totals` always cover every matching row
  in the range, whatever the page. Other groupings return all their groups, ordered by tokens as before. A group
  cursor carries a short hash of `group_by`, the filters and `from`/`to` as given (not the resolved times, so a range
  that defaults to now still pages); a cursor from another query is a 422 at `query.cursor` rather than a page that
  silently skips groups.
- **Trusted only.** Only `trusted_refs` (mint-time `bound_refs` and the client's `external_refs`) are indexed or
  filterable, never claims or headers. A client-filterable ref would let any agent add usage to another run's totals
  or hide its own. Refs are trusted per client: a minting credential chooses its runs' values, so consumers that share
  a ref namespace across clients also filter on `binding_id` or a key the client pins.
- **External session.** `ref=session:<value>` finds a bound session's requests, usage and `ses_…` id. The raw value is
  not added to `/activity/sessions` (session keys stay HMAC-only), and `session_id` keeps meaning the internal id.
- **Storage.** New tables `ledger_refs` (written in the ledger row's transaction, so totals never lose attribution) and
  `request_refs` (written with the request row by the telemetry writer). Corrections without refs copy the corrected
  row's; startup recovery copies them from the request row when it exists. The migration backfills both tables from
  `requests.trusted_refs`; a stored value that isn't a JSON object indexes nothing rather than failing the upgrade.
  Empty values are never indexed (writers and backfill alike), so they can't merge into the `""` group of rows
  without the key. `ledger.run_ref` stays for compatibility and is set on every gateway ledger row.
- **Hot path.** Two ledger writes run before a dispatch: the model classifier's usage row and the `unknown_usage` row
  of an ambiguous failed attempt. They were already synchronous. With refs to index they now run in one `BEGIN
  IMMEDIATE` transaction with a single multi-row `ledger_refs` insert, so the extra cost is one statement; without
  refs they stay a single statement outside a transaction.
- **Fees under a filter.** `subscription_fees` and the `subscription_fee` total stay whole-range figures and ignore
  filters. Allocation keeps the account's unfiltered token total as its denominator, so a filtered slice gets its
  share of the fee, not the whole fee.
- **Not yet.** `/usage/series` has no `ref` filter; that is a follow-up. (Added in D-043.)

## D-042: Mid-conversation system messages follow a per-model support list (2026-11-07)

- **Problem.** Since 0.1.2 a `system`-role entry inside `/v1/messages` `messages[]` reaches Anthropic upstreams
  unchanged. A routing plan that maps the request to a model without support for mid-conversation system messages
  got the provider's 400.
- **Decision.** The Anthropic request builder (`internal/protocol/anthropic/models.go`) keeps a list of models known
  not to accept the role. For those it sends the entry as user text in place and records `mid_conversation_system`
  (approximate), as it already does for other targets without support. Message-level fields (`clear_at`,
  `output_config`) are dropped and reported as `system_message_field:*` losses. Every other model, including any
  model the list doesn't name, still gets the entry unchanged.
- **Default is "supported".** The list is negative on purpose: an unknown or newer model keeps the pass-through, so
  the provider stays the judge, and a list that is out of date costs one 400 rather than silently rewriting requests
  to models that support the role.
- **Source.** The list comes from public Anthropic documentation only. The mid-conversation system messages page
  names the supporting models (the Claude 5 generation and Claude Opus 4.8) and says the feature isn't available on
  Claude Sonnet 5. Every earlier model in the public deprecations table is also on the list: Claude Opus 4.7, 4.6,
  4.5, 4.1 and 4, Claude Sonnet 4.6, 4.5 and 4, Claude Haiku 4.5, and the Claude 3 models. Names match as an alias
  (including the `-0` aliases `claude-opus-4-0` and `claude-sonnet-4-0`), a dated snapshot, a `-latest` alias, or with
  a platform prefix or version suffix (`-v1:0`, or a bare `-v2` as in `claude-3-5-sonnet-v2@20241022`).
- **Not a model-catalog capability.** Provider model `capabilities` are positive and optional, and routing treats an
  empty list as "undeclared". A new positive capability would mark every catalog model that declares capabilities as
  unsupported until an operator added it. So this stays protocol knowledge in the adapter, next to the other
  per-protocol rules. An operator override can be added later if a model is ever missing from the list.
- **Scope.** The CLI bridge keeps inlining for every model, and explicit native passthrough (breaker open) still
  forwards bytes unchanged.

## D-043: Optional budget check on mint; reference filters on the usage series (2026-11-07)

(One-time budgets are checked the same way, inside their window only: D-044.)

- **`check_budget` on mint.** `POST /v1/harbor/credentials` accepts an optional boolean `check_budget` (default
  `false`, so a mint without it behaves exactly as before, D-040). When it is `true` and an enabled `block` budget
  covering the child's scope has its current period used up, the mint is refused with `429` in the gateway's
  OpenAI-style error envelope: `type` `insufficient_quota`, `code` `budget_exceeded`, the budget's name and id in
  `message`, and `budget_id`. Validation runs first, so an invalid mint is still a `400`.
- **Scope.** The child's scope is what is known before routing: the parent's binding, the project its requests will
  be attributed to (a bound `project_id` the binding allows, otherwise the binding's own project; the same rule the
  request path uses, now one helper, `controlplane.CredentialProject`), that project's workspace, and global budgets.
  Account budgets are skipped: routing picks the account per request. A per-request `X-HP-Project` hint isn't known
  at mint either.
- **Same figures.** The check reads each budget with the code behind `GET /api/v1/budgets/status` (settled ledger
  cost plus active reservations) and applies the request path's test for a request with no estimate: spent plus
  reserved at or over the limit. A mint that passes can still see its first request refused, because the request
  adds its own estimate, or because routing picks a paid account whose budget is exhausted.
- **Local and free routes.** Budgets ignore local and free billing on the request path. The account isn't known at
  mint, so the check is skipped only when no request of the binding could reach a budgeted account: no pool member
  passes the binding plan's billing and provider restrictions with a billing other than `local` or `free`. Any alias
  can be requested by name, so every pool counts, not only the plan's mappings. Otherwise the budget is counted
  whatever account a request would use, so a run whose requests would all go to local or free accounts can still be
  refused with `429` when the plan could also reach a paid one. Restrict the plan's `allowed_billing` to `local` and
  `free` to avoid that.
- **Read failures.** If the check can't read usage, the mint fails with a generic internal error (`502`, code `null`,
  message `could not check budgets`), and the cause is logged on the server, never returned.
- **No audit entry, no event.** A refused mint writes nothing. The audit log records changes, and nothing changed.
  `budget.threshold.exceeded` was already sent once for the period when the budget ran out (D-027), and
  `access.request.blocked` describes a gateway request with a request row, which a mint isn't. An orchestrator that
  retries a refused mint would otherwise fill the outbox with copies of a state that is already known.
- **Usage series by reference.** `GET /api/v1/usage/series` accepts the same `ref=<key>:<value>` filters (repeatable,
  at most 4, ANDed, trusted refs only) and `binding_id` as `/usage/summary`, parsed by the shared parser and applied
  with the same `ledger_refs` conditions (`usageapi.refWhere`), as bound parameters. They combine with `filter`/`value`
  and with `by`. Over the same range the buckets add up to the summary's totals under the same filter, except
  `subscription_fee`, which the series never reports. The Spend page now keeps its chart under a reference filter.

## D-044: One-time budgets (2026-11-08)

- **Shape.** A budget with `period: "once"` has a total limit that never resets. Existing day/week/month budgets are
  unchanged. Two optional RFC 3339 spec fields, `starts_at` and `ends_at`, apply only to one-time budgets and are
  rejected on recurring ones; so is a non-empty `timezone`, since a one-time budget has no period boundaries. Both
  are stored in UTC, truncated to the second.
- **Window.** Spend counts from `starts_at`, or from the budget's creation when it is empty, so an omitted field
  (Terraform sends `""`) never moves the window. A backdated `starts_at` counts earlier spend. After `ends_at` the
  budget no longer applies (state `expired`); before `starts_at` it doesn't apply yet (state `scheduled`). Recreating
  a budget starts a new window unless `starts_at` is set.
- **Changes.** Raising the limit keeps the window and spend; it unblocks at once and re-arms threshold events for
  that window (this also applies to recurring budgets; `budget_state` records the limit each event fired at, and a
  threshold fires again only when the current limit is higher). `POST /api/v1/budgets/{id}/reset` (`config.write`,
  If-Match) sets `starts_at` to the server's time, truncated to the second like every `starts_at` (spend earlier in that
  second still counts), through the normal mutation path: audit, history, outbox,
  snapshot, with the revision summary `reset budget <name>`. On a Terraform-managed budget it shows as drift.
- **Same figures.** `usage()` reads the window from one helper (`BudgetSpec.Window`); `Check`, the mint check (D-043)
  and `GET /budgets/status` use the same `applies()`, `exhausted()` and a new `active()` predicate, so they never
  disagree.
- **Status and events.** `period_end` is `null` for an open-ended one-time budget, `projected_spend_e10` is `null`
  without an end (and outside the window), and every item gains `remaining_e10`. State precedence: `disabled` >
  `expired` > `scheduled` > `exceeded` > `warning` > `ok`. `budget.threshold.*` stays once per window (D-027): once
  ever for a one-time budget, again after a reset or a limit raise. Payload keys are unchanged. Client-facing texts
  say "in total" instead of "this <period>".
- **Spend cache.** Summing a one-time budget's whole window on each reservation, inside the writer transaction,
  grew with the window: `BenchmarkCheckOneTimeBudget` (200,000 rows over 12 months, one new row per reservation)
  measured 165 ms per check for a project, 214 ms for a workspace, 302 ms for a global budget and 686 ms for global,
  workspace and project budgets together. A `ledger(workspace_id, at)` index did not help and covering indexes only
  halved it, so no index was added. Instead `accounting.Budgets` keeps, per budget, the settled sum of each cost kind
  over ledger rows up to a rowid watermark, and a check reads only the rows past it. The ledger is insert-only
  (nothing updates, deletes or renumbers its rows; backups use `VACUUM INTO` and restore runs with the server
  stopped), so committed rowids only grow. The key is the budget id, window start and end, currency and scope; cost
  kinds are not part of it because every kind is kept. A reset, an edited window or scope, or a new calendar period
  changes the key, and a different key is a miss that reads the whole window. Entries are stored only from committed
  rows: reads outside a transaction store at once, and `Check` returns a func that `Reserve` calls after its
  transaction commits, because rowids of a rolled-back insert are reused. A late store from an older snapshot never
  replaces a later watermark. Check, the mint check and Status share the cache, so their figures stay identical,
  and recurring budgets use it too. A primer refreshes every budget on the read pool at start-up and after each
  configuration change, so a miss rarely lands in the writer. Measured warm, with one new row per reservation:
  0.4 ms for each single once budget and 0.7 ms for the three together (a monthly budget: 0.4 ms; 0.8 ms for three).
  A cold miss still costs the full read (174 to 704 ms for the cases above). The rowid walk past a watermark visits
  every row settled since then in any scope, so a primed budget on a quiet project or binding paid for other
  projects' traffic inside the writer (`BenchmarkCheckQuietScopeGap`: 28 ms for a 50,000-row gap and 109 ms for
  200,000, where an indexed read of the month took about 2 ms). Each entry therefore also counts its rows, and a
  refresh re-reads the window through the scope (or `at`) index, replacing the entry, once the gap exceeds both
  2,048 rows and ten times that count. The walk measured 0.3 to 0.5 µs per gap row and the indexed read 2.5 to
  5.5 µs per counted row. `NOT INDEXED` stays on the walk: EXPLAIN QUERY PLAN shows that without `ANALYZE` statistics
  the planner takes the scope index for the delta, which visits the whole window. The primer also runs every 30 s (`PrimeEvery`), so a quiet
  budget's gap stays short, and that pass drops entries of deleted budgets. With the bound, 200,000 foreign rows
  cost about 2 ms for a monthly project budget and about 23 ms for a year-long one-time budget holding 4,000 rows,
  the price of reading its own window. An entry is stored only while its key is still the budget's key in the
  current snapshot at that time, so a check that read before a reset or an edit and commits after it cannot
  replace the new window's entry. The ledger is never purged today; any
  future retention must keep the windows of active one-time budgets and drop the cache.
- **Also in this change.** `GET /api/v1/system` (version, commit, started_at, schema_version; `config.read`, so a
  service-principal token can read it; no host names, data directory, key ids or secrets), and
  `GET /api/v1/budgets/status` filters: `project_id` (+ `binding_id`) returns budgets covering that scope by the
  request path's `applies()`, whatever their state; `scope`/`scope_id` returns budgets defined on exactly that object.
  The commit comes from a `main.commit` build flag, falling back to the Go build info.
