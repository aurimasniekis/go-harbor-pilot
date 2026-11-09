# Changelog

Notable changes to Harbor Pilot. Until 1.0, a minor version may change configuration or defaults; the
[upgrade notes](https://aurimasniekis.github.io/go-harbor-pilot/operators/upgrades) say what needs action.

## Unreleased

Claude Code bridge: a fix for the 400 that broke every turn after the first, session retention so a paused
conversation reads the prompt cache instead of writing it again, and fewer translation losses. Failed requests are
attributed to the account they reached. No migrations. New options: `bridges.claude_code.shelf_max_per_account` and
`shelf_max_bytes`.

### Claude Code bridge

- **Sessions kept up to the cache TTL (D-045).** When a bridge process ends between turns (idle timeout, eviction,
  tool timeout or a crash), its CLI session is kept on disk for up to the conversation's prompt-cache TTL (5 minutes
  or 1 hour) and the next turn resumes it with `--resume`, so the prompt prefix is the CLI's own and the cache is
  read rather than written again. A conversation waiting for tool results resumes with the results added to the
  session. Bounded by `bridges.claude_code.shelf_max_per_account` (default 16) and `shelf_max_bytes` (default 256 MiB),
  oldest first; deleted at expiry, on account removal and at shutdown. This changes the privacy rule that
  conversation files are removed when the process ends: content may now stay up to 1 hour after it.
- **Which session served a turn** is recorded in the ledger provenance (`bridge_session`: `live_process`,
  `shelved_session`, `transcript`, `new_session`) and shown on the request page next to the cache figures.
- **Output settings are passed.** `max_tokens` goes to the CLI (`CLAUDE_CODE_MAX_OUTPUT_TOKENS`, up to the model's
  limit) instead of being a `max_output_tokens` loss. An effort-only `output_config` on a mid-conversation system entry
  sets the CLI's `--effort` and no longer records `system_message_field:…:output_config`. System entries without text
  no longer record `mid_conversation_system`. Effort and the output limit are now part of the process match (a parked
  process started at another effort used to be reused).
- `scripts/live/claude-code-bridge-cache.sh` checks cache reads and writes per turn on a deployment.

### Fixed

- **Claude Code bridge: 400 on every turn after the first.** A client that marks message blocks with
  `cache_control: {"ttl": "1h"}` (as Claude Code on a subscription does) had those markers copied into the bridge's
  transcript and stdin turn. The CLI puts its own breakpoints on the system prompt and tools it adds ahead of the
  conversation, so the client's markers could follow a 5m one or push the total past four, and the upstream refused
  the request ("a ttl='1h' cache_control block must not come after a ttl='5m' cache_control block"). The bridge now
  removes every client `cache_control` (message blocks and blocks inside tool results) and records a `cache_control`
  loss (approximate): the CLI's own breakpoints replace the client's. The client's TTL is kept: the CLI runs with
  `CLAUDE_CODE_PROMPT_CACHE_TTL` set to `1h` if any client breakpoint asks for `1h`, to `5m` if they all ask for
  `5m`, and to `1h` when the client set none. The TTL is part of the parked-process match, so a process is reused
  only for a conversation with the same TTL. The CLI also runs with `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1`, keeping
  repository status out of its prompt.
- **Claude Code bridge: an upstream 400 is no longer failed over.** The CLI reports the upstream's 400 without an
  error type, so the bridge passed it on as a generic upstream error, and the gateway retried it on the next
  subscription account, which refused it the same way. It is now `invalid_request`: one attempt, and the client gets
  a 400. Rate limits, overload and server errors are still retried.
- **Prompt-cache rules on Anthropic requests.** The request builder now enforces the Messages API's breakpoint rules on
  the body it sends, since merging, inlining or dropping blocks can break a request the client sent correctly. At most
  four breakpoints (a top-level automatic one counts): the earliest are removed and the last four kept
  (`cache_control_limit`, drop). TTLs must not increase across tools, system and messages, tool-result content
  included: a `1h` breakpoint after a `5m` one is lowered to `5m` (`cache_control_ttl`, approximate). A request that
  already follows the rules is sent unchanged. `cache_control` on blocks inside a tool result is now kept; it used to
  be dropped silently.
- **Failed requests are attributed.** A request that failed after reaching an account used to be recorded and logged
  with empty `final_model`, `account_id`, `provider_id` and `mode`. It now carries the last attempt's account,
  provider, model and mode, for HTTP upstreams and CLI bridges alike, and the translation losses of the request as
  built for that attempt (they used to be recorded for successful requests only). The request page says **Failed on**
  that account and labels the route step **Last tried** instead of **Served by**.
- **Skipped candidates are not shown as fallbacks.** A candidate passed over at dispatch (concurrency limit reached,
  local rate limit, open compatibility breaker, budget) used to take an attempt number and make the next attempt look
  like a fallback ("#2 … Fell back to another account" with no #1). Attempts are now numbered in the order they ran,
  a failover reason is recorded only after a real failed attempt, and skipped candidates are listed with the request's
  excluded candidates (stage `dispatch`, with the reason).
- The request page's cost line shows cache writes next to cache reads (both were already recorded, for the CLI
  bridges too).

## 0.1.4 (2026-11-08)

One-time budgets, a spend cache that makes budget checks fast for every period, a system information endpoint and
budget status filters. One migration (`20261108100000000_add_budget_state_limits`) adds two nullable columns to
`budget_state`; no configuration changes. See the upgrade note below: some budget alerts are sent once more.

### Budgets

- **One-time budgets (D-044).** `period: "once"` is a total limit that never resets, with optional `starts_at`
  (empty: from the budget's creation) and `ends_at` (empty: never). Before `starts_at` a budget is `scheduled`, after
  `ends_at` it is `expired` and no longer blocks or warns. `timezone` must be empty for them. In the UI: **Repeats:
  One time**, **Count spend from** and **Ends**, "$x left", **Never** / **Ends in…**, and **Scheduled** / **Ended**
  badges.
- **Start over.** `POST /api/v1/budgets/{id}/reset` (`config.write`, `If-Match`) sets a one-time budget's
  `starts_at` to now, truncated to the second; **Start over…** in the budget's row menu does the same (not on an
  ended budget).
- **Re-armed alerts.** Raising a budget's limit lets `budget.threshold.warning` and `.exceeded` fire again in the
  same period, for recurring budgets too.
- **Status.** `GET /api/v1/budgets/status` items gain `remaining_e10`; `period_end` is `null` for a one-time budget
  without an end, and `state` can be `scheduled` or `expired`. Clients that read `period_end` as always set need to
  handle `null`. New filters: `project_id` (optionally with `binding_id`) for the budgets that cover a project, and
  `scope` + `scope_id` for budgets defined on exactly one object.
- **Spend cache.** Budgets of every period keep their settled spend in memory and read only new ledger rows per
  request, so a budget check no longer grows with its window (for a one-time budget, about 0.4 ms instead of 165 to
  300 ms per budget over 200,000 rows in a year). A background primer sums each window once after start-up and each
  configuration change, and refreshes every budget every 30 s; see the limitations.

### System

- **`GET /api/v1/system`** returns `version`, `commit`, `started_at` and `schema_version` to any principal with
  `config.read`, including service-principal tokens. Builds record the commit (`-X main.commit=…`): release
  binaries, the release and CI images, and `make docker`.

### Upgrading

- Budgets that already sent `budget.threshold.warning` or `.exceeded` in the current period send it once more after
  the upgrade: the new limit columns start empty, which counts as a raised limit.

## 0.1.3 (2026-11-07)

An optional budget check when minting run tokens, per-model handling of `system` messages inside `/v1/messages`,
reference filters on the spend chart, and release images built natively for both architectures. No upgrade
actions beyond those for 0.1.2: there are no migrations or configuration changes.

### Gateway

- Run tokens can be refused before a run starts: `POST /v1/harbor/credentials` takes an optional `check_budget`.
  When it is `true` and an enabled blocking budget on the client, the run's project, that project's workspace, or a
  global budget is used up, the mint is refused with `429` and code `budget_exceeded`, naming the budget. The default
  (`false`) keeps today's behaviour. Provider account budgets aren't checked at mint (decision D-043). The check
  is skipped when the client's routing plan can only reach local or free accounts, which budgets ignore. If budgets
  can't be read, the mint fails with a `502` (`could not check budgets`).
- `system`-role entries inside `/v1/messages` `messages[]` are sent as user text, with a `mid_conversation_system`
  loss, when the target Anthropic model is known not to support mid-conversation system messages (Claude Sonnet 5,
  and older models up to Claude Opus 4.7, per Anthropic's public docs, including the `claude-opus-4-0` and
  `claude-sonnet-4-0` aliases and Vertex `-v2` versions). They used to get the provider's 400. Every other model,
  including unknown ones, still gets them unchanged (decision D-042).

### Activity and spend

- `GET /api/v1/usage/series` takes the same `ref=<key>:<value>` filters and `binding_id` as the usage summary, and
  its buckets add up to the summary's totals under the same filters. **Spend** keeps its chart over time while a
  reference filter is on.

### Release images

- Both images are built for linux/amd64 and linux/arm64 with the web and Go stages on the build machine's own
  architecture; the Go binary is cross-compiled, so arm64 builds no longer run `pnpm install` under emulation. The
  runtime images are unchanged. Release image builds share a build cache with CI and time out after 30 minutes, and
  CI now builds both images for both architectures on every pull request.

### Fixed

- A run-token mint that fails for an internal reason (other than validation, permission or budget) answers a `502`
  with the generic message `could not mint the credential` and logs the cause on the server. It used to answer
  `403` with the raw error text.
- End-to-end tests: the accessibility checks wait for the app shell before pressing keys or running axe, and the axe
  scans retry until the page settles, which removes a flaky failure of the skip-link test.

## 0.1.2 (2026-11-07)

Trusted-reference filters for usage and activity, `system` messages inside `/v1/messages`, and fixes found
while running agent orchestrators against 0.1.1. Read the upgrade notes first: one behaviour change can affect
alert rules, and the first start runs a one-off index migration.

### Gateway

- `/v1/messages` accepts `system`-role entries inside `messages[]`, as Claude Code sends them on models that support
  mid-conversation system messages. They used to be rejected with a 400. They now reach Anthropic upstreams
  unchanged. For OpenAI upstreams they become in-place system messages, and for Gemini they become user text. Each
  loss is recorded.

### Activity and spend

- Filter usage and activity by trusted reference: `ref=<key>:<value>` on `/api/v1/usage/summary`,
  `/api/v1/activity/requests` and `/api/v1/activity/sessions`, and `group_by=ref:<key>` on the usage summary, so an
  orchestrator gets per-run totals (for example `group_by=ref:spaceman.run`) and finds a bound session's id. Reference
  groups are paged with `cursor` and `limit`. The usage summary also takes `binding_id`. Existing history is indexed
  when the database is upgraded.
- The UI filters and groups by trusted reference too. **Requests → More filters → Filter by reference** lists one
  run's requests (for example **Run: run_42**, up to four references at once), and a request's verified context links
  to all requests with the same reference. **Spend** has **Filter by reference** and **Group by → Run** (or any other
  reference key), with **Load more** for long lists. Filters and groupings are kept in the page address. The demo's
  orchestrator client now makes its requests with two run tokens.

### Fixed

- Run tokens record the parent token that created them (`created_by` and the audit log) instead of `anonymous`.
- A request refused by a blocking budget shows as **Blocked** and emits `access.request.blocked`, as the operator
  docs describe. `budget.threshold.exceeded` is still sent once per period. This is a behaviour change: see the
  upgrade notes.
- The demo's `spaceman-runs` client pins only `spaceman.fleet` and allows the demo projects, so run tokens can bind
  their own `spaceman.space` and `project_id`.
- Docs: the agent orchestrators page now describes how client and run-token references combine, and the
  `access.credential.issued` data fields match what is sent (`prefix`, `scopes`, `child`).

### Upgrade notes

- **Behaviour change: budget refusals emit `access.request.blocked`.** A request that blocking budgets refuse is now
  recorded as **Blocked** (error `budget_exceeded`, HTTP 429) and sends `access.request.blocked` for every refused
  request, with the budget in `reason`. In 0.1.1 it was recorded as an error and sent no event. Alert rules, webhooks
  and Pulse consumers that subscribe to `access.request.blocked` will see one event per refused request while a budget
  is used up. `budget.threshold.exceeded` is unchanged and is still sent once per period (decision D-040).
- The first start after the upgrade indexes existing history in one migration, before the server listens. Its time
  and disk use grow with the number of requests and ledger rows times their trusted references: as a guide, 1M
  requests plus 1M ledger rows with 3 references each took about 27 s and added about 530 MB. Leave room for that
  next to the database and its pre-upgrade copy. Rows whose stored references aren't a JSON object are skipped.

## 0.1.1 (2026-11-06)

The first release. A `v0.1.0` tag exists, but its release build stopped on a flaky test and nothing was
published from it.

### Gateway

- One base URL and gateway token per client for OpenAI Chat Completions and Responses, Anthropic Messages and Gemini,
  with translation between them where a model needs it, streaming with keepalives, token counting, and native
  passthrough when a provider's protocol changes.
- Subscriptions through the vendors' own CLIs: a Claude Code bridge and a Codex bridge, with isolated per-account
  configuration, their built-in tools turned off and the client's tools bridged.
- Local and OpenAI-compatible endpoints (Ollama, vLLM, LM Studio and others).

### Routing

- Routing plans: model names, ordered fallback groups, hard restrictions, rules and decision tables, with drafts that
  are validated, compared with the live version and checked for whether they would block traffic.
- Failover between accounts before the first byte reaches the tool.
- "Why this route?": every request records the matched rule, each candidate and why it was skipped, every attempt,
  usage and cost.

### Limits and spend

- Quota tracking for subscriptions and provider accounts, budgets enforced in the transaction that reserves a request,
  and spend kept in separate categories (estimates, provider bills and subscription fees).

### Privacy

- Masking of credentials, configured values and optional personal data before a provider sees them, restored in
  replies.
- Optional encrypted captured content, kept for 24 hours to 30 days, crypto-erased on expiry and readable only with an
  audited grant.

### Operations

- Alerts to Telegram, Slack, Discord and webhooks; a durable event outbox to Pulse (NATS JetStream) and signed
  webhooks.
- Local accounts with step-up re-authentication, OIDC single sign-on, automation accounts with API tokens, and a
  readable audit log.
- Online backups and verified restore, a Quota Harbor importer, Prometheus metrics, and `harbor-pilot doctor`.
- One Go binary with the web UI embedded and SQLite storage; release archives for Linux and macOS on amd64 and arm64,
  and container images (`harbor-pilot`, and `harbor-pilot-bridge` with the Claude Code and Codex CLIs) for
  linux/amd64 and linux/arm64.
- Tested reverse proxy configurations for nginx, Apache httpd, Caddy, Traefik and HAProxy.
- A [Terraform provider](https://github.com/aurimasniekis/terraform-provider-harbor-pilot) released alongside, as 0.1.0.

### How it was tested

Every capability is covered by automated tests against fixtures, synthetic upstreams and fake binaries. The reverse
proxy configurations are verified against the real proxies. Nothing has been verified against live provider accounts
yet: the [compatibility matrix](https://aurimasniekis.github.io/go-harbor-pilot/reference/compatibility) and
[known limitations](https://aurimasniekis.github.io/go-harbor-pilot/security/limitations) say exactly what is and
isn't covered.
