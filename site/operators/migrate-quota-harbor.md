# Migrating from Quota Harbor

If you track subscription and provider quotas with Quota Harbor, `harbor-pilot migrate quota-harbor` brings that setup
into Harbor Pilot: the monitored accounts become quota trackers, the Telegram alerts become an alert channel and rule,
and the observation history comes along so forecasts work from the first day.

It is a **dry run by default**: it reads Quota Harbor's configuration (and optionally its database), prints what it
would do, and writes nothing until you add `--apply`. Running it again is safe.

::: info Status
The importer is **contract-tested** with synthetic Quota Harbor configurations and databases, including dry runs and
repeated runs. It has not been run against a production Quota Harbor installation yet.
:::

## What is imported

| Quota Harbor | Harbor Pilot | Notes |
|---|---|---|
| `accounts[]` (in the root file and `include` globs) | Quota trackers, marked as imported | The same adapter and options. The name is a slug of the account id. `enabled`, `poll_interval`, `shared_scope` and `unofficial.acknowledge_terms_risk` are kept. |
| `credentials` of `type: env` or `type: file` (without `key`) | Secret references: `env:NAME` or `file:/path` | Secrets are never copied, only references to them. Supported for `api_key`, `management_key`, `access_token` and `service_account`. |
| `notifications.telegram` | A `telegram` [alert channel](/operators/alerts) and a `telegram-quota-alerts` rule | The rule sends `quota.*`, `budget.*`, `provider.account.*` and `compat.breaker.*` events at warning or above. Quiet hours and the API base URL are kept, along with the cooldown and recovery messages. |
| `bucket_observations` (with `--qh-db`) | Quota history and the latest state of each window | Observations newer than `--since` (default 90 days) for each imported account. |

### Environment variable names

Harbor Pilot only reads secret environment variables with an allowed prefix, by default `HP_SECRET_`. A Quota Harbor
credential that reads `ANTHROPIC_ADMIN_KEY` is reported with a note: *rename it to `HP_SECRET_ANTHROPIC_ADMIN_KEY` (or
another configured prefix) and set the reference*. The tracker is still created; set its credential after renaming
the variable.

## What is reported, not imported

| Item | Why, and what to do |
|---|---|
| **Pricing** | Harbor Pilot keeps prices on provider models. Add them in [Providers](/operators/providers). |
| **Budgets** | Budgets on monitored accounts don't map to gateway spend. Recreate them as [budgets](/operators/limits) on clients, projects or accounts. |
| **Alert rules** | Thresholds are replaced by events and alert rules. Recreate custom ones as [alert rules](/operators/alerts#rules). |
| **Unsupported credential references** (`env_file`, or `file` with `key`) | The tracker is created; set its credential afterwards. |
| **Accounts that fail validation** (an unknown adapter, missing options) | Listed with the reason. |

## Steps

1. **Stop Harbor Pilot.** The importer takes the data-directory lock.
2. **Take a backup**, so you can go back:

   ```sh
   harbor-pilot backup --config /etc/harbor-pilot/config.yaml --out /srv/backups/pre-import
   ```

3. **Review the plan:**

   ```sh
   harbor-pilot migrate quota-harbor --config /etc/harbor-pilot/config.yaml \
     --qh-config /etc/quota-harbor/config.yaml \
     --qh-db /var/lib/quota-harbor/quota.db
   ```

   ```
   KIND                 NAME                   ACTION  NOTE
   quota_monitor        anthropic-team         create
   quota_monitor        claude-max             create  reads CLAUDE_TOKEN, which is not an allowed secret variable; …
   notification_channel telegram               create
   notification_rule    telegram-quota-alerts  create
   pricing                                     skip    12 price entries: Harbor Pilot keeps prices on provider models; …

   Dry run: nothing was written. Re-run with --apply to import.
   ```

4. **Apply it:** run the same command with `--apply`. Add `--json` for a machine-readable report.
5. **Start Harbor Pilot.** Set any credentials the report flagged. Trackers that used an in-app sign-in in Quota
   Harbor need that sign-in again, in [Limits](/operators/limits).
6. **Link the trackers to accounts.** The importer doesn't know which Harbor Pilot account a tracker belongs to.
   Edit each imported tracker in [Limits](/operators/limits) and choose its provider account, so routing knows when
   that account's quota is used up.

## Running it again

Re-running is safe:

- trackers, the channel and the rule that already exist by name are left unchanged and reported as `exists`;
- history is added to existing trackers, and observations already imported are skipped;
- an imported observation never replaces a newer current state.

So you can import once to try Harbor Pilot, keep Quota Harbor running, and import again later to pick up new history
before you switch over.
