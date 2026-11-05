# Troubleshooting

Start with `harbor-pilot doctor --config FILE`, adding `--server URL` when the server is running. It checks:
- the configuration and public URL;
- the data directory and database schema;
- the master key and that it opens the stored secrets;
- the CLI bridge binaries;
- server readiness.

| Symptom | Likely cause | What to do |
|---|---|---|
| Server exits: "master key file … must not be accessible by group or others" | Key file mode is wider than `0600` | `chmod 600` the key file, and make the service user its owner. |
| Server exits: "data directory … is in use by another harbor-pilot process" | Another `serve`, `restore` or `migrate` holds the lock | Stop the other process. Only one instance may use a data directory. |
| Server exits: "database has migration N unknown to this binary" | An older binary is running against an upgraded database | See [upgrade-rollback.md](upgrade-rollback.md). |
| Restore refuses: "the configured master key cannot open this backup" | The key was rotated after the backup | Add the old key to `security.previous_master_key_files`. |
| Sign-in loops, or `403` CSRF errors | `public_url` does not match the address in the browser, or HTTP is used with a proxy that terminates TLS | Set `HP_PUBLIC_URL` to the external HTTPS address, and list the proxy in `server.trusted_proxies`. |
| Streaming clients time out or receive the whole response at once | A proxy buffers responses | Disable proxy buffering for `/v1/`. |
| Harness requests fail with `401` | Wrong token, or the token was revoked or expired | Issue a new token for the client binding (`harbor-pilot token issue`). Re-run `harbor-pilot harness setup`. |
| Requests fail with "no eligible account is available for model …" or "no candidate could serve the request" | Every account in the pool is excluded by restrictions, cooldown, quota or budget | Open the request in Activity. "Why this route?" lists each excluded candidate and the reason. |
| Bridge requests fail at once | CLI missing or the wrong version, or the account token is invalid | `harbor-pilot doctor` shows the CLI versions. For Claude, create a new token with `claude setup-token` and update the account. |
| A quota monitor shows `auth_failed` | The monitor credential was rejected | Update the credential or reference on the monitor. A `quota.monitor.auth_failed` event is emitted. |
| Notifications or webhooks are not delivered | Destination down, signature mismatch, or rate limit | Check delivery history on the Events and Notifications pages. Dead-lettered deliveries can be replayed. Gateway traffic is never blocked by delivery failures. |
| Compatibility banner in the UI | A breaker opened after repeated protocol failures | Open Compatibility to see the breaker, its path and whether passthrough is active. Protocol Watch records the detected change. |

## Logs
Logs are structured JSON on stderr. Useful messages:
- `gateway.request.completed`: one per request, with route, model, account, attempts, usage, latency and intent. It never contains prompt content.
- `db.migration.applied` and `db.migration.backup`: written at startup.
- `setup.required`: no administrator exists yet. Run `harbor-pilot admin bootstrap`.

Set `HP_LOG_LEVEL=debug` temporarily for more detail. Debug logs still exclude secrets and prompt content.
