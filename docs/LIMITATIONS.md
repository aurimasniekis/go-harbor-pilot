# Limitations and External Blockers

This file is kept current alongside the implementation. Each entry is one of:

- **design**: a deliberate limitation.
- **pending**: the feature is not implemented yet. TRACEABILITY.md tracks the phase it belongs to.
- **blocked**: the work needs an external prerequisite before it can proceed.

## Design limitations

- **Single instance (design).** SQLite in WAL mode with one writer. There is no multi-writer HA and no shared database across hosts. You can scale up, not out. Recovery is restore-from-backup.
- **Masking is best-effort (design).** It uses deterministic detectors plus optional custom rules. Detectors miss novel secret formats, and they do not decode base64, normalise letter case or join a secret split across lines. JSON-escaped secrets are found in tool arguments and tool results, but not inside ordinary message text. Tool definitions, media titles and URLs, and protocol extension fields are not inspected. Tools running locally that bypass the gateway are not inspected. Configured secret values shorter than 6 characters are not masked: storing one is rejected with the line number, but values read through `secret_values_ref` are re-read at run time and short lines there are skipped.
- **Placeholder mappings are memory-only (design).** After a restart, placeholders that were already issued can no longer be restored. Responses that contain them pass through unchanged.
- **Physical erasure (design).** Trace deletes and expiry sweeps run with SQLite `secure_delete` and FTS5 secure-delete, merge the search index and truncate the WAL, so deleted trace text leaves the database files. Copies made outside SQLite are not reached: file-system snapshots, storage-level copies and backups taken with `--include-traces` (which hold the ciphertext and the wrapped DEKs as they were at backup time). A manual trace delete removes that trace's rows; its bucket DEK is shared with other traces of the same day and is destroyed only when the bucket expires, so a backup taken earlier can still decrypt it until then.
- **Accounting estimates (design).** Budgets enforced through reservations are local and estimate-based. They are not hard provider caps. Usage that happens outside the gateway shows up only through provider-reported quota and usage, reconciled later.
- **Blocking-traffic check is a sample (design).** Validating a plan version runs its rules against synthetic requests: each mapped model, each known tool, streaming and not, with every account healthy. It warns when all or most of them are refused. Rules that depend on other request details, time, quota or health can still block traffic the check did not try.
- **Provider quota freshness (design).** Quota observations can be stale or unknown. Routing treats unknown as unknown, never as zero or unlimited, and the plan's semantics decide what happens next.
- **Protocol Watch is observational (design).** It records changes it observed. It is not an authoritative changelog of provider releases. When a field stops appearing, that is not proof it was removed.
- **Test connection is a light check (design).** It asks the provider for its model list with the account's credential. That proves the address and the credential, not access to a given model, remaining quota or billing. A provider without a model list counts as reachable. CLI bridge accounts are not checked: the result only says whether the vendor binary is installed.
- **Harness detection is telemetry (design).** User-Agent and header heuristics do not prove which executable made a request.
- **CLI bridges (design).**
  - Bridges run on the Harbor Pilot host and need the vendor binary and a per-account login there.
  - They add process start-up latency.
  - They expose only what the vendor CLI exposes: no native passthrough, and usage granularity is limited to what the CLI reports.
  - Translating tool calls through MCP changes how tool-call IDs look to the vendor model.
  - Bridges run as the same OS user as the server, so a vendor CLI could read anything the server can if its tool restrictions failed. They are layered: tools off by flag, fail-closed item checks, and read-only sandboxes. For defence in depth, run the bridge image or service under a separate user, or in a container that cannot read the data directory and the master key.
  - Codex keeps its conversation rollouts so `exec resume` works. They are removed once older than the thread TTL (24 h by default), not when the turn ends.
- **Exactly-once delivery is not provided (design).** Events and webhooks are at-least-once. Consumers deduplicate by event ID.

- **No TOTP/WebAuthn MFA (design, for now).** Local accounts use password plus throttling. Organizations that need MFA should use OIDC SSO with an identity provider that enforces it. The brief does not require MFA.
- **Plain-HTTP deployments (design).** When `public_url` is `http`, session cookies drop the `__Host-` prefix and `Secure` flag. This is for local development and demo use only, and the server logs a warning.

- **Quota adapter extras not imported (design, for now).** Adapters can report model prices and API-key inventories. Harbor Pilot keeps provider model pricing in provider resources and does not import adapter price lists or key inventories; usage aggregates and quota buckets are imported.
- **Step-up re-authentication (design).** Unredacted trace content and trace exports need a sign-in within the last 10 minutes, so API tokens cannot read them, and SSO users re-authenticate by signing in again. There is no TOTP or WebAuthn second factor.
- **Gateway failure throttling is per address (design).** After 60 failed credential checks in a minute from one address, every request from that address is refused until the budget refills, including requests with valid tokens. A client behind a shared NAT that sends bad tokens can therefore briefly delay its neighbours.
- **Quota Harbor importer scope (design).** `harbor-pilot migrate quota-harbor` imports monitored accounts, the Telegram channel and observation history. Pricing, monitored-account budgets and threshold alert rules have no direct equivalent: they are listed as skipped, not converted. Credential references of type `env_file`, or `file` with a `key`, have no equivalent and must be set on the monitor after import. Stored OAuth sign-ins are not carried over, so in-app sign-in monitors need a new sign-in. The server must be stopped while the importer runs.
- **Response-derived rate limits are short-lived (design).** Header observations are fresh for two minutes, are not kept as history, and are dropped (and counted on the quota board) when the writer queue is full. They inform `candidate.health.remaining` but never mark an account exhausted; a 429 cooldown does that.
- **Budgets count settled ledger costs plus in-flight estimates (design).** The in-flight estimate prices input at four characters per token and output at the requested maximum. A budget can therefore block slightly early, never late, within one writer transaction.

- **Claude Code bridge fidelity (design).** Through the CLI the model sees a system prompt prefixed with the CLI's own identity line and an environment note, and client tools appear to it with an `mcp__client__` prefix (names are mapped back for the client). Sampling parameters, stop sequences, output limits and forced tool choice are not passed. Each is recorded as an approximate loss on the attempt. Provider-executed tools (web search and similar) are not available through the bridge.
- **Bridge processes are local state (design).** A parked CLI process holds a conversation between a tool call and its result. A restart loses parked processes; the next request resumes from the transcript with a recorded loss.

- **Codex bridge history (design).** The CLI runs one turn per process. Turns the bridge served continue the CLI thread with `codex exec resume`; a conversation seen for the first time is replayed to the CLI as text, recorded as an approximate `history_replay` loss. The CLI's own tools are turned off (D-038). A turn in which the CLI still starts a command, file change or web search is stopped with an error rather than served.
- **Codex sign-in refresh (design).** The account's `auth.json` is seeded from the stored credential once and refreshed in place by the CLI, so one process per account runs at a time by default (`bridges.codex.max_per_account`).

- **Masking boundary (design).** Masking inspects text, tool arguments (JSON-aware), tool results (JSON-aware) and exposed reasoning. Signed reasoning cannot be rewritten; a request whose signed reasoning contains a value the policy masks or blocks is blocked. Images and documents are not inspected; they are counted in the request's masking summary. Opaque provider state is never inspected or rewritten. Masking is defense in depth: tools running on the user's machine are outside the gateway's outbound boundary.
- **Placeholder lifetime (design).** Placeholder mappings live only in memory. After a restart, or after 24 hours idle, a placeholder the model repeats is returned to the client unchanged. The vault holds at most 200,000 mappings or 128 MiB, and 5,000 mappings or 8 MiB per credential; beyond a credential's budget values are still masked but their placeholders cannot be restored.

- **Passthrough telemetry (design).** In passthrough, usage is read best-effort from the raw stream; tool calls, intent signals in responses and traces are not available. Passthrough does not avoid provider-side flags or restrictions.
- **Protocol Watch is an observed changelog (design).** It reports what this gateway observed, not provider release history. Absent fields are reported with low confidence and are not proof of removal. The learned baseline lives in memory and is re-seeded at start.

- **Statistics are computed on demand (design).** `/stats` aggregates up to 200,000 requests per query directly from SQLite; larger ranges are flagged as truncated. Rollup tables are not used yet.
- **History deduplication needs a session (design).** Without a trusted session reference or session header, occurrences are recorded as uncertain and never merged across conversations.

## Pending

Pending items are tracked per requirement in TRACEABILITY.md (status `planned` or `in-progress`).

## External blockers

| Blocker | Affects | Status |
|---|---|---|
| Live provider credentials (Anthropic, OpenAI, Gemini, OpenRouter and others) | live-verified cells of the compatibility matrix | blocked: user-provided |
| Claude setup-token for a test account | live Claude Code bridge verification | blocked: user-provided |
| `codex` binary and a ChatGPT login | live Codex bridge verification | blocked: binary not installed |
| `opencode` and `pi` binaries | live harness verification | blocked: not installed |
| Porch instance with a registered app | live OIDC verification | blocked: user-provided |
| Pulse server with client creds | live Pulse verification (contract tests run against embedded JetStream) | blocked: user-provided |
| Telegram bot, Slack and Discord webhooks | live notification verification | blocked: user-provided |
| GitHub repositories and push | release workflows run in CI | blocked: owner pushes |
| Terraform Registry publication and GPG key | provider publication | blocked: owner |
