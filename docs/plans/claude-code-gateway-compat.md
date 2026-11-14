# Plan: Claude Code as a Harbor Pilot client (gateway protocol and auto mode)

Status: **plan, not implemented** (2026-11-14). Base: `feat/claude-code-compat` (v0.1.7 plus the bridge `max_tokens`
fix). Nothing here is `live-verified`. Every claim is labelled:

- **doc**: from Claude Code's public documentation (the source is cited);
- **code**: read in this repository (file and line);
- **binary**: read from the Claude Code 2.1.325 binary's embedded JavaScript (minified, so this is an inference about
  one build that may change in any release);
- **assumption**: not checked.

## 0. Problem

A Claude Code 2.1.325 client in auto mode, with `ANTHROPIC_BASE_URL=https://hp.example.com` and
`ANTHROPIC_AUTH_TOKEN=<gateway key>`, shows:

> We're changing auto mode to no longer charge for classifier requests in Claude Code. However, this session isn't
> eligible because your requests go through hp.example.com, which isn't compatible with this update.

In an interactive session the notice holds the first checked action until someone presses Enter, so an unattended
agent stalls (doc: auto-mode-classifier-billing, "Respond to the notice").

The target topology is: all Claude traffic goes **through the Claude Code CLI bridge** (`claude_code` provider,
subscription seats). That includes the main model, subagents and Claude Code's side calls. Other vendors go through an
OpenAI-compatible router. The Anthropic API-key paths are covered below, but at lower priority.

Sources:

| Key | Document |
|---|---|
| GW | https://code.claude.com/docs/en/llm-gateway-protocol.md |
| BILL | https://code.claude.com/docs/en/auto-mode-classifier-billing.md |
| PERM | https://code.claude.com/docs/en/permission-modes.md#server-side-classifier-review (and "Default classifier model") |
| ERR | https://code.claude.com/docs/en/errors.md#the-server-returned-no-safety-verdict (and "Auto mode cannot determine the safety of an action") |

## 1. How auto mode's check works behind a gateway

### 1.1 The two kinds of check

- **Server-side review (doc: PERM, BILL).** Claude Code adds an `anthropic-beta` value and a top-level `safeguards`
  body field to the session's own model requests. The server returns its verdicts as `safeguard_results` in the
  response. It does this whenever `ANTHROPIC_BASE_URL` names a gateway, by default from v2.1.278.
- **Client-side classifier (doc: PERM "Default classifier model"; binary).** This is Claude Code's own side request per
  checked action, and the session pays for it as tokens. Binary: the request is non-streaming, two stages
  (a short first stage with `max_tokens` of 64 plus a small allowance and `stop_sequences: ["</block>"]`, then an 8192
  token stage when needed), `temperature` 1, thinking disabled where the model allows it, and no tools. The verdict is
  parsed from XML text. Model: a server-configured one if present, else `CLAUDE_CODE_AUTO_MODE_MODEL`, else
  `claude-sonnet-5` (`ANTHROPIC_DEFAULT_SONNET_MODEL` when it is set and usable), else the main model (doc: PERM).

### 1.2 What decides the outcome (binary, 2.1.325)

| Gateway behaviour | What Claude Code does |
|---|---|
| Passes the beta, `safeguards` and `safeguard_results` through unchanged, and the upstream reviews | Server verdicts decide. `/status` shows **Auto mode server: Enabled**. No notice. |
| Drops `safeguards` (or the beta) and answers 200 without `safeguard_results` | "Server doesn't review this session". This is latched for the session on a non-Anthropic host, and the client classifier takes over. On an account billed by tokens (any `ANTHROPIC_AUTH_TOKEN`/API-key sign-in) **the notice appears**. `/status` reads "Not reaching this session". |
| Answers `400 invalid_request_error` naming `safeguards` | The client retries without the pair and latches the same way. The binary's embedded gateway guidance prefers this to silent stripping, but the latch still leads to the notice on a token-billed sign-in. |
| Cuts the stream short or rewrites `safeguard_results` | "No verdict" for that call. The client classifier decides it if one exists, otherwise the action is denied. After 10 in a row the turn stops (doc: ERR). |
| Client sets `CLAUDE_CODE_AUTO_MODE_SERVER=0` | Nothing is requested, there is no notice, and the client classifier always runs. `/status` shows **Auto mode server: Disabled** (doc: BILL, GW "Disable pre-release capabilities"). |

The notice appears only on token-billed sign-ins. Pro, Max, Team and claude.ai OAuth sessions don't get it (doc: BILL;
binary). An acknowledgement keeps it away for 24 h per config directory, and only when the notice named a host
(doc: BILL). That is no use to agents that start with a fresh config.

**Client classifier availability (binary, inference).** Server-controlled feature flags can retire the client
classifier for a first-party-style session such as an `ANTHROPIC_BASE_URL` gateway. When that happens and the server
check is also off, auto mode reports that it "can't check" actions: the `own_switch_off`, `betas_off` and similar
reasons. With `CLAUDE_CODE_AUTO_MODE_SERVER=0`, the client classifier stays available unless the retirement flag is on
for that account. `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` prevents that retirement, but it is exposed to a different
flag on its own. Setting **both** keeps the client classifier under every flag combination found in this build.
Task T1 confirms this with the real CLI.

## 2. Gap table

Paths:

- **P1 native**: an `anthropic` provider with the same protocol in and out. This is the normal API-key path. The
  request is parsed into IR and rebuilt; it is not byte passthrough.
- **P2 raw passthrough**: `ModePassthrough`, used only when a compatibility breaker is open and the plan allows it
  (`internal/gateway/compat.go:58`).
- **P3 cross-format**: Anthropic Messages in, and Chat, Responses or Gemini to the upstream.
- **P4 bridge**: `claude_code`.

Status values: **OK**, **GAP** (breaks or degrades a Claude Code feature), **n/a**, **accepted** (a known, documented
limitation that blocks nothing).

### 2.1 Requests

| # | Requirement (source) | P1 native | P2 passthrough | P3 cross-format | P4 bridge |
|---|---|---|---|---|---|
| R1 | Forward `anthropic-beta` verbatim; don't allowlist values (GW "Request headers", "Forward as open lists") | OK except `oauth-*`, which is dropped on purpose (`internal/executor/httpapi/httpapi.go:458-500`, D-048); the API-key upstream never needs it | same as P1 (`httpapi.go:201`) | not forwarded (`httpapi.go:201`, `up == req.Inbound` only). Correct: the betas mean nothing to that upstream | ignored entirely: the CLI builds its own request. **GAP** only for pairs whose body half needs a server feature (R3) |
| R2 | Forward `anthropic-version` (GW) | OK (`httpapi.go:463`, default at `:434`) | OK | n/a | n/a (the CLI sets its own) |
| R3 | `safeguards` body field and its beta travel as a pair (GW "Beta header and body field pairs"; BILL) | request OK: unknown top-level fields go to `Ext` (`internal/protocol/anthropic/request.go:189`) and are restored (`build.go:216-217`); the beta is forwarded | OK (raw bytes, `httpapi.go:379-416`) | consistent strip: the field is dropped as a recorded loss `field:anthropic_messages:safeguards` (`build.go:218` and the other builders' foreign-key handling), and the beta is not forwarded, so the client falls back | **GAP**: silently dropped. `shapeRequest` reads only `system`, `messages` and `tools` (`internal/executor/bridge/claudecode/claudecode.go:330-406`); there is no loss and no signal. The client latches and shows the notice |
| R4 | Forward other body fields unchanged, as an open list (GW "Forward as open lists") | mostly OK through `Ext`. Rewrites: `metadata` keeps only `user_id` (`request.go:137-144`); `thinking` of type `enabled` loses `display` (harness-wire-facts §1.7); a string `system` becomes blocks; the cache-marker normaliser edits markers only when the upstream would refuse them (`cache.go:96`) | OK | translated or dropped with losses (D-047) | **accepted**: the CLI builds the body (LIMITATIONS "Claude Code fidelity") |
| R5 | `system` array unchanged, attribution block first (GW "System prompt attribution block") | OK: order and blocks are kept (`build.go:46-61`) | OK | n/a | **accepted**: the CLI prefixes its own blocks |
| R6 | `cache_control` wherever it appears (GW "Prompt caching") | OK unless invalid (R4) | OK | per protocol | **accepted**: client markers are replaced by the CLI's, with the TTL carried (COMPATIBILITY "Claude Code bridge") |
| R7 | `thinking` / `output_config.effort` (GW "Adaptive reasoning", "Effort") | OK (`request.go:145-188`, kept in `Ext`) | OK | D-047 option | effort passed (`--effort`). `thinking` is not: `thinking: {type: "disabled"}` is ignored (`claudecode.go:413-418`). **GAP for the client classifier** (§3.2) |
| R8 | `stop_sequences`, `temperature` | OK | OK | translated | not passed (`claudecode.go:381-383`, approximate loss). **GAP for the client classifier**: its first stage relies on `</block>` (§3.2) |
| R9 | `count_tokens` endpoint (GW "Optional endpoints") | estimate only, labelled `X-HP-Token-Count: estimated` (`internal/gateway/models.go:129-145`) | same | same | same. **accepted** (optional endpoint, and the client falls back to its own estimate anyway) |
| R10 | Model IDs: Anthropic IDs and aliases (GW "Requests and defaults by connection method") | resolved by the plan (`internal/routing/routing.go:139-143`); an unmapped name returns 404 | same | same | same. **Config GAP**: the classifier asks for `claude-sonnet-5` and background calls may ask for a Haiku ID. Each must be mapped (globs are supported, `internal/controlplane/versions.go:28`) or binding `AllowedModels` refuses it |
| R11 | `HEAD /api/hello` may be rejected (GW "Optional endpoints") | 404/405, harmless | | | |
| R12 | `/v1/models` for discovery (GW "Model discovery") | served (`internal/gateway/gateway.go:133`); discovery is off by default in the client | | | |

### 2.2 Responses

| # | Requirement (source) | P1 native | P2 passthrough | P3 cross-format | P4 bridge |
|---|---|---|---|---|---|
| S1 | Don't drop response fields; keep `safeguard_results` (BILL "Make the session eligible") | **GAP**: the stream is decoded to IR and re-encoded. `message_start.message` keeps only `id`/`model`/`usage` (`internal/protocol/anthropic/response.go:228-240`), and `message_delta` keeps only `stop_reason`/`stop_sequence`/`usage` (`:327-342`), where `safeguard_results` arrives (binary). Non-streaming: `ParseResponse` keeps only known keys (`:82-103`), and the re-encoder rebuilds them (`:106-123`, `:407-415`) | OK: bytes are relayed (`compat.go:137`) | n/a (no such field) | n/a: the field never exists, see R3 |
| S2 | Don't drop unknown stream events or delta types (GW "Streaming", "Forward as open lists") | **GAP**: unknown events (`response.go:370`) and delta types (`:303`) are reported to Protocol Watch and dropped | OK | n/a | n/a |
| S3 | Don't rewrite tool-use IDs (BILL) | OK: IDs are kept; `sanitizeToolID` changes only IDs with characters the API rejects (`build.go:375`) | OK | IDs from the foreign upstream, sanitised | OK: the inner CLI's upstream IDs are returned (`internal/executor/bridge/claudecode/stream.go:256-265`), with names mapped back from `mcp__client__…`. To be pinned by a test (T6) |
| S4 | Full event order through `message_delta` and `message_stop`; `ping` keeps the stream alive (GW "Streaming") | OK: the encoder finishes the message; HP sends `ping` every 15 s while idle (`internal/gateway/deliver.go:239-246`, `internal/protocol/protocol.go:81`) | upstream's own | OK | OK |
| S5 | Content block indexes and the message `id` | indexes are renumbered when a block is skipped (`response.go:417-426`); the `id` is the upstream's when one is present | OK | renumbered | the CLI's |
| S6 | Response headers: `retry-after` as integer seconds, `x-should-retry` passed through, `anthropic-ratelimit-unified-*` forwarded (GW "Response headers") | `retry-after` OK on errors (`internal/gateway/gateway.go:166`). **GAP**: `x-should-retry` is not passed. Unified headers are HP's own (D-048) and are read only on OAuth sign-ins (harness-wire-facts §1.6) | headers not copied | n/a | n/a |
| S7 | Forward error bodies unmodified so capability-rejection retries can match the wording (GW "Automatic retry and error forwarding") | **partial**: the message is kept but the envelope is rebuilt, and `type` is recomputed from HP's kind (`response.go:145-208`, `gateway.go:161-177`). Wording-based retries should still match; the `type` may differ | OK | HP envelope | the CLI reports `API Error: 400 <message>` (harness-wire-facts §1.4). Prefix-sensitive matching may fail (assumption) |

### 2.3 Where `Ext`, beta filtering and SSE re-encoding drop or rewrite things

- **`Ext`** carries unknown top-level and message-level request fields to a native upstream (`wire.Object.Ext`,
  `wire.Restore`: `internal/protocol/wire/wire.go:65-89`). Fields foreign to the upstream protocol are dropped as
  `field:<key>` losses (`wire.ForeignExtKeys`). The response side has **no** equivalent: response IR has `Ext` only on
  parts.
- **Beta filtering** removes only `oauth-*` (`httpapi.go:480-500`). The comment on `executor.Request.ClientHeader`
  (`internal/executor/executor.go:42-45`) says "only allowlisted behaviour flags". In fact `anthropic-beta`,
  `anthropic-version` and `OpenAI-Beta` are copied whole. That fits GW and needs only a comment fix.
- **SSE re-encoding** is the S1/S2 gap. It is the only thing between P1 and full compliance for `safeguard_results`.

## 3. What's possible on each path

### 3.1 P1 native and P2 passthrough

The request side already forwards the pair. The response side must keep unknown fields and events when the inbound and
upstream protocols are both Anthropic Messages (T8). After that, P1 gets server-side review and no notice. This is
worth doing for completeness, but the target topology never uses P1.

Pass-through of `safeguards` is limited to same-protocol Anthropic upstreams. It already is, because foreign `Ext`
keys are dropped with a loss. P2 needs nothing.

**Budgets, policy and request fields.**

- Budget reservation estimates from the parsed text. `safeguards` (up to about 1 MB, binary) is not counted, which is
  acceptable because the server review is unbilled (doc: PERM).
- The D-046 `request_fields` allowlist governs **operator-configured** fields only. A client's unknown fields pass
  through `Ext` and are never matched against it. `safeguards` is not on the allowlist, so an operator can't inject
  or override it. No change is needed.
- Privacy masking rewrites message content but not `Ext`. With masking on, the server would review a transcript that
  differs from what the classifier context describes. Masking already blocks P2, and on P1 it should strip the pair
  (T8, small).
- `safeguards` holds transcript excerpts, so content capture must treat it as content (T8).

### 3.2 P4 bridge

**Server review through the bridge is not feasible.**

- The bridge doesn't forward requests. It runs the vendor CLI, which builds its own upstream request with its own
  system prompt, renamed tools (`mcp__client__<name>`) and its own transcript. The `safeguards` context describes the
  client's conversation and rules, which is not the request the upstream would review.
- The CLI's environment is fixed per process, while the context changes every turn.
- Nothing documented makes the CLI attach a caller's `safeguards` per turn, or surface `safeguard_results` in
  `stream-json` output.
- Faking verdicts is out of the question.

A research spike (T10, optional) can check whether the CLI's raw `stream_event` lines would carry `safeguard_results`.
Even so, the mismatch above remains, so it is not recommended.

**Proposed bridge behaviour:**

1. Strip the pair consistently, and say so:
   - drop `safeguards` (already effectively the case) and record a drop loss `safeguards` with the detail "server-side
     auto-mode review is not available through the CLI bridge; set CLAUDE_CODE_AUTO_MODE_SERVER=0 on the client";
   - emit a once-per-binding warning event so the operator sees which clients still request it (T4).
2. Do **not** answer 400 by default. A 400 latches and leads to the same notice on a token-billed sign-in (binary). It
   would also fail every request from client versions that don't retry without the pair (GW documents such retries
   only for other fields). This is open question OQ-1.
3. Make the bridge serve the client classifier correctly, since with the bridge it is the only working check:
   - **Thinking disabled must be honoured (T2).** The classifier sends `thinking: {type: "disabled"}` with
     `max_tokens` of about 64. Today the CLI picks adaptive thinking for the model, so thinking can use up the whole
     budget. The result is a truncated or empty verdict, and auto mode blocks the action ("could not evaluate").
   - **`stop_sequences` must be honoured (T3).** The bridge can do this exactly on the gateway side: cut the text at the
     first match, report `stop_reason: "stop_sequence"` with the matched value, and end the turn. This is the same
     pattern as the max-tokens fix in #26.
   - **Side calls each spawn a CLI process (T6).** Every checked action costs one or two new conversations (two
     stages), and so one or two process spawns. The default process limits are 16 overall and 4 per account. With
     several agents in auto mode, this risks queueing or refusals, which surface as "temporarily unavailable" denials
     (doc: ERR).
   - **The model must be routed (T5).** `claude-sonnet-5` and any Haiku ID must map to the bridge pool.

**What HP can signal:**

- the loss on the request page, plus a Protocol Watch observation;
- the warning event above;
- the setup docs, and `harbor-pilot harness setup claude` (T5), which writes `CLAUDE_CODE_AUTO_MODE_SERVER=0`.

HP can't stop the notice from the server side. The client decides it.

### 3.3 P3 cross-format

This already strips the pair consistently, with a loss. The client falls back exactly as on the bridge, so the same
client setting applies. No code change is needed. Only T4's warning event should cover it too.

## 4. Answers to the auto-mode questions

**(a) Does Claude Code 2.1.325 keep a client-side classifier with `CLAUDE_CODE_AUTO_MODE_SERVER=0` through HP?**

Yes, under the flag state the binary defaults to (binary, inference). The setting turns off the server role and leaves
the client classifier in charge. Auto mode becomes unusable (`own_switch_off`) only if a server-controlled flag has
retired the client classifier for that account. Adding `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` blocks that
retirement.

The client classifier's requests:

- model `claude-sonnet-5` by default, or a server-configured model, or `CLAUDE_CODE_AUTO_MODE_MODEL`. For an Opus 5.5
  or Sonnet 5.5 session, `CLAUDE_CODE_AUTO_MODE_MODEL` set to Opus 5.5 or Sonnet 5.5 is ignored (doc: PERM);
- non-streaming;
- stage 1 with `max_tokens` of 64 plus a small allowance and stop sequence `</block>`, then stage 2 with
  `max_tokens` of 8192 plus a small allowance;
- thinking disabled, or omitted with 2048 added to `max_tokens` on models that refuse disabled thinking;
- `temperature` 1 and no tools.

Not run. Task T1 settles it: whether the classifier request is sent, its exact body, the `/status` row, and whether
`-p` prints the notice to stderr instead of waiting.

**(b) Can HP's bridge make the server-side check work?**

No, not with any documented interface. See §3.2. The CLI owns the upstream request, so the review would cover a
different request from the one the client's `safeguards` context describes. The CLI also has no per-turn way to
attach the field or return `safeguard_results`. This is confirmed by code for the bridge's side
(`claudecode.go:330-406`, `:940-992`); the CLI's side is from documentation and binary inference. It is not strictly
impossible, but it is fragile and unsupported. An optional spike (T10) can test it.

**(c) What should Infra set?**

For every agent profile that talks to HP, in `settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_AUTO_MODE_SERVER": "0",
    "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"
  },
  "permissions": { "defaultMode": "auto" }
}
```

The `permissions` block is unchanged, as are the `autoMode` lists.

- Expected `/status` row: **Auto mode server: Disabled**. Expected behaviour: no notice, and classifier calls
  (`claude-sonnet-5`, two stages) appear in HP as side requests.
- `CLAUDE_CODE_AUTO_MODE_SERVER=0` alone is the documented fix (doc: BILL). The second variable is a hedge against the
  flag retirement in (a). It also stops sending context management, beta tool fields (`strict`, `defer_loading`), MCP
  tool search (all MCP tools load upfront), and structured-output format and task budget (doc: GW). The bridge
  doesn't carry those anyway (LIMITATIONS "Claude Code fidelity"), so the only visible cost is MCP tool definitions
  loading upfront. If the owner prefers the minimum, use `=0` only and add the second variable if `/status` or
  denials show "can't check" (OQ-3).
- **Prerequisite on HP:** the agents' routing plan must map `claude-sonnet-5`, and any `claude-haiku-*` the client
  asks for, to the bridge pool, for example with a `claude-*` glob. Bindings with `AllowedModels` must include them.
  Otherwise every checked action is denied as "temporarily unavailable" (doc: ERR).
- **Prerequisite on HP:** T2 and T3 land before the switch. Until then, expect client-classifier verdicts through the
  bridge to fail often (inference from §3.2).
- **Cost:** every checked action is one or two Sonnet 5 calls on a subscription seat (OQ-2).

## 5. Other Claude Code-as-client issues before the switch

| Item | Finding | Action |
|---|---|---|
| Tiny `max_tokens` side calls | handled by #26 (harness-wire-facts §1.8) | none here |
| `count_tokens` | estimate, labelled (R9) | accepted |
| Model aliases | `claude-sonnet-5` (classifier), Haiku IDs, `[1m]` suffix variants (assumption) must resolve | T5 (docs and setup) |
| Betas on the bridge | ignored. Harmless once the client stops sending pairs that need the server (§4c) | T4 records it |
| Tool-use ID stability | OK on all paths (S3) | T6 adds a regression test on the bridge |
| Prompt caching | bridge: CLI breakpoints with the client's TTL (accepted). Native: markers changed only when invalid | none |
| Effort and thinking | effort passed. `thinking: disabled` is not, which breaks the classifier | T2 |
| `stop_sequences` | not passed on the bridge, which breaks classifier stage 1 | T3 |
| Assistant prefill | the bridge refuses a last message that isn't a user message (`claudecode.go:667-679`). Not seen from Claude Code (assumption) | T1 records whether any side call prefills |
| `x-should-retry` and error envelope | S6 and S7 | T9 |
| Direct calls to `api.anthropic.com` (fast-mode check, WebFetch domain check) | bypass HP (doc: GW "Optional endpoints") | Infra: egress or `skipWebFetchPreflight`-style settings (out of scope) |
| Other dialogs that wait for Enter | any interactive prompt stalls an unattended agent | out of scope (agent profile) |

## 6. Tasks

Every task updates `docs/COMPATIBILITY.md`, `docs/LIMITATIONS.md`, `docs/TRACEABILITY.md` and the matching site pages
in the same change, as AGENTS.md requires. Nothing is marked `live-verified`.

| ID | Tier | Title | Depends on |
|---|---|---|---|
| T1 | implementor-medium | Verify auto-mode client-classifier behaviour with the real CLI against a local fake upstream | none |
| T2 | implementor-medium | Bridge honours `thinking: {type: "disabled"}` | T1 (request shape) |
| T3 | implementor-medium | Bridge honours `stop_sequences` by cutting on the gateway side | none (T1 confirms the need) |
| T4 | implementor-light | Bridge and cross-format: record `safeguards` as a loss and warn once | none |
| T5 | implementor-light | Setup and docs: classifier model routing and `CLAUDE_CODE_AUTO_MODE_SERVER=0` | T1 |
| T6 | implementor-medium | Bridge side-call capacity and an end-to-end classifier test | T2, T3 |
| T7 | implementor-light | Fix the misleading `ClientHeader` comment; pin beta forwarding with a test | none |
| T8 | implementor-hard | Native path: keep unknown response fields and events (`safeguard_results`) | none (low priority) |
| T9 | implementor-light | Native path: pass `x-should-retry`; keep the upstream error `type` | none (low priority) |
| T10 | implementor-medium (optional spike) | Can the bridge request server review on the client's behalf? | T1 |

Order for the batch: T1, T4, T7, then T2, T3 (parallel), then T5, then T6. T8, T9 and T10 come after the switch-over
review, if the owner wants them (OQ-4).

### T1: Verify client-classifier behaviour (experiment, no product code)

**Steps**, on a development node only, with no real credentials and no production:

1. Make an isolated `HOME` and `CLAUDE_CONFIG_DIR` and an empty work directory. Write
   `{"permissions":{"defaultMode":"auto"}}` to `$CLAUDE_CONFIG_DIR/settings.json`.
2. Start the fake upstream from the appendix on `127.0.0.1:<port>`. It records every request with credentials
   redacted.
3. Run `claude -p "run curl on example.com" --model claude-opus-5-5 --permission-mode auto --output-format stream-json --verbose`
   with `env -i`, `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>`, `ANTHROPIC_AUTH_TOKEN=<any fake value>` and
   `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, under `timeout 120`. The fake answers the first main-loop request with
   a Bash `curl` tool call, and every other request with "done". A classifier verdict is therefore unparseable, so the
   action is blocked and never runs.
4. Repeat the run four ways: default; `CLAUDE_CODE_AUTO_MODE_SERVER=0`; `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`;
   both.
5. Interactive variant (tmux, outside an unattended run): the default variant, then `/status`.
6. Repeat runs 3 and 4 through a local HP (`make demo`-style config) whose plan maps `claude-*` to a `claude_code`
   provider pointed at the synthetic upstream, as the bridge tests already do. This shows the classifier request as
   the inner CLI sends it.

**Acceptance:**

- `docs/contracts/harness-wire-facts.md` gets a new §1.9, "Auto mode behind a gateway (2.1.325)". It records, as
  observed with a synthetic upstream:
  - the beta value and the top-level keys of `safeguards` (not their content);
  - whether the classifier request is sent in each variant, with its model, `stream`, `max_tokens`, `stop_sequences`,
    `thinking` and `temperature`;
  - where the notice appears in `-p` (stderr, or a `system` message);
  - the `/status` row;
  - whether any side call ends with an assistant message.
- The fake script is committed under `testdata/` or `scripts/` with a README, and is synthetic.
- If the default-flag run shows "can't check" with `=0`, the plan's §4(c) recommendation is revisited before T5.

**Risks:** the flags differ from a networked session (`DISABLE_NONESSENTIAL_TRAFFIC` changes flag fetching). Record
that as a condition of the observation.

### T2: Bridge honours `thinking: {type: "disabled"}`

**Goal:** a request with thinking disabled reaches the upstream through the CLI without thinking.

**Files:** `internal/executor/bridge/claudecode/claudecode.go` (`shapeRequest`, `outputSettingLosses`, the spawn
environment and arguments, the parked-process match key), plus the tests.

**Approach:** find the CLI's documented control for turning thinking off (for example a thinking-token limit variable
or setting; check `claude --help` and the env-vars documentation). Verify it against the synthetic upstream as in
harness-wire-facts §1.7, and add it to the process key the same way as effort. If no control exists, record the loss
as `reject` for requests with `max_tokens` under some threshold and document it. Then the owner decides; see OQ-5.

**Acceptance:**

- A test with the real CLI and the synthetic upstream shows no `thinking` (or `disabled`) upstream for such a request.
- The `thinking` loss is no longer recorded for it.
- COMPATIBILITY's bridge table has a row for it.

### T3: Bridge honours `stop_sequences`

**Goal:** text output stops at the first client stop sequence, as the API does.

**Files:** `internal/executor/bridge/claudecode/stream.go` (the same turn-ending path as the `max_tokens` stop) and
`claudecode.go` (stop the `sampling` loss for `stop_sequences`).

**Approach:**

- Buffer only as much text as the longest stop sequence's length minus one, to match across deltas.
- On a match, emit text up to it, then `stop_reason: "stop_sequence"` with `stop_sequence` set to the match. Use
  that call's usage, and discard the process (the CLI would continue).
- Streaming and non-streaming both.
- Tool-use and thinking blocks are not scanned (the API doesn't either).

**Acceptance:**

- Unit tests: a match split across deltas; no match; several sequences; a match at the start.
- An end-to-end test through the gateway with the synthetic upstream.
- The loss detail now covers only `temperature`/`top_p`/`top_k`.
- COMPATIBILITY updated.

**Risk:** a stop inside a tool-use turn. Cut only text blocks; if a tool call follows a match, drop it, as the API
would never have produced it.

### T4: Record `safeguards` and warn

**Goal:** the bridge and cross-format paths report the dropped pair explicitly and tell the operator what to do.

**Files:** `claudecode.go` (`shapeRequest`); `internal/gateway` (a once-per-binding event through the existing event
outbox, not on the hot path); `internal/events/catalog.go`; `docs/contracts/events.md`; `site/reference/events.md`.

**Acceptance:**

- A bridge request with a top-level `safeguards` records a drop loss `safeguards` with the remediation text (§3.2).
- Cross-format keeps its existing `field:` loss, with a `foreignDetails` entry giving the same remediation
  (`internal/protocol/wire/wire.go:122`).
- The event fires at most once per binding per day, with no request content in it.
- A test shows the request still succeeds with the field absent upstream.
- LIMITATIONS states that auto mode's server review is unavailable through the bridge and cross-format routes.

### T5: Setup and documentation for Claude Code clients

**Goal:** an operator can configure Claude Code agents for the bridge without the notice and with a working client
classifier.

**Files:**

- `site/` (the Claude Code client page and the bridge page; troubleshooting entries for the notice, "no safety
  verdict" and "temporarily unavailable");
- the `harness setup claude` command: a flag that writes `env.CLAUDE_CODE_AUTO_MODE_SERVER=0` (and optionally the
  betas variable), defaulting to on when the target plan has no native Anthropic route. Or always on, see OQ-6;
- a demo plan example that maps `claude-*` to the bridge pool.

**Acceptance:**

- Setup tests cover the new keys, including the reversible-uninstall test.
- The docs give the exact `settings.json` from §4(c) and the expected `/status` row.
- A routing note lists `claude-sonnet-5` as the classifier's default model.
- No real hostnames appear.

### T6: Bridge side-call capacity and end-to-end classifier test

**Goal:** a two-stage classifier call per checked action does not starve the bridge.

**Files:** `claudecode.go` (spawn path and limits), `internal/config` (documented sizing), plus the tests.

**Approach:**

- Measure spawn-to-first-token with the synthetic upstream.
- For stateless requests (no tools, a single user message, no tool results to continue), spawn with
  `--no-session-persistence`, skip writing a transcript, and don't park or shelve the process.
- Document sizing for `max_processes` and `max_per_account` against the number of agents.

**Acceptance:**

- A gateway-level test sends a classifier-shaped request (non-streaming, `max_tokens` 64, `stop_sequences`, thinking
  disabled, model `claude-sonnet-5` mapped to the bridge) through the real CLI and the synthetic upstream. It gets a
  stop at the sequence and a verdict text.
- A test confirms tool-use IDs are returned unchanged on a tool turn (S3).
- Stateless requests leave no shelf entry.
- The measured latency is recorded in harness-wire-facts, as synthetic.

### T7: Beta-forwarding comment and test

**Acceptance:**

- The comment at `internal/executor/executor.go:42-45` describes what is forwarded.
- A test asserts that an arbitrary unknown `anthropic-beta` value and a top-level unknown body field (for example
  `safeguards` with an opaque object) both reach a native Anthropic upstream unchanged, and that `oauth-*` does not.

### T8: Native path keeps unknown response fields (hard, low priority)

**Goal:** P1 meets S1 and S2, so an API-key route gets server review with no notice.

**Files:** `internal/protocol/anthropic/response.go` (decoder and encoder), `internal/ir` (response- and event-level
`Ext`), `internal/protocol/protocol.go`, the gateway stream relay, and the privacy and trace capture of `safeguards`.

**Approach:**

- Carry unknown keys of `message_start.message`, `message_delta.delta`, `message_delta` and the non-streaming top
  level in IR `Ext` keyed by protocol.
- Re-emit them only when the client protocol is Anthropic Messages and the upstream family is Anthropic.
- Relay unknown event types and delta types verbatim under the same condition.
- Strip the request pair when privacy masking changes the request.
- Treat `safeguards` as content for capture and erasure.

**Acceptance:**

- Synthetic fixtures (an opaque `safeguard_results` object in `message_delta.delta`, an unknown event, an unknown delta)
  round-trip byte-equivalent in JSON terms through a native route.
- Cross-format clients never receive them.
- Fuzz targets still pass.
- Protocol Watch still records unknowns.

**Risks:** this touches the core encoder that every Anthropic client uses. Ship it behind golden tests, and keep it out
of the switch-over's critical path.

### T9: Native response headers and error type (low priority)

**Acceptance:**

- `x-should-retry` from the upstream is set on the client response, for errors and success, on Anthropic-in and
  Anthropic-out routes.
- An upstream Anthropic error's `type` is kept when HP doesn't override the kind.
- Tests cover both.

### T10: Spike: server review on the client's behalf (optional)

**Acceptance:** a short note in harness-wire-facts that answers, with the synthetic upstream:

- whether the CLI's `stream_event` lines carry an unknown `message_delta.delta` field such as `safeguard_results`;
- whether a body field can be added to the CLI's request per process.

It recommends go or no-go. No product code.

## 7. Assumptions

- A1. Agents run Claude Code interactively (TUI), so the notice blocks. In `-p` it would only print (binary).
- A2. The agents' clients fetch feature flags normally (no `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`), so the flag
  state is Anthropic's for that sign-in.
- A3. The routing plan for the agents maps the main models to the bridge pool. Whether it maps `claude-sonnet-5` and
  Haiku IDs is unknown.
- A4. Bridge seats are subscription accounts. The inner CLI is not in auto mode and requests no server review itself.
- A5. No captured traces from a live deployment were used; details of the live wiring may change A1–A3.

## 8. Open questions for the owner

- **OQ-1.** Bridge reaction to `safeguards`: silent strip with a loss and a warning (recommended), or
  `400 invalid_request_error` naming `safeguards` (the binary's embedded guidance; clients that don't retry would
  fail)?
- **OQ-2.** Is the extra seat usage of client-classifier calls acceptable: one or two Sonnet 5 calls per checked action?
  Alternatively pin `CLAUDE_CODE_AUTO_MODE_MODEL` to a cheaper routed model. (The client ignores Sonnet 5.5 and
  Opus 5.5 there.)
- **OQ-3.** Agent profile: `CLAUDE_CODE_AUTO_MODE_SERVER=0` alone, or together with
  `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` as a hedge (recommended until T1 shows the flag state)?
- **OQ-4.** Are T8 and T9 (native API-key path) in this batch, or deferred since the API key is never used?
- **OQ-5.** If the CLI has no way to disable thinking, should the bridge refuse classifier-shaped requests (so they
  fail over to another route), or accept degraded verdicts?
- **OQ-6.** Should `harness setup claude` write `CLAUDE_CODE_AUTO_MODE_SERVER=0` by default?

## Appendix: fake upstream for T1

This is a synthetic local server (Python standard library). It records each request as JSON (method, path, headers
with `authorization` and `x-api-key` redacted to their length, and body). It answers:

- `count_tokens` with a fixed count;
- `GET` with an empty model list and `HEAD` with 200;
- the first main-loop request, meaning one whose tools include `Bash` and that has no `tool_result` yet, with a
  `tool_use` block `{"name":"Bash","input":{"command":"curl -s https://example.com"}}` and `stop_reason: tool_use`;
- every other request with text "done" and `end_turn`.

Each answer is streamed as `message_start`, the block events, `message_delta` and `message_stop` when the request sets
`stream`, and returned as a single JSON message otherwise. Run it as
`python3 -I fake.py <port> <output-dir>`, from a directory separate from its output.

Because "done" is not a valid verdict, the classifier's answer can't be parsed and auto mode blocks the action. The
`curl` never runs.
