# Compatibility Matrix

Statuses are `implemented`, `contract-tested`, `live-verified (date, version, script)`, `unsupported` and `blocked`. `planned` means the item is not implemented yet. A cell may only be upgraded when there is evidence for the new status. The evidence column links to the relevant test or script.

## 1. Inbound protocols (client → Harbor Pilot)

| Inbound protocol | Endpoint(s) | Non-stream | Stream | Tools | Parallel tools | Tool-arg streaming | Structured output | Reasoning config | Images | Status | Evidence |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Anthropic Messages | `POST /v1/messages`, `/anthropic/v1/messages`, `/v1/messages/count_tokens` | planned | planned | planned | planned | planned | n/a | planned | planned | planned | |
| OpenAI Chat Completions | `POST /v1/chat/completions`, `/openai/v1/chat/completions` | planned | planned | planned | planned | planned | planned | planned | planned | planned | |
| OpenAI Responses | `POST /v1/responses`, `/openai/v1/responses` | planned | planned | planned | planned | planned | planned | planned | planned | planned | |
| Gemini generateContent | `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent` | planned | planned | planned | planned | n/a | planned | planned | planned | planned | |
| Model listing | `GET /v1/models`, `/v1beta/models` | planned | | | | | | | | planned | |

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
| Anthropic Messages | planned | planned | planned | planned | planned | planned | planned | planned |
| OpenAI Chat | planned | planned | planned | planned | planned | planned | planned | planned |
| OpenAI Responses | planned | planned | planned | planned | planned | planned | planned | planned |
| Gemini | planned | planned | planned | planned | planned | planned | planned | planned |

## 3. Harnesses

| Harness | Version tested | Speaks | Configuration | Setup tooling | Status | Evidence |
|---|---|---|---|---|---|---|
| Claude Code | 2.1.318 (installed locally) | Anthropic Messages | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, model env vars | planned | planned | |
| Codex | not installed | OpenAI Responses | `config.toml` `[model_providers.<id>]` | planned | blocked (binary) | |
| OpenCode | not installed | OpenAI-compatible / Anthropic | `opencode.json` provider options | planned | blocked (binary) | |
| Pi | not installed | OpenAI-compatible / Anthropic | `models.json` | planned | blocked (binary) | |
| Generic API clients | n/a | any inbound protocol | base URL plus bearer token | n/a | planned | |

## 4. Executors

| Executor | Kind | Auth | Quota source | Classification | Status | Evidence |
|---|---|---|---|---|---|---|
| Anthropic API | HTTP | API key | response headers, Admin API | official | planned | |
| OpenAI API (Chat, Responses) | HTTP | API key | response headers, Admin API | official | planned | |
| Gemini API | HTTP | API key | Cloud Quotas (monitor) | official | planned | |
| OpenAI-compatible (OpenRouter, DeepSeek, xAI, Together, Moonshot, …) | HTTP | API key | balance endpoints | official | planned | |
| Ollama / vLLM / llama.cpp / LM Studio | local HTTP | none or key | n/a | official (local) | planned | |
| Claude Code bridge | CLI | `CLAUDE_CODE_OAUTH_TOKEN` (setup-token) per account | stream-json rate-limit events | official binary, subscription | planned | |
| Codex bridge | CLI | per-account `CODEX_HOME` login | CLI-reported | official binary, subscription | planned | |
| Direct subscription token replay / client impersonation | — | — | — | unsupported (D-001) | unsupported | DECISIONS D-001 |

## 5. Quota monitoring adapters

The Quota Harbor adapters are inherited. Per-adapter tiers are listed in `docs/contracts/reference-quota-harbor.md`. Each adapter's status starts as `contract-tested (fixtures)`. None has been live-verified.
