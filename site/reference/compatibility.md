# Compatibility matrix

What works with what, and how sure we are, as of 0.1.0. Everything here is tested against synthetic fixtures, fakes
and embedded servers, and **nothing has yet been verified against live provider accounts**. A cell
only moves up when there is evidence for it.

| Status | Means |
|---|---|
| **contract-tested** | Covered by tests against fixtures written from the public API documentation, fake binaries or synthetic upstreams. |
| **implemented** | The code is there and exercised indirectly, but has no dedicated test. |
| **live-verified** | A recorded run against the real service, with date, version and script. So far only the reverse proxies below. |
| **blocked** | Needs something external before it can be verified: credentials, a login or a binary. |
| **unsupported** | Deliberately not provided. |

When translation to a provider starts failing in practice, **Settings → Diagnostics** shows the degraded paths,
circuit breakers and protocol changes Harbor Pilot observed.

<Shot name="diagnostics" alt="Settings, Diagnostics tab, protocol issues view with no degraded routes and no circuit breakers yet" caption="Diagnostics: degraded routes and circuit breakers" />

The same matrix, with the test that backs each cell, is in
[docs/COMPATIBILITY.md](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/docs/COMPATIBILITY.md).

## Protocols your tools can speak

| Protocol | Not streaming | Streaming | Tools | Parallel tools | Streamed tool arguments | Structured output | Reasoning settings | Images |
|---|---|---|---|---|---|---|---|---|
| Anthropic Messages | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | not translatable to other providers | contract-tested | implemented |
| OpenAI Chat Completions | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | implemented | implemented | implemented |
| OpenAI Responses | contract-tested | contract-tested | contract-tested | implemented | contract-tested | implemented | implemented | implemented |
| Gemini `generateContent` | implemented | contract-tested | contract-tested | implemented | n/a (calls arrive whole) | implemented | implemented | implemented |
| Model listing | contract-tested | | | | | | | |

Endpoints are listed in [Gateway endpoints](/reference/gateway).

## Translation between protocols

Each pair is checked for system and developer prompts, text, tool definitions, tool choice, tool calls and results,
streamed tool arguments, finish reasons, usage categories, errors and cancellation. Signed or encrypted reasoning
only crosses within its own protocol family; for every other pair it is downgraded according to policy.

| Your tool speaks ↓ / provider speaks → | Anthropic | OpenAI Chat | OpenAI Responses | Gemini | OpenAI-compatible | Local (Ollama and others) |
|---|---|---|---|---|---|---|
| Anthropic Messages | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested (OpenAI-compatible endpoint) |
| OpenAI Chat | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested (OpenAI-compatible endpoint) |
| OpenAI Responses | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested (OpenAI-compatible endpoint) |
| Gemini | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested | contract-tested (OpenAI-compatible endpoint) |

## CLI bridges

The bridges serve subscription accounts through the vendors' own CLIs. They take Harbor Pilot's internal request
form, so a tool speaking any protocol can reach them.

| | Through the gateway | The bridge itself | Live |
|---|---|---|---|
| **Claude Code bridge** | contract-tested from Anthropic Messages; implemented from the other protocols | contract-tested against the real `claude` 2.1.318 binary with a synthetic upstream, including failover when a subscription window is used up | blocked: needs a real subscription sign-in |
| **Codex bridge** | implemented | contract-tested against a fake binary that emits the documented `codex exec --json` events; its tool restrictions and per-run profile were checked against the real `codex` 0.166.0 without a model call | blocked: needs a ChatGPT sign-in |

What a bridge can and cannot carry is in [Known limitations](/security/limitations#cli-bridges).

## Coding tools

| Tool | Speaks | What setup writes | `harbor-pilot harness` | Live |
|---|---|---|---|---|
| [Claude Code](/tools/claude-code) | Anthropic Messages | `settings.json`: base URL, model, and an `apiKeyHelper` that reads the token file | contract-tested; request shape contract-tested against 2.1.318 | pending |
| [Codex](/tools/codex) | OpenAI Responses | `config.toml`: a `[model_providers.<id>]` entry | contract-tested | blocked: binary not installed |
| [OpenCode](/tools/opencode) | OpenAI-compatible or Anthropic | `opencode.json` provider options | contract-tested | blocked: binary not installed |
| [Pi](/tools/pi) | OpenAI-compatible or Anthropic | `models.json` | contract-tested | blocked: binary not installed |
| [SDKs and API clients](/tools/sdks) | any of the four | base URL and bearer token | n/a | contract-tested with a plain client |

Recognising which tool sent a request is a guess from its headers, not proof.

## Providers

| Provider | Kind | Sign-in | Quota from | Status |
|---|---|---|---|---|
| Anthropic API | HTTP | API key | response headers, Admin API | contract-tested; live: blocked (credentials) |
| OpenAI API (Chat and Responses) | HTTP | API key | response headers, Admin API | contract-tested; live: blocked |
| Gemini API | HTTP | API key | Cloud Quotas | implemented, quota adapter contract-tested; live: blocked |
| OpenAI-compatible (OpenRouter, DeepSeek, xAI, Together, Moonshot and others) | HTTP | API key | balance endpoints | contract-tested; live: blocked |
| Ollama, vLLM, llama.cpp, LM Studio | local HTTP (OpenAI-compatible) | none or key | none | contract-tested (synthetic); live: not run |
| Claude subscription | Claude Code bridge | setup token per account | the CLI's rate-limit events (5-hour and 7-day windows) | see CLI bridges |
| ChatGPT subscription | Codex bridge | per-account Codex sign-in | `codex_subscription` quota tracker | see CLI bridges |
| Replaying a subscription token directly, or impersonating a vendor client | | | | **unsupported**, by design |

## Quota tracking

| Adapter | Tier | Status |
|---|---|---|
| `anthropic_admin`, `openai_admin`, `gemini_gcp`, `xai` | official | contract-tested (fixtures) |
| `together`, `deepseek`, `moonshot`, `openrouter` | official | contract-tested (fixtures) |
| `claude_subscription`, `codex_subscription` | unofficial, opt-in | contract-tested (fixtures); in-app sign-in guards tested |
| `cursor`, `github_copilot`, `nexos`, `zai_coding_plan`, `minimax_coding_plan` | per adapter | contract-tested (fixtures) |
| `generic_http` | official (your own mapping) | contract-tested (local fake) |
| `claude_code_statusline` | reported by the client | registered; no ingestion endpoint yet |

Rate limits in provider responses (`anthropic-ratelimit-*`, `x-ratelimit-*`) are read as requests pass through:
contract-tested. None of the adapters has been verified against a live account.

## Reverse proxies

Each configuration in [Behind a reverse proxy](/guide/reverse-proxy) was run by `scripts/proxy-check.sh` against the
real proxy: streaming, a quiet 75-second stream, a 2-minute unstreamed reply and a 20 MiB request through a stand-in
backend that answers like the gateway, and sign-in through the proxy to the real demo.

| Proxy | Status |
|---|---|
| nginx | **live-verified** (2026-11-06, nginx 1.29.8) |
| Apache httpd | **live-verified** (2026-11-06, httpd 2.4.69) |
| Caddy | **live-verified** (2026-11-06, Caddy 2.11.9) |
| Traefik | **live-verified** (2026-11-06, Traefik 3.7.17) |
| HAProxy | **live-verified** (2026-11-06, HAProxy 3.2.26) |
| Cloudflare Tunnel | documented from Cloudflare's limits; not run |
