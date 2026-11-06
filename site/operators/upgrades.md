# Upgrades and rollback

Harbor Pilot is pre-release. Until 1.0, read the release notes of every version you upgrade to: defaults and the
configuration can still change between versions, and this page lists the changes that need action from you.

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

With Docker, images are tagged by version. Pin a tag rather than `latest`, so an upgrade is a deliberate change:

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

### Secret references are confined to a prefix

`env:` secret references read only environment variables with an allowed prefix, by default `HP_SECRET_`. `file:`
references can't read the data directory or the master key files.

Before upgrading:

1. Find your references: export the configuration (`GET /api/v1/config/export`) and search it for `"env:` and
   `_ref`.
2. Rename the variables, for example `ANTHROPIC_API_KEY` to `HP_SECRET_ANTHROPIC_API_KEY`, and update the references.
   Or set `security.secret_env_prefixes` (or `HP_SECRET_ENV_PREFIXES`) to your own prefix. Make sure it covers only
   secrets: a prefix that is too broad is refused.

A reference that no longer resolves makes requests fail with *environment variable is not allowed as a secret
reference*, and saving one is refused with the allowed prefixes named.

### SSO no longer links existing accounts by email alone

With **New people: Must be invited first**, an existing account is linked to an SSO identity only after an admin marks
it for that provider (invite it with that provider, or `PATCH /api/v1/users/{id}` with
`{"sso_provider": "<slug>"}`). The provider's allowed email domains now apply to these accounts too. Accounts already
linked are unaffected. Mark the remaining people before upgrading, or they can't sign in through SSO. See
[Single sign-on](/operators/access#single-sign-on-oidc).

## Rotating the master key

Rotation is independent of upgrades, but has the same care around backups:

1. Generate a new key: `harbor-pilot keys generate > /etc/harbor-pilot/master-2.key` and `chmod 600` it.
2. Point `security.master_key_file` at the new key and add the old one to `security.previous_master_key_files`.
3. Restart. New secrets are sealed with the new key; old ones still open with the old key.
4. Run `harbor-pilot keys rotate --config …` to re-wrap every stored key under the new one.
5. Keep the old key in `previous_master_key_files` until you no longer need any backup taken before the rotation
   (a restore checks for it). Then remove it and restart.

Details in [Secrets and the master key](/security/secrets).
