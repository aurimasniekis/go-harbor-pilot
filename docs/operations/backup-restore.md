# Backup and restore

## What a backup contains
`harbor-pilot backup` takes a consistent snapshot of `harbor.db` with `VACUUM INTO` while the server keeps running. It writes `manifest.json` next to the snapshot. The manifest records:
- the format version and creation time;
- the server version;
- each file's SHA-256, size and schema version;
- the ids of the master keys that sealed the backup's secrets.

Trace content (`traces.db`) is excluded by default, because it is short-lived by policy. Pass `--include-traces` to keep it; it stays encrypted, and the backup then holds trace keys that expiry would otherwise destroy. The master key is never part of a backup, so back it up separately.

```
harbor-pilot backup --config /etc/harbor-pilot/harbor-pilot.yaml               # <data-dir>/backups/<time>
harbor-pilot backup --out /srv/backups/hp-2026-11-05 --include-traces
harbor-pilot backup verify /srv/backups/hp-2026-11-05
```

Administrators can also start a backup from the System page in the UI, or with `POST /api/v1/system/backups`. Those backups are written under `<data-dir>/backups`; copy them off the host.

`backup verify` recomputes the checksums and runs SQLite `integrity_check` and `foreign_key_check`.

## Restore
A restore replaces the databases, so the server must be stopped. The restore command takes the same data-directory lock as `serve`.

```
systemctl stop harbor-pilot
harbor-pilot restore --from /srv/backups/hp-2026-11-05 --config /etc/harbor-pilot/harbor-pilot.yaml        # verify only
harbor-pilot restore --from /srv/backups/hp-2026-11-05 --config /etc/harbor-pilot/harbor-pilot.yaml --yes  # install
systemctl start harbor-pilot
```

The restore runs these steps in order:
1. It verifies the backup.
2. It checks that the configured keyring holds every master key id in the manifest. If you have rotated the key since the backup, list the old key in `security.previous_master_key_files`.
3. It moves the current `harbor.db` and `traces.db` (with their `-wal` and `-shm` files) aside as `*.pre-restore-<time>`.
4. It installs the backup.

If the backup has no trace content, the server starts with an empty `traces.db`. On start, any newer migrations apply, and reservations that were in flight at backup time are settled by startup recovery. The ledger is not double-counted.

Remove the `*.pre-restore-*` files once you have confirmed the restored state.

## Schedule
Backups are cheap and online, so run one daily from a timer or cron job, copy it off the host, and verify the copy:

```
harbor-pilot backup --out /srv/backups/hp-$(date -u +%F) && harbor-pilot backup verify /srv/backups/hp-$(date -u +%F)
```
