# Threat Model

## Assets

| Asset | Where it lives | Protection |
|---|---|---|
| Upstream provider secrets (API keys, Claude setup-tokens, Codex logins) | Secret references; values encrypted under the KEK; bridge config dirs at 0700 | Write-only API; never logged; never sent anywhere except the matching upstream |
| Gateway tokens | Stored only as an HMAC verifier | Shown once; never forwarded upstream; prefix lookup plus constant-time compare |
| Human sessions | Server-side table holding the hash of each token | `__Host-` cookie, CSRF, idle and absolute expiry, revocation |
| Prompts, responses, tool payloads | In transit; optionally in traces | Content capture is off by default; masking; encrypted traces; TTL; crypto-erasure |
| Masking originals | Memory-only vault | Never persisted or logged; scoped per principal, binding and session |
| Accounting ledger | `harbor.db` | Append-only; idempotent; audit trail for corrections |
| Policy configuration | `harbor.db` | RBAC; revisions; audit; drift detection |
| Master KEK | File or environment variable outside the data directory | Never stored in the DB; backups exclude it; rotation command |

## Actors

- Operator (admin, operator or viewer role).
- Service principal (Terraform, CI).
- Gateway client: a harness, which may be driven by an untrusted agent with shell access.
- Upstream provider.
- Notification and event endpoint.
- Network attacker.
- Malicious prompt content, including prompt injection.

## Threats and mitigations (STRIDE-oriented)

1. **Token theft by the agent.** An agent with shell access can read its own token from the environment.
   - Mitigation: tokens are scoped, short-lived child tokens are available, and limits apply per binding. A stolen token reaches only that binding's plan.
   - Documented: the gateway cannot stop an agent from calling providers directly with other credentials it holds.
2. **Upstream credential exfiltration through the gateway.**
   - Outbound headers come from an allowlist, so client `Authorization` and `x-api-key` are never forwarded.
   - Upstream error bodies are redacted before they reach logs, events or traces.
   - Bridge environments are scrubbed.
3. **SSRF through provider base URLs, webhooks, OIDC discovery or diagnostic probes.**
   - All outbound HTTP goes through `netpolicy`, which denies by default: link-local, metadata (169.254.169.254, fd00:ec2::254), loopback and RFC1918 addresses, unless that specific endpoint has an explicit allowance.
   - DNS is resolved and pinned for each connection.
   - Redirect following is limited to the same host.
   - Response size and timeouts are bounded.
4. **Identity spoofing through headers.** Project, run and session headers are untrusted claims. Trusted refs come only from the binding or from the minting call. If a claim conflicts with a trusted ref, the conflict is recorded and the trusted value wins.
5. **Prompt injection steering routing or authorization.** Prompts and classifier output are data only. Intent from a classifier cannot widen permissions, raise budgets, or select accounts outside the plan's eligible set.
6. **Placeholder abuse.** A model could emit `HPSEC_…` tokens into tool arguments (URLs, commands) to exfiltrate secrets.
   - Restoring originals into tool arguments is off by default.
   - When enabled, it is limited to a configured allowlist of tool names and argument paths.
   - Placeholders are never restored into URLs whose host is outside the allowlist.
7. **Secret leakage into logs, metrics, events, errors or Terraform diagnostics.**
   - Logs pass through a redacting slog handler, and the `Secret` type redacts itself.
   - Metrics labels carry no request or session IDs.
   - Event payloads go through an allowlist.
   - The Terraform provider marks credential fields sensitive or write-only and never logs bodies.
   - A secret-leak test sweep runs in CI.
8. **CSRF and session fixation.** Defended by `__Host-` cookies, Origin checks plus an HMAC CSRF token, a session ID rotated at login, and step-up re-authentication for sensitive actions (reading unredacted traces, rotating keys).
9. **Brute force.** Login is throttled per account and per IP with exponential delays. Gateway token verification is rate-limited per IP on failures.
10. **Trace content exposure after expiry.**
    - Expiry is checked on every read.
    - On expiry the DEK is destroyed, which makes the ciphertext unrecoverable.
    - Rows and search-index entries are deleted.
    - Exports are generated with the TTL embedded and are refused after expiry.
    - Documented: SQLite free pages, WAL and file-system remnants persist until vacuum and checkpoint. Backups that included traces keep the ciphertext, but it is useless once its DEK is destroyed, unless the backup also captured that DEK.
11. **Webhook spoofing and replay.**
    - Outbound webhooks carry `X-HP-Signature: t=<unix>,v1=<hex hmac-sha256>`, computed over `t.body`.
    - Receivers should enforce a 5-minute window and track event IDs.
    - During rotation, both the old and the new secret produce signatures (`v1=` appears twice).
12. **Notification loops and storms.** Notification failures do not emit events that would themselves trigger notifications. Dedupe keys, cooldowns and grouping apply.
13. **Denial of service through large, complex or slow requests.**
    - Body, parser and complexity limits.
    - Per-binding concurrency.
    - Slow-client write deadlines and bounded buffering.
    - Upstream stream idle timeouts.
14. **Malicious Expr policies.** Expressions are compiled against a typed environment with node and memory limits and no I/O functions. Publishing requires the operator role, and every publish is audited.
15. **Supply chain.** Pinned dependencies, `go.sum`/`pnpm-lock`, a minimal runtime image, and SHA-256 verification of bridge binaries when Harbor Pilot downloads them. By default it does not download them; operators install them.
16. **Passthrough weakening controls.** Passthrough requires a policy authorization configured in advance, naming exactly which features are lost. It cannot bypass mandatory masking: a request that requires masking is blocked unless the policy explicitly allows sending it unmasked. Unsafe content capture is suspended during passthrough.

17. **Secret references as an exfiltration channel.** Whoever can set a secret reference can make its value leave the process: it is sent to the provider, OIDC issuer or notification endpoint they also configure. Setting a reference is therefore equivalent to reading that secret.
    - `env:` references may read only variables with a configured prefix (`security.secret_env_prefixes`, default `HP_SECRET_`). Bootstrap variables such as `HP_MASTER_KEY` and `HP_MASTER_KEY_FILE` are never readable.
    - `file:` references are confined to `security.secret_roots`. The roots may not contain the data directory or any master key file, and those paths are refused even when they are reachable.
    - References are checked when saved (a validation error names the allowed prefixes) and again when resolved.
    - Changing a resource that holds secrets, including its `*_ref` fields, requires `secrets.write`.

## Out of scope / residual risk

- **Local tools that bypass the gateway.** These are outside the gateway's outbound inspection boundary.
- **Upstream providers.** Providers see the content that reaches them. Masking is defense in depth, not a guarantee.
- **Host compromise.** An attacker with root on the host can read the KEK and the bridge logins.
- **Provider policy decisions.** Harbor Pilot does not claim to avoid provider rate limits, flags or restrictions.
