# SDKs and API clients

Anything that lets you set a base URL and an API key can use Harbor Pilot: the official Anthropic, OpenAI and Gemini
SDKs, OpenAI-compatible libraries, notebooks and scripts. Create a client with the tool **Other API client**, take its
access token, and use it where the provider key would go.

::: info Status
The Anthropic Messages, OpenAI Chat Completions, OpenAI Responses and Gemini endpoints are contract-tested, including
translation between them. See the [compatibility matrix](/reference/compatibility) for each pair.
:::

## Environment variables

Keep the token in a file and load it into the environment:

```sh
# OpenAI SDKs and OpenAI-compatible tools
export OPENAI_BASE_URL=https://pilot.example.com/v1
export OPENAI_API_KEY="$(cat ~/.config/harbor-pilot/tokens/sdk.token)"

# Anthropic SDKs
export ANTHROPIC_BASE_URL=https://pilot.example.com
export ANTHROPIC_AUTH_TOKEN="$(cat ~/.config/harbor-pilot/tokens/sdk.token)"
```

**Clients → Set up a tool** with **Other API client** selected shows these lines for your server.

## Examples

Python with the OpenAI SDK:

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
reply = client.chat.completions.create(
    model="coding/default",
    messages=[{"role": "user", "content": "Say hello"}],
)
print(reply.choices[0].message.content)
```

TypeScript with the Anthropic SDK:

```ts
import Anthropic from '@anthropic-ai/sdk'

const client = new Anthropic({
  baseURL: 'https://pilot.example.com',
  authToken: process.env.HARBOR_PILOT_TOKEN,
})
const msg = await client.messages.create({
  model: 'coding/default',
  max_tokens: 512,
  messages: [{ role: 'user', content: 'Say hello' }],
})
```

curl:

```sh
curl https://pilot.example.com/v1/chat/completions \
  -H "Authorization: Bearer $HARBOR_PILOT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"coding/default","messages":[{"role":"user","content":"Say hello"}]}'
```

## Endpoints

| Protocol | Path |
|---|---|
| Anthropic Messages | `POST /v1/messages`, `POST /v1/messages/count_tokens` (also under `/anthropic`) |
| OpenAI Chat Completions | `POST /v1/chat/completions` (also under `/openai`) |
| OpenAI Responses | `POST /v1/responses` (also under `/openai`) |
| Gemini | `POST /v1beta/models/{model}:generateContent` and `:streamGenerateContent` (also under `/gemini`) |
| Model list | `GET /v1/models`, `/anthropic/v1/models`, `/openai/v1/models`, `/v1beta/models` |

The model list returns the model names this client may ask for, in the protocol's own shape. All paths are in
[Gateway endpoints](/reference/gateway).

The protocol a client speaks and the provider that answers are independent: an OpenAI SDK can be served by an
Anthropic account, and the other way round. When a translation would lose something the request needs, the request
page says so.

## Sending the token

Harbor Pilot accepts the access token as `Authorization: Bearer …`, `x-api-key` or `x-goog-api-key`. For Gemini
clients, prefer the `x-goog-api-key` header. A `?key=` query parameter also works for compatibility, but a token in a
URL can end up in proxy logs.

The token is checked and then dropped: it is never forwarded to a provider.

## Model names

Ask for a Harbor Pilot model name, such as `coding/default`, or for a provider's model name, such as
`claude-sonnet-4-5`. Either way the client's plan decides: a mapped pattern sends it to a model name, and anything
else goes to the plan's default model name. A client can also be limited to an allowed list of models.

## Tags

These optional headers add context to a request. They are hints, recorded as claims:

| Header | Meaning |
|---|---|
| `X-HP-Project` | One of the client's allowed projects. It takes effect only if the client allows project hints. |
| `X-HP-Session` | Groups requests into a session, if the client groups sessions. |
| `X-HP-External-Ref` | A reference such as a ticket or a job id. |
| `X-HP-Intent` | The kind of work, for display. |
