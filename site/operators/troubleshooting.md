# Troubleshooting

## Start with `doctor`

```sh
harbor-pilot doctor --config /etc/harbor-pilot/config.yaml --server https://pilot.example.com
```

It checks, and prints `ok`, `warn` or `fail` for each:

| Check | What it looks at |
|---|---|
| configuration | The configuration file parses and validates; the public URL. |
| public URL | Warns when it is plain http: session cookies aren't `Secure`. |
| data directory | Exists and is writable. |
| master key | Configured, loads, and opens the secrets already stored (fails if they were sealed with a key that isn't configured). |
| database | Exists and its schema version. |
| CLI bridges | The `claude` and `codex` binaries are found and report a version. |
| harness … | Tools configured on this machine with `harbor-pilot harness setup`. |
| server | With `--server`, the running server is ready. |

With Docker: `docker exec harbor-pilot harbor-pilot doctor --server http://127.0.0.1:8080`.

**Settings → System & backups** shows the same from inside the app: the version, public URL, data directory, master
key fingerprint, whether session cookies are secure, and whether each CLI bridge is available.

## Find the request

For anything about a particular request, open it in **Requests**. Its page says in one sentence where it went and why,
lists every attempt with its error, and every candidate that was skipped with the reason. Every API error also carries
a `request_id`; search for it in **Requests**, the [audit log](/operators/audit) or the server log.

## Common problems

### The server won't start

| Message | Cause | Fix |
|---|---|---|
| `master key file … must not be accessible by group or others` | The key file mode is wider than `0600`. | `chmod 600` the key file and make the service user its owner. |
| `data directory … is in use by another harbor-pilot process` | Another `serve`, `restore` or `migrate` holds the lock. | Stop the other process. Only one instance may use a data directory. |
| `database has migration N unknown to this binary` | An older binary against an upgraded database. | Run the newer binary, or roll back by restoring. See [Upgrades](/operators/upgrades#roll-back). |
| `setup.required` in the log | No administrator yet. | `harbor-pilot admin bootstrap` prints a setup link. |

### Restore refuses

`the configured master key cannot open this backup`: the key was rotated after the backup. Add the old key to
`security.previous_master_key_files` and run the restore again. See [Backup and restore](/operators/backup#restore).

### Sign-in loops, or changes fail with a 403

| Error | Cause | Fix |
|---|---|---|
| *this page is open at …, but the server only accepts changes from its public URL* (`origin_mismatch`) | The browser address doesn't match `public_url`. | Open Harbor Pilot at its public URL, or set `HP_PUBLIC_URL` / `server.public_url` to the address people use. When the public URL is a loopback address, `localhost`, `127.0.0.1` and `[::1]` on the same port count as the same. |
| *your session changed since this page loaded* (`csrf_invalid`) | You signed in again in another tab. | Reload the page. |
| Cookies not kept behind a TLS proxy | The proxy terminates HTTPS but Harbor Pilot sees http. | Set `public_url` to the `https://` address and list the proxy in `server.trusted_proxies`. |
| *this action needs a sign-in within the last 10 minutes* (`reauth_required`) | Reading unredacted captures or downloading needs a recent sign-in. | Sign in again. See [Step-up sign-in](/operators/access#step-up-sign-in). |
| *permission … required* | Your role doesn't include it. | Ask an admin. See [Roles](/operators/access#roles). |

### A tool can't connect

| Symptom | Cause | Fix |
|---|---|---|
| `401` from the gateway | Wrong token, or it was revoked or expired. | Issue a new token on the client's page (or `harbor-pilot token issue --binding …`), then run `harbor-pilot harness setup` again. See [Clients](/operators/clients). |
| Streaming tools time out, or get the whole reply at once | A reverse proxy buffers responses. | Turn off proxy buffering for the gateway paths (`/v1/`, `/anthropic/`, `/openai/`, `/v1beta/`). |

### Requests fail

| Error | Cause | Fix |
|---|---|---|
| *no eligible account is available for model …* | Every account for that model is excluded: plan restrictions, cooldown after errors, used-up quota, a budget, or a paused protocol path. | Open the request in **Requests**. The **Candidates** list shows each skipped account and why. |
| *no candidate could serve the request* | Every candidate was tried and failed. | The **Attempts** list shows each error. |
| Blocked by the privacy policy | A detector or rule with action **Block** matched, or masking couldn't run and the policy blocks. | Check the request's checks; try the text on **Privacy → Try a policy on sample text**. See [Privacy](/operators/privacy). |
| *environment variable is not allowed as a secret reference* | An `env:` reference without the allowed prefix. | Rename the variable to `HP_SECRET_…`. See [Upgrades](/operators/upgrades#secret-references-are-confined-to-a-prefix). |
| Subscription (bridge) requests fail at once | The CLI is missing or the wrong version, or the account's token is invalid. | `harbor-pilot doctor` shows the CLI versions. For Claude, create a new token with `claude setup-token` and update the account. See [Subscriptions](/operators/subscriptions). |

### Limits look wrong

| Symptom | Fix |
|---|---|
| Quota tracking shows *cannot sign in* (`auth_failed`) | The tracking credential was rejected. Update it on the tracker in **Limits**. A `quota.monitor.auth_failed` event is recorded, and the **Quota used up** alert preset includes it. |
| A subscription shows as untracked | No quota tracker is attached. See [Limits](/operators/limits). |

### Alerts or events don't arrive

- Open **Alerts → History** for alerts, or the relay in **Alerts → Event relays** for events: each message or delivery
  shows its status, attempts and the last error.
- **Failed** messages and deliveries can be sent again. See [Alerts](/operators/alerts#history) and
  [Events](/operators/events#delivery-history-and-replay).
- A receiver that rejects signatures: check the secret and that it verifies the raw body (see
  [Verify a webhook signature](/operators/events#verify-a-webhook-signature)).
- `hp_outbox_pending` growing means a destination keeps failing.

Delivery failures never block gateway traffic.

### A banner says a protocol path is paused

A circuit breaker opened after repeated protocol failures on one provider and protocol path. Open **Settings →
Diagnostics** to see the breaker, what each plan does meanwhile, and the protocol change that was observed. See
[Diagnostics](/operators/diagnostics).

## Logs

Logs are JSON on stderr: `journalctl -u harbor-pilot` or `docker logs harbor-pilot`. Set `HP_LOG_LEVEL=debug`
temporarily for more detail; debug logs still exclude secrets and prompt content. See
[Monitoring](/operators/monitoring#logs).
