# Home and what needs attention

Home answers two questions: is anything broken, and how did the last 24 hours go. Open it first.

<Shot name="home" alt="Home page with three things that need attention, the last 24 hours tiles, the limits closest to running out and the latest requests" caption="Home: problems first, then the numbers" />

## Things that need your attention

When something needs action, a box at the top says **N things need your attention**, most severe first. Each item
links to where you fix it, and the link says what you will do there:

| Problem | Severity | Link |
|---|---|---|
| A security alert was raised | Critical | **Review →** |
| Quota tracking for an account can't sign in (the provider rejected its credential) | Critical | **Update credential →** |
| Quota data for an account is out of date | Warning | **Check tracker →** |
| A quota window is used up | Warning | **View limit →** |
| A budget is at its warning level, or used up | Warning, or Critical when used up | **Review budget →** |
| The Claude Code or Codex CLI that subscription accounts need can't run | Critical | **Set up the CLI →** |
| Requests to a provider are paused after repeated failures (circuit breaker open) | Warning | **View issue →** |
| Events or notifications could not be delivered | Warning | **View deliveries →** |
| No backup has been taken, or the newest is more than a week old | Warning | **Take a backup →** |
| Sign-in uses plain HTTP (set `public_url` to an https address) | Warning | **View settings →** |

The box shows five items; **Show all** lists the rest. When nothing needs you, it says **All clear** with one calm
line, for example "4 of 4 accounts available · 0 errors in 24h".

## Last 24 hours

Four tiles summarise gateway traffic:

| Tile | What it counts |
|---|---|
| **Requests** | Requests the gateway handled |
| **Error rate** | Share of requests that failed |
| **Fallbacks** | Requests served by a backup account after the first choice failed or was skipped |
| **Estimated cost** | Gateway requests priced at list price |

**Estimated cost** is an estimate from token counts and the prices on each provider. It is not a bill. The
[Spend](/operators/spend) page shows estimates, provider bills and subscription fees side by side, without adding them
up.

## Closest to a limit

The limits with the least room left, tightest first. Quota windows show a bar and **% left**; budgets show
**$x of $y spent**. **All limits** opens [Limits](/operators/limits).

## Latest requests

The newest requests, each with its client, model and outcome: **Running**, **Failed**, **Blocked**, **Canceled**,
**Fallback**, or its latency when it simply worked. Click one to open its request page and see
[why it went where it did](/operators/requests#the-request-page). **All requests** opens the full list.

A new server shows **No requests yet** until the first tool connects. [Connect your first tool](/guide/first-tool).

## Finish setting up

Until you dismiss it, a checklist at the bottom suggests the next steps:

- **Connect a tool**: create a client and an access token ([Clients](/operators/clients)).
- **Invite your team** ([People](/operators/access)).
- **Connect single sign-on** ([Sign-in](/operators/access)).
- **Create an API token for Terraform or CI** ([API tokens](/operators/access), [Terraform](/operators/terraform)).

The ✕ (**Hide setup checklist**) removes it.

## On a phone

Home works the same on a phone. The tiles sit two to a row, and the bottom bar takes you to Requests, Limits and Spend.

<Shot name="home" mobile alt="Home page on a phone with three things needing attention, the four tiles in two columns, and the bottom tab bar" caption="Home on a 390 px screen" />
