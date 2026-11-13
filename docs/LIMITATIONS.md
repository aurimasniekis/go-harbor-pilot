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
  one-time tokens do expire. A future ledger purge must keep the windows of active one-time budgets (D-044).

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
- **A budget's first read after start-up or a change reads its whole window (D-044).** Budgets keep an in-memory
  sum of settled spend and read only new ledger rows on each reservation (about 0.4 ms per budget over 200,000 rows
  in 12 months). After a restart, a reset, an edited window or scope, or for a recurring budget a new period, the
  first read sums the whole window: up to about 0.3 s per long-running one-time budget on that data. A background
  primer does that read off the request path right after start-up and each configuration change, but under traffic
  the first reservation after start-up or an edit can still arrive before it finishes and pay the read itself. New
  rows in other scopes count too: a budget on a quiet project reads every row settled anywhere since its last
  refresh, until the gap exceeds both 2,048 rows and ten times the rows its window holds; then it re-reads its own
  window through the index instead. The primer refreshes every budget every 30 s, so the gap stays short. Measured
  with 200,000 foreign rows: about 2 ms for a monthly project budget and about 23 ms for a year-long one-time budget
  holding 4,000 rows (the cost of reading its window). Re-creating a one-time budget with an empty `starts_at` starts a new count;
  any future ledger retention must keep the windows of active one-time budgets.
- **The mint budget check is opt-in and partial (D-043).** Without `check_budget`, minting a child credential never
  consults budgets; a run token under an exhausted blocking budget is refused (`429 budget_exceeded`) on its first
  request. With it, binding, project, workspace and global budgets are checked, but account budgets are not (routing
  picks the account per request), nor a project chosen later by an `X-HP-Project` hint. A mint that passes can still
  see its first request refused once that request's own estimate is added. The check counts budgets even when the
  run's requests would all go to `local` or `free` accounts, which budgets ignore, so it can refuse such a run with
  `429`. It is skipped only when the binding's plan can't reach any other account (for example `allowed_billing`
  restricted to `local` and `free`).
- **Quota freshness.** Observations can be stale or unknown; routing treats unknown as unknown, never as zero or
  unlimited.
- **Response-derived rate limits are short-lived.** Fresh for two minutes, not kept as history, dropped (and counted)
  when the writer queue is full. They inform `candidate.health.remaining` but never mark an account exhausted; a 429
  cooldown does.
- **Usage headers (D-048) are off by default and approximate.** With `gateway.usage_headers: auto`, Anthropic Messages
  clients get their usage in `anthropic-ratelimit-unified-*` headers. A subscription seat's windows are account-wide:
  every client routed to the seat sees the same figures, which include usage from outside Harbor Pilot. They are only
  as fresh as the quota board, so they usually lag one request behind, and a stale window is left out. Budget figures
  include in-flight reservations, so they run slightly high. Global, workspace and account budgets are shown to every
  token they cover: only a fraction, the window end and the period, never a name, id or amount, but a token in one
  project can still watch the aggregate spend of a budget it shares with others. A one-time budget's `-reset` is its
  `ends_at`, when it stops applying, which Claude Code words as "resets". Claude Code reads the headers only when the token is
  passed as `CLAUDE_CODE_OAUTH_TOKEN` (not with `apiKeyHelper` or `ANTHROPIC_AUTH_TOKEN`). Its device-flow gateway
  sign-in is not implemented. A budget refusal keeps the existing 429 shape, so the client shows the error text,
  not its own "limit reached" line (for now). Unified headers on direct Anthropic API responses are not read into the
  quota board (for now).
- **Quota adapter extras are not imported (for now).** Model prices and API-key inventories reported by adapters are
  ignored; usage aggregates and quota buckets are imported.
- **Statistics on demand.** `/stats` aggregates up to 200,000 requests per query; larger ranges are flagged truncated.
- **Trusted-ref filters (D-041).** Usage totals by `ref` cover every ledger row, but `/activity/requests` and
  `/activity/sessions` by `ref` follow request telemetry, which can drop rows under back-pressure. Usage recovered
  after a restart is attributed to refs only when its request row was written. Refs are trusted per client: a token
  with `tokens:mint` chooses its runs' values, so filter on `binding_id` too when clients share a ref namespace.
  `subscription_fees` and the `subscription_fee` total ignore filters, so the Spend page hides provider billing and
  fees while a reference filter is on.

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
- **Mid-conversation system messages use a fixed model list (D-042).** Anthropic accepts a `system`-role entry
  inside `messages[]` only on some models. For the models the public documentation lists as unsupported, Harbor
  Pilot sends the entry as user text and records an approximate loss. Any other model, including one released after
  this version, gets the entry as sent. If such a model doesn't support it, the client gets the provider's 400 until
  the list is updated. There is no operator override. Placement rules are still left to the provider. From other
  protocols the entry reaches Anthropic as user text, and the Codex bridge drops it.
- **Passthrough telemetry.** Usage is read best-effort from the raw stream; tool calls, response intent signals and
  traces are unavailable. Passthrough does not avoid provider flags or restrictions.
- **Per-model upstream request fields apply to HTTP executors only.** They are added to the upstream body for
  Anthropic, OpenAI, OpenAI-compatible, Gemini and Ollama providers, on translated, native and passthrough paths. CLI
  bridge providers refuse them: the vendor CLI builds its own request, so it cannot carry them. They configure body
  fields only, never headers or the query string, and only names on the provider type's allowlist
  (`docs/contracts/config.md`) are accepted; the provider pin reaches `openai_compatible` providers only. The gateway
  checks names, size and JSON validity, not the values: a value the upstream rejects surfaces as that upstream's
  error. Conflicts are detected from the client's request only, so a client setting with no equivalent in the
  canonical request and sent under a different name on another protocol is not seen as a conflict.
- **Gemini requests are parsed under the camelCase spelling only.** The Gemini parser reads `generationConfig` and
  its `maxOutputTokens`, not `generation_config` or `max_output_tokens`, so a client that sends only the snake_case
  object gets no output cap in the canonical request, and the object is forwarded to a Gemini upstream as sent,
  outside the budget's view. A model that configures `generationConfig` drops the snake_case object instead (D-046).
  Reading both spellings is a follow-up.

## Reasoning translation

- **Opt-in per model (D-047).** Without a model `reasoning` option, Anthropic adaptive thinking and a top-level
  `output_config` reach only native Anthropic upstreams. Elsewhere they are dropped with a loss naming the option.
- **Reasoning is hidden from Anthropic clients.** Reasoning a non-Anthropic upstream returns is not shown to a Messages
  client: a thinking block needs an Anthropic signature, so the client could not send it back. Reasoning continuity
  across a tool loop is therefore lost for Claude Code on other models. DeepSeek's thinking mode requires earlier
  `reasoning_content` back on every request that has `tools` (it answers 400 otherwise), so for Claude Code → DeepSeek
  directly use `mode: off` (or a router that handles it). Chat clients can carry it with `history: field`.
- **`output_config.format` is not translated (for now).** An `output_config` with a `format` is dropped outside
  Anthropic, effort included.
- **`context_management` is dropped** outside Anthropic: no other API has server-side context editing, so the full
  history is sent.
- **Accepted levels are configured, not discovered.** Clamping uses the model's `efforts` or the style's defaults. An
  OpenAI-compatible router with `require_parameters` routes only to hosts that accept the reasoning parameter sent.
- **Passthrough ignores the option**, and the CLI bridges refuse it (the CLI chooses its own thinking).
- **Not live-verified.** The OpenRouter and DeepSeek shapes follow their public documentation and are tested against
  synthetic upstreams only.

## CLI bridges

- Bridges run on the Harbor Pilot host, need the vendor binary and a per-account login there, and add process start-up
  latency.
- They expose only what the vendor CLI exposes: no native passthrough, usage only as detailed as the CLI reports.
  Tool calls go through MCP, so tool-call ids look different to the vendor model.
- They run as the server's OS user. Tool restrictions are layered (flags, fail-closed item checks, read-only
  sandboxes); for defence in depth run the bridge image or service as a separate user, or in a container that cannot
  read the data directory or the master key. Codex's code-mode wrapper and patch tool remain, contained only by the
  read-only sandbox and the fail-closed check (D-038).
- **Parked processes are local state.** A Claude Code process that ends between turns leaves its CLI session on a
  shelf for up to its prompt-cache TTL after the conversation was last used (D-045), and the next turn resumes it. A
  conversation waiting for tool results is resumed this way only with CLI versions whose session format was verified;
  with others it falls back to the rebuilt transcript. After that, after a restart (the shelf
  index is in memory) or when the shelf limits evicted it, the next request resumes from a rebuilt transcript with a
  recorded loss. Conversation content therefore stays on disk up to 1 hour after the conversation's last turn (0600
  files under the bridge's data directory).
- **Claude Code 400s.** The CLI reports an upstream 400 without its error type. The bridge treats it as the client's
  error (no failover), except the documented account-level ones it recognises by their text (credit balance, billing,
  a disabled organization), which fail over and cool the account down. An account-level 400 worded differently is
  returned to the client instead of failing over.
- **Claude Code fidelity.** The model sees a system prompt prefixed with the CLI's identity line and an environment
  note (`system_prompt`, approximate: inherent to running the CLI); client tools appear with an `mcp__client__` prefix
  (mapped back). Sampling parameters, stop sequences and forced tool choice are not passed, each recorded as an
  approximate loss. `max_tokens` (up to the model's limit) and effort (the top-level `output_config.effort`, else a
  thinking budget's level, and an effort-only `output_config` on a mid-conversation system entry) are passed per
  process. `thinking` itself is not: the CLI picks adaptive or budgeted thinking for the model. A mid-conversation system entry with text is sent as user
  text (`mid_conversation_system`): the CLI's session format is not documented for caller-provided system entries.
  Provider-executed tools (web search and similar) are unavailable. The client's `cache_control` markers are removed (`cache_control`,
  approximate): the CLI places its own breakpoints (system prompt and last message), and the client's could break the
  upstream's TTL-order and four-breakpoint rules. Only the TTL carries over (`CLAUDE_CODE_PROMPT_CACHE_TTL`), not
  the placement. A process resumed from a rebuilt transcript sends a different prefix than the previous process did,
  so it writes the cache again; a shelved session keeps the CLI's own prefix.
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
