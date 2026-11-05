# Connecting coding harnesses

Every harness needs only a base URL and a gateway token. Issue a token for a client binding in the UI (Clients → Credentials) or with `harbor-pilot token issue --binding <id>`; it is shown once.

`harbor-pilot harness` edits harness configuration reversibly:

- `preview` prints the diff and writes nothing.
- `setup --yes` backs up the original file (`<file>.hp-backup-<time>`), writes it atomically, and records the keys it owns in `<file>.hp-managed.json`. It refuses to write when the file changed after the preview.
- `uninstall --yes` removes only those keys. Add `--restore-backup` to put the original file back instead.
- `status` shows which harnesses are configured and whether they were edited since.

Tokens never need to be pasted into config files. `--token-stdin` stores the token in `~/.config/harbor-pilot/tokens/<harness>.token` (mode 0600), and the harness references that file where it can, or an environment variable where it cannot.

| Harness | File edited | What is set | Token |
|---|---|---|---|
| Claude Code | `$CLAUDE_CONFIG_DIR/settings.json` (default `~/.claude`) | `env.ANTHROPIC_BASE_URL`, optional `env.ANTHROPIC_MODEL` | `apiKeyHelper` prints the token file, or export `ANTHROPIC_AUTH_TOKEN` |
| Codex | `$CODEX_HOME/config.toml` (default `~/.codex`) | managed `[model_providers.harbor-pilot]` block (`wire_api = "responses"`) and `model_provider` | `env_key` (default `HARBOR_PILOT_TOKEN`) |
| OpenCode | `~/.config/opencode/opencode.json` | `provider.harbor-pilot` (OpenAI-compatible, `/v1`) | `{file:…}` or `{env:…}` substitution |
| Pi | `~/.pi/agent/models.json` | `providers.harbor-pilot` (`openai-completions`, `/v1`) | environment variable name |

Examples:

```sh
# Claude Code, token read from stdin into a 0600 file
pbpaste | harbor-pilot harness setup claude --url https://hp.example.com --token-stdin --model coding/default --yes

# Codex, token from an environment variable
harbor-pilot harness setup codex --url https://hp.example.com --token-env HARBOR_PILOT_TOKEN --yes
export HARBOR_PILOT_TOKEN="$(cat ~/.config/harbor-pilot/tokens/codex.token)"

# Undo
harbor-pilot harness uninstall codex --yes
```

Facts about each harness's configuration are recorded in `docs/contracts/harness-wire-facts.md`. Only Claude Code (2.1.318) has been checked against the installed binary; Codex, OpenCode and Pi follow vendor documentation and remain unverified until a live run.

Generic API clients need no tooling: OpenAI SDKs use `OPENAI_BASE_URL=<url>/v1` and `OPENAI_API_KEY=<token>`; Anthropic SDKs use `ANTHROPIC_BASE_URL=<url>` and `ANTHROPIC_AUTH_TOKEN=<token>`; Gemini clients use `<url>` with the token in `x-goog-api-key`.

Run `harbor-pilot doctor` to check configuration, master key, storage, CLI bridges and configured harnesses.
