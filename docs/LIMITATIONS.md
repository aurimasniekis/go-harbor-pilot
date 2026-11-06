# Limitations and external blockers

Kept current with the code; the rendered page is `site/security/limitations.md`. Every entry is a deliberate limit of
the design unless marked **for now** (could change later). Work not yet done is tracked per requirement in
TRACEABILITY.md (`planned`, `in-progress`).

## Deployment and access

- **Single instance.** SQLite in WAL mode, one writer, no shared database across hosts. Scale up, not out; recover by
  restoring a backup.
- **Plain HTTP.** With an `http` `public_url`, cookies drop `__Host-` and `Secure`; local and demo use only, and the
  server logs a warning.
- **No TOTP or WebAuthn (for now).** Local accounts use a password plus throttling; use OIDC with an identity provider
  that enforces MFA.
- **Step-up.** Unredacted trace content and exports need a sign-in within 10 minutes, so API tokens cannot read them;
  SSO users re-authenticate by signing in again.
- **Gateway failure throttling is per address.** After 60 failed credential checks a minute from one address, every
  request from it is refused until the budget refills, valid tokens included; a client behind a shared NAT can briefly
  delay its neighbours.
- **No retention purge for activity (for now).** Requests, attempts, the ledger, events and the audit log are kept
  until deleted by hand; there are no rollup tables, so `/stats` aggregates raw rows. Trace content, sessions and
  one-time tokens do expire.

## Masking

- **Best-effort detectors.** Novel formats are missed; base64 is not decoded, letter case not normalised, split
  secrets not joined.
- **Inspection boundary.** Text, tool arguments and tool results (both JSON-aware) and exposed reasoning are
  inspected. JSON-escaped secrets inside ordinary message text, tool definitions, media titles and URLs, protocol
  extension fields, images and documents (counted in the masking summary) and opaque provider state are not. Tools
  running on the user's machine are outside the gateway.
- **Signed reasoning** cannot be rewritten; a request whose signed reasoning holds a masked or blocked value is
  blocked.
- **Short configured values.** Values under 6 characters are rejected when stored (with the line number); values read
  through `secret_values_ref` are re-read at run time and short lines are skipped.
- **Placeholders are memory-only.** After a restart or 24 hours idle, a repeated placeholder passes through unchanged.
  The vault holds 200,000 mappings or 128 MiB, and 5,000 or 8 MiB per credential; over a credential's budget values
  are masked but not restorable.

## Captured content

- **Physical erasure reaches SQLite only.** Deletes and expiry use `secure_delete` and FTS5 secure-delete, merge the
  index and truncate the WAL. File-system snapshots, storage copies and `--include-traces` backups (ciphertext plus
  wrapped DEKs as of backup time) are not reached.
- **Manual deletes keep the bucket key.** A deleted trace's DEK is shared with the rest of its day and policy and is
  destroyed only when that bucket expires, so an earlier backup can still decrypt it until then.

## Accounting and limits

- **Budgets are local estimates**, enforced through reservations, not provider caps. Usage outside the gateway shows
  up only through provider-reported quota and usage, reconciled later.
- **Budgets block slightly early, never late.** They count settled ledger costs plus in-flight estimates (input at four
  characters per token, output at the requested maximum), within one writer transaction.
- **Quota freshness.** Observations can be stale or unknown; routing treats unknown as unknown, never as zero or
  unlimited.
- **Response-derived rate limits are short-lived.** Fresh for two minutes, not kept as history, dropped (and counted)
  when the writer queue is full. They inform `candidate.health.remaining` but never mark an account exhausted; a 429
  cooldown does.
- **Quota adapter extras are not imported (for now).** Model prices and API-key inventories reported by adapters are
  ignored; usage aggregates and quota buckets are imported.
- **Statistics on demand.** `/stats` aggregates up to 200,000 requests per query; larger ranges are flagged truncated.
- **Trusted-ref filters (D-041).** Usage totals by `ref` cover every ledger row, but `/activity/requests` and
  `/activity/sessions` by `ref` follow request telemetry, which can drop rows under back-pressure. Usage recovered
  after a restart is attributed to refs only when its request row was written. Refs are trusted per client: a token
  with `tokens:mint` chooses its runs' values, so filter on `binding_id` too when clients share a ref namespace.
  `subscription_fees` and the `subscription_fee` total ignore filters. `/usage/series` has no `ref` filter yet, so
  the Spend page hides its chart (and provider billing and fees) while a reference filter is on.

## Routing and diagnostics

- **The blocking-traffic check is a sample**: each mapped model, each known tool, streaming and not, every account
  healthy. Rules depending on other request details, time, quota or health can still block traffic it did not try.
- **Test connection is light.** It lists models with the account's credential: proves address and credential, not
  model access, quota or billing. A provider without a model list counts as reachable; CLI bridge accounts only report
  whether the binary is installed.
- **Harness detection is telemetry.** User-Agent and header heuristics do not prove which executable sent a request.
- **Protocol Watch is observational.** It reports what this gateway saw, not provider release history; absent fields
  are low-confidence, not proof of removal; the learned baseline is in memory and re-seeded at start.
- **History deduplication needs a session.** Without a trusted session reference or header, occurrences are recorded
  as uncertain and never merged across conversations.
- **Passthrough telemetry.** Usage is read best-effort from the raw stream; tool calls, response intent signals and
  traces are unavailable. Passthrough does not avoid provider flags or restrictions.

## CLI bridges

- Bridges run on the Harbor Pilot host, need the vendor binary and a per-account login there, and add process start-up
  latency.
- They expose only what the vendor CLI exposes: no native passthrough, usage only as detailed as the CLI reports.
  Tool calls go through MCP, so tool-call ids look different to the vendor model.
- They run as the server's OS user. Tool restrictions are layered (flags, fail-closed item checks, read-only
  sandboxes); for defence in depth run the bridge image or service as a separate user, or in a container that cannot
  read the data directory or the master key. Codex's code-mode wrapper and patch tool remain, contained only by the
  read-only sandbox and the fail-closed check (D-038).
- **Parked processes are local state.** A restart loses them; the next request resumes from the transcript with a
  recorded loss.
- **Claude Code fidelity.** The model sees a system prompt prefixed with the CLI's identity line and an environment
  note; client tools appear with an `mcp__client__` prefix (mapped back). Sampling parameters, stop sequences, output
  limits and forced tool choice are not passed, each recorded as an approximate loss. Provider-executed tools (web
  search and similar) are unavailable.
- **Codex history.** One turn per process. Turns the bridge served continue with `codex exec resume`; a conversation
  seen for the first time is replayed as text (`history_replay` loss). A turn in which the CLI still starts a command,
  file change or web search is stopped with an error.
- **Codex rollouts** are kept so `exec resume` works and removed once older than the thread TTL (24 h), not when the
  turn ends.
- **Codex sign-in refresh.** `auth.json` is seeded once and refreshed in place by the CLI, so one process per account
  runs at a time by default (`bridges.codex.max_per_account`).

## Events and imports

- **At-least-once delivery.** Events and webhooks can repeat; consumers deduplicate by event id.
- **Quota Harbor importer scope.** Imports monitored accounts, the Telegram channel and observation history. Pricing,
  monitored-account budgets and threshold alert rules are listed as skipped. `env_file` references and `file`
  references with a `key` must be set on the monitor after import; stored OAuth sign-ins are not carried over. The
  server must be stopped while it runs.

## External blockers

| Blocker | Affects | Status |
|---|---|---|
| Live provider credentials (Anthropic, OpenAI, Gemini, OpenRouter and others) | live-verified cells of the compatibility matrix | blocked: user-provided |
| Claude setup-token for a test account | live Claude Code bridge verification | blocked: user-provided |
| `codex` binary and a ChatGPT login | live Codex bridge verification | blocked: binary not installed |
| `opencode` and `pi` binaries | live harness verification | blocked: not installed |
| Porch instance with a registered app | live OIDC verification | blocked: user-provided |
| Pulse server with client credentials | live Pulse verification (contract tests use embedded JetStream) | blocked: user-provided |
| Telegram bot, Slack and Discord webhooks | live notification verification | blocked: user-provided |
| Repository push | release workflows in CI | blocked: owner pushes |
| Terraform Registry publication and GPG key | provider publication | blocked: owner |
