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

## D-040: Run token provenance and budget refusals

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
