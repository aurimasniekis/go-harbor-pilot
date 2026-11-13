# Compatibility matrix

Statuses: `implemented` (code exists, no dedicated test), `contract-tested` (tests against fixtures, fakes or synthetic upstreams), `live-verified (date, version, script)` (a recorded run against the real service), `unsupported`, `blocked` (needs an external prerequisite; see LIMITATIONS.md). Upgrade a cell only with evidence for the new status, and name the evidence. "Scenario A/B/E" are `TestScenarioA_StandaloneHarness`, `TestScenarioB_CrossProtocolTools` and `TestScenarioE_ConcurrentReservations` in `internal/acceptance`. The rendered page is `site/reference/compatibility.md`; keep both in step.

## 1. Inbound protocols (client → Harbor Pilot)

| Inbound protocol | Endpoint(s) | Non-stream | Stream | Tools | Parallel tools | Tool-arg streaming | Structured output | Reasoning config | Images | Status | Evidence |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Anthropic Messages | `POST /v1/messages`, `/anthropic/v1/messages`, `/v1/messages/count_tokens` (estimate) | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | reject→other upstreams | contract-tested | implemented | contract-tested | protocol_test.go, scenarios A and B |
| OpenAI Chat Completions | `POST /v1/chat/completions`, `/openai/v1/chat/completions` | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | implemented | implemented | implemented | contract-tested | protocol_test.go, scenario B |
| OpenAI Responses | `POST /v1/responses`, `/openai/v1/responses` | contract-tested | contract-tested | contract-tested | implemented | contract-tested | implemented | implemented | implemented | contract-tested | protocol_test.go, scenario B |
| Gemini generateContent | `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent` | implemented | contract-tested | contract-tested | implemented | n/a (calls arrive whole) | implemented | implemented | implemented | contract-tested | protocol_test.go, scenario B |
| Model listing | `GET /v1/models`, `/v1beta/models` | contract-tested | | | | | | | | contract-tested | scenario A |

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

Sealed reasoning state only crosses within the same family. Every other pair downgrades it according to policy. The CLI bridges take the IR, so any inbound protocol can reach them; their fidelity limits are in LIMITATIONS.md.

| Inbound \ Upstream | Anthropic | OpenAI Chat | OpenAI Responses | Gemini | OpenAI-compatible | Ollama | Claude Code bridge | Codex bridge |
|---|---|---|---|---|---|---|---|---|
| Anthropic Messages | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | contract-tested (TestClaudeCodeBridgeThroughGateway) | implemented (executor contract-tested with a fake binary) |
| OpenAI Chat | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | implemented (through the IR) | implemented (executor contract-tested with a fake binary) |
| OpenAI Responses | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | implemented (through the IR) | implemented (executor contract-tested with a fake binary) |
| Gemini | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | via OpenAI-compatible endpoint (contract-tested) | implemented (through the IR) | implemented (executor contract-tested with a fake binary) |

### Reasoning settings (D-047)

Without a model `reasoning` option each upstream protocol keeps its built-in translation of an explicit level: Chat
`reasoning_effort`, Responses `reasoning.effort`, an Anthropic `thinking` budget, Gemini `thinkingBudget`. Anthropic
adaptive thinking and a top-level `output_config` reach only a native Anthropic upstream, and elsewhere they are
dropped with a loss that names the option. The option (`contracts/config.md`) translates them with a style:

| Style | Provider types | Upstream fields | Status | Evidence |
|---|---|---|---|---|
| `anthropic_thinking` | anthropic | adaptive `thinking` unchanged; a level becomes an enabled budget | contract-tested | TestScenarioP3_NativeAnthropicByteIdentical, TestReasoningPolicyBuild |
| `openai_reasoning_effort` | openai, openai_compatible, ollama | `reasoning_effort`, clamped (low/medium/high by default) | contract-tested | TestReasoningPolicyBuild, TestResolve |
| `openai_responses_reasoning` | openai | `reasoning.effort` and `summary` | contract-tested | TestLegacyMethodsWritePlans |
| `openrouter_reasoning` | openai_compatible | `reasoning: {effort}`, `{max_tokens}` or `{enabled: true}`; off is `{effort: "none"}` | contract-tested | TestScenarioP1_ClaudeCodeAdaptiveReachesRouterReasoning, TestScenarioP4_FixedAndOff |
| `deepseek_thinking` | openai_compatible | `thinking: {type}` plus `reasoning_effort` (low/high/max) | contract-tested | TestScenarioP5_DeepSeekToggle |
| `gemini_thinking_config` | gemini | `thinkingConfig.thinkingBudget` (adaptive: `-1`) | contract-tested | TestLegacyMethodsWritePlans, TestReasoningPolicyBuild |
| `none` | all HTTP types | nothing | contract-tested | TestNewMethodsWritePlans |

The wire shapes follow each upstream's public API documentation; none is live-verified. Earlier reasoning can be sent
back in a Chat history field (TestScenarioP6_HistoryField). An `output_config` that also carries `format` is still
dropped, and `context_management` is always dropped outside Anthropic (its loss says why). Without the option every
upstream body is byte for byte what 0.1.6 sent (TestReasoningGoldens; only loss details changed).

### Mid-conversation system messages

The Messages API accepts `{"role": "system", ...}` entries inside `messages[]` on the models its documentation lists for mid-conversation system messages. Other models answer 400. The entry is text only, may not be `messages[0]`, must follow a user turn and must be last or followed by an assistant turn. It can carry `clear_at` (turn-scoped, beta) or, with empty `content`, `output_config` (per-message effort, beta). Harbor Pilot leaves the placement rules to the upstream. For model support it keeps a list of models the public documentation names as unsupported (D-042, `internal/protocol/anthropic/models.go`): for those the entry is sent as user text. Every other model, known or not, gets it unchanged. Fixtures: `testdata/protocol/anthropic/messages_system_*.json` (synthetic, written from the public documentation).

| Inbound → upstream | Behaviour | Status | Evidence |
|---|---|---|---|
| Anthropic Messages → Anthropic | Passed through unchanged, in place, with `clear_at` and `output_config`, unless the target model is listed as unsupported. Unknown models are passed through | contract-tested (synthetic upstream and demo); live: blocked (credentials) | TestAnthropicMidConversationSystemPassthrough, TestAnthropicMidConversationSystemPerModel, TestMessagesSystemRoleEntries, TestDemoAcceptsMessagesSystemRole |
| Anthropic Messages → Anthropic, model listed as unsupported (D-042) | User text in place (`mid_conversation_system`, approximate). Message-level fields are dropped (`system_message_field:*`, drop) | contract-tested (synthetic upstream) | TestAnthropicMidConversationSystemPerModel, TestSupportsMidConversationSystem, TestMessagesSystemRoleEntries |
| Anthropic Messages → OpenAI Chat, OpenAI-compatible | A `system` message in place; text blocks joined with a blank line. A settings-only entry (empty `content`) is omitted. `clear_at` and `output_config` are dropped (`system_message_field:*`, drop) | contract-tested | TestAnthropicSystemMessagesTranslated |
| Anthropic Messages → OpenAI Responses | A `system` input message in place. Message-level fields are dropped as above | contract-tested | TestAnthropicSystemMessagesTranslated, TestMessagesSystemRoleEntries |
| Anthropic Messages → Gemini | User text in place, because Gemini has a single system instruction ahead of the conversation (`mid_conversation_system`, approximate). Message-level fields are dropped as above | contract-tested | TestAnthropicSystemMessagesTranslated |
| Anthropic Messages → Claude Code bridge | User text in place (`mid_conversation_system`, approximate). Message-level fields are dropped as above | contract-tested (request builder) | TestAnthropicSystemMessagesInlinedOnRequest |
| Anthropic Messages → Codex bridge | Not carried: the bridge skips system messages after the first turn | implemented | LIMITATIONS.md |
| OpenAI Chat or Responses → Anthropic | A later `system` or `developer` message is sent as user text (`mid_conversation_system`, approximate), because the target model may not accept the role | contract-tested | TestChatSystemMessageToAnthropicIsUserText |

### Prompt-cache breakpoints

The Messages API accepts at most four `cache_control` breakpoints (a top-level automatic one included), and their TTLs must not increase in processing order: tools, then system, then messages. A `1h` breakpoint after a `5m` one (a missing `ttl` is `5m`) is a 400. Gateway reshaping (merging consecutive same-role messages, inlining system messages, dropping blocks) can break a request the client sent correctly, so the request builder checks the body it sends (`internal/protocol/anthropic/cache.go`). The synthetic Anthropic upstream enforces both rules, so a regression shows as a 400 in acceptance tests.

| Upstream | Behaviour | Status | Evidence |
|---|---|---|---|
| Anthropic | Well-formed markers pass unchanged, including on blocks inside tool results. Over four: the earliest are removed and the last four kept, since the last breakpoint covers the longest prefix and is where a growing conversation writes its next entry (`cache_control_limit`, drop). A `1h` after a `5m` is lowered to `5m` (`cache_control_ttl`, approximate) | contract-tested (synthetic upstream) | TestBuildRequestNormalizesCacheControl, TestBuildRequestCacheControlOnProviderTool, TestAnthropicCacheControlReachesUpstreamValid |
| Claude Code bridge | The client's breakpoints are replaced by the CLI's own: every client `cache_control` is removed from the transcript and the stdin turn (message blocks and tool-result content), and the CLI places its own on its system prompt and the last message (`cache_control`, approximate). Their TTL follows the client through `CLAUDE_CODE_PROMPT_CACHE_TTL`: `1h` if any client breakpoint asks for it, `5m` if all ask for `5m`, `1h` when there are none (harness-wire-facts §1.4). The TTL is part of the parked-process match. A resumed or new process must rebuild an identical prefix to hit the cache. The CLI's cache reads and writes are recorded on the request and the ledger | contract-tested (real `claude` 2.1.318 and 2.1.321 with a synthetic upstream, 2026-11-09); live: blocked (credentials) | TestShapeRequestStripsCacheControl, TestBridgeDropsClientCacheControl, TestClientCacheTTL, TestBridgeCacheTTLFollowsClient, TestClaudeCodeBridgeRecordsCacheTokens |
| Codex bridge | Not applicable: the conversation is rendered as text | implemented | LIMITATIONS.md |

### Claude Code bridge: what the CLI carries

Each request records the features the bridge cannot carry as losses. Settings the CLI fixes per process (cache TTL, effort, output limit) are part of the parked-process and shelf match, so a process only continues a conversation with the same settings. In particular `max_tokens` is part of the key: a client that changes it on every turn gets no process or shelf reuse (and so fewer cache reads). Claude Code sends a constant value. Facts about the CLI are in `contracts/harness-wire-facts.md` §1.4, §1.5 and §1.7.

| Feature | Status | Reason | Evidence |
|---|---|---|---|
| `max_tokens` | passed (`CLAUDE_CODE_MAX_OUTPUT_TOKENS`, exact match per process); above the model's declared limit: approximate (`max_output_tokens`) | verified against the CLI: it sends the value as `max_tokens` (and clamps above 128000 itself). A smaller request does not reuse a process with a larger limit, since the reply could exceed what the client asked for | TestShapeRequestCarriesOutputSettings, TestBridgeOutputSettingsReachUpstream |
| Effort | passed (`--effort`): the top-level `output_config.effort` (what Claude Code sends with adaptive thinking), else the level a `thinking.budget_tokens` maps to. Until 0.1.6 only the budget's level was read and a top-level `output_config.effort` was not passed. `thinking` itself is not passed: the CLI chooses adaptive or budgeted thinking for the model | documented flag | TestShapeRequestCarriesOutputSettings, TestBridgeTopLevelOutputEffortReachesUpstream, TestBridgeOutputSettingsReachUpstream |
| `output_config` on a mid-conversation system entry | passed when it holds only `effort` (the latest entry sets `--effort` for later turns); anything else dropped (`system_message_field:…:output_config`) | the public docs define message-level `output_config` as a per-message effort change; the CLI has no per-turn setting, so it applies to the process | TestShapeRequestCarriesOutputSettings |
| Mid-conversation system entry with text | approximate (`mid_conversation_system`): sent as user text | the CLI's session format is not documented for caller-provided system entries (the CLI's own environment notes use internal attachment entries), so it is not written natively. An entry without text (settings only) records no loss | TestShapeRequestCarriesOutputSettings, TestAnthropicSystemMessagesInlinedOnRequest |
| `cache_control` | approximate: replaced by the CLI's own breakpoints with the client's TTL | see above | TestBridgeCacheTTLFollowsClient |
| System prompt | approximate (`system_prompt`) | inherent: the CLI prefixes its identity line and adds an environment note | LIMITATIONS.md |
| Sampling (`temperature`, `top_p`, `top_k`), `stop_sequences` | approximate (`sampling`) | no CLI flag | LIMITATIONS.md |
| Forced `tool_choice` | approximate (`tool_choice`) | no CLI flag | LIMITATIONS.md |
| Provider-executed tools | dropped (`builtin_tool:*`) | the CLI's tools are off; client tools go through MCP | bridge/claudecode tests |
| Tool results for a call no live process holds | approximate (`tool_continuation`) | resumed from the shelved session with the results appended, or from a rebuilt transcript (CLI versions whose session format was not verified), then a "Continue." turn | TestBridgeResumesShelvedToolCall, TestBridgeShelvedToolCallUnverifiedCLI, TestImagePinsVerifiedCLI |

## 3. Harnesses

| Harness | Version tested | Speaks | Configuration | Setup tooling | Status | Evidence |
|---|---|---|---|---|---|---|
| Claude Code | 2.1.318 | Anthropic Messages | `settings.json`: `env.ANTHROPIC_BASE_URL`, `env.ANTHROPIC_MODEL`, `apiKeyHelper` (or `ANTHROPIC_AUTH_TOKEN`); `CLAUDE_CODE_OAUTH_TOKEN` instead to see usage headers (D-048) | contract-tested | contract-tested (request shape, including mid-conversation system messages); usage headers: the client's reading of them observed with 2.1.323 against a synthetic server (harness-wire-facts §1.6), the gateway's headers contract-tested; live: pending | TestScenarioA_StandaloneHarness, TestSetupAndUninstallPreserveUserConfig, TestMessagesSystemRoleEntries, TestUsageHeadersReportSeatWindows, TestUsageHeadersReportBudget, TestOAuthBetaNotForwarded |
| Codex | not installed | OpenAI Responses | `config.toml` `[model_providers.<id>]` | contract-tested | contract-tested (setup); live: blocked (binary) | TestSetupAndUninstallPreserveUserConfig |
| OpenCode | not installed | OpenAI-compatible / Anthropic | `opencode.json` provider options | contract-tested | contract-tested (setup); live: blocked (binary) | TestSetupAndUninstallPreserveUserConfig |
| Pi | not installed | OpenAI-compatible / Anthropic | `models.json` | contract-tested | contract-tested (setup); live: blocked (binary) | TestSetupAndUninstallPreserveUserConfig |
| Generic API clients | n/a | any inbound protocol | base URL plus bearer token | n/a | contract-tested | TestScenarioA_StandaloneHarness |

## 4. Executors

| Executor | Kind | Auth | Quota source | Classification | Status | Evidence |
|---|---|---|---|---|---|---|
| Anthropic API | HTTP | API key | response headers, Admin API | official | contract-tested (synthetic upstream); live: blocked (credentials) | scenarios A, B and E |
| OpenAI API (Chat, Responses) | HTTP | API key | response headers, Admin API | official | contract-tested; live: blocked | scenario B |
| Gemini API | HTTP | API key | Cloud Quotas (monitor) | official | implemented (adapter contract-tested); live: blocked | protocol_test.go |
| OpenAI-compatible (OpenRouter, DeepSeek, xAI, Together, Moonshot, …) | HTTP | API key | balance endpoints | partial (per server) | contract-tested; live: blocked | scenario B |
| Ollama / vLLM / llama.cpp / LM Studio | local HTTP (OpenAI-compatible endpoint) | none or key | n/a | local | contract-tested (synthetic); live: not run | demo traffic |
| Claude Code bridge | CLI | `CLAUDE_CODE_OAUTH_TOKEN` (setup-token) per account | `rate_limit_event` (5-hour / 7-day windows) | official binary, subscription | contract-tested against the real `claude` 2.1.318 binary with a synthetic upstream (2026-11-05); live with a real setup-token: blocked (credentials) | bridge/claudecode tests, TestClaudeCodeBridgeThroughGateway, TestClaudeCodeBridgeExhaustionFailsOver |
| Codex bridge | CLI | per-account `CODEX_HOME` sign-in (`auth.json` seeded from the account credential) | `codex_subscription` quota monitor | official binary, subscription | contract-tested against a fake binary emitting the documented `codex exec --json` events; built-in tool flags and the per-run profile checked against the real codex 0.166.0 with `codex debug prompt-input` (2026-11-05, no model call); live: blocked (no ChatGPT login) | bridge/codex tests |
| Direct subscription token replay / client impersonation | — | — | — | unsupported (D-001) | unsupported | DECISIONS D-001 |

Every HTTP executor adds a model's configured [upstream request fields](contracts/config.md) to the body, on
translated, native and passthrough paths: allowlisted fields per provider type, and a typed provider pin on
`openai_compatible` providers only (D-046). CLI bridge executors refuse them, because the vendor CLI builds its own
request. Status: contract-tested against synthetic upstreams (scenario O, including a Messages API client reaching an
`openai_compatible` upstream with the pin); live: blocked with the provider rows above.

## 5. Quota monitoring adapters

Adapters live in `internal/quota/providers`; their tiers are served by `GET /api/v1/quota/adapters` (reference: `docs/contracts/reference-quota-harbor.md`). None has been live-verified.

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

## 6. Reverse proxies

Example configurations live in `deploy/proxies`; the operator page is `site/guide/reverse-proxy.md`. `scripts/proxy-check.sh` runs each real proxy in Docker with the shipped file (hostname, ports and certificate paths swapped) and checks it in front of the stand-in backend `scripts/proxycheck` (gateway streaming headers and keepalives; event-by-event delivery, a 75s keepalive-only stream, an unstreamed reply after 2 minutes, a 20 MiB body, `X-Forwarded-For`) and in front of the real demo binary (web UI, sign-in and sign-out with the `Secure` cookie and Origin check). Controls with broken configurations fail as expected (nginx 413 without `client_max_body_size`, 504 without `proxy_read_timeout`; Apache 502 after 60s without `ProxyTimeout`).

| Proxy | Status | Evidence |
|---|---|---|
| nginx | live-verified (2026-11-06, nginx 1.29.8, scripts/proxy-check.sh) | real proxy; streaming checks against the stand-in backend, sign-in against the demo |
| Apache httpd | live-verified (2026-11-06, httpd 2.4.69, scripts/proxy-check.sh) | as above |
| Caddy | live-verified (2026-11-06, Caddy 2.11.9, scripts/proxy-check.sh) | as above |
| Traefik | live-verified (2026-11-06, Traefik 3.7.17, scripts/proxy-check.sh) | as above |
| HAProxy | live-verified (2026-11-06, HAProxy 3.2.26, scripts/proxy-check.sh) | as above |
| Cloudflare Tunnel | implemented (documentation only) | limits quoted from Cloudflare's documentation; not run |
