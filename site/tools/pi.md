# Pi

Pi connects to Harbor Pilot as a custom OpenAI-compatible provider. Requests go to `/v1/chat/completions` and are
routed with the client's plan.

::: warning Status
Harbor Pilot's Chat Completions endpoint is contract-tested. The Pi configuration below follows Pi's documentation and
has not yet been run against Pi itself. See the [compatibility matrix](/reference/compatibility).
:::

## Set it up with the command line

```sh
harbor-pilot harness setup pi \
  --url https://pilot.example.com \
  --token-stdin --model coding/default --yes < ~/pi.token
```

This adds a `harbor-pilot` provider to `~/.pi/agent/models.json`:

```json
{
  "providers": {
    "harbor-pilot": {
      "baseUrl": "https://pilot.example.com/v1",
      "api": "openai-completions",
      "apiKey": "HARBOR_PILOT_TOKEN",
      "models": [
        { "id": "coding/default", "name": "Harbor Pilot coding/default" }
      ]
    }
  }
}
```

Pi reads the key from the environment variable named in `apiKey`, so export it from the saved token file:

```sh
export HARBOR_PILOT_TOKEN="$(cat ~/.config/harbor-pilot/tokens/pi.token)"
```

`--token-env <VAR>` names your own variable instead. To offer more model names, add entries to `models`.
`harbor-pilot harness uninstall pi --yes` removes the `harbor-pilot` provider and nothing else.

## Set it up by hand

**Clients → Set up a tool** with **Pi** selected shows the same snippet for your server and model name.
