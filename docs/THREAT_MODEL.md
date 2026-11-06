# Threat model

The operator-facing summary is `site/security/index.md`. This file is the full list: assets, actors, threats and
residual risk. Decisions D-035 to D-039 record the hardening from the security review.

## Assets

| Asset | Where it lives | Protection |
|---|---|---|
| Provider secrets (API keys, Claude setup-tokens, Codex logins) | Secret references, or values encrypted under the KEK; bridge config dirs at 0700 | Write-only API; never logged; sent only to the matching upstream |
| Gateway tokens (`hpk_`) and API tokens (`hpa_`) | Keyed hash only | Shown once; gateway tokens never forwarded; prefix lookup plus constant-time compare |
| Human sessions | Server-side table of token hashes | `__Host-` cookie, CSRF, idle and absolute expiry, revocation |
| Prompts, responses, tool payloads | In transit; optionally in traces | Capture off by default; masking; encrypted traces with TTL and crypto-erasure |
| Masking originals | Memory-only vault | Never persisted or logged; scoped per client, credential family and session |
| Accounting ledger | `harbor.db` | Append-only; idempotent; corrections audited |
| Policy configuration | `harbor.db` | RBAC, revisions, audit, drift detection |
| Master key (KEK) | File or environment variable outside the data directory | Never in the database or backups; CLI rotation |

## Actors

- Operators (`admin`, `operator`, `viewer`) and service principals (Terraform, CI).
- Gateway clients: harnesses, possibly driven by an untrusted agent with shell access.
- Upstream providers, and notification and event endpoints.
- Network attackers.
- Malicious prompt content, including prompt injection.

## Threats and mitigations

1. **Token theft by the agent.** An agent with a shell can read its own token.
   - Tokens are scoped and can expire; short-lived child tokens are available; limits apply per binding. A stolen
     token reaches only its binding's plan and budgets.
   - Residual: the gateway cannot stop an agent calling providers directly with other credentials it holds.
2. **Provider credential exfiltration through the gateway.**
   - Outbound headers come from an allowlist; client `Authorization` and `x-api-key` are never forwarded.
   - Upstream and CLI error text is scrubbed before it reaches logs, events, traces or the client.
   - Bridge environments are scrubbed.
3. **SSRF** through provider base URLs, webhooks, OIDC discovery, quota adapters, Pulse or diagnostic probes.
   - Every outbound connection goes through `netpolicy`. It refuses loopback, RFC 1918 and CGNAT addresses unless the
     destination explicitly allows private networks, and always refuses link-local and cloud metadata addresses
     (169.254.169.254, fd00:ec2::254, 168.63.129.16, 192.0.0.192, 100.100.100.200).
   - IPv4 embedded in NAT64, 6to4, IPv4-compatible and Teredo forms is checked as IPv4.
   - DNS is resolved and pinned per connection; redirects stay on the same host; sizes and timeouts are bounded;
     environment proxies are ignored; connection errors are generic, so probes cannot scan ports.
   - Stored secrets do not follow a destination: moving a channel, destination or quota monitor to another origin
     requires re-entering its secrets, and moving a provider clears its accounts' stored credentials.
4. **Identity spoofing through headers.** Project, run and session headers are claims. Trusted refs come only from the
   binding or the mint call; on conflict the trusted value wins and the conflict is recorded.
   - A minter (`tokens:mint`) can bind a child only to the binding's own or allowed projects and cannot replace refs
     the operator configured. The gateway re-checks the bound project on every request.
5. **Prompt injection steering routing or authorization.** Prompts and classifier output are data. An intent cannot
   widen permissions, raise a budget or pick an account outside the plan's eligible set.
6. **Placeholder abuse.** A model could put `HPSEC_…` into tool arguments (URLs, commands) to exfiltrate a secret.
   - Restoring into tool arguments is off by default. When on, it is limited to allowlisted tool names and argument
     paths, and refused when the value names any host outside the allowlist: URLs are parsed (userinfo and backslash
     authorities refused), and bare host names and IP addresses count.
   - Streamed responses restore only into text; reasoning is never rewritten.
   - Placeholders are scoped to the client, the credential family and the session, so another token cannot recover a
     value by sending the same session hint.
   - Signed reasoning cannot be masked; if it holds a value the policy masks or blocks, the request is blocked.
7. **Secrets in logs, metrics, events, errors or Terraform diagnostics.**
   - A redacting slog handler, and a `credentials.Secret` type that redacts itself.
   - Metric labels carry no request or session ids; event payloads are allowlisted.
   - The Terraform provider marks credentials sensitive or write-only and never logs bodies.
   - Error text is scrubbed of the attempt's credential (including escaped forms) and key-shaped strings, also inside
     streams.
   - `TestSecretsNeverLeak` and `TestStreamErrorsAreScrubbed` plant canaries and check every surface, including the
     raw database files.
8. **CSRF, session fixation, stolen sessions.**
   - `__Host-` cookies, an Origin check plus an HMAC CSRF token, session rotation at sign-in.
   - Step-up: reading unredacted trace content, exporting traces and setting a first password on an SSO-only account
     need a sign-in within 10 minutes (`POST /api/v1/session/reauth`, or SSO sign-in again). API tokens never
     qualify.
   - Key rotation is a host CLI operation, not an API action.
9. **Brute force.**
   - Login throttling per account and per address with exponential delay; each attempt reserves its failure
     atomically before the password is checked, so parallel guesses do not bypass it.
   - Failed gateway credential checks are limited to 60 a minute per address.
   - Client addresses come from trusted proxies only, reading every `X-Forwarded-For` line.
   - `/metrics` without a token is served only to direct loopback clients.
   - Demo mode (public admin password) refuses non-loopback addresses unless overridden.
10. **Trace content after expiry.**
    - Expiry is checked on every read; on expiry the bucket DEK is destroyed.
    - Rows and index entries are removed with `secure_delete` and FTS5 secure-delete, the index merged and the WAL
      truncated.
    - The search index holds only text redacted by both the trace policy and the masking that ran.
    - Exports embed the TTL and are refused after expiry.
    - Residual: file-system snapshots and earlier copies keep what they captured; backups with traces keep the
      ciphertext and wrapped DEKs as of backup time.
11. **Webhook spoofing and replay.** Webhooks carry `X-Harbor-Signature: t=<unix>,v1=<hex>`, an HMAC-SHA256 over
    `<t>.<body>`. During rotation both secrets sign (`v1=` twice). Receivers enforce a 5-minute window and track event
    ids.
12. **Notification loops and storms.** Notification failures emit no events that could trigger notifications; dedupe
    keys, cooldowns and grouping apply.
13. **Denial of service** through large, complex or slow requests: body, parser and complexity limits; per-binding
    concurrency; slow-client write deadlines and bounded buffering; upstream stream idle timeouts.
14. **Malicious Expr policies.** Expressions compile against a typed environment with node and memory limits and no I/O.
    Publishing needs the operator role and is audited.
15. **Supply chain.** Pinned Go and pnpm dependencies (`go.sum`, `pnpm-lock.yaml`), a minimal runtime image, and pinned
    vendor CLI versions in the bridge image. Harbor Pilot never downloads binaries itself; operators install them.
16. **Passthrough weakening controls.** Passthrough needs a policy authorization made in advance that names the
    features lost. Required masking still applies: such a request is blocked unless the policy explicitly allows
    sending it unmasked. Unsafe content capture is suspended during passthrough.
17. **Secret references as an exfiltration channel.** Setting a reference is equivalent to reading the secret, since its
    value goes to the provider, OIDC issuer or endpoint the same person configures.
    - `env:` references read only variables with an allowed prefix (`security.secret_env_prefixes`, default
      `HP_SECRET_`); `HP_MASTER_KEY`, `HP_MASTER_KEY_FILE` and the previous-key variables are never readable.
    - `file:` references are confined to `security.secret_roots`, which may not contain the data directory or a key
      file; those paths are refused even when reachable.
    - References are checked when saved and again when resolved. Changing any secret-holding resource, `*_ref` fields
      included, needs `secrets.write`.
18. **CLI bridge escape.** The vendor CLIs run with their own tools turned off by flag, under read-only sandboxes,
    with a fail-closed check that ends any turn that starts a command, file change or web search. Secrets never appear
    in process arguments. Residual: the CLIs run as the server's OS user; isolate the bridge service or container for
    defence in depth.

## Out of scope and residual risk

- **Local tools that bypass the gateway** are outside its inspection boundary.
- **Providers** see what reaches them. Masking is defence in depth, not a guarantee.
- **Host compromise.** Root on the host can read the KEK and the bridge logins.
- **Provider policy.** Harbor Pilot does not claim to avoid provider rate limits, flags or restrictions.
