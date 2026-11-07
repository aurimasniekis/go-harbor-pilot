# Changelog

Notable changes to Harbor Pilot. Until 1.0, a minor version may change configuration or defaults; the
[upgrade notes](https://aurimasniekis.github.io/go-harbor-pilot/operators/upgrades) say what needs action.

## Unreleased

### Gateway

- Run tokens can be refused before a run starts: `POST /v1/harbor/credentials` takes an optional `check_budget`.
  When it is `true` and an enabled blocking budget on the client, the run's project, that project's workspace, or a
  global budget is used up, the mint is refused with `429` and code `budget_exceeded`, naming the budget. The default
  (`false`) keeps today's behaviour. Provider account budgets aren't checked at mint (decision D-043).
- `system`-role entries inside `/v1/messages` `messages[]` are sent as user text, with a `mid_conversation_system`
  loss, when the target Anthropic model is known not to support mid-conversation system messages (Claude Sonnet 5,
  and older models up to Claude Opus 4.7, per Anthropic's public docs). They used to get the provider's 400. Every other model,
  including unknown ones, still gets them unchanged (decision D-042).

### Activity and spend

- `GET /api/v1/usage/series` takes the same `ref=<key>:<value>` filters and `binding_id` as the usage summary, and
  its buckets add up to the summary's totals under the same filters. **Spend** keeps its chart over time while a
  reference filter is on.

### Fixed

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
