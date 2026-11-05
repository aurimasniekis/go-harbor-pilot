# Upgrade and rollback

## Upgrade
1. Read the release notes for migrations and changed defaults.
2. Take a backup: `harbor-pilot backup`. Verify it.
3. Replace the binary or image, and restart.

At startup the server applies pending schema migrations in order, each in its own transaction. Before the first one, it writes `harbor.db.pre-<version>.bak` next to the database, also with `VACUUM INTO`. Migrations are append-only and never rewritten after release. The server refuses to start if a migration fails.

After an upgrade, check:
- `harbor-pilot doctor` passes;
- `/readyz` returns 200;
- the UI's System page shows the new version.

### Notes for specific versions
- **Secret references are confined (D-035).** `env:` references now read only variables with an allowed prefix, by default `HP_SECRET_`. `file:` references can no longer read the data directory or the master key files.
  - Before upgrading, list your references: search the configuration export for `"env:` and `_ref`.
  - Rename the variables, for example `ANTHROPIC_API_KEY` to `HP_SECRET_ANTHROPIC_API_KEY`. Alternatively, set `security.secret_env_prefixes`, but make sure the prefix covers only secrets.
  - References that no longer resolve show up as failed requests ("environment variable is not allowed as a secret reference"), and saving such a reference is refused with the allowed prefixes named.

## Rollback
An older binary refuses to run against a schema it does not know: "database has migration N unknown to this binary". Choose the path that matches the upgrade:

- **No migrations in the upgrade:** stop the server, reinstall the previous binary, and start it.
- **The upgrade applied migrations:** stop the server and restore the backup from step 2 with `harbor-pilot restore --from DIR --yes`. Then start the previous binary. Changes made after the upgrade are lost, so restore the newest backup taken before it. The automatic `harbor.db.pre-<version>.bak` copy can also be used: move it into place as `harbor.db` while the server is stopped.

Container images are tagged by version, so roll back by pinning the previous tag, with the same data-volume steps.

## Master key rotation
See [master-key.md](master-key.md). Keep the old key in `security.previous_master_key_files` until `harbor-pilot keys rotate` has run, and until you no longer need any backup taken before the rotation.
