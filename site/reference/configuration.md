# Configuration

Harbor Pilot reads a small bootstrap file: only what the process needs before it can open its database (where to
listen, its public address, the data directory, the master key, TLS, logging and the CLI bridges). Everything else
(providers, accounts, routing plans, clients, policies, alert channels) lives in the database and is changed through
the UI, the API, the CLI or Terraform. There is no second file-based source of truth.

## Where settings come from

From lowest to highest precedence:

1. built-in defaults;
2. the configuration file, given with `--config FILE` or `HP_CONFIG`;
3. `HP_*` environment variables;
4. command-line flags.

The file is optional: with none, defaults, environment variables and flags are used. Unknown keys in the file are an
error, so a typo cannot be silently ignored. Check a file before you deploy it:

```sh
harbor-pilot config validate --config /etc/harbor-pilot/harbor-pilot.yaml
```

## Example

```yaml
server:
  listen: 127.0.0.1:8080
  public_url: https://harbor-pilot.example.com   # https outside local development
  data_dir: /var/lib/harbor-pilot
  trusted_proxies: []                           # e.g. ["10.0.0.0/8"] behind a reverse proxy
  shutdown_timeout: 30s
  # metrics_token_file: /etc/harbor-pilot/metrics.token
  # tls:
  #   cert_file: /etc/harbor-pilot/tls.crt
  #   key_file: /etc/harbor-pilot/tls.key
security:
  master_key_file: /etc/harbor-pilot/master.key   # harbor-pilot keys generate
  previous_master_key_files: []
  secret_roots: [/etc/harbor-pilot/secrets]
  # secret_env_prefixes: [HP_SECRET_]
log:
  level: info
# bridges:
#   claude_code:
#     binary: /usr/local/bin/claude
#   codex:
#     disabled: true
```

The same file is in the repository as
[deploy/examples/harbor-pilot.yaml](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/deploy/examples/harbor-pilot.yaml).

## `server`

| Key | Default | Meaning |
|---|---|---|
| `listen` | `127.0.0.1:8080` | Address and port to listen on (`host:port`). |
| `public_url` | `http://<listen>` | The address people and tools use, scheme and host only (no path). It sets the cookie origin, the links the CLI prints and the URLs shown in setup steps. `https://` turns on `Secure`, `__Host-` cookies. When unset it is derived from `listen` (`localhost` for a wildcard address, `https` when TLS is configured). |
| `data_dir` | `./.data` | Holds the databases, CLI bridge state and default backups. One server per data directory. |
| `trusted_proxies` | none | CIDRs of reverse proxies whose `X-Forwarded-For` is believed for the client address. With any set, `/metrics` always needs a token. |
| `shutdown_timeout` | `30s` | How long a graceful shutdown waits for in-flight requests. Must be positive. |
| `metrics_token_file` | none | File holding a bearer token for `/metrics`. Without one, `/metrics` answers only direct loopback clients. |
| `tls.cert_file`, `tls.key_file` | none | Terminate TLS in the process. Set both or neither. |

## `security`

| Key | Default | Meaning |
|---|---|---|
| `master_key_file` | none | File holding the 32-byte master key, base64 or hex. Must not be readable by group or others. Required outside demo mode, unless `HP_MASTER_KEY` is set. |
| `previous_master_key_files` | none | Older master keys, kept only so values sealed with them can still be opened while `harbor-pilot keys rotate` runs. |
| `secret_roots` | none | Directories that `file:` secret references may read. A root may not contain the data directory or a master key file. |
| `secret_env_prefixes` | `HP_SECRET_` | Prefixes an environment variable needs before an `env:` secret reference may read it. Each must be at least three characters, and no `HP_` prefix other than `HP_SECRET_` is allowed. |

See [Secrets and the master key](/security/secrets) for how these fit together.

## `log`

| Key | Default | Meaning |
|---|---|---|
| `level` | `info` | `debug`, `info`, `warn` (or `warning`) or `error`. Logs are structured JSON on standard error. |

## `bridges`

The CLI bridges serve subscription accounts through the vendors' own command-line tools. Each bridge is on when its
binary is found.

| Key | Claude Code default | Codex default | Meaning |
|---|---|---|---|
| `binary` | `claude` on `PATH` | `codex` on `PATH` | Path of the vendor CLI. |
| `disabled` | `false` | `false` | Turn the bridge off even when the binary is present. |
| `max_processes` | `16` | `8` | Concurrent CLI processes in total. |
| `max_per_account` | `4` | `1` | Concurrent CLI processes per account. Codex refreshes its sign-in in place, so keep it at 1. |
| `idle_timeout` | `10m` | not used | How long a Claude Code process waits for the next turn of its conversation. |
| `tool_timeout` | `30m` | `30m` | How long a process waits for your tool to return a tool result. |

Use `bridges.claude_code` and `bridges.codex` as the section names. Bridge state lives under
`<data_dir>/bridges/`. See [Subscriptions and CLI bridges](/operators/subscriptions).

## `demo`

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Serve the synthetic demo (same as `--demo`). |
| `clock` | none | Freeze time at an RFC 3339 instant for repeatable output. Needs demo mode. |
| `allow_public` | `false` | Allow the demo on a non-loopback address. The demo administrator's password is public, so leave this off. |

## Environment variables

| Variable | Sets |
|---|---|
| `HP_CONFIG` | Path of the configuration file (`--config`). |
| `HP_LISTEN` | `server.listen` |
| `HP_PUBLIC_URL` | `server.public_url`. Also the default `--url` of `harbor-pilot harness`. |
| `HP_DATA_DIR` | `server.data_dir` |
| `HP_MASTER_KEY_FILE` | `security.master_key_file` |
| `HP_MASTER_KEY` | The master key itself, used when no key file is configured. |
| `HP_SECRET_ENV_PREFIXES` | `security.secret_env_prefixes`, comma-separated. |
| `HP_LOG_LEVEL` | `log.level` |
| `HP_CLAUDE_BINARY` | `bridges.claude_code.binary` |
| `HP_CODEX_BINARY` | `bridges.codex.binary` |
| `HP_SERVER` | Server URL for the API commands (`token issue`, `policy simulate`); default `http://127.0.0.1:8080`. |
| `HP_API_TOKEN` | API token for the API commands, when neither `--api-token-file` nor `--api-token-stdin` is given. |
| `HP_SECRET_*` | Not settings: variables with this prefix are what `env:` secret references may read. |

`HP_MASTER_KEY` and `HP_MASTER_KEY_FILE` can never be read through a secret reference, whatever the prefixes say.

## Command-line flags

`serve` and the offline commands (`admin`, `keys rotate`, `config validate`, `doctor`, `backup`, `restore`,
`migrate`) accept the same overrides:

| Flag | Overrides |
|---|---|
| `--config FILE` | Configuration file |
| `--listen ADDR` | `server.listen` |
| `--data-dir DIR` | `server.data_dir` |
| `--public-url URL` | `server.public_url` |
| `--demo` | `demo.enabled` |
| `--demo-clock RFC3339` | `demo.clock` |
| `--demo-allow-public` | `demo.allow_public` |

Every command is listed in the [command reference](/reference/cli).

## What is in the data directory

| Path | Contents |
|---|---|
| `harbor.db` | Configuration, requests, usage, audit log and encrypted secrets. |
| `traces.db` | Captured content, encrypted, short-lived. Kept apart so it can expire and be excluded from backups. |
| `bridges/` | CLI bridge working directories and per-account logins. |
| `backups/` | Default destination of `harbor-pilot backup`. |
| `demo.db`, `demo-traces.db`, `demo-master.key` | Demo mode only; the demo never touches `harbor.db`. |

The master key should never be inside the data directory.

## Startup checks

The server refuses to start when:

- `listen` is not `host:port`, or `public_url` is not an absolute `http(s)` URL without a path;
- only one of `tls.cert_file` and `tls.key_file` is set;
- no master key is configured outside demo mode, or the key file can be read by its group or others;
- a secret root contains the data directory or a master key file, or a secret prefix is too broad;
- demo mode would listen on a non-loopback address without `allow_public`;
- the log level is unknown.

`harbor-pilot doctor` runs the same checks without starting the server, and also reports whether stored secrets
were sealed with a master key that is not configured, whether the data directory is writable and which CLI
bridges are installed.
