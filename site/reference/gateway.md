# Gateway endpoints

The gateway is the part your tools talk to. It speaks four wire protocols on the same address as the UI and API, and
every endpoint accepts the same gateway token. A tool needs only three things:

- a **base URL**, for example `https://harbor-pilot.example.com`;
- a **gateway token** (`hpk_…`) issued to its client;
- a **model name**: an alias such as `coding/default`, or any name the client's routing plan maps.

No custom headers, local daemon or SDK changes are required.

## Base URLs and tokens

| Protocol | Base URL | Token in |
|---|---|---|
| Anthropic Messages (Claude Code, Anthropic SDKs) | `<origin>` or `<origin>/anthropic` | `Authorization: Bearer` (`ANTHROPIC_AUTH_TOKEN`) or `x-api-key` |
| OpenAI Chat Completions and Responses (Codex, OpenCode, OpenAI SDKs) | `<origin>/v1` or `<origin>/openai/v1` | `Authorization: Bearer` |
| Gemini `generateContent` | `<origin>` or `<origin>/gemini` | `x-goog-api-key`, `?key=` or `Authorization: Bearer` |

The prefix only picks a namespace; it never authenticates or routes. The gateway token is **never** forwarded to a
provider: upstream requests carry the account's own credential, or none for local model servers.

## Endpoints

| Method and path | What it does |
|---|---|
| `POST /v1/messages` (also under `/anthropic`) | Anthropic Messages, streaming or not. |
| `POST /v1/messages/count_tokens` (also under `/anthropic`) | A character-based **estimate** (about four characters per token), marked `X-HP-Token-Count: estimated`. Never recorded as usage. |
| `POST /v1/chat/completions` (also under `/openai`) | OpenAI Chat Completions. |
| `POST /v1/responses` (also under `/openai`) | OpenAI Responses. |
| `POST /v1beta/models/{model}:generateContent` (also under `/gemini`) | Gemini, not streaming. |
| `POST /v1beta/models/{model}:streamGenerateContent` (also under `/gemini`) | Gemini, streaming. |
| `POST /v1beta/models/{model}:countTokens` (also under `/gemini`) | The same estimate in Gemini's shape, `{"totalTokens": N}`. Accepts `contents` or a wrapped `generateContentRequest`. |
| `GET /v1/models` | The models the client may use: Anthropic shape when the request has `anthropic-version` or `x-api-key`, otherwise OpenAI shape. |
| `GET /anthropic/v1/models`, `/openai/v1/models`, `/v1beta/models` | The same list in an explicit shape. |
| `POST /v1/harbor/credentials` | Mint a short-lived child token (see below). |
| `DELETE /v1/harbor/credentials/{id}` | Revoke a child token. |

Calling a model needs the `gateway:invoke` scope; listing models needs `gateway:models`.

Outside the gateway, the server also answers:

| Path | Purpose |
|---|---|
| `GET /healthz` | Liveness: `{"status":"ok"}` whenever the process is up. |
| `GET /readyz` | Readiness: `200` once started and both databases answer, `503` otherwise. |
| `GET /metrics` | Prometheus metrics. Needs the bearer token from `server.metrics_token_file`, or a direct loopback connection when none is set. |
| `/api/v1/…` | The [control-plane API](/reference/api). |

## Response headers

| Header | Meaning |
|---|---|
| `X-Request-ID` | The request's id (`req_…`). Open it in **Requests** to see why it went where it did. |
| `Retry-After` | On a refusal that can be retried (a rate limit, a cooldown), seconds to wait. |
| `X-Harbor-Mode: passthrough` | The request was forwarded byte for byte to a provider that speaks the same protocol, instead of being translated. |
| `X-HP-Token-Count: estimated` | On token-count answers: the figure is an estimate. |

## Optional hints from tools

Tools may send these headers. They are recorded as **claims**, with where they came from, and never authorize
anything, raise a budget or pick an account by themselves.

| Header | Effect |
|---|---|
| `X-HP-Session`, and the tools' own session headers (`X-Claude-Code-Session-Id`, `Session_id`, `Session-Id`, `X-Session-Id`, `X-Conversation-Id`) | Group requests into sessions, but only when the client trusts session hints. Otherwise recorded only. |
| `X-HP-Project` | Picks one of the client's allowed projects, only when the client trusts project hints and the token has no project bound to it. |
| `X-HP-Intent` | Recorded. Used as the request's intent for routing only when the client trusts intent hints; otherwise the plan's classifier, if any, decides. |
| `X-HP-External-Ref` | Recorded only. Trusted references come from the client's configuration or from a child token's mint call. |

A request with no session evidence is recorded with an unknown session. Requests are never merged into one session
just because they share a token.

## Errors

Errors come back in the tool's own protocol (an Anthropic, OpenAI or Gemini error body), so SDKs handle them
normally. The kind of error decides the status:

| Kind | Status |
|---|---|
| `invalid_request`, `context_length`, `unsupported` | 400 |
| `authentication` | 401 |
| `permission`, `policy_blocked`, `content_filter` | 403 |
| `not_found` | 404 |
| `rate_limited`, `quota_exhausted`, `budget_exceeded` | 429 |
| `timeout` | 504 |
| `overloaded` | 529 |
| `upstream`, `protocol`, `internal` | 502 |

When a provider answers with its own status, that status is passed on. Provider error text is scrubbed of credentials
before it reaches the tool.

## Child tokens

A client token with the `tokens:mint` scope can mint short-lived child tokens, for example one per agent run, so a
leaked child expires quickly and can be revoked on its own.

```sh
curl -sS https://harbor-pilot.example.com/v1/harbor/credentials \
  -H "Authorization: Bearer $(cat ~/.config/harbor-pilot/tokens/orchestrator.token)" \
  -H 'Content-Type: application/json' \
  -d '{"name":"run-42","ttl_seconds":3600,"bound_refs":{"run_id":"run-42"}}'
```

The answer (`201`) holds the new token, shown once, in `token` and its description in `credential`; use
`credential.id` to revoke it. A child token:

- uses its parent's client, plan, limits and budgets;
- has a subset of its parent's scopes and never `tokens:mint`, so it cannot mint further tokens;
- lives 12 hours by default and at most 7 days, and never longer than its parent;
- carries its `bound_refs` as **trusted** references on every request, and a bound project when the client allows it;
- can be revoked by its parent or by itself with `DELETE /v1/harbor/credentials/{id}`.

See [Agent orchestrators](/tools/orchestrators).

## How a request is handled

1. **Authenticate** the token and load its client from the current configuration.
2. **Apply limits**: the client's requests per minute, then the body size (32 MiB by default, adjustable per client).
3. **Parse** the request into one internal form, with bounded messages, parts and tools.
4. **Work out what the request needs** from what it contains: streaming, tools, structured output, reasoning, images,
   PDFs.
5. **Resolve the model**: an exact alias name, then the plan's model mappings in order, then the default alias, within
   the client's allowed models.
6. **Filter candidates.** Hard restrictions run first and no score overrides them: a disabled account, the plan's
   provider rules, billing restrictions, a missing capability, cooldowns and quota. A paid API account is not used as
   a fallback after a subscription unless the plan allows paid fallback.
7. **Rank** the rest by the group's strategy, then by session affinity.
8. **Attempt** the best candidate: reserve capacity and budget, then send. On failure, the next candidate is tried
   only when no output has reached the tool yet, the failure was unambiguous, the plan's fallback steps allow it, and
   attempts remain. A request that may have reached the provider (a timeout after sending, a stream that ended without
   output) is never replayed.
9. **Settle**: record usage and its cost estimate, release the reservation, and record the request in the background.

## Streaming

- Response headers are sent when the first real upstream event arrives, or after 10 seconds without one, so failover
  can still happen before the tool sees anything.
- Keepalives are sent every 15 seconds.
- Streams are never spliced: a failure after output started ends the stream with an error event in the tool's
  protocol.
- Parallel tool-call deltas are serialised into sequential blocks for every protocol.
- When the tool disconnects, the upstream request is cancelled. Each write to a slow client may take at most 60
  seconds.

## Translation

When the tool and the provider speak different protocols, the request is translated. Anything that cannot be carried
across is recorded on the attempt as a **loss**:

- **approximate**: carried with slightly different meaning;
- **drop**: optional information left out;
- **reject**: something the request depends on cannot be carried. The attempt fails as `unsupported` unless the plan
  allows that loss, and routing moves to the next candidate.

Signed or encrypted reasoning state is only ever sent back to its own protocol family, never turned into text. Usage
keeps "unknown" apart from zero. The [compatibility matrix](/reference/compatibility) lists every pair.
