# Changelog

Notable changes to Harbor Pilot. Until 1.0, a minor version may change configuration or defaults; the
[upgrade notes](https://aurimasniekis.github.io/go-harbor-pilot/operators/upgrades) say what needs action.

## Unreleased

### Activity and spend

- Filter usage and activity by trusted reference: `ref=<key>:<value>` on `/api/v1/usage/summary`,
  `/api/v1/activity/requests` and `/api/v1/activity/sessions`, and `group_by=ref:<key>` on the usage summary, so an
  orchestrator gets per-run totals (for example `group_by=ref:spaceman.run`) and finds a bound session's id. Reference
  groups are paged with `cursor` and `limit`. The usage summary also takes `binding_id`. Existing history is indexed
  when the database is upgraded.

### Upgrade notes

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
