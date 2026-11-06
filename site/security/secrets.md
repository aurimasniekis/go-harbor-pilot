# Secrets and the master key

Harbor Pilot keeps three kinds of secret: credentials you give it (provider API keys, subscription sign-in tokens,
webhook and alert secrets, the single sign-on client secret), tokens it issues (gateway tokens for tools, API tokens
for automation), and its own keys. This page explains how each is stored and how to manage the master key that
protects them.

## The master key

Every stored secret is encrypted under one 32-byte master key (the key-encryption key). The master key is never
written to the database, never included in backups and never sent anywhere.

```sh
harbor-pilot keys generate > /etc/harbor-pilot/master.key   # outside the data directory
chmod 600 /etc/harbor-pilot/master.key
```

Point `security.master_key_file` (or `HP_MASTER_KEY_FILE`) at it. You can pass the key itself in `HP_MASTER_KEY`
instead, but a file is easier to protect. The key may be base64 or hex.

The server refuses to start when:

- the key file can be read by its group or by others;
- no key is configured (demo mode is the exception: it creates a throwaway key inside its own data directory).

The key's fingerprint (`master_key_id`, not secret) is shown on **Settings → System & backups** and recorded in every backup's
manifest, so you can tell which key opens which backup.

<Shot name="system" alt="Settings, System and backups tab, showing backups, this instance's version, data directory and master key id, and CLI bridge status" caption="The master key id under This instance" />

::: warning Back up the master key separately
Losing the master key makes every stored credential and every piece of captured content unrecoverable. Keeping it
next to the database backups defeats the encryption.
:::

## How secrets are stored

- **Envelope encryption.** Each secret gets its own data key; the value is sealed with AES-256-GCM, and the data key is
  wrapped by the master key. The wrapped key records which master key sealed it, so rotation can happen without
  downtime.
- **Write-only.** The API and the UI accept secrets but never return them. Reads show only whether a secret is set,
  where it comes from (`stored`, `reference` or `none`), a short fingerprint and when it changed.
- **Issued tokens are not stored.** Gateway tokens (`hpk_…`) and API tokens (`hpa_…`) are shown once. Harbor Pilot keeps
  only a keyed hash and compares in constant time.
- **Captured content** uses a data key per day and policy, wrapped by the master key. Expiry destroys that key.

## Secret references

Instead of storing a value, any secret field can reference one that lives outside Harbor Pilot. The reference goes in
the field's `*_ref` companion (for example `credential_ref` on a provider account, `client_secret_ref` on a sign-in
provider):

| Reference | Reads |
|---|---|
| `env:HP_SECRET_ANTHROPIC_KEY` | An environment variable of the server process |
| `file:/etc/harbor-pilot/secrets/openai.key` | A file inside a configured secret root |

A reference and a stored value are mutually exclusive. References are resolved when they are used, so rotating the
underlying value needs no change in Harbor Pilot.

Whoever can set a reference can make its value leave the process: it is sent to the provider, identity provider or
alert endpoint they also configure. Harbor Pilot therefore limits what a reference may read:

- **Environment variables must carry an allowed prefix**, `HP_SECRET_` by default (`security.secret_env_prefixes`,
  or `HP_SECRET_ENV_PREFIXES` as a comma-separated list). Prefixes must be specific: at least three characters, and
  no `HP_` prefix other than `HP_SECRET_`.
- **The master key variables are never readable**: `HP_MASTER_KEY`, `HP_MASTER_KEY_FILE` and the previous-key
  variables are refused whatever the prefixes say.
- **Files must sit under `security.secret_roots`.** A root may not contain the data directory or any master key file;
  the server refuses to start if one does. Those paths are refused even when a root would reach them.
- References are checked when they are saved (the error names the allowed prefixes) and again when they are resolved.
- Changing a resource that holds secrets, including its `*_ref` fields, needs the `secrets.write` permission.

::: tip Upgrading from an older build
References to variables without the prefix stop resolving. Rename them, for example `ANTHROPIC_API_KEY` to
`HP_SECRET_ANTHROPIC_API_KEY`, or add a dedicated prefix of your own.
:::

## Destinations keep their secrets only while they stay put

A stored secret does not follow its destination to a new address. Changing the URL of an alert channel, event relay or
quota tracker to a different origin requires entering its secrets again. Changing a provider's base URL to a different
origin clears its accounts' stored credentials, and the clearing is audited.

## Rotating the master key

1. Generate a new key: `harbor-pilot keys generate > /etc/harbor-pilot/master-2.key`.
2. Set `security.master_key_file` to the new key and list the old one in `security.previous_master_key_files`.
3. Restart. New secrets are sealed with the new key; existing ones still open with the old key.
4. Run `harbor-pilot keys rotate --config /etc/harbor-pilot/harbor-pilot.yaml`. It re-wraps every stored data key
   (provider and alert secrets, sign-in secrets, server keys, quota sign-in tokens and the captured-content day keys)
   under the new key. The encrypted values themselves are unchanged.
5. The command checks that nothing still depends on a previous key. Only then does it say the previous keys can be
   removed; otherwise it names the key ids that are still in use.
6. Remove the old key from `previous_master_key_files` and restart.

Keep the old key as long as you keep backups taken before the rotation: restoring one needs every key id listed in its
manifest.

## Secrets on the command line

The CLI never accepts a secret as an argument, where it would show up in shell history and process listings. Commands
read tokens from a file (`--token-file`, `--api-token-file`), from standard input (`--token-stdin`,
`--api-token-stdin`) or from the environment (`HP_API_TOKEN`). See the [command reference](/reference/cli).
