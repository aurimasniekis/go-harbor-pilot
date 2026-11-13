# Claude Code

Claude Code talks the Anthropic Messages API. Pointed at Harbor Pilot, it sends its requests to `/v1/messages` with a
Harbor Pilot access token, and the client's routing plan decides which provider and account answer.

::: info Status
The request shape Claude Code sends is contract-tested, and the configuration names were checked against the Claude
Code 2.1.318 binary. A recorded live run with a real account is still pending; see the
[compatibility matrix](/reference/compatibility).
:::

## Before you start

- A client for Claude Code, with a published routing plan. See [First run](/guide/first-run).
- An access token for that client (`hpk_…`).
- Harbor Pilot's public address, for example `https://pilot.example.com`.

## Set it up with the command line

On the machine that runs Claude Code:

```sh
harbor-pilot harness setup claude \
  --url https://pilot.example.com \
  --token-stdin --model coding/default --yes < ~/claude.token
```

This saves the token to `~/.config/harbor-pilot/tokens/claude.token` (mode 0600) and adds the following to
`~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json` when that variable is set:

```json
{
  "apiKeyHelper": "cat ~/.config/harbor-pilot/tokens/claude.token",
  "env": {
    "ANTHROPIC_BASE_URL": "https://pilot.example.com",
    "ANTHROPIC_MODEL": "coding/default"
  }
}
```

The paths in the real file are absolute. Your other settings stay as they are; if `ANTHROPIC_BASE_URL` or
`ANTHROPIC_MODEL` were already set to something else, the preview says so. Run
`harbor-pilot harness preview claude …` first to see the diff, and `harbor-pilot harness uninstall claude --yes` to
remove exactly these keys again.

`apiKeyHelper` prints the token when Claude Code needs it, so the token itself never lives in `settings.json`.

## Set it up by hand

Open **Clients → Set up a tool**, choose the client, a model name and **Claude Code**, and copy the steps. They save
the token to a file without echoing it and show the same snippet as above.

<Shot name="client-setup" alt="Set up a tool page with Claude Code selected, showing the save-token command and the settings.json snippet" caption="Clients → Set up a tool, for Claude Code" />

If you prefer an environment variable to a token file, set `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` in the
shell that starts Claude Code. `harbor-pilot harness setup claude --token-env <VAR>` writes the base URL and model and
reminds you to export `ANTHROPIC_AUTH_TOKEN` yourself.

## See your usage in Claude Code

When the server has `gateway.usage_headers: auto` (see [Configuration](/reference/configuration#gateway)), Harbor
Pilot reports usage in the response headers Claude Code uses for `/usage`, the status line and its "You've used NN%"
notice:

- **Requests served by a Claude subscription** show that subscription's 5-hour session and 7-day week, as in a
  direct sign-in. The seat is shared: everyone routed to it sees the same figures.
- **Other requests** (an API account, or a model behind an OpenAI-compatible router) show the budget closest to its
  limit that covers your client, as "usage credits" with its reset date. Without a budget, nothing is shown.

Claude Code reads these headers only when it believes it is signed in to a subscription. With `apiKeyHelper` or
`ANTHROPIC_AUTH_TOKEN` it ignores them and `/usage` shows no limits. To see them, pass the same Harbor Pilot token as
`CLAUDE_CODE_OAUTH_TOKEN` instead:

```sh
harbor-pilot harness uninstall claude --yes      # removes apiKeyHelper, which would take precedence
export ANTHROPIC_BASE_URL=https://pilot.example.com
export ANTHROPIC_MODEL=coding/default
export CLAUDE_CODE_OAUTH_TOKEN="$(cat ~/.config/harbor-pilot/tokens/claude.token)"
claude
```

Keep `ANTHROPIC_AUTH_TOKEN` unset, since it takes precedence too. Claude Code then sends the token as
`Authorization: Bearer`, which Harbor Pilot accepts like any other gateway token. It also adds an OAuth flag to
`anthropic-beta`, which Harbor Pilot drops before forwarding the request. Figures are as fresh as Harbor Pilot's
quota board: a subscription's windows usually lag one request behind. Claude Code's own browser sign-in to a gateway
(a device code flow) is not supported.

## Models

`ANTHROPIC_MODEL` selects the main model name. Claude Code also asks for other models for small background tasks.
Those requests go through the same plan: a mapping such as `claude-*-haiku-*` → `coding/fast` sends them somewhere
cheap, and anything unmapped goes to the plan's default model name. You can also set Claude Code's own model
variables (`ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` and others, as described in Claude Code's
documentation) to Harbor Pilot model names.

**Routing → Test a request** with a model such as `claude-sonnet-4-5` shows where each name goes before you change
anything.

## Sessions

Claude Code sends an `X-Claude-Code-Session-Id` header. When the client's **Group requests into sessions** setting is
on (the default), Harbor Pilot uses it to group requests into conversations under **Requests → By session**. The header
is a hint from the tool: it is used for grouping only, never for access or billing scope.

<Shot name="requests-sessions" alt="Requests grouped by session, three Claude Code sessions with request counts, tokens, cost and grouped by reported by the tool" caption="Requests → By session" />

## Extra headers

To tag requests, Claude Code can send extra headers from `ANTHROPIC_CUSTOM_HEADERS` (one `Name: value` per line,
according to Claude Code's documentation). Harbor Pilot reads:

| Header | What it does |
|---|---|
| `X-HP-Project` | Selects one of the client's allowed projects, if the client allows project hints. Otherwise recorded only. |
| `X-HP-Session` | A session id, used like the native session header. |
| `X-HP-External-Ref` | A free-form reference (a ticket, a branch) recorded on the request. |
| `X-HP-Intent` | A hint about the kind of work, recorded on the request. |

All of these are untrusted hints, recorded as claims on the request.

## Using a Claude subscription

This page is about Claude Code as a *client* of Harbor Pilot. To have Harbor Pilot *serve* requests from a Claude Pro
or Max subscription, connect the subscription as a provider account; Harbor Pilot then runs the official `claude` tool
for it. See [Subscriptions and CLI bridges](/operators/subscriptions).

## Troubleshooting

- `harbor-pilot harness status` shows whether the setup is still in place, or was edited since.
- A `401` means the token is wrong, revoked or expired.
- A refused request appears in **Requests** with the reason, for example a model the client isn't allowed to use.
- If responses arrive all at once instead of streaming, the reverse proxy is buffering `/v1/`.
