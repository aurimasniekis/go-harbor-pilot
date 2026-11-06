# Limits: quotas and budgets

**Monitor → Limits** answers "can my accounts still serve requests, and for how long?". It brings together two kinds
of limit:

- **Quotas** are set by a provider: a Claude subscription's 5-hour and 7-day windows, an API key's monthly spend cap, a
  coding plan's request allowance. Harbor Pilot reads them through **quota trackers** and from the rate-limit
  information providers send back with each response.
- **Budgets** are set by you: a spending cap per workspace, project, client or account, which warns or blocks.

Routing uses both. An account whose quota is used up is skipped until its window resets, and a blocking budget refuses
new requests in its scope before the limit is crossed.

<Shot name="limits" alt="Limits overview with accounts available, limits reached, budgets near limit and tracking problems tiles, the accounts table with each account's tightest quota window, and the budgets table" caption="Limits: accounts, their tightest window, and budgets" />

## Overview

The line under the title names the worst problem and the closest limit, for example "Limit reached: weekly-exhausted"
and "Closest: anthropic-team-1 · 7-day opus · 49% left · resets in 3d 19h". **Refresh all** asks every tracker for
fresh data now.

| Tile | Meaning |
|---|---|
| **Accounts available** | How many accounts routing can use right now, such as "2 of 4" |
| **Limits reached** | Accounts with a used-up window. Routing skips these until reset |
| **Budgets near limit** | Budgets at or past their warning level |
| **Tracking problems** | Trackers that can't sign in or can't fetch data. Credentials need attention |

### Accounts

One row per provider account, with:

- **Status**: **Available**, **Limit reached**, **Paused** (with "Paused until hh:mm" after a rate limit),
  **Unavailable**, **Unknown** (tracking failed, for example "Quota tracking failed: Sign-in failed") or **No data yet**.
- **Tightest window**: the quota window with the least room left, as a bar and "% left". **Stale** means the data is
  older than the tracker's out-of-date limit.
- **Resets**: when that window resets.
- **Quota tracking**: "Checked just now", **Not tracked**, or the tracker's problem, such as **Sign-in failed**.

Click an account to open every window it has: one block per tracker (with who it is signed in as and when it checks
next), plus **Rate limits seen in responses**: limits the provider reported in its response headers. Windows read
through an unofficial adapter carry an **Unofficial source** badge.

Trackers that are not linked to an account appear under **Not linked to an account**. They are watched, but routing
does not use them.

### Forecasts

**Forecast** on a window opens its history and a forecast: how fast it is being used (**Uses about**), when it
**Runs out** at that pace, when it **Resets**, and **How sure** the estimate is. **How we estimate** explains the method,
**Predicted unavailable windows** shows when the account is expected to be out, and **Caveats** lists what makes the
estimate less certain.

<Shot name="quota-forecast" alt="Forecast for the 5-hour session window of team-1-subscription: a summary saying the window still has room when it resets, seven days of observed usage and a shaded forecast range, Uses about 5.0 % per hour, Runs out not within the forecast horizon, the reset time and How sure: High" caption="A window's history and forecast" />

Forecasts extrapolate recent use. They are estimates, not provider guarantees.

## Quota tracking

The **Quota tracking** tab lists the trackers. Each reads one provider's usage endpoint on a schedule.

<Shot name="limits-tracking" alt="Quota tracking tab listing trackers with their adapter, the account each routes for, how often each checks and its status" caption="Quota trackers and the accounts they route for" />

**+ Add quota tracker** opens the tracker dialog:

| Field | What to enter |
|---|---|
| **Provider API** | The adapter (see below) |
| **Name** | A name for the tracker |
| **Routes for** | The account this tracker's limits apply to. When a limit is reached, routing skips that account until the window resets. **No account (watch only)** just records the data |
| Credential | What the adapter needs: an API key, a management or admin key, an OAuth access token or a service account JSON. Paste it (stored encrypted, never shown again) or choose **Read from an environment variable or file** to give a [secret reference](/security/secrets) |
| **Check every** | How often to check, in minutes |
| **Mark out of date after** | When data counts as stale |
| **Treat as used up at** | Percentage left at which a window counts as used up. 0 means only when fully used |
| **Shared scope** | Give trackers that read the same provider quota the same id, so it is not counted twice |
| **Adapter options (JSON object)** | Non-secret settings such as a base URL or organization id |

### Adapters

| Group | Adapters |
|---|---|
| **Official APIs** | Anthropic API (Admin), OpenAI API (Admin), Google Gemini API (Cloud project), xAI, DeepSeek, Moonshot (Kimi), OpenRouter, nexos.ai, Together AI, Cursor (Admin API), GitHub Copilot (premium requests), Generic HTTP |
| **Reported by the tool** | Claude Code (statusline): Claude Code would push its own subscription usage. The adapter is registered, but its ingestion endpoint is not exposed yet |
| **Unofficial (undocumented endpoints)** | Claude subscription, ChatGPT / Codex plan, Z.ai GLM Coding Plan, MiniMax Coding Plan |

::: warning Unofficial adapters
Unofficial adapters read undocumented provider endpoints that can change or break without notice, and using
subscription credentials this way may conflict with the provider's terms. Each one needs **I understand and accept
these risks for this tracker** ticked before it saves.
:::

Subscriptions served through a [CLI bridge](/operators/subscriptions) also report their windows from the CLI itself:
the Claude Code bridge reads the 5-hour and 7-day windows from the CLI's rate-limit events, without a tracker.

Some adapters sign in inside Harbor Pilot rather than with a pasted key. Those show their sign-in status, and **Sign in
again** when a sign-in expires. Sign-in tokens are sealed with the master key and never displayed.

None of the adapters has been verified against a live provider account yet: they are tested against recorded
fixtures and local fakes. Rate limits read from response headers are contract-tested the same way. See the
[compatibility matrix](/reference/compatibility).

## Budgets

**+ Add budget** (here, on Spend, or from the [command palette](/operators/#finding-your-way-around)) opens the budget
dialog.

<Shot name="budget-add" alt="Add budget dialog with name, limit in USD, applies to, period and what happens when the limit is reached" caption="A budget: how much, for what, per which period, and what happens at the limit" />

| Field | Options | Default |
|---|---|---|
| **Name** | | |
| **Limit** | An amount greater than 0 | |
| **Applies to** | **Everything**, **Workspace**, **Project**, **Client** or **Provider account** | Workspace |
| **Period** | **Day**, **Week (Monday start)** or **Month** | Month |
| **When the limit is reached** | **Block new requests** or **Warn only** | Block new requests |
| **Warn at** (advanced) | 1–100 % | 80 % |
| **Currency** (advanced) | Only costs in this currency count | USD |
| **Time zone** (advanced) | Where each period starts | |
| **Budget on** (advanced) | **Estimated cost**, **Billed cost**, **Subscription usage at list prices** | Estimated and billed cost |

**Subscription usage at list prices** is not money spent. Tick it only to cap how much of a subscription gets used.

The budgets table shows each budget's spend this period (for example "$0.11 of $50.00") with a **Forecast** marker for
where the period is heading, its status (**On track**, **Near limit**, **Over budget**, or **Blocking** when a blocking
budget is used up), whether it **Blocks requests** or **Warns only**, and when it resets.

### How blocking works

A blocking budget is checked when the gateway **reserves** a request, inside the same database transaction that
records the reservation. Two requests can't both slip under the limit at once. When a request finishes, the
reservation is settled with the real cost. Requests in the budget's scope are then refused with a clear error until
the period resets or you raise the limit; they show as **Blocked** in [Requests](/operators/requests). Each refusal
sends `access.request.blocked`; `budget.threshold.exceeded` is sent once per period, when the budget is first used up
(see [Events](/reference/events)).

## On a phone

<Shot name="limits" mobile alt="Limits page on a phone with the limit-reached line, Refresh all and Add budget buttons, four summary tiles and account cards with their tightest window" caption="Limits on a 390 px screen" />

## Related

- [Alerts](/operators/alerts): get a message when a window is used up or a budget nears its limit.
- [Spend](/operators/spend): the cost figures budgets count.
- [Migrating from Quota Harbor](/operators/migrate-quota-harbor): import existing trackers and their history.
