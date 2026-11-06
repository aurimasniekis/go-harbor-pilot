# OpenCode

OpenCode connects to Harbor Pilot as an OpenAI-compatible provider. Requests go to `/v1/chat/completions` and are
routed with the client's plan.

::: warning Status
Harbor Pilot's Chat Completions endpoint is contract-tested. The OpenCode configuration below follows OpenCode's
documentation and has not yet been run against OpenCode itself. See the
[compatibility matrix](/reference/compatibility).
:::

## Set it up with the command line

```sh
harbor-pilot harness setup opencode \
  --url https://pilot.example.com \
  --token-stdin --model coding/default --yes < ~/opencode.token
```

This adds a `harbor-pilot` provider to `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "harbor-pilot": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Harbor Pilot",
      "options": {
        "baseURL": "https://pilot.example.com/v1",
        "apiKey": "{file:~/.config/harbor-pilot/tokens/opencode.token}"
      },
      "models": {
        "coding/default": { "name": "coding/default" }
      }
    }
  }
}
```

The paths in the real file are absolute. `{file:…}` makes OpenCode read the token from the file, so the token never
lives in `opencode.json`. With `--token-env <VAR>`, the key becomes `{env:VAR}` instead.

Then select the model as `harbor-pilot/coding/default` in OpenCode.

To offer more model names, add them under `models`, for example `"coding/fast": { "name": "coding/fast" }`.
`harbor-pilot harness uninstall opencode --yes` removes the `harbor-pilot` provider and leaves the rest of the file
alone.

## Set it up by hand

**Clients → Set up a tool** with **OpenCode** selected shows the same snippet for your server and model name.

## Sessions

OpenCode identifies itself in its `User-Agent`, which Harbor Pilot records as the tool on each request. Harbor Pilot
groups requests into sessions only from a session header it recognises (`X-HP-Session` among them); OpenCode's
provider `headers` option can send one.
