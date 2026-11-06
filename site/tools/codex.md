# Codex

Codex talks the OpenAI Responses API. Harbor Pilot serves it at `/v1/responses` and routes each request with the
client's plan, to an OpenAI account, another provider, or a ChatGPT subscription through the Codex bridge.

::: warning Status
Harbor Pilot's Responses endpoint is contract-tested. The Codex configuration below follows Codex's documentation and
has not yet been run against Codex itself. See the [compatibility matrix](/reference/compatibility).
:::

## Set it up with the command line

```sh
harbor-pilot harness setup codex \
  --url https://pilot.example.com \
  --token-stdin --model coding/default --yes < ~/codex.token
```

This adds two managed blocks to `~/.codex/config.toml` (or `$CODEX_HOME/config.toml`):

```toml
# >>> harbor-pilot (managed; remove with `harbor-pilot harness uninstall codex`)
model_provider = "harbor-pilot"
model = "coding/default"
# <<< harbor-pilot

# >>> harbor-pilot (managed; remove with `harbor-pilot harness uninstall codex`)
[model_providers.harbor-pilot]
name = "Harbor Pilot"
base_url = "https://pilot.example.com/v1"
wire_api = "responses"
env_key = "HARBOR_PILOT_TOKEN"
# <<< harbor-pilot
```

Codex reads the key from an environment variable, so export it from the saved token file in the shell that runs
Codex:

```sh
export HARBOR_PILOT_TOKEN="$(cat ~/.config/harbor-pilot/tokens/codex.token)"
```

`--token-env <VAR>` makes `env_key` name your own variable instead.

If your `config.toml` already sets `model_provider`, setup leaves it alone and adds only the provider table. Select
Harbor Pilot per run with:

```sh
codex -c model_provider=harbor-pilot
```

`harbor-pilot harness uninstall codex --yes` removes both managed blocks and nothing else.

## Set it up by hand

**Clients → Set up a tool** with **Codex** selected shows the same TOML for your server and model name, and the
command that saves the token to a file without echoing it.

<Shot name="client-setup-codex" alt="Set up a tool page with the Codex in CI client and Codex selected: a button to create an access token, the command that saves it to a private file, the config.toml block with the harbor-pilot model provider, base URL and wire_api responses, and the export of HARBOR_PILOT_TOKEN" caption="Set up a tool with Codex selected" />

## In CI

In a CI job, skip the file and pass the provider on the command line, with the token in a masked secret:

```sh
export HARBOR_PILOT_TOKEN="$CI_HARBOR_PILOT_TOKEN"
codex exec \
  -c model_provider=harbor-pilot \
  -c model_providers.harbor-pilot.base_url=https://pilot.example.com/v1 \
  -c model_providers.harbor-pilot.wire_api=responses \
  -c model_providers.harbor-pilot.env_key=HARBOR_PILOT_TOKEN \
  -m coding/fast "…"
```

Give CI its own client, so its requests, limits and spend are separate from people's laptops, like the demo's
`codex-ci` client.

## Models and sessions

`model` (or `-m`) is a Harbor Pilot model name. A name the plan doesn't map goes to the plan's default model name.
According to Codex's documentation it sends a `session_id` header; with **Group requests into sessions** on, Harbor Pilot groups requests by it.

## Using a ChatGPT subscription

To *serve* requests from a ChatGPT plan, connect it as a provider account. Harbor Pilot runs the official `codex` tool
for it. See [Subscriptions and CLI bridges](/operators/subscriptions).
