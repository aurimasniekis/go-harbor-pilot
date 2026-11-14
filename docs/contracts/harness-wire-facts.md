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
  upstreams**. Unknown betas on translated routes trigger the capability/downgrade policy. OAuth flags (`oauth-*`, added
  when the token is passed as `CLAUDE_CODE_OAUTH_TOKEN`, §1.6) are never forwarded (D-048).
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

### 1.4 Prompt caching in the bridge (2026-11-09, 2.1.318 and 2.1.321)

Observed the same way as 1.3: the real binaries (`~/.local/share/claude/versions/2.1.318` and `2.1.321`) against a local synthetic upstream, with a synthetic `sk-ant-oat01-…` token and the bridge's own flags (`--system-prompt-file`, `--tools ""`, MCP client tools).

| Fact | Status | Observation |
|---|---|---|
| The CLI's own breakpoints | verified (observed) | Three `cache_control` markers per request: the identity block and the `--system-prompt-file` block in `system`, and the last message. With `CLAUDE_CODE_PROMPT_CACHE_TTL` unset, all three were `{"type":"ephemeral","ttl":"1h"}` in this setup (subscription-style token, synthetic upstream). A live deployment has shown a `5m` marker from the CLI ahead of a client's `1h` one, so the default may depend on the version, the account or a server-side setting; the bridge always sets the variable. |
| `CLAUDE_CODE_PROMPT_CACHE_TTL=5m\|1h` | verified (observed) | Sets the TTL of all three markers: `5m` gives `{"type":"ephemeral"}`, `1h` gives `ttl: "1h"`. The name is also a string literal in the binary. |
| `FORCE_PROMPT_CACHING_5M=1` | verified (observed) | Forces `5m` on all three. The bridge removes it from the operator's extra environment so the per-conversation TTL wins. |
| `DISABLE_PROMPT_CACHING`, `ENABLE_PROMPT_CACHING_1H` | literal in the binary | Not used by the bridge; semantics not checked. |
| `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1` | literal in the binary | With the bridge's flags (own system prompt, no built-in tools) no git status reached the request with or without it, even with the work directory inside a repository with a commit; only the environment note's "Is a git repository" line changed with the directory. The bridge sets it as a safeguard for prompt stability. |
| Client markers in the transcript | verified (observed) | Markers on transcript or stdin blocks are sent as is, after the CLI's own system markers: a client `1h` after a CLI `5m`, or more than four in total, is a 400 from the upstream. The bridge removes them. |
| A 400 from the upstream | verified (observed) | Reported as an `assistant` line with `api_error_status: 400` and no `error` type, text `API Error: 400 <upstream message>`; the bridge classifies it as `invalid_request` (not failed over). |
| Help text | verified | `claude --help` lists no flag for the cache TTL; the environment variable is the only control found. The one cache-related flag, `--exclude-dynamic-system-prompt-sections`, moves per-machine sections out of the default system prompt for cross-user reuse and is ignored with a custom system prompt, which the bridge always uses. |

### 1.5 Sessions and output settings (2026-11-09, 2.1.318 and 2.1.321)

Observed as in 1.3 and 1.4 (synthetic upstream, synthetic token, the bridge's flags).

| Fact | Status | Observation |
|---|---|---|
| Session file | verified (observed) | With `--session-id <uuid>` (or `--resume`) the CLI writes `CLAUDE_CONFIG_DIR/projects/<cwd, non-alphanumerics as '-'>/<uuid>.jsonl`, mode 0600. `CLAUDE_CODE_SKIP_PROMPT_HISTORY=1` turns this off; the bridge no longer sets it. No other file with conversation content appeared in `-p` mode (no prompt history file). |
| When it is written | verified (observed) | A completed turn is on disk before the `result` line. A turn that ends in a pending MCP tool call is written about 20–40 ms after the call is streamed; the bridge waits for it (up to 2 s) before ending such a process. |
| Entry shapes | observed, undocumented | Entries chain by `uuid`/`parentUuid`. Besides `user` and `assistant` messages the file holds bookkeeping entries (`queue-operation`, `api-request`, `attachment` with `renderedRole: "system"` for the environment note and date). A tool result is a `user` entry whose `message.content` holds the `tool_result` blocks, with `toolUseResult` and `sourceToolAssistantUUID`. The bridge appends only that shape. |
| Resume from another work directory | verified (observed) | Copying the file to the new work directory's projects folder and passing `--resume <uuid>` replays the conversation: every earlier message is sent unchanged (the environment note as recorded), and the CLI appends an "Environment update" naming the new directory after the new turn. A string-content message and a one-text-block message are the same prompt; the CLI switches between them when it moves its breakpoint. |
| Resume with a pending tool call | verified (observed) | Without a recorded result the CLI inserts an "interrupted" `tool_result` and a stdin `tool_result` arrives as "(no content)", so the client's result is lost. With the result appended as above, the CLI sends it, inserts "No response requested." and continues with the typed turn. |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | verified (observed) | Sets the request's `max_tokens` (default 32000; values above 128000 were sent as 128000 for an unknown model). |
| `--effort <level>` | verified (help, observed) | Sets `output_config.effort`; without it the CLI sent `high`. |
| Per-process files | verified (observed) | The CLI writes `CLAUDE_CONFIG_DIR/sessions/<pid>.json` and `<pid>.<hash>.key` (0600: a peer token, the session id, the working directory) and leaves them when it is killed; 0.1.4's flags left them too. The bridge removes them when it reaps the process and sweeps those of no live process. |
| Appended tool results | verified for 2.1.318, 2.1.319, 2.1.321, 2.1.322 | The bridge reads `version` from the session's last entry and appends only for these versions. 2.1.322 (2026-11-12): a tool round trip wrote the same entry types with the same keys as 2.1.321, including the CLI's own tool-result `user` entry, and a shelved tool call resumed with the appended results. The bridge image pins 2.1.318 with the auto-updater off (`TestImagePinsVerifiedCLI`). The shelf tests read the installed CLI's version: a verified one must resume the shelved session, any other must fall back to the transcript with the results delivered, so a CLI update alone does not fail them. |
| Mid-conversation system entries | not verified | No documented session entry carries caller-provided system text; the bridge keeps inlining it as user text. |

### 1.6 Usage headers as a gateway client (2026-11-13, 2.1.323)

Observed by running the CLI (`-p --output-format stream-json`, and interactively with a statusline command and
`/usage`) against a local synthetic server that answered `/v1/messages` with fixed `anthropic-ratelimit-unified-*`
headers. Only the sign-in variables changed between runs. Used by D-048.

| Fact | Status | Observation |
|---|---|---|
| Sign-in decides whether the headers are read | verified (observed) | With `CLAUDE_CODE_OAUTH_TOKEN` (any opaque value, e.g. a gateway token) and `ANTHROPIC_BASE_URL`, the CLI reads the headers. With `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY` or an `apiKeyHelper` it ignores them: no `rate_limit_event`, the statusline's `rate_limits` is null and `/usage` shows no limits. `ANTHROPIC_AUTH_TOKEN` and `apiKeyHelper` each take precedence over `CLAUDE_CODE_OAUTH_TOKEN` when both are set. |
| OAuth-mode request | verified (observed) | Sends `Authorization: Bearer <token>`, adds `oauth-2025-04-20` to `anthropic-beta` and makes a `HEAD /api/hello` to the base URL. It made no other call to the base URL. |
| Window headers | verified (observed) | `-5h-utilization` and `-7d-utilization` are fractions (0.87 shows as 87%), `-5h-reset` and `-7d-reset` are unix seconds, and `-status` is `allowed`, `allowed_warning` or `rejected`. They appear in the statusline's `rate_limits.five_hour` and `rate_limits.seven_day` (`used_percentage`, `resets_at`), in `/usage` as "Current session" and "Current week", and in the stream-json `rate_limit_event`. |
| Near-limit notice | verified (observed) | With `-5h-surpassed-threshold: 0.75` the CLI printed "You've used 87% of your 5-hour limit · resets …". |
| `overage` claim | verified (observed) | With `-representative-claim: overage`, `-overage-utilization: 0.82`, `-overage-surpassed-threshold: 0.75`, `-overage-reset` and `-overage-period: monthly`, the CLI printed "You've used 82% of your usage credits · resets …". The statusline's `rate_limits` stayed null and `/usage` showed no windows for that sign-in. |

### 1.7 Thinking and output settings (2026-11-13, 2.1.323)

Observed as in 1.3: the real binary against a local synthetic Anthropic upstream with the bridge's flags, no `--effort`, and the target model name varied. The bodies are what the CLI itself sends upstream, which is also what a Claude Code client sends to a gateway.

| Fact | Status | Observation |
|---|---|---|
| Adaptive thinking | verified (observed) | For `claude-opus-4-6`, `claude-opus-5-5` and an unrecognised model name (`deepseek-chat`) the CLI sent `"thinking":{"type":"adaptive","display":"updates"}` together with a top-level `"output_config":{"effort":…}`. No `budget_tokens` is sent in this mode. |
| Budgeted thinking | verified (observed) | For `claude-sonnet-4-5` and `claude-haiku-4-5` it sent `"thinking":{"type":"enabled","budget_tokens":31999,"display":"updates"}` (one below the default `max_tokens` of 32000) and no `output_config`. |
| Default effort | verified (observed) | `high` for `claude-opus-4-6` and the unrecognised name, `medium` for `claude-opus-5-5`. `--effort` overrides it (1.5). |
| `context_management` | verified (observed) | Every request carried `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`, Anthropic's server-side context editing. It has no Chat, Responses or Gemini equivalent. |
| `thinking.display` | verified (observed) | Sent in both modes. The gateway's parser models `enabled` and `disabled` only by type and budget, so on a rebuilt native request `display` is not carried for `enabled` (an `adaptive` object is kept whole). Characterised in `testdata/protocol/reasoning/golden/`. |

### 1.8 Output limit reached (2026-11-14, 2.1.325)

Observed as in 1.3: the real binary, a synthetic token, and a synthetic upstream that answers with
`stop_reason: "max_tokens"` and a partial text. Not run against a real subscription account (none is reachable from
the development node), so the real upstream's own behaviour at the limit is a doc-claim.

| Fact | Status | Observation |
|---|---|---|
| Recovery loop | verified (observed, synthetic upstream) | With `CLAUDE_CODE_MAX_OUTPUT_TOKENS=64` and every reply stopping at `max_tokens`, the CLI streamed the first partial reply, then made three more upstream calls (a continuation prompt each) and streamed their text too. A continuation that ends normally is merged into the same turn. |
| Error after recovery | verified (observed, synthetic upstream) | When the continuations also stop at `max_tokens`, the turn ends with `API Error: Claude's response exceeded the 64 output token maximum. To configure this behavior, set the CLAUDE_CODE_MAX_OUTPUT_TOKENS environment variable.` and no HTTP status or error type, so it looked like a generic upstream failure and was failed over. |
| Bridge behaviour | decision | The turn ends at the first upstream `max_tokens` stop: the client gets the content streamed so far, `stop_reason: max_tokens` and the usage of that call, and the process is discarded. The error text above, when it arrives without such a stop, ends the turn the same way. Thinking does not change this: effort only reaches the CLI's `--effort`, and a thinking-only reply is still a truncated reply. A higher floor for `CLAUDE_CODE_MAX_OUTPUT_TOKENS` with truncation on the gateway side was considered and not chosen: the gateway has no exact token counter for the model's output, a floor makes every small side call generate (and bill) more than the client asked for, and it would add a second cut-off rule beside the upstream's own. Ending at the upstream's stop costs one call and is exact. |

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
