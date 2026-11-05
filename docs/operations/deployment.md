# Deployment

Harbor Pilot is one binary. It serves the gateway (`/v1/...`), the control-plane API (`/api/v1`), the UI, and `/healthz`, `/readyz` and `/metrics`. It keeps its state in two SQLite databases in the data directory:

- `harbor.db` holds configuration, the ledger, activity, the outbox and the audit log.
- `traces.db` holds encrypted, short-lived trace content.

Run exactly one instance per data directory. The server takes a lock on it at startup.

## Before you start
1. Generate a master key and store it outside the data directory. See [master-key.md](master-key.md).
2. Decide on the public URL. Use HTTPS anywhere except local development: session cookies are `Secure` and `__Host-` only under HTTPS.
3. If Harbor Pilot runs behind a reverse proxy, list the proxy in `server.trusted_proxies`. Disable response buffering for `/v1/` so streamed responses and keepalives reach clients.

## Linux with systemd
`deploy/systemd/harbor-pilot.service` runs the server as an unprivileged `harbor-pilot` user, with a hardened sandbox and `/var/lib/harbor-pilot` as the data directory. The install steps are in the unit's header. After `systemctl enable --now harbor-pilot`, create the first administrator:

```
sudo -u harbor-pilot harbor-pilot admin bootstrap --config /etc/harbor-pilot/harbor-pilot.yaml
```

This prints a one-time setup link, valid for 30 minutes.

## Containers
| Image | Dockerfile | Contents |
|---|---|---|
| `harbor-pilot` | `deploy/Dockerfile` | A static binary on distroless, running as non-root (uid 65532). It serves API-key, OpenAI-compatible and local providers. About 45 MB. |
| `harbor-pilot` with tag suffix `-bridge` | `deploy/Dockerfile.bridge` | Adds the vendor `claude` and `codex` CLIs, pinned by the `CLAUDE_CODE_VERSION` and `CODEX_VERSION` build arguments, for the subscription bridges. About 1.6 GB. |

Both images:
- read the master key from `/run/secrets/master_key`;
- listen on `:8080` and keep data in the `/data` volume;
- run `harbor-pilot healthcheck` against `/readyz` as their container health check.

The key file must have mode `0600` and be owned by uid 65532; the server refuses keys that other users can read.

With Compose:
```
harbor-pilot keys generate > deploy/master.key
chmod 600 deploy/master.key && sudo chown 65532 deploy/master.key
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml exec harbor-pilot harbor-pilot admin bootstrap
```

The Compose service runs with a read-only root filesystem, no capabilities and `no-new-privileges`, and publishes only on loopback. Put a TLS-terminating proxy in front of it, and set `HP_PUBLIC_URL` to the address users reach.

### Bridge image notes
- Each provider account that runs through a bridge gets its own CLI config directory under `/data`. The credentials are the per-account tokens stored in Harbor Pilot (a `claude setup-token` token, or a Codex login per account). No login is shared or rewritten.
- The CLIs' auto-updaters are disabled. Upgrade them by rebuilding with new version arguments, after checking the new versions against `docs/COMPATIBILITY.md`.
- `harbor-pilot doctor` inside the container reports the CLI versions it found.

## Environment variables
| Variable | Meaning |
|---|---|
| `HP_LISTEN` | Listen address (`server.listen`) |
| `HP_PUBLIC_URL` | External base URL (`server.public_url`). If unset, it is derived from the listen address, using `localhost` for wildcard addresses. |
| `HP_DATA_DIR` | Data directory |
| `HP_MASTER_KEY_FILE` | Master key file. `HP_MASTER_KEY` passes the key itself; a file is preferred. |
| `HP_LOG_LEVEL` | `debug`, `info`, `warn` or `error` |
| `HP_CLAUDE_BINARY`, `HP_CODEX_BINARY` | Paths to the bridge CLIs |

Everything else, such as providers, accounts, plans, clients, policies and notification channels, is managed through the UI, the API, the CLI or Terraform, and lives in the database.

## Monitoring
- `/healthz` reports that the process is alive. `/readyz` reports that both databases answer and startup recovery has finished.
- `/metrics` uses the Prometheus format. Without `server.metrics_token_file` it is served only to loopback clients; with the file set, clients must send `Authorization: Bearer <token>`.
- Each gateway request logs one `gateway.request.completed` line, as structured JSON with no prompt content.
