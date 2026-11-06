# Harness wire facts

How each supported harness is pointed at a custom gateway: which wire protocol it speaks, how it
authenticates, and what session signals it sends.

**Verification status**
- Claude Code 2.1.318 is installed locally (`~/.local/bin/claude`, which resolves to
  `~/.local/share/claude/versions/2.1.318`). Its facts are **verified-in-source** in the sense of
  "observed from the binary": either from `claude --help` output, or as a string literal found in the binary.
  A literal shows the name exists. It does **not** prove the exact semantics until a live run (`scripts/live/`).
- Codex, OpenCode and Pi are **not installed**. Their facts are **doc-claim**, from vendor docs and the
  isolated-lab study (`reference-magpie-observations.md`), and stay `unverified` in `../COMPATIBILITY.md` until a
  live run.

## 1. Claude Code (2.1.318)

### 1.1 As a gateway client (harness → Harbor Pilot)

| Item | Value | Evidence |
|---|---|---|
| Wire protocol | Anthropic Messages (`POST <base>/v1/messages`, SSE streaming), plus `count_tokens` | inferred from Anthropic SDK use; live test pending |
| Base URL | env `ANTHROPIC_BASE_URL` | string in binary |
| Credential | env `ANTHROPIC_AUTH_TOKEN` (sent as `Authorization: Bearer`) **or** `ANTHROPIC_API_KEY` (sent as `x-api-key`) | `ANTHROPIC_AUTH_TOKEN` string in binary; header mapping is a doc-claim |
| Extra headers | env `ANTHROPIC_CUSTOM_HEADERS` (newline-separated `Name: value`) | string in binary; format is a doc-claim |
| Model selection | `--model`, env `ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU,FABLE}_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `CLAUDE_CODE_SUBAGENT_MODEL` | `--model` in `--help`; env names are strings in the binary |
| Session signal | header `X-Claude-Code-Session-Id` | string literal in binary (2 occurrences) |
| Other headers seen in binary | `x-app`, `anthropic-beta` (many), `x-claude-code-signature`, `x-claude-code-keep-branch` | string literals; semantics unknown, so treated as opaque |
| Settings location | `settings.json` `env` block under `CLAUDE_CONFIG_DIR` (default `~/.claude`) | `CLAUDE_CONFIG_DIR` string in binary; file layout is a doc-claim |

`harbor-pilot harness setup claude` writes `env.ANTHROPIC_BASE_URL=<public_url>`, optionally `env.ANTHROPIC_MODEL`, and an
`apiKeyHelper` that prints the token file (with `--token-env`, the user exports `ANTHROPIC_AUTH_TOKEN` instead), so the
token never lands in `settings.json`. The setup is reversible: it backs up
the file, writes atomically, and refuses on conflict.

Harbor Pilot must tolerate:
- `anthropic-beta` values that change between versions. These are **forwarded only to native-Anthropic
  upstreams**. Unknown betas on translated routes trigger the capability/downgrade policy.
- Keepalive needs (see `reference-magpie-observations.md` §5). These are a doc-claim until a live test.

### 1.2 As a subscription executor (Harbor Pilot → `claude` CLI bridge)

Flags confirmed in `claude --help` (2.1.318):

| Flag | Help text summary |
|---|---|
| `-p, --print` | non-interactive; workspace trust dialog skipped; **settings files that fail validation are silently ignored** |
| `--input-format text\|stream-json` | stream-json = realtime streaming input (needs `--print`) |
| `--output-format text\|json\|stream-json` | stream-json = realtime streaming |
| `--include-partial-messages` | partial message chunks (needs `--print` and `stream-json`) |
| `--replay-user-messages` | re-emit user messages on stdout (stream-json both ways) |
| `--mcp-config <configs...>` | MCP servers from JSON files or strings |
| `--strict-mcp-config` | only MCP servers from `--mcp-config` |
| `--tools <tools...>` | built-in tool set; `""` disables all built-in tools |
| `--allowedTools` / `--disallowedTools` | allow and deny lists |
| `--permission-mode` | `acceptEdits, auto, bypassPermissions, manual, dontAsk, plan` |
| `--permission-prompts host\|none` | with `--print`, `none` auto-denies anything that would prompt |
| `--system-prompt` / `--append-system-prompt` | system prompt control |
| `--model`, `--fallback-model`, `--effort low\|medium\|high\|xhigh\|max` | model controls |
| `--session-id <uuid>`, `--no-session-persistence`, `-r/--resume`, `--fork-session` | session controls |
| `--setting-sources user,project,local`, `--settings <file-or-json>` | settings isolation |
| `--json-schema`, `--max-budget-usd` | structured output; spend cap (print mode) |
| `--bare` | minimal mode. **"Anthropic auth is strictly ANTHROPIC_API_KEY or apiKeyHelper via --settings (OAuth and keychain are never read)."** |
| `--restricted` | removes code-running built-ins and WebFetch unless named, and ignores user/project/local settings |
| `--safe-mode` | disables customisations |
| `setup-token` (subcommand) | "Set up a long-lived authentication token (requires Claude subscription)" |

Bridge consequences (inferred, then verified against the binary while building the bridge):
- **Do not use `--bare`** with subscription accounts: it never reads OAuth credentials, so a setup-token would
  not authenticate. Get equivalent isolation from `--setting-sources ""` (or a minimal `--settings`),
  `--strict-mcp-config`, `--tools ""`, `--no-session-persistence` and a per-request `CLAUDE_CONFIG_DIR`.
- Authenticate with `CLAUDE_CODE_OAUTH_TOKEN` (the setup-token value) in the **child env only**. Never write it
  to a shared credentials file, and never rotate accounts by rewriting a global file.
- Strip `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` from the child env. Otherwise the
  CLI could loop back into Harbor Pilot or use the wrong credential.
- Client tools go through a per-request MCP server (loopback or unix socket, authenticated with a nonce), and
  the built-in tools are disabled. The MCP tool names the CLI sees are namespaced, and the bridge maps them back
  to the client's tool names.
- `-p` silently ignores invalid settings files. The bridge therefore passes settings explicitly and verifies
  the effective behaviour in a startup self-test.

### 1.3 Observed with a synthetic upstream (2026-11-05, 2.1.318)

These were observed by running the real binary with `ANTHROPIC_BASE_URL` pointed at a local synthetic upstream, an isolated `HOME`/`CLAUDE_CONFIG_DIR`, and fake credentials. No real account or network was used.

| Fact | Observation |
|---|---|
| Auth header | `CLAUDE_CODE_OAUTH_TOKEN` is sent as `Authorization: Bearer …` with `anthropic-beta` including `oauth-2025-04-20`; `ANTHROPIC_API_KEY` is sent as `x-api-key`. |
| Request path | `POST /v1/messages?beta=true`, `User-Agent: claude-cli/2.1.318 (external, sdk-cli)`, `X-Claude-Code-Session-Id`, `x-app: cli`. |
| System prompt | `--system-prompt` text is sent after a billing marker block and an identity line; the CLI also appends an environment note as a `system`-role message. |
| stream-json output | `system/init`, `system/status`, `stream_event` (Anthropic SSE events with `--include-partial-messages`), full `assistant`/`user` messages, `rate_limit_event`, and a final `result`. |
| Multi-line stdin | each `user` line triggers a model call; injected `assistant` lines are reordered. Not usable for history. |
| `--resume` with a synthesized transcript | entries `{type, uuid, parentUuid, sessionId, cwd, message}` under `CLAUDE_CONFIG_DIR/projects/<cwd with non-alphanumerics as '-'>/<id>.jsonl` replay exactly, including `tool_use`/`tool_result`. A transcript ending in an unanswered `tool_use` is repaired with an "interrupted" result; one ending in a user turn gets an assistant "No response requested." inserted. |
| MCP over HTTP | handshake: `server/discover` probe (method-not-found is fine), `initialize`, `notifications/initialized`, `GET` (405 is fine), `tools/list`. Tools appear to the model as `mcp__<server>__<name>`. `tools/call` carries `_meta["claudecode/toolUseId"]` and blocks the turn until answered. |
| Tool execution timing | the CLI executes a tool right after the assistant message completes and then calls the model again; with `--allowedTools mcp__client` the call reaches the MCP server and blocks. |
| Errors | an upstream error produces an `assistant` line with `error` (`rate_limit`, `authentication_failed`, …) and `api_error_status`, then a `result` with `is_error: true`, `terminal_reason: api_error` and, for subscription limits, `api_error: usage_limit_reached`. |
| `rate_limit_event` | `{status, resetsAt, rateLimitType, utilization?, unifiedWindows: {five_hour, seven_day, seven_day_overage_included: {utilization, resetsAt, status}}}`, derived from `anthropic-ratelimit-unified-*` response headers; emitted for OAuth (setup-token) auth only. |

## 2. Codex (not installed; doc-claim)

| Item | Value |
|---|---|
| Wire protocol | OpenAI **Responses** (`wire_api = "responses"`) |
| Config | `$CODEX_HOME/config.toml` (default `~/.codex`): `[model_providers.<id>]` with `base_url = "<hp>/v1"`, `wire_api = "responses"`, and `env_key = "<VAR>"` (token from env) or `experimental_bearer_token`; optional `http_headers = { … }`; top-level `model_provider = "<id>"` |
| CLI override | `-c key=value` (e.g. `-c model_provider=harbor -c model_providers.harbor.base_url=…`) |
| Session signal | `session_id` header and turn metadata (`x-codex-turn-metadata`), per the isolated-lab study |
| Bridge mode | `codex exec --json` (non-interactive, JSONL events), per-account `CODEX_HOME`; a ChatGPT login lives in `$CODEX_HOME/auth.json` and must be guarded by a per-account lock because refresh tokens rotate |

Status: `unverified` until a live run (`scripts/live/codex.sh`). The bridge is contract-tested against a fake
`codex` binary that emits documented JSONL shapes.

Bridge invocation (doc-claim): `codex exec [resume <thread_id>] --json --skip-git-repo-check --sandbox read-only --color never -m <model>
[-c developer_instructions="…"] [-c model_reasoning_effort="…"] [-c experimental_use_rmcp_client=true -c mcp_servers.client.url="…"] -`
with the prompt on stdin. Events consumed: `thread.started {thread_id}`, `item.started` / `item.completed` with items of type
`agent_message {text}`, `reasoning {text}`, `mcp_tool_call {id, server, tool, arguments}`, `command_execution`, `file_change`, `web_search`;
`turn.completed {usage: {input_tokens, cached_input_tokens, output_tokens}}`; `turn.failed {error: {message}}` and `error {message}`.

## 3. OpenCode (not installed; doc-claim)

| Item | Value |
|---|---|
| Wire protocol | configurable per provider. OpenAI-compatible Chat Completions is the common case; Anthropic is also possible |
| Config | `opencode.json`: `provider.<id>.options.baseURL`, `provider.<id>.options.apiKey`, optional `headers`; models listed under `provider.<id>.models` |
| Session signal | `x-opencode-session` header (isolated-lab study) |

## 4. Pi (not installed; doc-claim)

| Item | Value |
|---|---|
| Wire protocol | per model entry (OpenAI-compatible chat/responses, Anthropic, Gemini) |
| Config | `models.json` (custom providers: base URL, API key, `api` kind), plus `settings.json` |
| Session signal | a session header (exact name unverified) |

## 5. Generic API clients

Any client that can set a base URL and bearer/API key:
- OpenAI SDKs: `OPENAI_BASE_URL=<hp>/v1`, `OPENAI_API_KEY=<gateway token>`.
- Anthropic SDKs: `ANTHROPIC_BASE_URL=<hp>`, plus `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY`.
- Gemini: base URL override to `<hp>` (`/v1beta/models/...`) with `x-goog-api-key` or `?key=` carrying the
  gateway token.

  > **Gemini token caution:** a token sent in the query string can land in proxy logs. Harbor Pilot accepts it
  > only for compatibility, redacts it from access logs, and documents the header form as preferred.

Harbor Pilot accepts the gateway token from `Authorization: Bearer`, `x-api-key` or `x-goog-api-key`, according
to the endpoint namespace. **It never forwards the token upstream** (D-015).
