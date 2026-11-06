# Command line

`harbor-pilot` is the server and its operations tool in one binary. Run it with no arguments, or `harbor-pilot help`,
for a summary.

**Secrets are never accepted as arguments.** Arguments end up in shell history and process listings, so every
command that needs a token or key reads it from a file, from standard input or from the environment.

## Common flags

`serve`, `admin`, `keys rotate`, `config validate`, `doctor`, `backup`, `restore` and `migrate` load the same
configuration and accept the same overrides:

| Flag | Meaning |
|---|---|
| `--config FILE` | Configuration file (default `$HP_CONFIG`). |
| `--listen ADDR` | Listen address. |
| `--data-dir DIR` | Data directory. |
| `--public-url URL` | Public base URL. |
| `--demo` | Use demo mode and its databases. |
| `--demo-clock RFC3339` | Freeze the demo clock. |
| `--demo-allow-public` | Allow the demo on a non-loopback address. |

See [Configuration](/reference/configuration) for what each setting does.

## Running the server

### `serve`

```sh
harbor-pilot serve --config /etc/harbor-pilot/harbor-pilot.yaml
```

Runs the gateway, the control-plane API and the web UI on one address. It applies pending database migrations at
start (taking a backup first), stops gracefully on `SIGINT` or `SIGTERM`, and holds a lock on the data directory so a
second server cannot share it.

```sh
harbor-pilot serve --demo
```

Serves the synthetic demo on `127.0.0.1:8080`, with its own databases and a throwaway master key in the data
directory. See [Try the demo](/guide/demo).

### `healthcheck`

```sh
harbor-pilot healthcheck [--url http://127.0.0.1:8080/readyz]
```

Exits with status 0 when the readiness URL answers `200`, otherwise 1. Meant for container health checks.

### `version`

Prints the version (`harbor-pilot version`, `--version` or `-v`).

## Setting up

### `keys generate`

```sh
harbor-pilot keys generate > /etc/harbor-pilot/master.key
chmod 600 /etc/harbor-pilot/master.key
```

Prints a new random 32-byte master key, base64-encoded.

### `admin bootstrap`

```sh
harbor-pilot admin bootstrap --config /etc/harbor-pilot/harbor-pilot.yaml
```

Prints a one-time link, valid for 30 minutes, that creates the first administrator. Refused once an administrator
exists. See [First run](/guide/first-run).

### `config validate`

```sh
harbor-pilot config validate --config /etc/harbor-pilot/harbor-pilot.yaml
```

Loads the file, environment and flags, and prints `configuration is valid` or every problem found.

### `doctor`

```sh
harbor-pilot doctor --config /etc/harbor-pilot/harbor-pilot.yaml [--server https://harbor-pilot.example.com]
```

Checks, without starting the server:

- the configuration, and whether the public URL is plain HTTP;
- that the data directory is writable;
- that the master key loads, and that stored secrets were not sealed with a key that is not configured;
- the database and its schema version;
- whether the `claude` and `codex` CLIs are installed, and their versions;
- tool configurations written by `harbor-pilot harness`, and whether they were edited since;
- with `--server`, that a running server is ready.

Each line is marked `✓`, `!` (warning) or `✗`. The command exits non-zero when any check fails.

## Connecting tools

### `harness preview | setup | uninstall`

```sh
harbor-pilot harness preview claude --url https://harbor-pilot.example.com --token-file ~/.config/harbor-pilot/tokens/claude.token
harbor-pilot harness setup   claude --url https://harbor-pilot.example.com --token-file ~/.config/harbor-pilot/tokens/claude.token --yes
harbor-pilot harness uninstall claude --yes
```

Writes, or removes, the Harbor Pilot settings in a coding tool's own configuration. `KIND` is `claude`, `codex`,
`opencode` or `pi`. Nothing is written without `--yes`: without it the command prints the file it would change and a
diff. Setup keeps a backup of the previous file and touches only the keys it manages.

| Flag | Meaning |
|---|---|
| `--url URL` | Harbor Pilot's public URL (default `$HP_PUBLIC_URL`). |
| `--token-file FILE` | File holding the gateway token. Where the tool supports it, the configuration references the file rather than copying the token. |
| `--token-stdin` | Read the token from standard input and store it in `~/.config/harbor-pilot/tokens/<kind>.token` (mode `0600`). The file is written only by `setup --yes`. |
| `--token-env VAR` | Have the tool read the token from this environment variable. |
| `--model M` | Model or alias to select, for example `coding/default`. |
| `--config-dir DIR` | The tool's configuration directory, if not its default. |
| `--yes` | Apply the change. |
| `--restore-backup` | `uninstall` only: restore the file saved before setup instead of removing the managed keys. |

Each tool's page under [Tools](/tools/) shows the exact command and what it changes.

### `harness status`

Lists each tool's configuration file and whether Harbor Pilot configured it, and when, or whether it was edited since.

## Talking to a running server

`token issue` and `policy simulate` call the control-plane API. They need an API token (`hpa_…`) for a service
principal, and accept:

| Flag | Meaning |
|---|---|
| `--server URL` | Harbor Pilot URL (default `$HP_SERVER`, else `http://127.0.0.1:8080`). |
| `--api-token-file FILE` | File holding the API token. |
| `--api-token-stdin` | Read the API token from standard input. |

With neither flag, the token is read from `HP_API_TOKEN`.

### `token issue`

```sh
harbor-pilot token issue --api-token-file ~/.config/harbor-pilot/api.token \
  --binding cbn_01J… --name laptop --ttl 720h
```

Issues a gateway token for a client. The token is printed once, on standard output, so it can be piped straight into
`harness setup --token-stdin`.

| Flag | Meaning |
|---|---|
| `--binding ID` | The client to issue it for. Required. |
| `--name NAME` | A name for the token (default `cli`). |
| `--scopes a,b` | Comma-separated scopes (default: `gateway:invoke` and `gateway:models`). |
| `--ttl DURATION` | Expiry, for example `720h`. `0`, the default, means no expiry. |

Scopes are `gateway:invoke` (call models), `gateway:models` (list models), `usage:read` (read the token's own usage)
and `tokens:mint` (mint short-lived child tokens; see [Agent orchestrators](/tools/orchestrators)).

### `policy simulate`

```sh
harbor-pilot policy simulate --api-token-file ~/.config/harbor-pilot/api.token \
  --plan pln_01J… --model claude-sonnet-4-5 --harness claude_code
```

Runs a routing decision against the plan's published version without sending anything, and prints it as JSON: the
resolved alias and pool, the ranked candidates, the excluded accounts and why, and the rules that matched.

| Flag | Meaning |
|---|---|
| `--plan ID` | Routing plan. Required. |
| `--model M` | Requested model or alias. Required. |
| `--protocol P` | Inbound protocol (default `anthropic_messages`). |
| `--harness H` | Tool to simulate, for example `claude_code` or `codex`. |
| `--stream` | Simulate a streaming request (default `true`; `--stream=false` for not). |

## Backup and recovery

### `backup`

```sh
harbor-pilot backup --config /etc/harbor-pilot/harbor-pilot.yaml [--out DIR] [--include-traces]
```

Takes an online, consistent backup while the server runs, with a manifest of checksums and the master key ids it
needs. The default destination is `<data_dir>/backups/<time>`. Captured content is left out unless you pass
`--include-traces` (it stays encrypted). The master key is never included.

### `backup verify`

```sh
harbor-pilot backup verify DIR
```

Checks every file against the manifest and prints when and by which version it was made, the schema versions and
the master key ids it needs.

### `restore`

```sh
harbor-pilot restore --from DIR --config /etc/harbor-pilot/harbor-pilot.yaml --yes
```

Verifies the backup and replaces the databases. Without `--yes` it only verifies. The server must be stopped; the
current files are moved aside, not deleted, and their new names are printed. See
[Backup and restore](/operators/backup).

### `keys rotate`

```sh
harbor-pilot keys rotate --config /etc/harbor-pilot/harbor-pilot.yaml
```

Re-wraps every stored secret and captured-content key under the current master key. Needs the old key listed in
`security.previous_master_key_files`. It reports whether anything still depends on a previous key. See
[Rotating the master key](/security/secrets#rotating-the-master-key).

### `admin recover`

```sh
harbor-pilot admin recover --email admin@example.com --config /etc/harbor-pilot/harbor-pilot.yaml
```

Prints a single-use break-glass sign-in link for an existing user, valid for 30 minutes. Its use is audited and
raises a security alert that stays until an administrator acknowledges it.

### `migrate quota-harbor`

```sh
harbor-pilot migrate quota-harbor --qh-config /etc/quota-harbor/config.yaml [--qh-db FILE] [--since 2160h] [--apply] [--json]
```

Imports monitored accounts, the Telegram alert channel and observation history from Quota Harbor. It is a dry run
unless you pass `--apply`. Stop the server first.

| Flag | Meaning |
|---|---|
| `--qh-config FILE` | Quota Harbor configuration file (includes are followed). Required. |
| `--qh-db FILE` | Quota Harbor database, to import observation history. |
| `--since DURATION` | Import history newer than this (default `2160h`, 90 days). |
| `--apply` | Write the import. Without it, only the plan is printed. |
| `--json` | Print the plan as JSON. |

See [Migrating from Quota Harbor](/operators/migrate-quota-harbor).

## Other

### `openapi`

Prints the control-plane OpenAPI document as YAML. The same document is committed as
[api/openapi.yaml](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/api/openapi.yaml).

### `hp-loadgen`

A separate load generator for capacity testing, built from `cmd/hp-loadgen`. It sends concurrent requests to a
gateway and prints latency percentiles.

```sh
HP_GATEWAY_TOKEN="$(cat ~/.config/harbor-pilot/tokens/load.token)" \
  hp-loadgen --url http://127.0.0.1:8080 --model coding/default --concurrency 32 --requests 500
```

Other flags: `--token-file`, `--token-env` (default `HP_GATEWAY_TOKEN`), `--stream` (default `true`), `--read-delay`
(pause after each received line, to simulate slow clients), `--max-tokens` (default 64) and `--json`.
