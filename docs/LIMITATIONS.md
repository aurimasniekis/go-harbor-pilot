# Limitations and External Blockers

This file is kept current alongside the implementation. Each entry is one of:

- **design**: a deliberate limitation.
- **pending**: the feature is not implemented yet. TRACEABILITY.md tracks the phase it belongs to.
- **blocked**: the work needs an external prerequisite before it can proceed.

## Design limitations

- **Single instance (design).** SQLite in WAL mode with one writer. There is no multi-writer HA and no shared database across hosts. You can scale up, not out. Recovery is restore-from-backup.
- **Masking is best-effort (design).** It uses deterministic detectors plus optional custom rules. Detectors miss novel secret formats. Tools running locally that bypass the gateway are not inspected.
- **Placeholder mappings are memory-only (design).** After a restart, placeholders that were already issued can no longer be restored. Responses that contain them pass through unchanged.
- **Physical erasure (design).** Deleting rows in SQLite does not securely erase the data. Free pages, the WAL and filesystem or snapshot copies can retain bytes until `VACUUM` and checkpoint. Trace content is encrypted with per-bucket DEKs, and destroying the DEK is the erasure mechanism. Backups taken with `--include-traces` contain ciphertext plus the wrapped DEKs as they existed at backup time.
- **Accounting estimates (design).** Budgets enforced through reservations are local and estimate-based. They are not hard provider caps. Usage that happens outside the gateway shows up only through provider-reported quota and usage, reconciled later.
- **Provider quota freshness (design).** Quota observations can be stale or unknown. Routing treats unknown as unknown, never as zero or unlimited, and the plan's semantics decide what happens next.
- **Protocol Watch is observational (design).** It records changes it observed. It is not an authoritative changelog of provider releases. When a field stops appearing, that is not proof it was removed.
- **Harness detection is telemetry (design).** User-Agent and header heuristics do not prove which executable made a request.
- **CLI bridges (design).**
  - Bridges run on the Harbor Pilot host and need the vendor binary and a per-account login there.
  - They add process start-up latency.
  - They expose only what the vendor CLI exposes: no native passthrough, and usage granularity is limited to what the CLI reports.
  - Translating tool calls through MCP changes how tool-call IDs look to the vendor model.
- **Exactly-once delivery is not provided (design).** Events and webhooks are at-least-once. Consumers deduplicate by event ID.

- **No TOTP/WebAuthn MFA (design, for now).** Local accounts use password plus throttling. Organizations that need MFA should use OIDC SSO with an identity provider that enforces it. The brief does not require MFA.
- **Plain-HTTP deployments (design).** When `public_url` is `http`, session cookies drop the `__Host-` prefix and `Secure` flag. This is for local development and demo use only, and the server logs a warning.

- **Quota adapter extras not imported (design, for now).** Adapters can report model prices and API-key inventories. Harbor Pilot keeps provider model pricing in provider resources and does not import adapter price lists or key inventories; usage aggregates and quota buckets are imported.
- **Response-derived rate limits are short-lived (design).** Header observations are fresh for two minutes, are not kept as history, and are dropped (and counted on the quota board) when the writer queue is full. They inform `candidate.health.remaining` but never mark an account exhausted; a 429 cooldown does that.
- **Budgets count settled ledger costs plus in-flight estimates (design).** The in-flight estimate prices input at four characters per token and output at the requested maximum. A budget can therefore block slightly early, never late, within one writer transaction.

- **Claude Code bridge fidelity (design).** Through the CLI the model sees a system prompt prefixed with the CLI's own identity line and an environment note, and client tools appear to it with an `mcp__client__` prefix (names are mapped back for the client). Sampling parameters, stop sequences, output limits and forced tool choice are not passed. Each is recorded as an approximate loss on the attempt. Provider-executed tools (web search and similar) are not available through the bridge.
- **Bridge processes are local state (design).** A parked CLI process holds a conversation between a tool call and its result. A restart loses parked processes; the next request resumes from the transcript with a recorded loss.

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
