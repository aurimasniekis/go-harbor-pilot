# Upgrades and rollback

Until 1.0, read the release notes of every version you upgrade to: a minor version can still change defaults and the
configuration, and this page lists the changes that need action from you.

## Upgrade

1. Read the release notes for migrations and changed defaults.
2. Take a backup and verify it:

   ```sh
   harbor-pilot backup --config /etc/harbor-pilot/config.yaml --out /srv/backups/pre-upgrade
   harbor-pilot backup verify /srv/backups/pre-upgrade
   ```

3. Replace the binary, or pull the new image, and restart.

At startup the server applies pending database migrations in order, each in its own transaction. Before the first
one it writes a copy of the database, `harbor.db.pre-<version>.bak`, next to it. The log shows `db.migration.backup`
and one `db.migration.applied` line per migration. If a migration fails, the server refuses to start and the database
stays as it was before that migration.

Then check:

- `harbor-pilot doctor --config … --server https://pilot.example.com` passes;
- `/readyz` returns 200;
- **Settings → System & backups → This instance** shows the new **Version**, and **Database schema versions** the
  schema version of the main and captured content databases.

With Docker, pin a version tag rather than `latest`, so an upgrade is a deliberate change. `0.1` takes patch releases
of 0.1; moving to `0.2` is an upgrade to read the notes for. Change the tag in your Compose file, then:

```sh
docker compose pull && docker compose up -d
```

## Roll back

An older binary refuses to run against a database it doesn't know:

```
database has migration 20261105201500000 unknown to this binary; refusing to run an older version against a newer schema
```

Choose the path that matches the upgrade:

- **The upgrade applied no migrations:** stop the server, reinstall the previous binary (or pin the previous image
  tag), and start it.
- **The upgrade applied migrations:** stop the server and restore the backup from step 2:

  ```sh
  harbor-pilot restore --from /srv/backups/pre-upgrade --config /etc/harbor-pilot/config.yaml --yes
  ```

  Then start the previous binary. Changes made after the upgrade are lost, so use the newest backup taken before it.
  The automatic `harbor.db.pre-<version>.bak` works too: move it into place as `harbor.db` while the server is stopped.

See [Backup and restore](/operators/backup).

## Notes for specific versions

### Unreleased

- **Reasoning option per model (D-047).** A model can carry a `reasoning` option that translates Claude Code's adaptive
  thinking and effort, and other clients' reasoning settings, for its upstream; see
  [Providers](/operators/providers#reasoning). Models without it send the same upstream bodies as before. Only the
  wording of some recorded losses changed (`context_management`, `thinking` and `output_config` drops now say why).
- **Writing providers through the API or Terraform.** As with the request fields, a provider update that leaves
  `reasoning` out of a model clears it.
- **New losses on Claude Code bridge requests.** The bridge now records the client's thinking mode (`thinking`,
  approximate: the CLI chooses its own) and a top-level `output_config.format` (dropped). Requests are sent as before;
  alerts or reports that count losses will see them.

### 0.1.6

- **No migrations, no new configuration keys.**
- **Upstream request fields (D-046).** A provider's models can pin the upstream provider (OpenAI-compatible providers
  only) and set allowlisted request fields; see [Providers](/operators/providers#upstream-request-fields). Models that
  configure nothing are unchanged.
- **Writing providers through the API or Terraform.** Updating a provider replaces its whole spec, so a client that
  leaves out `provider_routing`, `request_fields` or `request_field_policy` clears values set in the UI. Add them to
  the resource's `spec` before applying.

### 0.1.5

- **No migrations.**
- **Bridge sessions may stay on disk longer (D-045).** When a Claude Code bridge process ends between turns, its CLI
  session is kept for up to the conversation's prompt-cache TTL (5 minutes or 1 hour) after its last use, so the next
  turn can resume it. Before, conversation files were removed when the process ended. Kept sessions are deleted at
  expiry, on account removal and at shutdown, and are bounded by the new keys
  `bridges.claude_code.shelf_max_per_account` (default `16`) and `bridges.claude_code.shelf_max_bytes` (default
  256 MiB); a value of `0` means the default, and there is no switch to turn retention off. See
  [Limitations](/security/limitations).
- **Behaviour changes.** The Claude Code bridge removes client `cache_control` markers (recorded as a `cache_control`
  loss), a 400 reported by the CLI is returned to the client instead of failing over, and attempts are numbered in
  the order they ran (skipped candidates are listed as excluded instead). Review alerts or scripts that count
  failovers or read attempt numbers.

### 0.1.4

- **One migration.** `20261108100000000_add_budget_state_limits` adds two nullable limit columns to `budget_state`.
  It is quick and needs no action; the server writes `harbor.db.pre-<version>.bak` before applying it, so rolling
  back means restoring a backup.
- **One repeated budget alert.** A budget that already sent `budget.threshold.warning` or `budget.threshold.exceeded`
  in its current period sends it once more after the upgrade. Alerts now re-arm when a budget's limit is raised, and
  budgets upgraded mid-period have no recorded limit yet, which counts as raised.

### 0.1.2

- **Budget refusals are now policy blocks.** A request that blocking budgets refuse is recorded as **Blocked** and
  sends `access.request.blocked` for every refused request, with the budget in `reason`; 0.1.1 recorded it as an error
  without the event. Review alert rules, webhooks and Pulse consumers that subscribe to `access.request.blocked`.
  `budget.threshold.exceeded` is still sent once per period.
- The first start indexes existing request history by trusted reference (for `ref=` filters and
  `group_by=ref:<key>`). This runs as one migration before the server listens, and its time and disk use grow with
  requests and ledger rows times their references. As a guide, 1M requests plus 1M ledger rows with 3 references each
  took about 27 s and added about 530 MB. Make sure the disk has room for that, the database and the
  `harbor.db.pre-<version>.bak` copy.

### 0.1.1

The first release: there is nothing to upgrade from. (0.1.0 was tagged but never published.)

## Rotating the master key

Rotation is independent of upgrades, but has the same care around backups:

1. Generate a new key: `harbor-pilot keys generate > /etc/harbor-pilot/master-2.key` and `chmod 600` it.
2. Point `security.master_key_file` at the new key and add the old one to `security.previous_master_key_files`.
3. Restart. New secrets are sealed with the new key; old ones still open with the old key.
4. Run `harbor-pilot keys rotate --config …` to re-wrap every stored key under the new one.
5. Keep the old key in `previous_master_key_files` until you no longer need any backup taken before the rotation
   (a restore checks for it). Then remove it and restart.

Details in [Secrets and the master key](/security/secrets).
