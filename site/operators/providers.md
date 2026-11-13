# Providers and accounts

**Configure → Providers** is where Harbor Pilot learns what it can send requests to.

- A **provider** is one upstream service: the Anthropic API, OpenAI, Gemini, an OpenAI-compatible server, a local Ollama,
  or a Claude Code or Codex CLI bridge. It holds the address, timeouts and the list of models with their prices.
- An **account** is one credential for that provider: an API key, or one subscription seat's sign-in. A provider can
  have several accounts (Team key 1, Team key 2), and [fallback groups](/operators/routing#fallback-groups) fail over
  between them.

<Shot name="providers" alt="Providers page with three providers, each listing its accounts with billing, status, tightest limit, requests and cost in the last 24 hours" caption="Providers and their accounts" />

The line under the title counts what you have, for example "3 providers · 4 accounts · 4 working · 1 subscription".
Each provider shows its API type and base URL, **+ Add account**, and a ⋯ menu to edit or delete it. Each account row
shows its billing, status, tightest quota window, and requests and cost over the last 24 hours.

## Connect a provider

**+ Connect a provider** asks **What are you connecting?**

<Shot name="provider-connect" alt="Connect a provider dialog asking what you are connecting, with Anthropic API, OpenAI API, Gemini API, OpenAI-compatible API, Ollama, Claude Code CLI bridge and Codex CLI bridge, and the API key choices" caption="Connect a provider: pick the type, paste a key, done" />

| Choice | Use it for |
|---|---|
| **Anthropic API** | Claude models with an API key |
| **OpenAI API** | GPT models with an API key |
| **Gemini API** | Gemini models with an API key |
| **OpenAI-compatible API** | OpenRouter, DeepSeek, vLLM and other servers that speak the OpenAI API (needs a base URL) |
| **Ollama (local)** | A model server on your network (default `http://127.0.0.1:11434`) |
| **Claude Code CLI bridge** | A Claude subscription through its CLI, see [Subscriptions](/operators/subscriptions) |
| **Codex CLI bridge** | A ChatGPT subscription through its CLI, see [Subscriptions](/operators/subscriptions) |

Then:

1. Give it a **Display name** and a **Short name** (used in IDs, Terraform and the request list).
2. For an OpenAI-compatible server, enter its **Base URL**, such as `https://llm.example.com/v1`.
3. Choose how to give the **API key**:
   - **Paste it**: stored encrypted and never shown again. It becomes this provider's first account.
   - **From the server environment**: a [secret reference](#api-keys-and-secret-references) such as
     `env:HP_SECRET_OPENAI_KEY`.
   - **Add later**: create the provider now and add accounts afterwards.
4. Click **Connect and test** (or **Connect provider** without a key).

### Connection

The collapsed **Connection** section holds:

- **Base URL**: leave empty for the provider's standard address.
- **Allow a local network address**: needed for local model servers and plain `http`. Cloud metadata addresses stay
  blocked either way.
- **Request timeout** (default 600 s) and **Idle stream timeout** (default 300 s).
- **Shown as**: how the provider is classified (Cloud, Partial compatibility, Unofficial, Local). Routing plans can
  [restrict](/operators/routing#hard-restrictions) by this.
- **Static headers**: non-secret headers sent upstream, one `Name=value` per line. Authentication headers are refused.

### Models and prices

With no models listed, fallback groups can use any model id the provider accepts, but Harbor Pilot can't estimate
cost. **Add a model** to set:

- **Model id**, exactly as the provider names it, and a **Display name**.
- **Context window** and **Max output**.
- **Capabilities**: streaming, tool calls, parallel tool calls, streamed tool calls, images, reasoning, structured
  output, prompt caching. Routing uses these to skip an account that can't serve a request (for example one with
  images).
- **Prices (for cost estimates)**: **Input**, **Output**, **Cache read** and **Cache write** per million tokens, a
  **Currency** and a **Price list label**.

These prices are what [Spend](/operators/spend) calls _estimated cost_.

### Upstream request fields

Some routers let the caller choose which provider serves a model, and some clients (Claude Code, for one) have no
setting for it. Rather than putting that in every client's configuration, set it once on the model under **Upstream
request fields**:

- **Pin the upstream provider** (`openai_compatible` providers only; other provider types never receive it). Name a
  particular provider in **Prefer in order**, **Only these** or **Never these**; choose a **Rank by** order, restrict
  **Providers that store prompts**, require **zero data retention** or **all parameters**, list **Quantizations**, and
  **Cap the price** per million tokens, per request or per image. A field left at its default is omitted from the
  request rather than sent, so the router's own default applies.
- **Other fields (JSON object)** takes fields from a short allowlist per provider type:

  | Provider type | Allowed fields |
  |---|---|
  | OpenAI-compatible | `transforms`, `provider` (instead of the pin), `temperature`, `top_p`, `top_k`, `min_p`, `top_a`, `frequency_penalty`, `presence_penalty`, `repetition_penalty`, `seed`, `stop`, `logit_bias`, `user`, `metadata` |
  | OpenAI | `temperature`, `top_p`, `user`, `metadata` |
  | Ollama | `temperature`, `top_p`, `seed`, `stop`, `frequency_penalty`, `presence_penalty` |
  | Anthropic | `metadata`, `temperature`, `top_p`, `top_k`, `stop_sequences` |
  | Gemini | `safetySettings`; `generationConfig` with only `temperature`, `topP`, `topK`, `stopSequences` |

  Spelling doesn't matter (`top_p` and `topP` are the same field). Anything else is refused when you save, and the
  message lists what is allowed. Output limits, extra candidates, model lists, tools, plugins, stored state, prompt
  caching, streaming and the request's structure are never configurable: they would bypass budgets or change what the
  client asked for. Gemini's `generationConfig` is merged key by key, so the client's other settings in it stay. Only the
  `generationConfig` spelling is merged: a `generation_config` in the same request is dropped and shows as a loss.
- **When the client already sends one of these** decides the conflict: **Send mine and replace theirs** (the default),
  **Keep theirs, only fill missing ones**, or **Refuse requests that set them**. Only what the client sent counts;
  values the gateway fills in itself are never a conflict. A refusal answers the client with 400 before the request is
  sent to any candidate (an intent classifier may already have run to choose the route), and if several models could serve a request and any of them refuses, the request is refused. An
  override or a kept client value appears on the request page as a translation loss, so it is never silent.

Models that keep this section empty are forwarded exactly as before. A model with any of it set shows an **Upstream
fields** badge in the model list. Saving a provider replaces its whole spec, so a tool that updates providers through
the API must send these fields back (see [Terraform](/operators/terraform#upstream-request-fields)).

### Reasoning

Clients ask for reasoning in their own protocol's terms. Claude Code, for example, sends adaptive thinking with an
effort level (`thinking: {type: "adaptive"}` and `output_config: {effort: "high"}`), while routers and other providers
each take their own parameter. A model's `reasoning` option tells Harbor Pilot how to write these settings for that
model. Without it, an explicit level is translated as before, and adaptive thinking reaches only Anthropic models.

```json
{"id": "deepseek/deepseek-chat", "reasoning": {"mode": "client", "style": "openrouter_reasoning", "effort": "medium", "efforts": ["low", "medium", "high"]}}
```

- **Mode.** `client` follows what the client asks for, using `effort` (or `budget_tokens`) when the client names no
  level. `fixed` always asks for that level. `off` always turns reasoning off. `auto`, the default, keeps the built-in
  translation.
- **Style** is the parameter the upstream understands:

  | Style | For | Sends |
  |---|---|---|
  | `openrouter_reasoning` | OpenAI-compatible routers | `reasoning: {effort}`, `{max_tokens}` or `{enabled: true}`; off is `{effort: "none"}` |
  | `deepseek_thinking` | DeepSeek's own API | `thinking: {type: "enabled"}` plus `reasoning_effort`; off is `{type: "disabled"}` |
  | `openai_reasoning_effort` | OpenAI, OpenAI-compatible, local | `reasoning_effort` |
  | `openai_responses_reasoning` | OpenAI | `reasoning.effort` |
  | `anthropic_thinking` | Anthropic | Adaptive thinking unchanged; a level as a thinking budget |
  | `gemini_thinking_config` | Gemini | A thinking budget |
  | `none` | all | Nothing |

  Leave it empty to use the upstream protocol's default.
- **Efforts** lists the levels the model accepts. A higher level is lowered to the nearest one in the list, and a
  thinking budget above **max_budget_tokens** is reduced. Each change shows on the request page as a translation loss.
- **History.** Some Chat APIs want earlier reasoning sent back on assistant messages. `history: "field"` sends it in
  `history_field` (the style's default: `reasoning_content` for DeepSeek, `reasoning` for routers).

Claude Code's `context_management` (Anthropic's server-side context editing) has no equivalent elsewhere and is
always dropped. Passthrough requests and the CLI bridges ignore the option. Saving a provider replaces its whole spec,
so tools that update providers must send `reasoning` back. The full reference is in the
[configuration contract](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/docs/contracts/config.md#per-model-reasoning-translation).

Routing is unchanged: a request that names a level or a thinking budget (Claude Code sends a budget for older Claude
model names) goes only to models with the **reasoning** capability, whatever the mode. Adaptive thinking alone needs
no capability.

#### Recipe: Claude Code to DeepSeek

Claude Code can't send back reasoning from other providers (see
[limitations](/security/limitations#reasoning-settings)), and DeepSeek's own API answers 400 when a request with tools
lacks the earlier reasoning.

- **DeepSeek's own API:** use `{"mode": "off", "style": "deepseek_thinking"}`, and declare the **reasoning**
  capability so that requests carrying a thinking budget still reach the model.
- **Through an OpenAI-compatible router:** the router's documentation describes sending reasoning back for tool calls
  but doesn't say whether a request without it fails. Start with `{"mode": "off", "style": "openrouter_reasoning"}`,
  or a low fixed effort (`{"mode": "fixed", "style": "openrouter_reasoning", "effort": "low"}`), and move to
  `{"mode": "client", "style": "openrouter_reasoning"}` (Claude Code's effort, `high` by default, then reaches the
  router as `reasoning: {effort: "high"}`) once a multi-turn tool session works.

## Add an account

**+ Add account** on a provider opens the account dialog:

| Field | Notes |
|---|---|
| **Name** | Shown in fallback groups and on request pages |
| Credential | **Paste it** (stored encrypted), **From the server environment** (a secret reference) or **None**. Editing an account that has a saved credential shows its fingerprint; leave the field empty to keep it, or tick **Remove the saved credential** |
| **How it is paid** | **Pay per use** (an API key billed for each request), **Subscription** (a flat-fee plan, such as Claude Max or ChatGPT Pro), **Local model** (runs on your own hardware) or **Free**. Routing uses this to decide when [falling back to a paid account](/operators/routing#fallback-steps) is allowed |
| **Account on** | Off accounts are never routed to |
| **Limits, models and labels** | **Requests at once** and **Requests per minute** for this account, **Models this account may use** (default: all of the provider's models) and **Labels** |

## Test the connection

Click an account to open its drawer, then **Test connection**. Harbor Pilot lists the provider's models with the
account's credential. That proves the address and the credential work without sending a request to a model, so it
spends no tokens. The result is stored and shown on the account:

- **Connection works**, with how long the provider took to answer.
- **Connection failed**, with the provider's reason (credential rejected, unreachable, …).
- **Ready to use**, for CLI bridge accounts: Harbor Pilot reports whether the CLI can run on the server. A bridge
  account is only confirmed by its first real request.
- No usable credential, when nothing is saved or a reference can't be read.

<Shot name="account-test" alt="Team key 1 account drawer after Test connection, showing Connection works, the provider accepted the credential, checked just now, followed by the account's provider, billing, credential, limits, quota windows and subscription" caption="An account's drawer with a passing connection test" />

A passing test does not prove that the account can use a given model, has quota left, or that its billing is set up.

The same check is `POST /api/v1/provider-accounts/{id}/test`; stored results for all accounts are at
`GET /api/v1/account-checks`. Each test is recorded in the [audit log](/operators/audit).

The drawer also shows the account's **Quota** (its windows, or **Add quota tracking**), and for subscription accounts
its [**Subscription**](/operators/subscriptions#subscriptions-and-fees).

## Account status

Each account shows the first status that applies, in this order:

| Status | Meaning |
|---|---|
| **Off** | The account is switched off |
| **No credential** | Nothing to authenticate with |
| **Credential rejected** | The provider refused the credential |
| **Unreachable** | The provider couldn't be reached |
| **Limit reached** | A quota window is used up. Routing skips it until reset |
| **Mostly failing** | More than half of its recent requests failed |
| **Working** | It is serving requests |
| **Connection OK** | No traffic yet, but the connection test passed |
| **Not used yet** | A CLI bridge account with no traffic yet |
| **Not tested** | No traffic and no connection test |

## API keys and secret references

Harbor Pilot can hold a credential in two ways:

- **Stored**: you paste it once; it is encrypted with the server's master key and never shown again. Backups contain it
  encrypted.
- **Referenced**: Harbor Pilot reads it from the server when it needs it, so it never enters the database:
  - `env:HP_SECRET_OPENAI_KEY` reads an environment variable. Only variables starting with `HP_SECRET_` (or another
    prefix set in `security.secret_env_prefixes`) are allowed, so a reference can't read the master key or other
    server settings.
  - `file:/run/secrets/openai` reads a file. Files must be under one of the directories in `security.secret_roots`,
    which may not contain the data directory or the master key. `file:/path#key` reads one field from a JSON file.

Use references when keys already live in a secret manager, Docker or Kubernetes secrets, or systemd credentials. See
[Secrets and the master key](/security/secrets) for the details and how to rotate.

## Delete

Deleting an account stops routing to it and removes its saved credential. Deleting a provider stops routing to it. In
both cases, fallback groups that list it must be changed first, and a provider's accounts must be deleted before the
provider.

## Related

- [Subscriptions and CLI bridges](/operators/subscriptions)
- [Routing plans](/operators/routing): put accounts into fallback groups.
- [Limits](/operators/limits): quota tracking per account.
- [Terraform](/operators/terraform): manage providers and accounts as code.
