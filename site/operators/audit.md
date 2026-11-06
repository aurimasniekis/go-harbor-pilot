# Audit log

The audit log records who did what, to which object, and whether it worked. It covers every change made through the
UI, the API, the CLI and Terraform, every sign-in, and every read of captured content.

**Settings → Audit log.** Reading it needs `audit.read` (operators and admins).

<Shot name="audit" alt="Settings, Audit log tab, with search and filters for who, what, outcome and time, and today's entries such as a password sign-in and the system issuing access tokens and creating budgets" caption="Settings → Audit log: newest first, grouped by day" />

## What is recorded

| Area | Examples |
|---|---|
| Configuration | Creating, changing and deleting any object (providers, accounts, plans, clients, budgets, policies, channels, relays…). Changes from Terraform are made by its automation account, so they show under that account. |
| Routing plans | Publishing a plan version. |
| Access | Issuing and revoking access tokens and API tokens; inviting, editing and removing people; role changes. |
| Sign-in | Password and SSO sign-ins and refusals (`auth.login`), sign-outs, recent-sign-in confirmations (`session.reauth`), break-glass recovery (`auth.breakglass`), sign-in settings and SSO providers. |
| Captured content | Every read, search, download, refusal and deletion (`trace.read`, `trace.search`, `trace.export`, `trace.delete`), with the content mode. |
| Protocol paths | Forcing a breaker open or closed (with the reason you gave), running a live canary, triaging follow-ups. |
| System | Taking, downloading and deleting backups; replaying events and alerts; acknowledging security alerts. |

Each entry holds:

- the time;
- who did it: a person, an automation account, **System** (Harbor Pilot itself), or an anonymous caller;
- the action, for example `routing_plan.publish` or `trace.read`;
- the target object;
- the outcome: **Succeeded**, **Denied** or **Failed**;
- the request id and IP address;
- details.

Details are redacted: secrets, tokens and captured content never appear in the audit log.

An audit entry is written in the same transaction as the change it describes, so a change can't happen without its
entry.

## Find something

| Filter | Options |
|---|---|
| **Search** | Text in the action, actor, target or details: a name, an action or an id. |
| **Who** | **Anyone**, **System**, or a person or automation account. |
| **What** | **Any action**, or a kind of action. |
| **Outcome** | **Any outcome**, **Succeeded**, **Denied**, **Failed**. |
| **When** | **Last 24 hours**, **Last 7 days** (default), **Last 30 days**, **All time**. |

Click an entry to open its details: **When**, **Who**, **Outcome**, **Action**, **Target**, **Actor id**, **Request
id**, **IP address** and the details. The request id matches the one in error messages and in the server log, so you
can follow one action across all three.

Some useful searches:

- *Who read captured content this week?* Search `trace.`, **When** = Last 7 days.
- *Who changed this plan?* Search for the plan's name or id.
- *Refused sign-ins:* **Outcome** = Denied, search `auth.login`.

## Security alerts

Some events are serious enough to stay visible until someone looks at them. Using a
[break-glass recovery link](/operators/access#break-glass-recovery) raises a **Security alert** that is pinned above the
audit log, and counted for admins in the app, until an admin chooses **Acknowledge**. Acknowledging is itself audited.

## Retention

Audit entries are kept in `harbor.db` and are not pruned automatically in this release. They are included in every
[backup](/operators/backup).

## API

```sh
curl -H "Authorization: Bearer $HP_API_TOKEN" \
  "https://pilot.example.com/api/v1/audit?action=trace.&since=2026-11-01T00:00:00Z"
```

| Parameter | Meaning |
|---|---|
| `action` | Action prefix, for example `auth.` or `trace.`. |
| `actor_id` | One person or automation account. |
| `target_kind`, `target_id` | One object. |
| `outcome` | `success`, `denied` or `error`. |
| `since` | RFC 3339 time. |
| `q` | Free text in the action, actor, target or details. |

Results are newest first, with cursor pagination. `GET /api/v1/security-alerts` lists security alerts, and
`POST /api/v1/security-alerts/{id}/acknowledge` acknowledges one (admins).
