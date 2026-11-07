# Gateway Data-Plane Contract

## Onboarding
A client needs only three things:
- a **base URL**
- a **gateway token** (`hpk_…`), issued to a client binding
- a **model name**, either an alias such as `coding/default` or a name the binding's plan maps (e.g. `claude-*`)

No custom headers, SpaceMan, or local daemon are required.

| Client | Base URL | Token transport |
|---|---|---|
| Anthropic Messages (Claude Code, SDKs) | `<origin>` or `<origin>/anthropic` | `Authorization: Bearer` (`ANTHROPIC_AUTH_TOKEN`) or `x-api-key` |
| OpenAI Chat Completions / Responses (Codex, OpenCode, SDKs) | `<origin>/v1` or `<origin>/openai/v1` | `Authorization: Bearer` |
| Gemini generateContent | `<origin>` or `<origin>/gemini` | `x-goog-api-key`, `?key=`, or `Authorization: Bearer` |

Base URLs select a namespace only; they never authenticate. The gateway token is **never** forwarded upstream. Upstream requests carry the provider account's credential, or none for local endpoints.

## Endpoints
| Method and path | Protocol |
|---|---|
| `POST /v1/messages`, `/anthropic/v1/messages` | Anthropic Messages (stream and non-stream) |
| `POST /v1/messages/count_tokens` | Character-based **estimate**, labelled `X-HP-Token-Count: estimated`; never recorded as usage |
| `POST /v1/chat/completions`, `/openai/v1/chat/completions` | OpenAI Chat Completions |
| `POST /v1/responses`, `/openai/v1/responses` | OpenAI Responses |
| `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens` | Gemini (also under `/gemini`) |
| `GET /v1/models` | Anthropic shape when `anthropic-version` or `x-api-key` is present, otherwise OpenAI shape |
| `GET /anthropic/v1/models`, `/openai/v1/models`, `/v1beta/models` | Explicit shapes |
| `POST /v1/harbor/credentials` | Mint a child credential (requires `tokens:mint`). Body: `name`, `scopes`, `ttl_seconds`, `bound_refs`, and optional `check_budget` (default `false`; when `true`, an exhausted enabled blocking budget on the child's binding, project, workspace or global scope refuses the mint with 429 `budget_exceeded` and `budget_id`, D-043) |
| `DELETE /v1/harbor/credentials/{id}` | Revoke a child credential (its parent or itself) |

Every response carries `X-Request-ID`, which keys the request in Activity ("Why this route?"). Errors are returned in the client protocol's own error envelope.

## Optional client hints (untrusted)
These headers are recorded as claims, together with their provenance. They never authorize anything, raise budgets or select accounts.

| Header | Effect |
|---|---|
| `X-HP-Session`, harness session headers (`X-Claude-Code-Session-Id`, `session_id`, …) | Group requests into sessions only when the binding sets `trust.session_hints`; otherwise recorded only |
| `X-HP-Project` | Selects one of the binding's `allowed_project_ids` only when `trust.project_hints` is set and the credential has no bound project |
| `X-HP-Intent` | Recorded; used as the routing intent only under `trust.intent_hints`, otherwise the plan's classifier decides |
| `X-HP-External-Ref` | Recorded only. Trusted references come from the credential's `bound_refs` or the binding's `external_refs` |

Requests without session evidence get `session_confidence: unknown`. They are never merged into a session merely because they share a token.

## Pipeline
1. Authenticate the credential (HMAC verifier) and load the binding from the immutable snapshot.
2. Apply limits: binding requests per minute, then body size (default 32 MiB; per-binding override).
3. Parse into the canonical IR, with bounded messages, parts and tools.
4. Derive capability requirements from observable facts only: streaming, tools, structured output, reasoning, vision, pdf.
5. Resolve the model: exact alias name, then the plan's mappings in order, then the default alias. The binding's `allowed_models` is applied first.
6. Filter candidates (hard restrictions run first and cannot be overridden by scores): account disabled, plan provider allow/deny, billing and classification restrictions, missing executor, missing capabilities (only when the model declares capabilities; undeclared capabilities are noted as "unverified"), health (cooldowns, quota). **Unapproved paid fallback**: a `metered_api` candidate after a non-metered first choice is excluded unless `fallback.allow_paid_fallback`.
7. Rank by pool strategy (ordered, weighted per session, round robin, reset_soonest/least_used once quota data exists), then session affinity, then a same-family preference when the history carries opaque state.
8. For each attempt:
   - Reserve capacity atomically (account and binding concurrency, budgets).
   - Execute.
   - On failure, the next candidate is tried only when **all** of these hold:
     - no byte of model output reached the client;
     - execution was unambiguous (connect errors, error responses, or an upstream error event before any content);
     - the plan's `fallback.steps` permit the transition (`alternate_account`, `alternate_model`, `alternate_provider`);
     - `fallback.max_attempts` is not exhausted.
   - Ambiguous executions are never replayed. These include timeouts after the request was sent and streams that end without output; they are recorded as `unknown_usage`.
9. Reconcile:
   - Append ledger usage (`upstream_reported` or `unknown`) with a cost estimate labelled by billing class.
   - Settle the reservation.
   - Record activity asynchronously.

## Streaming semantics
- Response headers are sent when the first semantic upstream event arrives, or after 10 s without one. In the second case SSE comments keep the connection alive; once headers are sent, later failures are reported in-band in the client's protocol.
- Keepalives are SSE comments before the stream starts and protocol pings afterwards (Anthropic `ping`). The interval is 15 s.
- Streams are never spliced: a failure after output has started ends the stream with an error event.
- Interleaved parallel tool-call deltas from an upstream are serialized into sequential content blocks or items for every client protocol.
- If the client disconnects, the upstream request is canceled. Writes to slow clients are bounded at 60 s each.

## Translation rules
See [COMPATIBILITY.md](../COMPATIBILITY.md).
- **Losses.** Features that cannot be represented are reported as losses:
  - `approximate`: mapped with slightly different semantics.
  - `drop`: optional information removed.
  - `reject`: a feature the request depends on (e.g. structured output to Anthropic, custom tools, server-side `previous_response_id` across protocols). The attempt fails with `unsupported` unless policy explicitly allows the loss, and routing continues to the next permitted candidate.
- **Opaque state.** Opaque signed or encrypted reasoning state (thinking signatures, encrypted reasoning items, thought signatures) is only sent back to its own protocol family. It is never converted to text.
- **Usage.** Usage keeps `unknown` distinct from zero. Ledger input tokens exclude cache categories, so figures are comparable across providers.
