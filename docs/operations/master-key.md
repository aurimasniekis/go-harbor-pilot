# Master Key Management

Harbor Pilot encrypts the following under a 32-byte master key-encryption key (KEK):

- stored provider secrets
- OIDC client secrets
- PKCE verifiers
- server HMAC keys
- trace data-encryption keys

The KEK is **never** stored in the database.

## Generate and configure
```sh
harbor-pilot keys generate > /etc/harbor-pilot/master.key   # outside the data directory
chmod 600 /etc/harbor-pilot/master.key
```
Set `security.master_key_file` (or `HP_MASTER_KEY_FILE`). You can pass the key itself in `HP_MASTER_KEY`, but a file is preferred.

The server refuses to start in these cases:
- the key file is readable by group or others;
- no key is configured. Demo mode is the exception: it creates a throwaway key inside its own data directory.

## Backup
**Back up the master key separately from the database backups.** Losing it makes every stored secret and trace unrecoverable. Storing it alongside the database backups defeats the encryption at rest.

The master-key fingerprint (`master_key_id`, not secret) is shown under `GET /api/v1/system/status`. Use it to match a key to its backups.

## Rotation
1. Generate a new key.
2. Set `master_key_file` to the new key and add the old key to `previous_master_key_files`.
3. Restart. New secrets are now sealed with the new key, and old envelopes still open with the old one.
4. Run `harbor-pilot keys rotate --config …` to re-wrap every stored DEK under the new key. Ciphertexts are unchanged.
5. Remove the old key from `previous_master_key_files` and restart.

Rotation re-wraps server keys, OIDC client secrets and every column registered as sealed. Feature modules register their own sealed columns.
