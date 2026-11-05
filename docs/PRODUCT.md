# Harbor Pilot — Product

> Harbor Pilot connects coding agents to models and subscriptions, routes each
> request through explicit policies, and explains what happened.

Harbor Pilot is a self-hosted LLM gateway and policy control plane, built as a single Go binary on SQLite. Coding harnesses (Claude Code, Codex, OpenCode, Pi and generic API clients) connect to it with a gateway token, a base URL and, where the client needs one, a model identifier. Harbor Pilot then:

1. **Authenticates** the caller as a client binding, which ties together a principal, a routing plan, an optional project, permissions, limits and telemetry policy.
2. **Translates** between wire protocols (OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, Gemini). The capability matrix states which combinations are supported. Unsupported features are rejected or downgraded only under an explicit policy.
3. **Routes** each request through a versioned routing plan. Plans contain aliases, account and model pools, hard restrictions, Expr-based decision rules and scores, retry and fallback policy, and privacy and trace policy.
4. **Executes** the request against API-key providers, local model endpoints, or native CLI bridges that run a vendor's own binary under an isolated, authorized subscription login.
5. **Accounts** for the request truthfully. Provider-reported quota, gateway-observed consumption, internal budgets, actual API charges, estimates and subscription fees are kept as separate figures.
6. **Explains** each request through the "Why this route?" view. It shows the trusted context, the requested, resolved and actual model, the policy version, which candidates were excluded and why, quota freshness, attempts, tool observations, masking, degradation and final accounting.

## Who it is for
- **Individual developers** who run several harnesses against a mix of subscriptions, API keys and local models, and want one place to switch accounts, enforce budgets and see what happened.
- **Small teams and platforms**, such as agent orchestrators (SpaceMan is one consumer) and CI systems. They need per-project or per-run attribution, budgets, privacy controls and durable events, without a dependency on any one consumer.

## What it is not
- It does not bypass provider restrictions or evade detection. Account switching selects among accounts the operator is authorized to use.
- It does not present subscription consumption as an API invoice.
- It is not a multi-writer high-availability cluster. The supported deployment is a single instance with SQLite WAL (see ARCHITECTURE.md).
- It does not guarantee masking. Masking is defense in depth: local tools that bypass the gateway are outside its inspection boundary.
- Harness detection is telemetry, not proof of which executable made the request.

## Capability areas
| Area | Summary | Doc |
|---|---|---|
| Gateway | Multi-provider and multi-account gateway; inbound protocols; streaming; cancellation | `docs/contracts/gateway.md` |
| Harnesses | Setup guides and reversible config tooling for Claude Code, Codex, OpenCode, Pi | `docs/guides/harnesses.md` |
| Executors | API keys, local endpoints, Claude Code and Codex CLI bridges | `docs/COMPATIBILITY.md` |
| Routing | Plans, aliases, pools, decision tables, Expr, simulator, replay, explanations | `docs/reference/policy.md` |
| Quotas and accounting | Quota windows, freshness, budgets, reservations, ledger, forecasts | `docs/reference/accounting.md` |
| Activity | Models, intents, tools, sessions, attempts, statistics | `docs/reference/activity.md` |
| Privacy | Outbound masking, trace redaction, rule sandbox | `docs/reference/privacy.md` |
| Traces | Short-lived encrypted traces with 24h, 3d or 7d retention, or a custom TTL | `docs/reference/traces.md` |
| Compatibility | Circuit breakers; translated, native-inspected and passthrough modes; Protocol Watch | `docs/reference/compatibility.md` |
| Events | Transactional outbox, Pulse/NATS relay, signed webhooks | `docs/contracts/events.md` |
| Notifications | Telegram, Slack, Discord, generic webhooks | `docs/reference/notifications.md` |
| Auth | Local passwords, OIDC SSO (Porch-compatible), RBAC, service principals | `docs/reference/auth.md` |
| API, CLI, Terraform | Versioned OpenAPI control plane, operational CLI, Terraform provider | `api/openapi.yaml`, `docs/contracts/terraform.md` |
| Operations | Backup and restore, upgrade and rollback, Quota Harbor migration, troubleshooting | `docs/operations/` |

## Evidence vocabulary
Every capability is reported with one of these statuses, here and in `docs/COMPATIBILITY.md` and `docs/TRACEABILITY.md`:

- **implemented**: code exists and unit tests pass.
- **contract-tested**: verified against recorded or synthetic fixtures, or a fake upstream or binary, that follow the documented contract.
- **live-verified**: exercised against the real upstream, harness or service with authorized credentials. The record includes date, version and script.
- **unsupported**: deliberately not supported, with the reason.
- **blocked**: needs an external prerequisite (credentials, binaries, services) that is not available yet.

Mocks are tests. They are not evidence that a real upstream is compatible.
