# Architecture

Harbor Pilot is a self-hosted LLM gateway and policy control plane: one Go binary on SQLite with an embedded React UI.
Coding tools (Claude Code, Codex, OpenCode, Pi, any SDK) connect with a gateway token, a base URL and a model name.
For each request Harbor Pilot:

1. **authenticates** the caller as a client binding (principal, routing plan, optional project, scopes, limits,
   telemetry policy);
2. **translates** between wire protocols (Anthropic Messages, OpenAI Chat Completions and Responses, Gemini), rejecting
   or downgrading unsupported features only under explicit policy;
3. **routes** it through a versioned routing plan (aliases, pools, hard restrictions, Expr rules and decision tables,
   fallback, privacy and trace policy);
4. **executes** it against API-key providers, local model servers, or CLI bridges that run a vendor's own binary under
   an isolated subscription login (D-001);
5. **accounts** for it truthfully: provider-reported quota, observed consumption, budgets, actual charges, estimates
   and subscription fees stay separate figures;
6. **explains** it: "Why this route?" shows the trusted context, requested, resolved and actual model, policy version,
   excluded candidates and why, quota freshness, attempts, masking, losses and accounting.

It does not bypass provider restrictions (it picks among accounts the operator may use), present subscription usage
as an API invoice, cluster, or guarantee masking. Operator documentation is the site (`site/`); this file is for
people changing the code.

## Evidence vocabulary

Every capability carries one status, in COMPATIBILITY.md, TRACEABILITY.md and the site:

- **implemented**: code exists and unit tests pass;
- **contract-tested**: verified against synthetic fixtures, fakes or fake binaries that follow the documented
  contract;
- **live-verified**: exercised against the real upstream, harness or service with authorised credentials, recorded
  with date, version and script;
- **unsupported**: deliberately not provided, with the reason;
- **blocked**: needs an external prerequisite (LIMITATIONS.md).

Mocks are tests, not evidence that a real upstream is compatible.

## 1. Deployment topology

```
 harness / client ──HTTPS──▶ ┌────────────────────── harbor-pilot (one process) ───────────────────────┐
  (token + base URL)         │                                                                          │
                             │  data plane  /v1/*, /anthropic/*, /openai/*, /gemini/*                   │
 operator browser ──────────▶│  control plane /api/v1/*  (huma, OpenAPI 3.1)                            │
 terraform / CLI ───────────▶│  UI  /*  (embedded SPA)          health /healthz /readyz /metrics        │
                             │                                                                          │
                             │  ┌─────────┐  ┌──────────┐  ┌───────────┐  ┌───────────┐  ┌──────────┐  │
                             │  │ gateway │─▶│ routing  │─▶│ executors │─▶│ upstreams │  │ bridges  │  │
                             │  └────┬────┘  └────┬─────┘  └─────┬─────┘  └───────────┘  └──────────┘  │
                             │       │ bounded    │ snapshot      │ attempts                           │
                             │       ▼ channels   ▼ (atomic)      ▼                                    │
                             │  telemetry · observer · traces    accounting (sync, small txn)          │
                             │       │                              │                                  │
                             │  ┌────▼──────────────────────────────▼──────┐   ┌────────────────────┐  │
                             │  │ harbor.db (WAL): control plane, ledger,  │   │ traces.db (WAL):   │  │
                             │  │ activity, quota, outbox, audit           │   │ encrypted content  │  │
                             │  └────┬─────────────────────────────────────┘   └────────────────────┘  │
                             │       ▼ outbox relays (async)                                           │
                             │  Pulse/NATS · signed webhooks · notifications (Telegram/Slack/Discord)  │
                             └─────────────────────────────────────────────────────────────────────────┘
```

- **Supported:** one instance with one data directory on local disk. SQLite runs in WAL mode with a single writer connection and a pool of read-only connections.
- **Not supported:** several instances sharing a database, or SQLite on network filesystems. Interfaces are kept clean (store, outbox, relay), but no distributed infrastructure is built speculatively.
- **Native:** a static binary (`CGO_ENABLED=0`, modernc SQLite), with a systemd unit in `deploy/systemd/`.
- **Container:** a distroless non-root image. The bridge variant adds the vendor CLIs so the Claude Code and Codex bridges can run. Bridges are disabled when their binary is missing.
- **Demo:** `serve --demo [--demo-clock=RFC3339]` uses separate `demo.db`/`demo-traces.db`, a throwaway key, synthetic upstreams and accounts, and listens only on loopback unless `--demo-allow-public`.

## 2. Layers and packages
| Layer | Packages | Responsibility |
|---|---|---|
| Platform | `internal/platform/{clock,money,credentials,observability,ratelimit,netpolicy,crypto,ids}` | Time, fixed-precision money, secret references, redacting logs and metrics, rate limits, outbound-network policy (SSRF), KEK/DEK envelope encryption, prefixed ULIDs |
| Storage | `internal/store` + `migrations/` | SQLite open, pragmas, migrator, repositories, backup |
| Auth | `internal/auth` | Passwords, sessions, CSRF, OIDC, RBAC, service tokens, bootstrap and recovery |
| Control plane | `internal/controlplane` | Managed objects, revisions, drift, snapshot compiler, import/export, audit |
| API | `internal/api` | Huma registration, errors, pagination, ETag, idempotency |
| Canonical model | `internal/ir` | Typed requests, messages, parts, tools, events, usage, capabilities |
| Protocols | `internal/protocol/*` | Parse/build/decode/encode for each wire protocol |
| Gateway | `internal/gateway` | Inbound handlers, trusted context, pipeline, SSE writer, passthrough |
| Executors | `internal/executor/*` | HTTP API, local endpoints, CLI bridges |
| Routing | `internal/routing` | Expr environment, rules, decision tables, ranking, simulator, replay, explanation |
| Accounting | `internal/accounting` | Ledger, reservations, reaper, pricing, reconciliation |
| Quota | `internal/quota` | Provider quota adapters, scheduler, analytics, forecasting |
| Telemetry | `internal/telemetry` | Activity writer, sessions, tool observation, dedupe, intents, statistics |
| Privacy | `internal/privacy` | Detectors, masking, placeholder vault, sandbox |
| Traces | `internal/traces` | Encrypted store, expiry and crypto-erasure, search, access audit |
| Compatibility | `internal/compat` | Circuit breakers, degradation, Protocol Watch |
| Events | `internal/events` | Domain events, outbox, Pulse and webhook relays |
| Notifications | `internal/notify` | Rules, grouping, hysteresis, quiet hours, templates, adapters |
| Harness tooling | `internal/harness` | Detection, reversible config editors |
| Demo | `internal/demo` | Deterministic synthetic scenario and upstream simulator |
| Bootstrap and wiring | `internal/config`, `internal/app` | Bootstrap config, keyring, process wiring, root handler, background jobs |
| Audit | `internal/audit` | Audit log writer |
| Operations | `internal/ops`, `internal/migrate` | Backup, verify and restore; the Quota Harbor importer |
| Testing | `internal/apptest`, `internal/acceptance`, `internal/synthetic`, `internal/loadgen` | Real-server test harness, acceptance scenarios, synthetic upstreams, load generator |
| UI | `web/`, `internal/web` | React SPA, embedded |

Dependencies point downward. `gateway` depends on `routing`, `executor`, `accounting`, `privacy`, `compat` and `telemetry`. It never depends on `notify` or the `events` relays. It only appends outbox rows through `accounting` and `compat` transactions.

## 3. Domain model
```
Principal (user | service) ──owns──▶ GatewayCredential ──binds──▶ ClientBinding
ClientBinding = { principal, routing_plan, project?, expected_harness?, permissions, limits, telemetry_policy, external_refs }
Workspace ─▶ Project ─▶ (Run ref) ─▶ Session ─▶ LogicalRequest ─▶ UpstreamAttempt
Provider ─▶ ProviderAccount ─▶ SubscriptionEntitlement
Pool = ordered/weighted set of (account, model) members; Alias = name → pool | alias (acyclic)
RoutingPlan ─▶ PolicyVersion (draft → validated → published → superseded); decision tables, rules, scores
QuotaObservation (per account × window; provenance, freshness)  BudgetReservation / LedgerEntry
Trace (encrypted, TTL)   ProtocolChange / WorkItem   EventDestination   NotificationChannel / NotificationRule
```
- **Trust:**
  - Identity comes only from a verified credential, its binding, and refs bound at mint time.
  - Client-supplied project, run and session metadata, and declared intent, are stored as *claims* with their provenance. They are honoured for attribution only when the binding permits.
  - Harness detection is recorded with `confidence`.
- **Sessions:** derived from trusted refs first, then from harness session headers (when the binding allows), then from content-continuity fingerprints. Failing all of these, the session is marked `unknown`. Unrelated requests are never merged merely because they share a token.

## 4. Request lifecycle (data plane)
1. **Authenticate.** Look up the gateway token by prefix and verify its HMAC. Load the binding from the current `Snapshot` (an immutable compiled config read through `atomic.Pointer`).
2. **Parse.** The inbound protocol adapter converts the request to the IR. Size and complexity are bounded, so parser cost is bounded.
3. **Privacy.** Apply access and privacy restrictions. Outbound masking runs before any external dispatch and before any remote classifier.
4. **Capabilities.** Derive the required capabilities (tools, streaming, reasoning, images, sealed state, structured output).
5. **Routing.** Filter ineligible candidates, then evaluate decision rules, then rank deterministically. The routing engine returns a `Decision` with a complete explanation.
6. **Reserve.** Reserve budget and concurrency capacity in one `BEGIN IMMEDIATE` transaction, which also writes the ledger `reserve` entry.
7. **Dispatch.** The executor produces an IR event stream. The outbound protocol encoder writes SSE or JSON to the client.
   - Before sending, the executor adds the routed model's configured upstream request fields (D-045) to the body and applies its conflict policy; a model that configures none is sent unchanged.
   - Failover to the next candidate happens **only before the first byte** reaches the client, and only when the attempt failed in a way that is provably unexecuted (the safe-to-retry classification). A field refused by the conflict policy fails the attempt as `invalid_request` and is never failed over.
   - Streams are never spliced.
8. **Reconcile.** Commit actual usage to the ledger, release the remainder, and settle unknown usage per policy. Domain transitions (switch, fallback, block, threshold) go to the outbox in the same transaction.
9. **Telemetry.** Activity, tool observations, trace capture and Protocol Watch observations are published to bounded channels. A full channel drops the item, increments a counter, and marks coverage as `partial`.

The `Snapshot` keeps accounts, pools, plans and compiled Expr programs read-only. Quota state is read from an in-memory `QuotaBoard`, updated by the scheduler and by header-derived observations.

## 5. Storage
- `harbor.db` holds:
  - control-plane tables, each with `revision`, `managed_by`, `drift_state` and `created_at`/`updated_at`
  - `config_revisions`
  - the ledger (append-only), reservations, and activity (requests, attempts, tool observations, session index)
  - the trusted-reference index (D-041): `ledger_refs` has one row per ledger row and trusted ref, written in the ledger
    row's transaction; `request_refs` has one row per request and trusted ref, written with the request row by the
    telemetry writer. Only `trusted_refs` (bound refs and the client's external refs) are indexed, never header claims.
    Usage and activity filters and the `ref:<key>` grouping read these tables.
  - quota observations
  - the outbox, deliveries and audit log
- `traces.db` holds encrypted trace bodies, a per-bucket wrapped DEK table, and an FTS5 index over masked text only.
- Timestamps are INTEGER unix milliseconds in UTC. Money is INTEGER in 1e-10 units, with an explicit currency and price version.
- Migrations are embedded `migrations/{main,traces}/NNNNNNNNNNNNNNNNN_name_up.sql` files. The migrator takes a pre-migration `VACUUM INTO` backup and refuses unknown or out-of-order migrations.
- Retention: trace content expires per trace policy (swept in batches of 500, keys destroyed); sessions, OIDC state
  and one-time tokens are purged. Activity, the ledger, events and the audit log have no retention purge yet, and
  there are no rollup tables (LIMITATIONS.md).

## 6. Configuration authority
- The database is authoritative for every managed object.
- `harbor-pilot.yaml` plus `HP_*` environment variables hold only process bootstrap settings: listen address, data dir, public URL, master key, secret-reference rules, TLS, logging, CLI bridges and demo (`internal/config`; documented in `site/reference/configuration.md`).
- Every mutation runs through one service layer, whether it comes from the UI, the API, the CLI, Terraform or an import. That layer:
  - validates the change and checks `If-Match` against the object's revision
  - writes the change, an audit row and an outbox event
  - bumps `config_revisions`
  - recompiles the `Snapshot`, and swaps it in only after validation succeeds (last-known-good)
- Objects with `managed_by=terraform` that are edited by any other actor are flagged `drifted` and shown as such in the UI. Terraform's next refresh shows the change as a diff.
- Export and import bundles (YAML or JSON) are versioned. They declare per-kind `mode: merge | replace` and carry secrets only as references.

## 7. Concurrency and back-pressure
| Resource | Bound | Overflow behavior |
|---|---|---|
| Request body | 32 MiB default; per binding `limits.max_request_bytes` (at most 64 MiB) | 413 |
| Parser complexity | max messages, parts, tool count, schema depth | 400 with `limit` details |
| Concurrent upstream attempts | per account, per binding, global | queue with timeout, or 429 per policy |
| Telemetry, observer and trace channels | fixed capacity | drop, increment counter, set coverage to `partial` |
| Outbox relays | batch size and in-flight limit | rows wait; no data-plane impact |
| Notification workers | per-channel rate limit and `retry_after` | defer, or dead-letter after the configured number of attempts |
| Background purge | rows per batch and batches per tick | continues on the next tick |

## 8. Capacity

Load tests in `internal/acceptance/load_test.go` (`make load` for full scale) run the gateway in-process in front of
a synthetic upstream that answers at once, so they measure Harbor Pilot's own overhead. Recorded run, 2026-11-05,
Go 1.27.3, 15-core laptop, `HP_LOAD=1`:

| Scenario | Load | Result |
|---|---|---|
| Streaming throughput | 3,200 streamed requests, 64 concurrent clients | ~2,550 req/s; p50 22 ms, p95 54 ms, p99 77 ms; first byte p50 5 ms; every request billed once, no reservation left open |
| Slow clients | 256 clients reading a line every 40 ms beside 1,600 fast requests | fast p95 19 ms (baseline 17 ms); all slow streams completed |
| Telemetry back-pressure | 2,400 requests with a one-slot activity queue | all succeeded, accounting complete; 2,820 activity records dropped and counted (`telemetry_dropped`) |

In production, upstream latency and provider rate limits dominate. The SQLite writer serialises reservations and
ledger writes; it was not saturated above, and its latency was not measured separately. `cmd/hp-loadgen` drives a
running server.

## 9. Security boundaries
See THREAT_MODEL.md (and `site/security/`). In summary:
- Gateway tokens are never forwarded upstream.
- Provider secrets are held as references or encrypted under the KEK, and the API treats them as write-only.
- Outbound requests pass through `netpolicy` (SSRF guard). Private and local endpoints must be explicitly authorized for each provider.
- Human sessions and automation credentials are kept separate.
- Reading trace content requires a separate permission and is audited.
