# Connect your first tool

Every tool needs two things: Harbor Pilot's address and an access token from a client. This page connects Claude Code;
the [Tools](/tools/) section covers Codex, OpenCode, Pi and SDKs the same way.

## 1. Get an access token

Creating a client shows its first token once. To make another one, open the client and choose **Create token** on its
**Access tokens** tab, or use the command line with an [API token](/operators/access):

```sh
export HP_SERVER=https://pilot.example.com
export HP_API_TOKEN="$(cat ~/.config/harbor-pilot/api.token)"
harbor-pilot token issue --binding <client-id> --name laptop > ~/claude.token
```

Tokens start with `hpk_`. Harbor Pilot stores only a hash, so a lost token can't be shown again: create a new one and
revoke the old one.

<Shot name="client" alt="A client page with recent requests, its access tokens and the setup steps for its tool" caption="A client's page: activity, tokens and setup together" />

## 2. Configure the tool

Open **Clients → Set up a tool**. Choose the client, the model and the tool, and the page shows the exact steps for it,
built from your server's public address.

<Shot name="client-setup" alt="Set up a tool page with the client and model chosen, Claude Code selected and the settings.json snippet with apiKeyHelper and ANTHROPIC_BASE_URL" caption="Set up a tool: copyable steps for each tool" />

You can follow those steps by hand, or let the `harbor-pilot` command do it. On the machine that runs Claude Code:

```sh
harbor-pilot harness setup claude \
  --url https://pilot.example.com \
  --token-stdin --model coding/default --yes < ~/claude.token
```

This:

- saves the token in `~/.config/harbor-pilot/tokens/claude.token` with mode 0600;
- backs up `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`) next to it;
- sets `env.ANTHROPIC_BASE_URL` and `env.ANTHROPIC_MODEL`, and an `apiKeyHelper` that prints the token file, so the
  token never appears in `settings.json`;
- records what it changed, so `harbor-pilot harness uninstall claude --yes` removes exactly that.

Run `harbor-pilot harness preview claude …` first to see the diff without writing anything.

## 3. Send a request

Start Claude Code as usual and ask it something. The request appears in **Requests** within a second or two. Open it
to see which account served it and why.

<Shot name="request" alt="A request page explaining that the request was served by a backup account after the first was rate limited" caption="Every request explains its route" />

If nothing arrives:

- `harbor-pilot harness status` shows whether the tool is configured and whether its file changed since.
- `harbor-pilot doctor --server https://pilot.example.com` checks the server, its keys and the CLI bridges.
- A `401` means the token is wrong, revoked or expired. A request that is refused by the plan shows up in Requests
  with the reason.

More in [Troubleshooting](/operators/troubleshooting).

## Next

- [Claude Code](/tools/claude-code), [Codex](/tools/codex), [OpenCode](/tools/opencode), [Pi](/tools/pi) and
  [SDKs and API clients](/tools/sdks).
- [Clients and access tokens](/operators/clients): limits, projects and rotating tokens.
