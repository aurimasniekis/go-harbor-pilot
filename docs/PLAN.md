# Implementation Plan

Source of truth for sequencing. Status per phase is updated as work lands; requirement-level status lives in TRACEABILITY.md.

| Phase | Scope | Status |
|---|---|---|
| 0 | Discovery docs and scaffold | done |
| 1 | Platform foundation (clock, money, credentials, observability, netpolicy, store/migrator, crypto, ids, audit, huma API skeleton, outbox core) | done |
| 2 | Authentication and authorization (passwords, sessions, CSRF, OIDC, RBAC, service principals, bootstrap/recovery) | done |
| 3 | Control-plane domain (all managed objects, revisions, drift, snapshot compiler, import/export) | done |
| 4 | Canonical IR, protocol adapters, gateway vertical slice, HTTP/local executors, ledger, activity (scenarios A, B, E) | done |
| 5 | Routing engine, decision tables, simulator, publish workflow, failover (scenarios C, D) | done |
| 6 | Quotas, budgets, forecasting (migrated adapters, header-derived observations) | done |
| 7 | CLI bridges (Claude Code, Codex) | done (live runs blocked on credentials and the codex binary) |
| 8 | Privacy masking and short-lived traces (scenarios F, G, J) | done |
| 9 | Compatibility breakers, passthrough, Protocol Watch (scenarios H, I) | done |
| 10 | Events (outbox, Pulse, webhooks) and notifications (scenario K) | done |
| 11 | Intents, statistics, SpaceMan integration, Why-this-route (scenario N) | done |
| 12 | Harness setup tooling and operational CLI | done |
| 13 | Operations: backup/restore, Quota Harbor migration, release automation (scenario M) | done (release workflow runs once the repository is pushed) |
| 14 | Terraform provider (scenario L) | done (Registry publication blocked on the owner) |
| 15 | Hardening: load tests, E2E, a11y, security review, final report | done (see docs/REPORT.md) |

## Working rules

- Every phase ends with green `make check`, phase tests, UI for the phase, and doc updates (TRACEABILITY, COMPATIBILITY, LIMITATIONS).
- Capabilities are never upgraded in status without evidence (see PRODUCT.md, Evidence vocabulary).
- Reference projects are not modified. Integration changes they would need are documented in docs/contracts/.
