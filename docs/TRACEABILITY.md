# Traceability Matrix

This matrix maps every requirement in the product brief to the phase, package and evidence that cover it. Requirement IDs follow the brief's section numbers, in the form `R<section>.<n>`.

**Status values:**
- `planned`: not started.
- `in-progress`: being built.
- `implemented`: the code exists and its unit tests pass.
- `contract-tested`: checked against the contract with fixtures, fakes or a simulator.
- `live-verified`: run against a real system. The Evidence column must record the date and version.
- `blocked`: waiting on an external prerequisite. See LIMITATIONS.
- `unsupported`: will not be built. Requires a decision reference.

**Evidence:** the test name, script, or doc that proves the status.

## §2 Non-negotiable goals
| ID | Requirement | Phase | Package(s) | Status | Evidence |
|---|---|---|---|---|---|
| R2.1 | Multi-provider, multi-account gateway | 4–5 | gateway, executor, routing | implemented | gateway, routing, executor/httpapi; acceptance A/B/E |
| R2.2 | Harness integrations and protocol translation | 4, 12 | protocol, harness | in-progress | protocol adapters contract-tested; harness tooling Phase 12 |
| R2.3 | API, subscription (CLI bridge) and local executors | 4, 7 | executor | contract-tested | HTTP API + local executors; Claude Code bridge against the real CLI with a synthetic upstream (bridge/claudecode, TestClaudeCodeBridge*); Codex bridge against a fake CLI (bridge/codex); live runs blocked (credentials / binary) |
| R2.4 | Explainable Expr routing plans and decision tables | 5 | routing | in-progress | routing/engine.go (rules, decision tables, score), routingapi; TestEngineRules; editor UI pending |
| R2.5 | Quota monitoring, account switching, budgets, attribution | 5–6 | quota, accounting | contract-tested | quota monitors feed routing health (TestQuotaMonitorDrivesRouting), account switching (scenario C), budgets enforced in reservations (TestBudgetBlocksMeteredSpend), ledger attribution by workspace/project/binding |
| R2.6 | Consumer-neutral identity via tokens and base URLs | 3–4 | controlplane, gateway | implemented | gateway auth by hpk_ token + base URL; acceptance A |
| R2.7 | Activity statistics (models, intents, tools, sessions, attempts) | 4, 11 | telemetry | in-progress | requests/attempts/sessions/tool observation + activityapi; intents & rollups Phase 11 |
| R2.8 | Optional short-lived traces | 8 | traces | planned | |
| R2.9 | Secret masking and privacy controls | 8 | privacy | planned | |
| R2.10 | Authorized native passthrough during protocol drift | 9 | compat, gateway | planned | |
| R2.11 | Protocol Watch changelog | 9 | compat | planned | |
| R2.12 | Durable events plus Pulse/NATS | 10 | events | planned | |
| R2.13 | Telegram, Slack, Discord and webhook notifications | 10 | notify | planned | |
| R2.14 | Local password and OIDC SSO | 2 | auth | implemented | internal/auth, internal/auth/authapi; auth/oidc_test.go (contract) |
| R2.15 | UI preserving Quota Harbor's stack and theme | 0–15 | web | planned | |
| R2.16 | Terraform provider | 14 | ../terraform-provider-harbor-pilot | planned | |
| R2.17 | Migration, backup, recovery, security, ops docs | 13 | store, cmd, docs | planned | |

## §3 Discovery and design deliverables
| ID | Requirement | Status | Evidence |
|---|---|---|---|
| R3.1 | PRODUCT, ARCHITECTURE, PLAN, DECISIONS, THREAT_MODEL, TRACEABILITY, LIMITATIONS, COMPATIBILITY, contracts/, AGENTS.md | implemented | `docs/`, `AGENTS.md` |
| R3.2 | Verified reference contracts, licensing, UI tokens | implemented | `docs/contracts/reference-*.md` |
| R3.3 | Domain model, topology, capability matrix, migration strategy, API/event/config/TF contracts | in-progress | ARCHITECTURE §1–6, docs/contracts/, docs/reference/auth.md |

## §4 Platform
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R4.1 | Go, one binary, embedded UI | 0–1 | implemented | cmd/harbor-pilot, internal/app, internal/web; app_test.go |
| R4.2 | SQLite WAL, single instance, documented capacity | 1, 15 | planned | |
| R4.3 | Quota Harbor UI stack, pnpm, lint/test/E2E conventions | 0 | implemented | web/ (pinned stack), make check, web/e2e |
| R4.4 | Container and native deployment | 13 | planned | |
| R4.5 | Deterministic, isolated demo mode | 4+ | implemented | demo seed + synthetic upstreams + real-path traffic (internal/demo) |
| R4.6 | Execution, control, telemetry, events and notifications kept separate; bounded queues | 4, 10 | implemented | bounded telemetry queue with drop counters; outbox-only events (D-020) |

## §5 Domain model and identity
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R5.1 | All domain concepts listed in the brief | 3 | in-progress | controlplane/specs.go (workspace, project, provider, account, entitlement, pool, alias, plan, binding, credential); policy/trace/event/notification kinds land with their phases |
| R5.2 | Client binding composition | 3 | implemented | BindingSpec (owner, plan, projects, harness, limits, trust, refs); controlplane tests |
| R5.3 | Token-plus-base-URL onboarding with no custom headers | 4 | implemented | acceptance A |
| R5.4 | Token scopes, expiry, revocation, ownership, last-used, nonreversible storage, shown once, never forwarded upstream | 3–4 | implemented | controlplane/credentials.go (scopes, expiry, revocation, verifier, last-used, children); TestGatewayCredentials |
| R5.5 | Base URLs select namespaces but do not authenticate | 4 | implemented | namespaced bases /anthropic,/openai,/gemini; token required |
| R5.6 | Trusted versus untrusted metadata; harness detection treated as telemetry | 4 | implemented | claims vs trusted_refs; DetectHarness confidence; gateway/handle.go |
| R5.7 | Optional external refs (SpaceMan, CI, repo) | 3, 11 | in-progress | project/binding external_refs; credential bound_refs (trusted, mint-time) |
| R5.8 | Session uncertainty recorded; no merging by token | 4, 11 | implemented | sessions only from bound refs or trusted hints; acceptance A asserts no merge |

## §6 Harness, protocol and provider layers
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R6.1 | Three distinct adapter layers | 4 | implemented | internal/protocol (wire), internal/executor (provider), harness detection (telemetry) |
| R6.2 | Setup guides and reversible config tooling (preview, backup, atomic write, conflict, uninstall) | 12 | planned | |
| R6.3 | Typed canonical IR with capability negotiation and extensions | 4 | implemented | internal/ir + capability Requirements |
| R6.4 | Coverage of instructions, multimodal, tools, streaming args, structured output, reasoning, opaque state, cache, usage, finish/errors/cancel/rate-limit | 4 | contract-tested | protocol_test.go (tools, streaming args, reasoning, sealed, usage, errors, cancellation via ctx) |
| R6.5 | No invented reasoning; opaque state never turned into text | 4 | contract-tested | TestAnthropicToChatTranslation/TestResponsesNativeAndForeign assert no leak of sealed state |
| R6.6 | Explicit-policy rejection or downgrade; unknown extensions preserved | 4, 9 | implemented | ir.Loss severities; executor rejects LossReject unless AllowLosses |
| R6.7 | Capability-qualified compatibility matrix | 4+ | in-progress | COMPATIBILITY.md |
| R6.8 | Bridge isolation: no shared credential rewrite | 7 | contract-tested | per-account CLAUDE_CONFIG_DIR/HOME, per-process work dir, setup-token only in the child env, built-in tools disabled, client tools via per-process loopback MCP token (bridge/claudecode tests) |

## §7 Providers, quotas, accounting
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R7.1 | Migrate Quota Harbor integrations, forecasting, alerts, provenance, staleness, reconciliation | 6 | contract-tested | internal/quota/{providers,polling,analytics,forecasting,oauthflow} ported with fixture tests (testdata/quota); provenance/quality per observation; staleness; external usage + /usage/reconciliation; alerting arrives with notifications (Phase 10) |
| R7.2 | Integration classification (official, partial, unofficial opt-in, unsupported) | 6 | implemented | adapter tiers official/unofficial/client_reported/demo; unofficial requires acknowledge_terms_risk (TestUnofficialAdapterNeedsAcknowledgement); capability matrix per adapter (GET /quota/adapters) |
| R7.3 | Multiple windows, resets, freshness, unknown vs zero, concurrency, auth health, cooldowns, affinity, external usage | 6 | contract-tested | multiple buckets per monitor with window/reset semantics; freshness and stale flags; nil remaining stays unknown (TestBoardHealth, TestParseHeaders); local concurrency via reservations; monitor auth health; gateway cooldowns merged with quota health; affinity (routing); provider-reported external usage |
| R7.4 | The seven separated money/usage categories | 6 | contract-tested | usageapi categories: gateway_observed, estimated_api_charge, actual_api_charge, list_equivalent, subscription_fee, allocation, plus provider-reported quota on the board; never summed across categories (TestBudgetBlocksMeteredSpend usage summary) |
| R7.5 | Hierarchy: workspace → run → session → request → attempts | 4, 11 | in-progress | ledger carries workspace/project/run/session/request/attempt |
| R7.6 | Immutable, idempotent ledger with corrections; fixed-precision money | 4 | implemented | accounting ledger (append-only, idempotency_key, provenance, money.Amount) |
| R7.7 | Atomic reservations; abandoned and crash recovery | 4 | contract-tested | accounting.Reserve (IMMEDIATE tx), reaper, RecoverStartup; scenario E |
| R7.8 | Policy-controlled failover order; no ambiguous replay; no splicing; no unapproved paid fallback | 5 | contract-tested | pre-first-byte failover, Transition steps, no ambiguous replay, paid fallback filter; TestScenarioC, TestScenarioD, TestTransitions |

## §8 Routing
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R8.1 | Plan contents (aliases, pools, restrictions, capabilities, quota, ranking, retry, privacy, degradation) | 5 | planned | |
| R8.2 | Aliases as data, not hard-coded | 3, 5 | implemented | model_alias kind (path names), plan model_mappings; ResolveModel tests |
| R8.3 | Bounded Expr: typed, no side effects | 5 | contract-tested | expr-lang typed env (expr.Env, AsBool/AsFloat64, MaxNodes, no now()/side-effect builtins); TestCompileErrors |
| R8.4 | Nine evaluation stages; hard restrictions cannot be overridden by scores | 5 | in-progress | request stage (block/require/set_intent) → hard restrictions → health → candidate rules (exclude/boost) → score; exclusions never re-admitted (TestEngineRules); remaining stages tracked with quota/privacy/degradation phases |
| R8.5 | Rule builder, decision tables, Expr editor, variable reference | 5 | in-progress | variable reference API (GET /routing/variables), expression validation API; rule builder/table/Expr editor UI pending |
| R8.6 | Unknown, stale and missing semantics; immutable snapshots | 5 | in-progress | known() guard, unknown values error instead of matching (TestEngineRules), immutable snapshot; quota freshness exposed as candidate.health.stale (TestBoardHealth) |
| R8.7 | Draft, validate, publish, rollback; simulator; fixtures; replay distinct from simulation | 5 | contract-tested | draft/validate/publish/rollback (TestPlanVersionsPublishRollback); simulate and replay APIs labelled mode=simulation/replay, no upstream calls (TestSimulationAndReplay); UI pending |
| R8.8 | Cycle and ambiguity prevention; read-only decision graph | 3, 5 | in-progress | alias cycle/depth detection, duplicate mapping rejection (snapshot.go); decision graph UI pending |

## §9 Activity and intents
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R9.1 | Requested, alias, capability, recommendation, actual, rules: each recorded separately | 4–5 | implemented | requests table: requested/alias/resolution/plan/caps/actual + decision JSON |
| R9.2 | Intent sources with classifier controls | 11 | planned | |
| R9.3 | Honest tool observation (offered, generated, results seen, provider-executed) | 4 | implemented | tools offered/calls generated/results seen/errors (ObserveTools) |
| R9.4 | History dedupe; unique vs uncertain occurrences | 11 | planned | |
| R9.5 | Statistics across all listed dimensions | 11 | planned | |
| R9.6 | OpenSearch-ready logs; no high-cardinality metric labels | 1, 11 | in-progress | platform/observability (redacting JSON logs, low-cardinality metrics) |

## §10 Traces
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R10.1 | Metadata and content configured independently; four content modes | 8 | implemented | trace_policy kind (off, masked_diagnostic, masked_full, time-boxed unredacted); request metadata recorded independently in activity |
| R10.2 | 24h, 3d and 7d presets plus a bounded custom TTL; off by default | 8 | contract-tested | retention 24h/3d/7d/custom (1h–30d), default off (normalizeTrace); TestScenarioG uses custom 1h |
| R10.3 | Expiry enforced at read; bounded deletion; index removal; quotas; truncation flags | 8 | contract-tested | read-time expiry (Get/List/Search filter expires_at), bounded batch sweep removing rows, parts and FTS entries; per-plan byte quota and per-trace truncation flags (traces.write); TestScenarioG |
| R10.4 | Encryption, access control, audit, excluded from backups, export policy | 8 | contract-tested | AES-GCM per (policy, expiry-day) DEK wrapped by the master key; traces.content.read / .unredacted / .export / .delete grants; audited read, search, export and delete; export only when the policy allows; TestScenarioF |
| R10.5 | Physical-deletion limits documented; crypto-erasure defined | 8 | implemented | LIMITATIONS physical erasure; D-013, D-029 key partitioning and destruction |

## §11 Masking
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R11.1 | Deterministic detectors, custom rules, optional PII detection | 8 | contract-tested | internal/privacy detectors: private keys, cloud/API tokens, secret assignments, credential URLs, configured secret values, custom RE2 rules, optional personal data, entropy (warn only); TestDetectors |
| R11.2 | Mask, block, warn and allow actions with precedence | 8 | contract-tested | precedence block > allow > mask > warn, allow never overrides block (TestPrecedence, TestPrivacyBlockRule) |
| R11.3 | Placeholder properties (stable, isolated, unpredictable, chunk- and escape-safe, not applied to sealed parts) | 8 | contract-tested | HPSEC_ placeholders: random 100-bit, collision-checked, stable per binding+session scope, isolated across scopes, JSON-aware masking, chunk-boundary restoration, sealed parts untouched, memory-only bounded vault (TestMaskRequestPlaceholders, TestRestorerAcrossChunks) |
| R11.4 | Tool-argument restoration off by default; anti-exfiltration | 8 | contract-tested | tool-argument restoration off by default; per-tool and per-field allowlists; never into URLs to unlisted hosts (TestToolArgRestorationIsGuarded) |
| R11.5 | Masking failure blocks unless explicitly allowed | 8 | contract-tested | masking errors block when required unless allow_unmasked_fallback (gateway/privacy.go); passthrough refused under required masking (TestScenarioJ) |
| R11.6 | Rule sandbox; per-request summaries | 8 | contract-tested | POST /privacy/sandbox (nothing stored or logged); per-request summaries in activity (TestPrivacyBlockRule, TestScenarioF) |

## §12 Passthrough and circuit breakers
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R12.1 | Translated, native inspected and native passthrough modes | 9 | contract-tested | executor modes translated, native, passthrough; gateway deliverRaw forwards bytes with auth, destination selection, credential isolation, transport limits and basic telemetry (TestScenarioH) |
| R12.2 | Pre-authorized feature loss; no silent weakening | 9 | contract-tested | plan compatibility.allowed_losses pre-authorizes masking, detailed_usage, intent_inspection, tool_statistics, traces; passthrough needs detailed_usage and tool_statistics listed; traces suspended; unknown_usage and unenforceable_budget policies (TestScenarioH, TestScenarioJ) |
| R12.3 | Failure classification; scoped breakers; half-open; manual override | 9 | contract-tested | compat.Classify separates compatibility from auth, rate limit, outage, client and capability failures; breakers per provider and protocol path with window threshold, cooldown backoff, single half-open probe, manual override, operator-triggered live canary (TestClassify, TestBreakerLifecycle) |
| R12.4 | Inbound incompatibility detection; no replay or splicing | 9 | contract-tested | reject-severity translation losses refuse before dispatch; executed attempts are never replayed and streams are never spliced: a request is entirely inspected or entirely passthrough (TestScenarioH) |
| R12.5 | Degradation shown in UI; durable transition events | 9–10 | in-progress | durable compat.breaker.{opened,half_opened,closed} events; GET /compat/degradations lists open breakers with each plan's action and losses; UI banner pending |

## §13 Protocol Watch
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R13.1 | Tracked change kinds and metadata | 9 | contract-tested | protocol_changes: new_event, new_field, new_enum_value, type_change, expected_absent, unsupported_sequence, schema_rejection, parse_failure, restored; direction, protocol, provider, adapter version, client harness/version, first/last seen, count, confidence, impact; coverage counters |
| R13.2 | Absence semantics; confidence | 9 | implemented | expected_absent findings are low confidence and labelled as not proof of removal; recency current / not_observed_recently derived from last_seen |
| R13.3 | Deduplicated work items with status workflow | 9 | contract-tested | one work item per change fingerprint; repeated observations add evidence and routes; resolved items reopen, ignored stay ignored; status workflow and issue/commit/release refs via PATCH with If-Match (TestScenarioH part I) |
| R13.4 | Bounded observer that never stalls | 9 | contract-tested | bounded 1024-signal queue, drops counted, passthrough samples capped at 256 KiB and taken after forwarding (deliverRaw) |
| R13.5 | Sanitized fingerprints; tool schemas separated; no raw dumps | 9 | contract-tested | structure-only fingerprints, dynamic and id-like keys normalized, tool schemas and arguments excluded, rejection messages stripped of values (TestFingerprintSanitization; scenario I asserts no content stored) |
| R13.6 | Fixture creation from synthetic data; no automatic replay of real prompts | 9 | contract-tested | fixtures generated from structural diffs with placeholder values and a review flag; probes use synthetic canary prompts only on explicit operator request |

## §14 Events and notifications
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R14.1 | Versioned transport-independent events | 10 | implemented | events.Event envelope with schema_version, type harborpilot.<area>.<entity>_<action>.v1, correlation id, subjects, allowlisted data; docs/contracts/events.md generated from internal/events/catalog.go (TestCatalogDoc) |
| R14.2 | Pulse/NATS against the verified contract | 10 | contract-tested | relay.ConnectPulse via go-pulse v0.1.5 (creds, CA, source); subject evt.harborpilot.*, Nats-Msg-Id = event id, ce-type, ce-time; TestPulseRelayPublishesWithIdempotencyHeaders against embedded JetStream (2-minute dedupe verified); live Pulse: blocked (server) |
| R14.3 | Outbox, retries with jitter, idempotency, dead letters, history, replay | 10 | contract-tested | transactional outbox, per-destination cursors, FIFO delivery stopping at first failure, 2^(n-1)s ±50% jitter capped at 5 min, Retry-After, dead letters (attempts/age), claims against double sends, delivery history, audited replay (TestWebhookRelaySignedRetriedAndReplayed) |
| R14.4 | Event catalog (all listed transitions) | 5–10 | implemented | catalog: account switched, fallback selected, request blocked, quota exhausted/recovered/auth failed, budget warning/exceeded, account cooldown/recovered, plan published, breaker opened/half-opened/closed, passthrough activated/blocked/deactivated, protocol change detected, config changes |
| R14.5 | Telegram, Slack, Discord and webhook adapters | 10 | contract-tested | notify adapters: Telegram (HTML escaping, retry_after), Slack (mrkdwn escaping, Retry-After), Discord (allowed_mentions none, retry_after), signed webhook; contract-tested against local fakes; live: blocked (credentials) |
| R14.6 | Rules, routing, dedupe, hysteresis, quiet hours, digests, recovery, templates, rate limits, test, replay | 10 | contract-tested | rules with event globs, severity and subject filters; channel routing; hysteresis/grouping by dedupe key with "+N similar"; recovery notices; quiet hours with timezone and critical bypass; digests; text/template with per-channel escaping and 3500-character bound; per-channel rate limit; test delivery; audited replay (TestNotificationsChannelsHysteresisRecoveryAndTest, notify unit tests) |
| R14.7 | Webhook signing and SSRF; outbound network policy | 1, 10 | contract-tested | webhook X-Harbor-Signature t=..,v1=.. over "<t>.<body>" with rotation (current + previous secret); outbound.Client: netpolicy dial guard, metadata always blocked, private only when allowed, no redirects, 10s timeout, 64 KiB response limit |
| R14.8 | No prompts by default; no loops; outages do not block | 10 | contract-tested | bodies built from allowlisted fields only; rules cannot match notification.* or event.* (no loops); delivery runs on background goroutines (TestScenarioK) |

## §15 Auth
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R15.1 | Local passwords plus OIDC (Porch-compatible) | 2 | contract-tested | auth/oidc_test.go (in-process OP, Porch claim shape); live Porch: blocked |
| R15.2 | Bootstrap, hashing, rate limits, cookies, CSRF, expiry, revocation | 2 | implemented | auth/service_test.go, app/app_test.go, web/e2e/auth.spec.ts |
| R15.3 | OIDC discovery, state, nonce, PKCE, iss/aud; account linking; role mapping | 2 | contract-tested | auth/oidc_test.go: PKCE, nonce, iss/aud, RFC 9207, browser binding, JIT/preprovisioned, group roles |
| R15.4 | Break-glass; admin/operator/viewer; scoped service accounts; trace-content permissions; audit | 2 | implemented | auth/rbac.go, auth/login.go (break-glass + alert), audit; app_test.go |
| R15.5 | Encrypted secrets or secret refs; KEK management documented | 1 | implemented | platform/crypto (+tests), store/serverkeys.go, app/rotate.go, docs/operations/master-key.md |

## §16 API, configuration and CLI
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R16.1 | Versioned OpenAPI: generated and validated | 1 | implemented | huma + api/openapi.yaml; cmd/harbor-pilot/openapi_test.go (drift + x-permission) |
| R16.2 | Stable IDs, pagination, structured errors, redaction, concurrency control, idempotency | 1, 3 | implemented | envelope, cursor pagination, ETag/If-Match (412), referenced-delete 409, idempotent import; app/controlplane_test.go |
| R16.3 | Configuration revision and effective-policy endpoints | 3 | implemented | GET /config/revisions, GET /config/effective |
| R16.4 | One authority; export and import; last-known-good; merge vs replace | 3 | implemented | controlplane/bundle.go merge/replace/dry-run, compile-before-commit + atomic snapshot swap; TestExportImportRoundTrip |
| R16.5 | Terraform-managed marking and drift | 3 | implemented | managed_by + drift_state; TestRevisionsAndDrift, TestControlPlaneOverHTTP |
| R16.6 | CLI operations; secrets read from stdin, file or env | 1–13 | in-progress | serve, admin bootstrap/recover, keys generate/rotate, config validate, openapi, healthcheck |

## §17 Terraform
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R17.1 | Plugin Framework provider in its own module | 14 | planned | |
| R17.2 | Provider configuration (URL, auth, TLS, timeouts); no secrets in logs | 14 | planned | |
| R17.3 | All resource kinds listed in the brief, plus data sources | 14 | planned | |
| R17.4 | CRUD, import, drift, not-found, conflict, state upgrade, references, acceptance tests | 14 | planned | scenario L |
| R17.5 | Secret handling: references, write-only, ephemeral, opt-in token in state | 14 | planned | |
| R17.6 | Convergence: empty plan after apply | 14 | planned | |
| R17.7 | Seven examples | 14 | planned | |
| R17.8 | Release builds; no Registry claim | 14 | planned | |

## §18 UI
| ID | Requirement | Phase | Status | Evidence |
|---|---|---|---|---|
| R18.1 | All twelve screen groups listed in the brief | 2–11 | planned | |
| R18.2 | "Why this route?" screen | 4, 11 | implemented | GET /activity/requests/{id}; web/src/routes/activity/$requestId.tsx; e2e activity.spec.ts |
| R18.3 | Loading, empty, error, stale, partial, expired and denied states; keyboard access; responsive | all | in-progress | states/ErrorBoundary, a11y + responsive e2e for current screens |
| R18.4 | Deterministic synthetic demo exercising real paths | 4+ | planned | |

## §19 Testing and acceptance scenarios
| ID | Scenario | Phase | Status | Evidence |
|---|---|---|---|---|
| A | Standalone harness using only a token and base URL | 4 | planned | `internal/acceptance/scenario_a_test.go` |
| B | Cross-protocol tools and streaming | 4 | planned | scenario_b |
| C | Exhaustion leads to failover and one switch event | 5 | contract-tested | internal/acceptance/routing_test.go TestScenarioC_ExhaustionFailoverOneSwitchEvent |
| D | Unapproved paid fallback blocked | 5 | contract-tested | internal/acceptance/routing_test.go TestScenarioD_UnapprovedPaidFallbackBlocked |
| E | No over-allocation of concurrent reservations | 4 | planned | scenario_e |
| F | Masked secret absent upstream and from persistence | 8 | contract-tested | internal/acceptance/privacy_test.go TestScenarioF_MaskedSecretAbsentUpstreamAndPersistence |
| G | Expired traces unreadable and unsearchable | 8 | contract-tested | internal/acceptance/privacy_test.go TestScenarioG_ExpiredTracesUnreadableAndUnsearchable |
| H | Drift activates approved passthrough with no splice | 9 | contract-tested | internal/acceptance/compat_test.go TestScenarioH_DriftActivatesApprovedPassthrough |
| I | Protocol Watch dedupes a sanitized change | 9 | contract-tested | internal/acceptance/compat_test.go TestScenarioH_DriftActivatesApprovedPassthrough (Protocol Watch assertions) |
| J | Required privacy blocks raw fallback | 8 | contract-tested | internal/acceptance/compat_test.go TestScenarioJ_RequiredPrivacyBlocksRawFallback |
| K | Pulse or notification outage does not block requests | 10 | contract-tested | internal/acceptance/events_test.go TestScenarioK_DeliveryOutagesDoNotBlockGateway |
| L | Terraform lifecycle | 14 | planned | TF acceptance |
| M | Restart and restore integrity | 13 | planned | scenario_m |
| N | SpaceMan and standalone client isolated | 11 | planned | scenario_n |

## §20 Final delivery
Phase 15 tracks the final delivery checklist. It is updated when the project completes.
