# Isolated lab: design observations (yetone/magpie)

- **Source studied:** `yetone/magpie` @ `bd0f338`. Go 1.26.8, MIT ("Copyright (c) 2026 yetone"). It contains a
  separately MIT-licensed subtree, `internal/qoder/LICENSE`, and a `codex_instructions.json` that came from an
  Apache-2.0 upstream.
- **Status: clean room (decision D-002).**
  - Harbor Pilot copies **no code, no fixtures, no captured payloads and no prompts** from this repo.
  - This page records *observed designs and wire facts* as design input only.
  - Harbor Pilot code never names this project.
  - Every Harbor Pilot fixture is written from scratch, from public protocol documentation and synthetic data.
- Each fact below was read in source unless marked. Observing a design does **not** verify it against live
  upstreams. Harbor Pilot re-verifies everything it relies on with its own tests.

## 1. Product shape (for contrast)

The project is a **local, desktop-first** gateway:
- It listens on `127.0.0.1:3425` and rewrites each agent's own config to point at itself.
- On loopback it accepts any key. Caller identity is inferred from a per-agent token or the User-Agent.

Harbor Pilot differs on purpose:
- It is a **self-hosted multi-user server**.
- Identity comes **only** from Harbor Pilot credentials.
- Harness detection is telemetry, never authorization.

## 2. Protocol coverage observed

Routes are registered in `internal/gateway/gateway.go`:
- `/v1/chat/completions`
- `/v1/responses` (plus a WebSocket variant)
- `/v1/messages` and `/v1/messages/count_tokens`
- `/v1beta/models/{call...}` (Gemini)
- Ollama `/api/chat`
- media endpoints, a native passthrough prefix, and admin endpoints

There is one large handler file (about 9.9k lines). Harbor Pilot splits it into
`internal/protocol/<proto>` adapters plus `internal/gateway`.

## 3. Canonical IR concepts (`internal/gateway/ir.go`)

- **Parts:** text, image, file, tool_call, tool_result, thinking, web_search. Thinking parts carry a signature
  and record which API "signed" it. A "sealed" variant carries opaque reasoning byte-for-byte, and it can only
  be returned to the API that produced it.
- **Request:** model, system, messages, tools (name, description, schema, strict), tool_choice
  (`auto|none|required|name:<tool>`), max tokens, sampling, stop, stream, effort/thinking, parallel tool calls,
  cache key, metadata, response format.
- **Stream events:** start, text, think, signature, tool-start, tool-args (partial JSON), stop, usage, error,
  search, image, think-start, sealed, text-signature, alive (`ir.go:384-409`).
- **Usage:** input, output, cache read, cache write (5 min and 1 h), reasoning, credits.
- **Pipeline:** parse (client protocol → IR), build (IR → upstream body), decode (upstream stream → IR events),
  encode (IR events → client stream), render (non-streaming).

Harbor Pilot keeps the shape (decision D-016) and adds:
- per-protocol `Extensions` for unknown safe fields;
- an explicit capability descriptor for each protocol × executor × model;
- a typed usage model with category semantics;
- an explicit **downgrade policy**: sealed state is dropped or rejected per policy, never converted to text.

## 4. Opaque state carriage (observed techniques)

| Technique | Observed |
|---|---|
| Anthropic thinking block returned to a Responses client | stored in `encrypted_content` with the prefix `magpie-claude1:` (`internal/claudeseal/claudeseal.go:23`) and stripped before any OpenAI upstream |
| Gemini thought signatures | carried inside the tool-call ID with the marker `__ts` (`gemini_signature.go:53`) |
| Responses encrypted reasoning to a non-Responses client | a key carried in the call ID `<id>__rk16__<key>` (`reasoning_carry.go:27`) |

Harbor Pilot assessment:
- These techniques work, but they put state into client-visible identifiers. Some keep server-side state.
- Harbor Pilot's default is the **conservative** one: sealed state goes back only to the same protocol family
  and endpoint lineage.
- Cross-family carriage is a separate, explicitly policy-gated capability with its own tests, and is
  `unsupported` until designed. The decision is recorded in `../DECISIONS.md`.

## 5. Streaming keepalive rationale (`gateway.go:8602-8632`)

| Constant | Value | Stated reason (code comment) |
|---|---|---|
| `keepaliveGap` | 1 s | client heard nothing while the upstream was active → send a keepalive |
| `keepaliveEvery` / `keepaliveLongest` | 15 s / 5 min | keep a quiet relayed reply alive, but not forever |
| `translatedQuietLongest` | 30 min | an upstream that sends its reply in one late burst can be silent for over 10 minutes |
| `pingEvery` | 1 min | per the comment, Claude Code (2.1.311) resets its 300 s stream watchdog on a ping only a limited number of times, so 1 s pings use that allowance up within about 30 s. Pings a minute apart, with SSE comments in between, keep both the watchdog and proxies alive |

Harbor Pilot:
- Keepalive cadence is configurable per inbound protocol, with defaults that follow this rationale.
- The rationale is a **doc-claim** about Claude Code internals. It is re-verified by a live Claude Code test
  (`scripts/live/claude-code-long-stream.sh`) before it becomes a compatibility claim.

## 6. Failover rules (`internal/gateway/fallback.go`)

- Failover to another candidate happens only **before the first byte is written to the client**. There is no
  stream splicing. Harbor Pilot adopts this as a hard invariant.
- After a failure the provider is cooled down for `fallbackCooldown = time.Minute` (`fallback.go:43`).
- The retryable set includes 401/402/403/404/408/429/5xx plus some 400/422 bodies matched by wording.
  Context-overflow errors are never retried. A vendor risk-control refusal stops attempts on that vendor's
  other accounts. (Agent report.)
- Harbor Pilot keeps the "never retry context overflow" and "stop on risk refusal" ideas. It does **not**
  treat 401/403 as an ordinary retry: auth failures mark the account's auth health.

## 7. Harness configuration targets

| Harness | Mechanism observed | Source |
|---|---|---|
| Claude Code | the `env` block in `~/.claude/settings.json`: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU,FABLE}_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `CLAUDE_CODE_SUBAGENT_MODEL` | `internal/agent/claude.go:27-71` |
| Codex | `config.toml`: a `[model_providers.<id>]` table with `base_url = <gw>/v1`, `wire_api = "responses"`, `experimental_bearer_token`, optional `http_headers`; top-level `model_provider = "<id>"`; a model catalog file | `internal/agent/codex.go:41-73,638-670` |
| OpenCode | `provider.<id>.options.{baseURL, apiKey}`; other settings: `headers`, `timeout`, ... | `internal/agent/opencode_providers.go:78,206` |
| Pi | `models.json` / `settings.json` | `internal/agent/pi*.go` (agent report) |

Harbor Pilot implements its own reversible editors for these targets (`internal/harness`), contract-tested; live
runs are recorded in COMPATIBILITY.md.

## 8. Session headers observed (`gateway.go:8908-8915`)

`x-opencode-session`, `x-session-affinity`, `x-session-id`, `session_id`, `session-id`,
`x-claude-code-session-id`, `x-conversation-id`, `x-deepseek-harness-session-id`.
Subagent headers `x-claude-code-agent-id` and `x-claude-code-parent-agent-id` come from the agent report.

Harbor Pilot reads these as **untrusted session hints**, recorded with their provenance. Its own hint header is
`X-HP-Session`.

## 9. Subscription mechanisms observed, and Harbor Pilot's position (D-001)

| Mechanism observed | Harbor Pilot |
|---|---|
| **Claude subscription via the vendor CLI**: launches `claude -p --input-format stream-json --output-format stream-json --verbose --strict-mcp-config --mcp-config <own>`, bridges client tools over MCP, and authenticates with per-account `CLAUDE_CONFIG_DIR` or `CLAUDE_CODE_OAUTH_TOKEN` (setup-token). Strips `ANTHROPIC_BASE_URL/API_KEY/AUTH_TOKEN` from the child env. Its default is an interactive mode inside a PTY, and its doc claims `-p` costs more plan allowance (doc-claim, unverified) | **Adopted as a pattern:** native CLI bridge (D-001, D-026) using the vendor binary and a per-account token or config dir. The PTY variant is **not** adopted |
| **Codex via the vendor CLI** | **Adopted:** `codex exec --json` with a per-account `CODEX_HOME` |
| ChatGPT/Codex OAuth token replayed against `chatgpt.com/backend-api/codex` with Codex `originator`/User-Agent headers | **Not adopted.** Direct token replay plus client impersonation |
| Copilot session tokens with spoofed `Editor-Version` / `Copilot-Integration-Id` | **Not adopted.** Impersonation |
| Gemini CLI / Antigravity embedded OAuth client IDs against `cloudcode-pa.googleapis.com` | **Not adopted.** Impersonation; the project's own comments warn that accounts may be suspended |
| Vendors the project itself retired from core for terms reasons (Cursor, Kiro, Qoder, ...) | **Not adopted** |

The user decided on 2026-11-05: subscriptions are executed **only** through native CLI bridges, and everything
else uses API keys or local endpoints. Subscription *quota monitoring* keeps the gated "unofficial,
acknowledge_terms_risk" adapters from Quota Harbor, and those never execute requests. Harbor Pilot never claims
that bridging or passthrough avoids provider restrictions or detection.

## 10. Other ideas noted (design input only)

- Scoped, issuable child keys for orchestrators. This shaped Harbor Pilot's child credentials; see
  `reference-spaceman.md`.
- Account ordering that prefers the account whose largest quota window resets soonest. Recorded as an optional
  ranking expression example, not a built-in.
- Session affinity, so a conversation stays on one account while its prompt cache is warm. Harbor Pilot makes
  affinity a routing-plan input with an explicit TTL.

## 11. Prompt caching in the Claude CLI bridge (2026-11-09, `7c1dc19`)

Read in source; observations only. Harbor Pilot's own behaviour is verified separately (`harness-wire-facts.md` §1.4).

- **Conflict it addresses.** The upstream refuses a `1h` cache breakpoint after a `5m` one, and the CLI places its
  own breakpoints on the system prompt ahead of the conversation, so client breakpoints carried into the CLI's
  conversation can break the order.
- **TTL through the child environment.** It sets `CLAUDE_CODE_PROMPT_CACHE_TTL=<5m|1h>` on the child and removes
  any inherited `CLAUDE_CODE_PROMPT_CACHE_TTL` / `FORCE_PROMPT_CACHING_5M`. The run's TTL is `1h` if any client
  breakpoint asks for `1h`, otherwise the TTL the breakpoints ask for (no `ttl` means `5m`), and `1h` when there
  are none (its comments call that the CLI's subscription default). The TTL is part of the process and
  conversation identity, so a parked process with one TTL is not reused for the other.
- **Default: client breakpoints dropped**, the CLI keeps its own caching; the number dropped is reported in a
  response header.
- **Opt-in mode:** client breakpoints are kept and the child gets `DISABLE_PROMPT_CACHING=1`, so the client's
  replace the CLI's. It validates the TTL order and the four-breakpoint limit itself first. Its rationale: past
  about 64 KB of marked history, keeping the client's breakpoints costs less than the CLI rewriting the cache.
- **Prompt stability:** it sets `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1`, keeping per-commit git status out of the
  system prompt.
- **Harbor Pilot:** adopts the default mode, the TTL rule, the TTL in the process match and the git setting. The
  opt-in mode is not adopted.
- **Session retention (public issue #4863).** The project's issue tracker reports that when a bridge process ends,
  the next turn starts a CLI from a transcript rebuilt from the client's history, and unless that prefix is
  byte-identical to what the earlier CLI sent, the whole history is written to the cache again (the report measured a
  `/compact` writing 84 tokens directly and about 67K through the bridge). The fix described there keeps the CLI's own
  session and resumes it. Harbor Pilot adopts the idea with its own bounds (D-045): kept at most the cache TTL, per
  account and total caps, keyed like the process match.
