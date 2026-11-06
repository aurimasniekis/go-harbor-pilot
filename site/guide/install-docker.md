# Install with Docker

Harbor Pilot ships as two container images, published with each release:

| Image | Use it when | Contents |
|---|---|---|
| `docker.io/aurimasniekis/harbor-pilot` | You use API keys, OpenAI-compatible services or local models | A static binary on a distroless base, running as a non-root user (uid 65532). About 45 MB. |
| `docker.io/aurimasniekis/harbor-pilot-bridge` | You also want to use Claude or ChatGPT subscriptions | The same, plus the official `claude` and `codex` command-line tools at pinned versions. About 1.6 GB. |

Both images listen on port 8080, keep their data in `/data`, read the master key from `/run/secrets/master_key`, and
run `harbor-pilot healthcheck` against `/readyz` as their container health check.

Each release is tagged three ways, for linux/amd64 and linux/arm64: the exact version (`0.1.0`), the minor version
(`0.1`, which moves to each patch release) and `latest`. The examples pin `0.1`.

## 1. Create a master key

Harbor Pilot encrypts provider credentials and other secrets with a master key that never goes into the database.
Generate one and keep a copy somewhere safe, apart from your backups:

```sh
docker run --rm docker.io/aurimasniekis/harbor-pilot:0.1 keys generate > master.key
chmod 600 master.key
sudo chown 65532 master.key   # readable only by the container's user
```

The server refuses to start when the key file is readable by anyone but its owner. See
[Secrets and the master key](/security/secrets).

## 2. Start it with Compose

The repository has a Compose file at `deploy/docker-compose.yml`. A minimal version:

```yaml
services:
  harbor-pilot:
    image: docker.io/aurimasniekis/harbor-pilot:0.1
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      HP_PUBLIC_URL: https://pilot.example.com
      HP_LOG_LEVEL: info
    secrets:
      - master_key
    volumes:
      - harbor-data:/data
    stop_grace_period: 40s
    read_only: true
    tmpfs:
      - /tmp
    security_opt:
      - no-new-privileges:true
    cap_drop: [ALL]

secrets:
  master_key:
    file: ./master.key

volumes:
  harbor-data:
```

```sh
docker compose up -d
```

The container publishes only on the loopback address. Put a TLS-terminating reverse proxy in front of it and set
`HP_PUBLIC_URL` to the address people and tools will use: sign-in cookies are only `Secure` under HTTPS, and setup
links and tool snippets are built from this URL.

::: warning Reverse proxies and streaming
Coding tools stream long replies and send large requests, which some proxy defaults cut off (nginx refuses bodies over
1 MB and gives up on a reply after 60 seconds). [Behind a reverse proxy](/guide/reverse-proxy) has tested
configurations for nginx, Apache, Caddy, Traefik and HAProxy. The proxy reaches the container from the Docker
network, so list that network in `server.trusted_proxies`.
:::

## 3. Create the first administrator

```sh
docker compose exec harbor-pilot harbor-pilot admin bootstrap
```

This prints a one-time setup link, valid for 30 minutes. Open it, choose your email and password, and continue with
[First run](/guide/first-run).

## Subscriptions: the bridge image

To route requests to a Claude or ChatGPT subscription, use `harbor-pilot-bridge` instead. It contains the official
`claude` and `codex` tools, which Harbor Pilot runs with each account's own sign-in:

- Each account gets its own configuration directory under `/data`. No sign-in is shared between accounts or rewritten.
- The tools' auto-updaters are off. To upgrade them, move to a newer image.
- Their own tools (shell, file edits, web search) are turned off, and a turn that tries to use one is stopped.

See [Subscriptions and CLI bridges](/operators/subscriptions).

## Environment variables

Only process settings live in the environment or the configuration file. Everything else (providers, accounts, plans,
clients, policies, alerts) is managed in the UI, the API or [Terraform](/operators/terraform) and stored in the
database.

| Variable | Meaning |
|---|---|
| `HP_LISTEN` | Listen address. The images use `:8080`. |
| `HP_PUBLIC_URL` | The external address. If unset, it is derived from the listen address. |
| `HP_DATA_DIR` | Data directory. The images use `/data`. |
| `HP_MASTER_KEY_FILE` | Master key file. The images use `/run/secrets/master_key`. |
| `HP_LOG_LEVEL` | `debug`, `info`, `warn` or `error`. |
| `HP_SECRET_ENV_PREFIXES` | Environment variable prefixes that `env:` secret references may read (default `HP_SECRET_`). |

Provider keys can stay in the environment instead of the database: start the container with
`HP_SECRET_OPENAI_API_KEY=…` and set the account's credential reference to `env:HP_SECRET_OPENAI_API_KEY`. Only
variables with an allowed prefix can be referenced. The full list of settings is in
[Configuration](/reference/configuration).

## Next steps

- [First run](/guide/first-run): sign in and connect a provider.
- [Backup and restore](/operators/backup): set up backups before you rely on it.
