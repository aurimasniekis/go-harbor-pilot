# Install the binary (systemd)

Harbor Pilot is one static binary. Each [release](https://github.com/aurimasniekis/go-harbor-pilot/releases) has
archives for Linux and macOS on amd64 and arm64, named `harbor-pilot_<version>_<os>_<arch>.tar.gz`, with a
`checksums.txt` file. Each archive also contains the example configuration and the systemd unit used below.

```sh
VERSION=0.1.1
ARCH=amd64          # or arm64
base=https://github.com/aurimasniekis/go-harbor-pilot/releases/download/v$VERSION
curl -fLO "$base/harbor-pilot_${VERSION}_linux_${ARCH}.tar.gz"
curl -fLO "$base/checksums.txt"
sha256sum --ignore-missing -c checksums.txt
tar -xzf "harbor-pilot_${VERSION}_linux_${ARCH}.tar.gz"
./harbor-pilot version
```

The commands below run from the extracted directory.

To build it yourself instead, you need Go 1.27, Node.js 24 and pnpm:

```sh
git clone https://github.com/aurimasniekis/go-harbor-pilot.git
cd go-harbor-pilot
make build          # builds the web UI and bin/harbor-pilot
```

## Install on Linux with systemd

The unit in `deploy/systemd/harbor-pilot.service` runs the server as an unprivileged `harbor-pilot` user, in a
hardened sandbox, with `/var/lib/harbor-pilot` as its data directory.

```sh
# A system user for the service
sudo useradd --system --home-dir /var/lib/harbor-pilot --shell /usr/sbin/nologin harbor-pilot

# Binary, configuration and master key
sudo install -m 0755 harbor-pilot /usr/local/bin/harbor-pilot
sudo install -d -m 0750 -g harbor-pilot /etc/harbor-pilot
sudo install -m 0640 -g harbor-pilot deploy/examples/harbor-pilot.yaml /etc/harbor-pilot/harbor-pilot.yaml
harbor-pilot keys generate | sudo install -m 0600 -o harbor-pilot /dev/stdin /etc/harbor-pilot/master.key

# Service
sudo install -m 0644 deploy/systemd/harbor-pilot.service /etc/systemd/system/harbor-pilot.service
sudo systemctl daemon-reload
sudo systemctl enable --now harbor-pilot
```

Edit `/etc/harbor-pilot/harbor-pilot.yaml` before starting it. The example looks like this:

```yaml
server:
  listen: 127.0.0.1:8080
  public_url: https://pilot.example.com   # must be https in production
  data_dir: /var/lib/harbor-pilot
  trusted_proxies: []                      # your reverse proxy, e.g. ["10.0.0.0/8"]
  shutdown_timeout: 30s
security:
  master_key_file: /etc/harbor-pilot/master.key
  previous_master_key_files: []
  secret_roots: [/etc/harbor-pilot/secrets]   # where file: secret references may point
log:
  level: info
```

Only process settings live in this file. Providers, accounts, plans and everything else are managed in the UI, the
API or [Terraform](/operators/terraform). All settings are listed in [Configuration](/reference/configuration).

::: tip Keep the master key safe
Back up `/etc/harbor-pilot/master.key` separately from your data. Backups can't be restored without it, and storing
it next to them defeats the encryption. See [Secrets and the master key](/security/secrets).
:::

## Check the installation

```sh
sudo -u harbor-pilot harbor-pilot doctor --config /etc/harbor-pilot/harbor-pilot.yaml --server http://127.0.0.1:8080
```

`doctor` checks the configuration and public URL, the data directory and database schema, the master key, the CLI
bridges and whether the server is ready. `harbor-pilot config validate --config …` checks only the file.

## Create the first administrator

```sh
sudo -u harbor-pilot harbor-pilot admin bootstrap --config /etc/harbor-pilot/harbor-pilot.yaml
```

This prints a one-time setup link (`https://pilot.example.com/setup?token=…`) that is valid for 30 minutes. Open it and
choose your email and password, then continue with [First run](/guide/first-run).

## Reverse proxy

Run Harbor Pilot behind a reverse proxy that terminates TLS:

- Set `server.public_url` to the HTTPS address.
- List the proxy in `server.trusted_proxies`, so client addresses are taken from `X-Forwarded-For`.
- Allow request bodies up to 32 MiB and replies that take several minutes.

[Behind a reverse proxy](/guide/reverse-proxy) has tested configurations for nginx, Apache, Caddy, Traefik and
HAProxy.

## Subscriptions

To route requests to Claude or ChatGPT subscriptions, install the official `claude` and `codex` tools on the same host
and make sure the `harbor-pilot` user can run them (or set `HP_CLAUDE_BINARY` and `HP_CODEX_BINARY`). `doctor` shows
which versions it found. See [Subscriptions and CLI bridges](/operators/subscriptions).

## Next steps

- [First run](/guide/first-run)
- [Upgrades and rollback](/operators/upgrades)
- [Backup and restore](/operators/backup)
