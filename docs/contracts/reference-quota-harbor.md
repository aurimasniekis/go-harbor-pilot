# Reference: Quota Harbor

- **Source:** `aurimasniekis/go-quota-harbor` @ `1bf2acb`. The read-only clone was used; the diverged local
  working copy `~/Projects/quota-harbor` was **not** used.
- **License:** the repo has **no LICENSE file** (no LICENSE, COPYING or NOTICE; no `license` field in
  `web/package.json`). It is the Harbor Pilot owner's own code, and the owner approved copying and
  evolving it into Harbor Pilot (Apache-2.0). The only third-party licensing note is D20 in
  `docs/decisions.md`: simple-icons marks are CC0, but the brands remain trademarks.
- **Role for Harbor Pilot:** the foundation that is copied and evolved (backend platform, quota adapters,
  usage/alerts logic, UI stack and theme), and the source system for `harbor-pilot migrate quota-harbor`.

## 1. Package reuse map

All are verified-in-source unless marked. The "Harbor Pilot destination" column follows the plan layout.

| Quota Harbor package | What it does | Harbor Pilot destination | Treatment |
|---|---|---|---|
| `internal/clock` | `Real`, `Frozen`, `Fake` clocks | `internal/platform/clock` | copy |
| `internal/money` | `Amount` int64 at 1e-10, `Money{Amount,Currency}`, exact `big.Rat` parsing, half-even rounding, overflow-checked sums | `internal/platform/money` | copy |
| `internal/credentials` | self-redacting `Secret`/`Bundle`, `Resolver` for `env`/`file`/`env_file` refs confined by `os.Root` | `internal/platform/credentials` | copy, plus envelope-encrypted values (D-014) |
| `internal/observability` | slog JSON with regex redaction, request IDs, Prometheus `qh_*` metrics | `internal/platform/observability` | copy, metric prefix becomes `hp_` |
| `internal/ratelimit` | keyed token bucket | `internal/platform/ratelimit` | copy |
| `internal/providers` (`HTTPClient`, `SSRFPolicy`) | hardened outbound client and SSRF guard | `internal/platform/netpolicy` | copy and generalise into an outbound-network policy |
| `internal/persistence/db.go` + migrator | writer (1 conn, `_txlock=immediate`) and reader pool (4, `query_only`); `NNN_up.sql` migrator with a pre-migration `VACUUM INTO` backup | `internal/store` | copy |
| `internal/api` helpers | error envelope `{"error":{code,message,request_id,details?}}`, strict `DecodeJSON`, security headers, `Admin`/`Viewer` gates | `internal/api` | reuse middleware and envelope; huma registers routes (D-011) |
| `internal/web` | `//go:embed all:dist` SPA handler, immutable `/assets`, fallback page | `internal/web` | copy |
| `internal/domain` | `ProviderInfo`, `BucketSpec`, `Provenance`, quality and origin enums | `internal/quota` (domain types) | copy and extend |
| `internal/providers/*` (17 adapters plus demo) | quota and usage fetchers | `internal/quota/adapters/*` | copy |
| `internal/polling` | single-flight scheduler with backoff, Retry-After, auth cooldown, persisted `next_attempt_at` | `internal/quota/scheduler` | copy |
| `internal/analytics`, `forecasting`, `insights` | burn segmentation, median/MAD/EWMA, event-simulation forecast, overview | `internal/quota/{analytics,forecast,insights}` | copy |
| `internal/usage` | ingestion, idempotent `InsertEvents`, aggregate revisions, `Reconcile`, `PriceBook`, budgets | `internal/accounting` + `internal/quota` | evolve: budgets become enforced through reservations |
| `internal/alerts` | 12 rule types, hysteresis, correlation, cooldown, snooze | `internal/notify` (rules) + domain events | evolve: alerts emit domain events |
| `internal/notifications/telegram` | outbox worker: lease, backoff, retry-after, dead letter, quiet-hours digest | `internal/notify/adapters/telegram` | copy; its semantics are shared by Slack, Discord and webhook |
| `internal/oauthflow`, `oauthapi`, `tokenstore` | PKCE login for **quota-monitoring** OAuth; AES-GCM token file | `internal/quota/oauth` | keep for monitoring only; never used to execute requests (D-001) |
| `internal/auth` | stateless HMAC cookie, argon2id, trusted header | — | **replaced** by Hallpass-style server sessions (see `reference-hallpass-auth.md`) |
| `internal/config` | YAML file as the source of truth, with hot reload | — | **replaced**: the database is authoritative and YAML is bootstrap-only (D-012). Its parser is reused only by the importer |
| `internal/events` (SSE hub) | ring buffer, `Last-Event-ID` | `internal/api/sse` | copy |
| `internal/providers/demo` | deterministic `splitmix64` simulator, 13 accounts, scripted incidents | `internal/demo` | copy and extend with synthetic upstreams |

## 2. Database schema (importer source)

Three migrations (verified-in-source, `migrations/`). Timestamps are INTEGER unix milliseconds in UTC.

| Migration | Table | Columns the importer reads |
|---|---|---|
| `20261017011356110_create_core_quota_schema` | `providers` | id, display_name, family, adapter_version, tier |
| | `accounts` | id, provider_id, alias, masked_identifier, tags (JSON), enabled, config_state (`active`/`removed`), provenance (JSON `{file,credential_type,credential_ref}`), is_demo, tier, shared_scope_id |
| | `quota_buckets` | id, account_id, key, label, scope, kind, window_seconds, unit, currency, model, model_family, reset_semantics, period_seconds, global, official, first_seen, last_seen |
| | `snapshots` | id, account_id, fetched_at, source_time, success, error_kind, http_status, latency_ms, is_demo |
| | `bucket_observations` | snapshot_id, bucket_id, observed_at, source_time, limit/used/remaining as REAL and `*_e10` INTEGER, reset_at, quality, source |
| | `account_poll_state` | auth_state, cooldown_until, consecutive_failures, last_success_at |
| | `bucket_rollups_hourly` | bucket_id, hour_start, samples, first/last/min/max_remaining, limit_value, has_reset, has_gap |
| | `config_reloads`, `kv` | not imported (`kv.demo_start` and the session key are QH-local) |
| `20261017015455936_create_usage_schema` | `usage_sources` | id, kind, origin, provider_id, account_id, reporting_delay_seconds |
| | `usage_events` | source_id, idempotency_key, payload_hash, duplicate_of, provider_request_id, timestamps, provider/account, workspace, project, requested_model, resolved_model, token categories, `cache_*_in_input`/`reasoning_in_output` flags, `*_cost_e10`, currency, pricing_version, origin, quality |
| | `usage_aggregates` (+ `usage_aggregate_revisions`) | breakdown, dims_hash, interval_start/end, granularity, token and request counts, currency_verified, partial, revision |
| | `api_keys` | provider_key_id, name, hint, status, workspace, project, expires_at |
| | `pricing_records` | provider_id, model, category, currency, effective_from, per-million price, source |
| | `ingestion_token_state` | not imported (token hashes are re-issued in Harbor Pilot) |
| `20261017075139645_create_alerts_and_outbox` | `alert_incidents`, `alert_events` | imported as **history only** (read-only incident archive) |
| | `alert_rule_state`, `notification_outbox` | not imported; transient delivery state |

Importer rules (implemented in `internal/migrate/quotaharbor`):
- `is_demo = 1` rows are always skipped.
- Provider-reported quota keeps its `quality` and `source`, so provenance survives.
- Usage rows keep `origin`. Imported rows become ledger `external_observed` entries with the `imported`
  provenance, and are never treated as gateway-observed.

## 3. Config keys relevant to import

From `internal/config/schema.go` (verified-in-source). Top-level keys: `server`, `auth`, `metrics`,
`secrets.roots`, `include`, `polling`, `retention`, `forecast`, `dashboard`, `accounts`, `ingestion.tokens`,
`pricing`, `budgets`, `alerts.rules`, `notifications.telegram`, `oauth.token_store`, `demo`.

| QH key | Harbor Pilot target |
|---|---|
| `accounts[]` (`id, provider, alias, enabled, tags, shared_scope, poll_interval, options, credentials, unofficial.acknowledge_terms_risk`) | provider + provider account. Credentials stay **secret refs** (`type: env\|file\|env_file, name, path, key, optional`); literal secrets are never written |
| `polling.*` (`interval, workers, jitter, stale_after, backoff_base/max, auth_cooldown, fetch_timeout, manual_min_interval, providers.<id>.{interval,requests_per_minute}`) | quota scheduler settings |
| `pricing[]` (`provider, model, currency, effective_from/to, per_million, source`) | price versions |
| `budgets[]` (`id, name, scope, period, start, length, timezone, amount, currency, warn_at`) | budgets (monitor-only until a policy enables reservation enforcement) |
| `alerts.rules[]` | notification rules (best-effort mapping; unknown types are reported, never silently dropped) |
| `notifications.telegram` (`bot_token` ref, `chat_id`, `thread_id`, `severities`, `quiet_hours`, `cooldown`, `send_recoveries`) | Telegram channel and rule |
| `ingestion.tokens[]` | not imported as secrets; each is reported so operators can re-issue it |
| `server`, `auth`, `secrets.roots`, `oauth.token_store` | not imported (process-local); the dry-run report lists them |

## 4. Provider adapter tiers

Verified-in-source (`cmd/quota-harbor/features.go`). Unofficial adapters require
`unofficial.acknowledge_terms_risk: true`; validation is in `internal/config/load.go`.

| Adapter ID | Tier | Data source |
|---|---|---|
| `generic_http` | official (operator-defined) | JSON endpoint plus field mapping, SSRF-guarded |
| `claude_code_statusline` | client_reported (push-only) | `POST /ingest/quota` |
| `nexos` | official | usage, api_keys/with_usage, budget_limits, models |
| `anthropic_admin` | official | Usage & Cost Admin API |
| `claude_subscription` | **unofficial** | `api.anthropic.com/api/oauth/usage` (monitoring only) |
| `openai_admin` | official | organisation usage and costs |
| `codex_subscription` | **unofficial** | `chatgpt.com/backend-api/wham/usage` (monitoring only) |
| `deepseek`, `moonshot`, `openrouter` | official | balance / credits |
| `xai` | official | management-api billing |
| `together` | official (beta) | billing usage |
| `cursor` | official | Admin API |
| `github_copilot` | official | premium_request billing usage |
| `gemini_gcp` | official | Cloud Quotas + Monitoring |
| `zai_coding_plan`, `minimax_coding_plan` | **unofficial** | community-observed endpoints |
| `demo_*` (4) | demo | simulator |

Doc-claim (`docs/provider-support.md`, `agent-handoff.md`): **no adapter was live-verified**. They were all
tested against `httptest` fixtures only. Harbor Pilot inherits them as `contract-tested`, not `live-verified`.

## 5. UI stack (exact pins, `web/package.json`)

Verified-in-source. `packageManager: pnpm@11.23.0`, and `.npmrc` sets `save-exact=true`.

| Runtime dependency | Version | Dev dependency | Version |
|---|---|---|---|
| react, react-dom | 19.3.1 | vite | 7.3.7 |
| @tanstack/react-router | 1.170.41 | @vitejs/plugin-react | 5.2.0 |
| @tanstack/react-query | 5.104.3 | @tanstack/router-plugin | 1.168.42 |
| @tanstack/react-store | 0.7.7 | typescript | 5.9.3 |
| @tanstack/react-table | 8.21.3 | eslint / @eslint/js | 9.39.5 |
| @tanstack/react-virtual | 3.14.13 | typescript-eslint | 8.71.2 |
| echarts | 5.6.0 | eslint-plugin-react-hooks | 7.0.1 |
| lucide-react | 1.53.0 | vitest | 4.1.12 |
| simple-icons | 16.33.0 | jsdom | 26.1.0 |
| zod | 4.6.7 | @testing-library/react / jest-dom / user-event | 16.3.3 / 6.9.1 / 14.6.8 |
| | | @playwright/test | 1.64.1 |
| | | @axe-core/playwright | 4.14.0 |
| | | openapi-typescript | 7.13.0 |
| | | @tanstack/react-query-devtools / react-router-devtools | 5.104.3 / 1.167.2 |
| | | @types/node / @types/react / @types/react-dom | 24.19.2 / 19.3.0 / 19.3.0 |

- **Styling:** CSS Modules plus custom properties. **No** Tailwind, shadcn, Radix or component library.
- **Primitives:** hand-rolled in `src/components/ui.tsx`.
- **Charts:** tree-shaken `echarts/core`, themed from CSS variables (`src/components/EChart.tsx`).
- **Icons:** lucide; brand marks come from simple-icons.
- **Theme:** set with `data-theme` on `<html>`; the preference is stored in `localStorage` under `qh.ui.v1`.
  Harbor Pilot uses `hp.ui.v1`.

## 6. Design tokens (`web/src/styles/tokens.css`, complete)

Verified-in-source. The dark "chartroom" theme is the `:root` default; the light theme overrides values under
`[data-theme='light']`. A dash means the light theme inherits the dark value.

| Token | Dark (`:root`) | Light override |
|---|---|---|
| `color-scheme` | dark | light |
| `--bg` | `#0c141a` | `#f6f2e8` |
| `--sidebar` | `#131d25` | `#ece6d8` |
| `--surface` | `#16212a` | `#fbf8f1` |
| `--surface-raised` | `#1d2b36` | `#f1ebde` |
| `--surface-sunken` | `#0f181f` | `#efe9dc` |
| `--border` | `#2b3b47` | `#d5cbb6` |
| `--border-strong` | `#3a4e5c` | `#bfb39a` |
| `--text` | `#eee6d2` | `#1b2329` |
| `--text-secondary` | `#b8af98` | `#4f4a3e` |
| `--text-muted` | `#8c979d` | `#5f6a70` |
| `--text-inverse` | `#0c141a` | `#fbf8f1` |
| `--accent` | `#d77a59` | `#b55a3a` |
| `--accent-soft` | `rgb(215 122 89 / 0.16)` | `rgb(181 90 58 / 0.12)` |
| `--success` | `#72bc87` | `#2f7d48` |
| `--success-soft` | `rgb(114 188 135 / 0.14)` | — |
| `--info` | `#79aed0` | `#2f6d95` |
| `--info-soft` | `rgb(121 174 208 / 0.14)` | — |
| `--warning` | `#d9b65a` | `#8a6a10` |
| `--warning-soft` | `rgb(217 182 90 / 0.14)` | — |
| `--error` | `#d76d6d` | `#b03a3a` |
| `--error-soft` | `rgb(215 109 109 / 0.15)` | — |
| `--purple` | `#b49ad6` | `#6e4fa0` |
| `--purple-soft` | `rgb(180 154 214 / 0.15)` | — |
| `--error-text` | `#e88a8a` | `#b03a3a` |
| `--accent-text` | `#e08e70` | `#b55a3a` |
| `--focus` | `#f0c27a` | `#b55a3a` |
| `--font-sans` | `ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif` | — |
| `--font-mono` | `ui-monospace, 'SF Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace` | — |
| `--radius-sm` / `--radius` / `--radius-lg` / `--radius-pill` | `6px` / `10px` / `14px` / `999px` | — |
| `--space-1`..`--space-7` | `4px`, `8px`, `12px`, `16px`, `24px`, `32px`, `48px` | — |
| `--text-xs`, `--text-sm`, `--text-base`, `--text-md` | `0.75rem`, `0.8125rem`, `0.875rem`, `1rem` | — |
| `--text-lg`, `--text-xl`, `--text-2xl`, `--text-metric` | `1.25rem`, `1.75rem`, `2.25rem`, `2.75rem` | — |
| `--sidebar-width` / `--content-max` | `232px` / `1480px` | — |
| `--shadow` | `0 1px 0 rgb(255 255 255 / 0.02), 0 8px 24px rgb(0 0 0 / 0.25)` | `0 1px 2px rgb(0 0 0 / 0.06)` |
| `--transition` | `140ms ease` | — (`0ms` under `prefers-reduced-motion: reduce`) |

The reduced-motion block also forces animation and transition durations to `0.001ms` and sets
`scroll-behavior: auto`. Harbor Pilot keeps every token name and value. Only the header comment and the
brand change.

## 7. Conventions inherited

Verified-in-source.
- **Make targets:** `web-deps web build build-go run demo vet test-go race typecheck lint test-web test check gen-api e2e-install e2e docker docker-up clean`.
- **Lint:** `lint` = eslint plus a gofmt check over cmd/internal/migrations. `check` = `vet lint race typecheck test-web` followed by the web build.
- **Docker:** node 24 alpine → golang 1.27 alpine → `gcr.io/distroless/static-debian12:nonroot`, built with `CGO_ENABLED=0`.
- **Go tests:** standard library only (no testify or gomock); table tests with `t.Run`; `httptest` provider fakes; `clock.Fake`; fixtures in `testdata/<provider>/*.json`.
- **Playwright:** runs the real binary with `serve --demo --demo-clock=2026-10-17T12:00:00Z`, timezone UTC, locale en-GB, one worker. Projects: desktop 1440×1000 and Pixel 7.
- **E2E specs:** a11y (axe), crawl, responsive, and a security spec that regex-scans every GET for secrets.
- **API contract test:** `TestOpenAPICoversAllRoutes` walks the chi router against `openapi.yaml`.

## 8. Known gaps, not carried forward

- The OpenAPI spec is hand-written, and the generated `schema.gen.ts` is **never imported**. Harbor Pilot
  generates the spec from Go types and the UI imports the generated types (D-011).
- No pagination (`{items:[...]}` plus ad-hoc `limit`). Harbor Pilot uses cursor pagination.
- The YAML file is the only source of truth, so the API cannot change configuration.
- Sessions are stateless HMAC cookies, so they cannot be revoked server-side.
- Budgets only monitor; they are never enforced.
- Telegram is the only notification channel. The outbox is notification-specific, with no domain event bus.
- No release workflow, goreleaser or image publishing. No golangci-lint.
- Doc drift: `agent-handoff.md` claims 49 routes, but the code has 44 operations plus the SPA catch-all.
