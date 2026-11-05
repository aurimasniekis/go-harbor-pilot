# Compatibility Matrix

Statuses are `implemented`, `contract-tested`, `live-verified (date, version, script)`, `unsupported` and `blocked`. `planned` means the item is not implemented yet. A cell may only be upgraded when there is evidence for the new status. The evidence column links to the relevant test or script.

## 1. Inbound protocols (client → Harbor Pilot)

| Inbound protocol | Endpoint(s) | Non-stream | Stream | Tools | Parallel tools | Tool-arg streaming | Structured output | Reasoning config | Images | Status | Evidence |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Anthropic Messages | `POST /v1/messages`, `/anthropic/v1/messages`, `/v1/messages/count_tokens` (estimate) | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | reject→other upstreams | contract-tested | implemented | contract-tested | protocol_test.go, acceptance A/B |
| OpenAI Chat Completions | `POST /v1/chat/completions`, `/openai/v1/chat/completions` | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | implemented | implemented | implemented | contract-tested | protocol_test.go, acceptance B |
| OpenAI Responses | `POST /v1/responses`, `/openai/v1/responses` | contract-tested | contract-tested | contract-tested | implemented | contract-tested | implemented | implemented | implemented | contract-tested | protocol_test.go, acceptance B |
| Gemini generateContent | `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent` | implemented | contract-tested | contract-tested | implemented | n/a (calls arrive whole) | implemented | implemented | implemented | contract-tested | protocol_test.go, acceptance B |
| Model listing | `GET /v1/models`, `/v1beta/models` | contract-tested | | | | | | | | contract-tested | acceptance A |

## 2. Translation pairs (inbound → upstream)

Each translated pair is checked against these semantics:

- System and developer prompts
- Text
- Tool definitions, tool choice, tool calls and tool results
- Incremental streaming of tool arguments
- Finish reasons
- Usage categories
- Errors
- Cancellation

Sealed reasoning state only crosses within the same family. Every other pair downgrades it according to policy.

| Inbound \ Upstream | Anthropic | OpenAI Chat | OpenAI Responses | Gemini | OpenAI-compatible | Ollama | Claude Code bridge | Codex bridge |
|---|---|---|---|---|---|---|---|---|
| Anthropic Messages | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | planned (Phase 7) | planned (Phase 7) |
| OpenAI Chat | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | planned (Phase 7) | planned (Phase 7) |
| OpenAI Responses | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | planned (Phase 7) | planned (Phase 7) |
| Gemini | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | planned (Phase 7) | planned (Phase 7) |

## 3. Harnesses

| Harness | Version tested | Speaks | Configuration | Setup tooling | Status | Evidence |
|---|---|---|---|---|---|---|
| Claude Code | 2.1.318 (installed locally) | Anthropic Messages | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, model env vars | planned (Phase 12) | contract-tested (request shape); live: pending | acceptance A |
| Codex | not installed | OpenAI Responses | `config.toml` `[model_providers.<id>]` | planned | blocked (binary) | |
| OpenCode | not installed | OpenAI-compatible / Anthropic | `opencode.json` provider options | planned | blocked (binary) | |
| Pi | not installed | OpenAI-compatible / Anthropic | `models.json` | planned | blocked (binary) | |
| Generic API clients | n/a | any inbound protocol | base URL plus bearer token | n/a | planned | |

## 4. Executors

| Executor | Kind | Auth | Quota source | Classification | Status | Evidence |
|---|---|---|---|---|---|---|
| Anthropic API | HTTP | API key | response headers, Admin API | official | contract-tested (synthetic upstream); live: blocked (credentials) | acceptance A/B/E |
| OpenAI API (Chat, Responses) | HTTP | API key | response headers, Admin API | official | contract-tested; live: blocked | acceptance B |
| Gemini API | HTTP | API key | Cloud Quotas (monitor) | official | implemented (adapter contract-tested); live: blocked | protocol_test.go |
| OpenAI-compatible (OpenRouter, DeepSeek, xAI, Together, Moonshot, …) | HTTP | API key | balance endpoints | partial (per server) | contract-tested; live: blocked | acceptance B |
| Ollama / vLLM / llama.cpp / LM Studio | local HTTP (OpenAI-compatible endpoint) | none or key | n/a | local | contract-tested (synthetic); live: not run | demo traffic |
| Claude Code bridge | CLI | `CLAUDE_CODE_OAUTH_TOKEN` (setup-token) per account | `rate_limit_event` (5-hour / 7-day windows) | official binary, subscription | contract-tested against the real `claude` 2.1.318 binary with a synthetic upstream (2026-11-05); live with a real setup-token: blocked (credentials) | bridge/claudecode tests, TestClaudeCodeBridgeThroughGateway, TestClaudeCodeBridgeExhaustionFailsOver |
| Codex bridge | CLI | per-account `CODEX_HOME` sign-in (`auth.json` seeded from the account credential) | `codex_subscription` quota monitor | official binary, subscription | contract-tested against a fake binary emitting the documented `codex exec --json` events; live: blocked (binary not installed) | bridge/codex tests |
| Direct subscription token replay / client impersonation | — | — | — | unsupported (D-001) | unsupported | DECISIONS D-001 |

## 5. Quota monitoring adapters

The Quota Harbor adapters are inherited (`internal/quota/providers`). Per-adapter tiers are listed in `docs/contracts/reference-quota-harbor.md` and served by `GET /api/v1/quota/adapters`. None has been live-verified.

| Adapter id | Tier | Status | Evidence |
|---|---|---|---|
| anthropic_admin | official | contract-tested (fixtures) | providers/anthropic tests |
| claude_subscription | unofficial (opt-in) | contract-tested (fixtures); in-app login guards tested | providers/anthropic tests, TestQuotaLoginFlowGuards |
| openai_admin | official | contract-tested (fixtures) | providers/openai tests |
| codex_subscription | unofficial (opt-in) | contract-tested (fixtures) | providers/openai tests |
| gemini_gcp | official | contract-tested (fixtures) | providers/gemini tests |
| xai | official | contract-tested (fixtures) | providers/xai tests |
| together, deepseek, moonshot, openrouter | official | contract-tested (fixtures) | providers/together, providers/balance tests |
| cursor, github_copilot, nexos, zai_coding_plan, minimax_coding_plan | per adapter | contract-tested (fixtures) | provider package tests |
| generic_http | official (operator mapping) | contract-tested (local fake) | TestQuotaMonitorDrivesRouting |
| claude_code_statusline | client_reported (push only) | registered; ingestion endpoint not yet exposed | providers/clientquota |
| demo_* | demo | synthetic only | demo mode |

Response-header rate limits (`anthropic-ratelimit-*`, `x-ratelimit-*`) are parsed from gateway responses: contract-tested (TestParseHeaders, TestResponseHeadersFeedQuotaBoard).
