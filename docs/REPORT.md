# Harbor Pilot: completion report

Date: 2026-11-05. Repositories: `go-harbor-pilot` (application) and `terraform-provider-harbor-pilot` (Terraform provider). Both are committed locally and nothing has been pushed.

Status words follow the evidence vocabulary in [PRODUCT.md](PRODUCT.md): implemented, contract-tested, live-verified and blocked. Nothing is marked live-verified. Every check ran against fixtures, synthetic upstreams, fake binaries or in-process servers, apart from the Claude Code bridge. That bridge was run with the real `claude` 2.1.318 binary, but against a synthetic upstream. The requirement-by-requirement evidence is in [TRACEABILITY.md](TRACEABILITY.md).

## What was built
- **Gateway:**
  - Inbound protocols: OpenAI Chat Completions and Responses, Anthropic Messages and Gemini, on `/v1` and on namespaced base URLs.
  - Execution:
    - **Upstreams:** API-key providers and local model endpoints.
    - **CLI bridges:** Claude Code and Codex, run per account in isolation; their built-in tools are disabled and enforced fail-closed.
  - Behaviour: streaming with keepalive, cancellation, failover only before the first byte, and per-path compatibility breakers with authorized native passthrough.
- **Routing:**
  - Versioned plans with aliases (shown as "model names"), pools ("fallback groups") and hard restrictions.
  - Decision tables and Expr rules.
  - A simulator and replay, and the "Why this route?" explanation for every request.
- **Accounting:**
  - Append-only ledger with atomic reservations and budgets enforced inside the reservation.
  - Cost categories kept separate.
  - Quota tracking with the migrated Quota Harbor adapters, response-header and CLI rate-limit observations, and forecasts.
- **Privacy:**
  - Streaming-safe masking with scoped placeholders; restoration only into text, plus allowlisted tool arguments.
  - Short-lived encrypted traces with crypto-erasure and a masked search index.
  - Step-up re-authentication for reading unredacted content.
- **Events:** a transactional outbox, the Pulse relay, signed webhooks, and Telegram, Slack, Discord and webhook notifications with hysteresis, quiet hours and digests.
- **Operations:**
  - Online backup and verified restore.
  - The Quota Harbor importer.
  - Container images (plain, and with the bridges), Compose, systemd and goreleaser.
  - Load generator, capacity notes, and upgrade, rollback and troubleshooting guides.
- **Web UI:** rebuilt on Mantine (D-034) after the owner's review. It has grouped navigation, a Home page that starts with what needs attention, readable logs, and layouts that work on phones. A Head-of-UX review of every screen guided the rebuild.
- **Terraform provider:**
  - Resources and data sources for every control-plane kind, plus typed resources for plan versions, gateway credentials, service principals, auth settings and OIDC.
  - Write-only secrets and an ephemeral token.
  - Seven worked examples.

## Acceptance scenarios
All fourteen scenarios (A–N) are **contract-tested** by named integration tests against a real server process with synthetic upstreams. Scenario L, the Terraform lifecycle, runs in the provider repository. The list is in TRACEABILITY §19.

## Verification run for this report (2026-11-05)

| Check | Result |
|---|---|
| `make check` | All 48 Go packages pass under the race detector. golangci-lint reports 0 issues. Web: typecheck, lint, 94 unit tests and build pass. |
| `make e2e` (Playwright against the demo binary, including axe, crawl and no horizontal scrolling at 390px) | 69 passed, 4 skipped (opt-in screenshot specs) |
| `make fuzz-smoke` | All fuzz targets pass |
| Load tests (`make load`, in the normal suite at quick scale) | Pass. Figures are in [operations/capacity.md](operations/capacity.md). |
| Secret-leak sweep (`TestSecretsNeverLeak`) | Passes. It plants canary secrets everywhere secrets can be configured, then checks logs, API responses, metrics, events, audit records, stream errors and the raw database files. |
| Terraform provider: `TF_ACC=1 go test ./...` against the final application | Passes. golangci-lint reports 0 issues. |
| `docker build -f deploy/Dockerfile` | Builds and runs |

## Security review
Three independent reviews covered the system:
- authentication, authorization and API;
- outbound network, processes and the filesystem;
- data protection.

Together they reported 4 high, 14 medium and 23 low findings. Every finding was fixed with a regression test, or, where it is a design limit, documented in LIMITATIONS and DECISIONS (D-035 to D-039). The high findings:
1. **Secret references could send the master key or any environment variable to an attacker-chosen host.** References are now confined to `HP_SECRET_`-prefixed variables and to file roots that cannot reach the data directory or key files.
2. **Codex's built-in shell was available to gateway clients.** It is now disabled, and any tool use stops the turn.
3. **Streamed placeholder restoration reached tool-call arguments.** Restoration now applies to text only, with allowlisted tool arguments still parsed and guarded.
4. **Upstream error messages that echo API keys were stored and returned.** They are now scrubbed on every path, including streams.

**Operator-visible changes from the fixes** (upgrade notes in [operations/upgrade-rollback.md](operations/upgrade-rollback.md)):
- `env:` secret references must use `HP_SECRET_`-prefixed variables.
- Existing users who sign in through a pre-provisioned SSO provider must be marked for that provider.
- Demo mode refuses to listen on non-loopback addresses unless `--demo-allow-public` is set.
- Changing a destination's URL to a different origin requires re-entering its stored secret.

## Not done, or blocked on you
- **Live verification** is blocked on credentials and instances you provide:
  - provider API keys;
  - a Claude setup-token;
  - the `codex`, `opencode` and `pi` binaries, with a ChatGPT login for Codex;
  - Porch, Pulse, and Telegram, Slack and Discord endpoints.

  Opt-in scripts and the compatibility matrix are ready to record results.
- **Publishing:** the GitHub repositories, CI and release runs, Terraform Registry publication and the GPG key are yours to do. Nothing has been pushed.
- **UI items left from the UX review:**
  - stepped create wizards;
  - a "try this policy" panel inside the policy editor;
  - a server-side time filter for the audit log;
  - turning event relays into an alert channel type.
- **Known limits** are listed in [LIMITATIONS.md](LIMITATIONS.md). They include:
  - masking gaps: base64, letter case, split lines and media metadata;
  - SQLite physical-erasure limits;
  - one instance per data directory;
  - Codex's code-mode wrapper and `apply_patch`, which only the read-only sandbox and the fail-closed check contain.

## Housekeeping
Commit `eb147dc5` (gateway credential scrubbing) also contains a UI fork's deletion of four unused web files, because the git index was shared. The change is correct; only the commit grouping is off. History was not rewritten.
