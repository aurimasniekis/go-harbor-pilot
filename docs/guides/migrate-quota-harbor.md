# Migrating from Quota Harbor

`harbor-pilot migrate quota-harbor` reads a Quota Harbor configuration file, and optionally its database, and imports what has an equivalent in Harbor Pilot. It runs as a dry run by default and prints the plan. Nothing is written until you pass `--apply`.

## What is imported

| Quota Harbor | Harbor Pilot | Notes |
|---|---|---|
| `accounts[]` (root file and `include` globs) | quota monitors, `managed_by=import` | The same adapter id and options. The name is a slug of the account id. `enabled`, `poll_interval`, `shared_scope` and `unofficial.acknowledge_terms_risk` are kept. |
| `credentials` with `type: env` or `type: file` (no `key`) | `*_ref` fields (`env:NAME`, `file:/path`) | Secrets are never copied, only references. `api_key`, `management_key`, `access_token` and `service_account` are supported. |
| `notifications.telegram` | a `telegram` notification channel and a `telegram-quota-alerts` rule | The rule routes `quota.*`, `budget.*`, `provider.account.*` and `compat.breaker.*` events at warning severity or above. It also carries the cooldown, quiet hours and recovery messages. |
| `bucket_observations` (with `--qh-db`) | quota history and the latest bucket state | Observations newer than `--since` (default 90 days) are imported for each imported account, so forecasts have history from the first day. |

## What is reported, not imported

- **Pricing.** Harbor Pilot keeps prices on provider models. Add them there.
- **Budgets.** Monitored-account budgets do not map to gateway spend. Recreate them as budgets on clients, projects or provider accounts.
- **Alert rules.** Thresholds are replaced by events and notification rules.
- **Unsupported credential references** (`env_file`, or `file` with `key`). The monitor is still created; set the credential on it after import.
- **Accounts that fail validation**, such as an unknown adapter or missing adapter options. They are listed with the reason.

## Steps

1. Stop Harbor Pilot. The importer takes the data-directory lock.
2. Take a backup: `harbor-pilot backup`.
3. Review the plan:
   ```
   harbor-pilot migrate quota-harbor --qh-config /etc/quota-harbor/config.yaml --qh-db /var/lib/quota-harbor/quota.db
   ```
4. Apply it: add `--apply`. Use `--json` for a machine-readable report.
5. Start Harbor Pilot. Set any credentials the report flagged, and sign in again for monitors that use in-app sign-in.

Re-running is safe. Monitors, the channel and the rule that already exist by name are kept unchanged and reported as `exists`. History is added to the existing monitors, and observations that were already imported are skipped. An imported observation never replaces a newer current state.
