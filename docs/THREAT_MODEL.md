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
   - All outbound connections go through `netpolicy`: provider executors, quota adapters, webhooks, notification channels, OIDC and Pulse. It denies loopback, RFC1918 and CGNAT addresses unless that endpoint explicitly allows private networks.
   - It always denies link-local ranges and cloud metadata addresses: AWS/GCP 169.254.169.254, fd00:ec2::254, Azure 168.63.129.16, Oracle Cloud 192.0.0.192 and Alibaba 100.100.100.200.
   - IPv4 addresses embedded in NAT64, 6to4, IPv4-compatible and Teredo IPv6 forms are checked as IPv4.
   - Environment proxies are ignored. Connection errors are reported generically, so they cannot be used as a port scanner.
   - Stored secrets do not follow a destination change: moving a channel, destination or quota monitor to another origin requires re-entering its secrets, and moving a provider clears its accounts' stored credentials.
   - DNS is resolved and pinned for each connection.
   - Redirect following is limited to the same host.
   - Response size and timeouts are bounded.
4. **Identity spoofing through headers.** Project, run and session headers are untrusted claims. Trusted refs come only from the binding or from the minting call. If a claim conflicts with a trusted ref, the conflict is recorded and the trusted value wins.
   - A minter (a credential holding `tokens:mint`) can bind a child only to the client's own project or one of its allowed projects, and cannot replace references the operator configured on the client. The gateway re-checks the bound project against the client on every request.
5. **Prompt injection steering routing or authorization.** Prompts and classifier output are data only. Intent from a classifier cannot widen permissions, raise budgets, or select accounts outside the plan's eligible set.
6. **Placeholder abuse.** A model could emit `HPSEC_…` tokens into tool arguments (URLs, commands) to exfiltrate secrets.
   - Restoring originals into tool arguments is off by default.
   - When enabled, it is limited to a configured allowlist of tool names and argument paths.
   - Restoration targets only an allowlisted tool's allowlisted fields, and is refused when the value names any host outside the allowlist: URLs are parsed (userinfo and backslash authorities refused) and scheme-less host names and IP addresses count too.
   - Streamed responses restore placeholders only into text. Tool-call arguments go through the guarded path above, and reasoning (often signed) is never rewritten.
   - Placeholders are scoped to the client, the credential (a child token shares its parent's) and the session, so another token cannot recover a value by sending the same session hint.
   - Signed reasoning cannot be masked; if it contains a value the policy masks or blocks, the request is blocked instead of sent.
7. **Secret leakage into logs, metrics, events, errors or Terraform diagnostics.**
   - Logs pass through a redacting slog handler, and the `Secret` type redacts itself.
   - Metrics labels carry no request or session IDs.
   - Event payloads go through an allowlist.
   - The Terraform provider marks credential fields sensitive or write-only and never logs bodies.
   - Upstream and CLI error text is scrubbed of the attempt's credential (also in escaped forms) and key-shaped strings before it is recorded, observed or returned, including errors that arrive inside a stream.
   - A secret-leak test sweep runs in CI (TestSecretsNeverLeak, TestStreamErrorsAreScrubbed).
8. **CSRF, session fixation and stolen sessions.**
   - Defended by `__Host-` cookies, Origin checks plus an HMAC CSRF token, and a session ID rotated at login.
   - Sensitive actions need a sign-in within the last 10 minutes (step-up): reading unredacted trace content, exporting traces, and setting a first password on an SSO-only account. Password sessions refresh it with `POST /session/reauth`; SSO sessions sign in again.
   - API tokens never satisfy step-up, so automation cannot read unredacted content or export traces.
   - Key rotation is a CLI operation on the host, not an API action.
9. **Brute force.**
   - Login is throttled per account and per IP with exponential delays. Each attempt reserves its failure atomically before the password is checked, so parallel guesses do not bypass the limit.
   - Failed gateway credential checks are limited per address (60 per minute). A misbehaving client behind a shared address can therefore briefly delay its neighbours.
   - Client addresses come from trusted proxies only, reading every `X-Forwarded-For` line.
   - `/metrics` without a token is served only to direct loopback clients, never through a proxy.
   - Demo mode, whose administrator password is public, refuses non-loopback listen addresses unless explicitly overridden.
10. **Trace content exposure after expiry.**
    - Expiry is checked on every read.
    - On expiry the DEK is destroyed, which makes the ciphertext unrecoverable.
    - Rows and search-index entries are deleted with `secure_delete` and FTS5 secure-delete, the index is merged and the WAL truncated, so the text leaves the database files.
    - The search index only ever holds text redacted by both the trace redaction policy and the masking policy that ran, so values the policy kept from the provider are never indexed.
    - Exports are generated with the TTL embedded and are refused after expiry.
    - Documented: file-system snapshots and copies made before deletion keep whatever they captured. Backups that included traces keep the ciphertext and the wrapped DEKs as they were at backup time.
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
