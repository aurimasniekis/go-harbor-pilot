# Operator guides

These guides are for the people who run Harbor Pilot day to day: checking what needs attention, finding out why a
request went where it did, keeping spend and quotas in view, and changing where traffic goes. Each one starts from a
screen in the web UI and says what every part of it means.

::: info How this was tested
What has been tested, and how, is listed in the [compatibility matrix](/reference/compatibility). Nothing on these
pages claims a live run against a provider unless that page records one.
:::

## Finding your way around

The sidebar has four parts:

| Part | Pages | Use it to |
|---|---|---|
| **Home** | [Home](/operators/home) | See what needs attention and how the last 24 hours went |
| **Monitor** | [Requests](/operators/requests), [Limits](/operators/limits), [Spend](/operators/spend) | Look into requests, quotas, budgets and cost |
| **Configure** | [Clients](/operators/clients), [Providers](/operators/providers), [Routing](/operators/routing), [Privacy](/operators/privacy), [Alerts](/operators/alerts) | Change who can connect, which accounts exist and where requests go |
| **Settings** | People, single sign-on, audit log, backups, API tokens, diagnostics, event stream | Administer the server (see [Administer](/operators/access)) |

<Shot name="home" alt="Harbor Pilot Home page with the sidebar showing Home, the Monitor section with Requests, Limits and Spend, and the Configure section" caption="The sidebar: Home, Monitor, Configure, and Settings at the bottom" />

**Search** at the top of the sidebar (<kbd>Ctrl</kbd> <kbd>K</kbd>, or <kbd>⌘</kbd> <kbd>K</kbd> on a Mac) opens a
command palette. It finds pages and actions by keyword, so typing "budget" offers **Limits**, **Add a budget** and
**Budgets**.

<Shot name="palette" alt="Command palette searching for budget, offering Limits, Add a budget and Budgets" caption="The command palette finds pages and actions" />

On a phone the sidebar becomes a bar along the bottom with **Home**, **Requests**, **Limits**, **Spend** and **More**.
Every list is built to fit a 390 px wide screen without scrolling sideways.

<Shot name="home" mobile alt="Home page on a phone, with the bottom tab bar showing Home, Requests, Limits, Spend and More" caption="On a phone" />

## Everyday use

| Task | Guide |
|---|---|
| See what needs attention first | [Home](/operators/home) |
| Find a request and see why it was routed the way it was | [Requests and "why this route"](/operators/requests) |
| See what was spent, billed and estimated | [Spend](/operators/spend) |
| Watch subscription quotas and budgets, and see forecasts | [Limits](/operators/limits) |

## Configure

| Task | Guide |
|---|---|
| Add an API key, a local model server or another account | [Providers and accounts](/operators/providers) |
| Use a Claude or ChatGPT subscription through its official CLI | [Subscriptions and CLI bridges](/operators/subscriptions) |
| Give a tool or a person an access token | [Clients and access tokens](/operators/clients) |
| Decide which account and model serve each request | [Routing plans](/operators/routing) |
| Mask secrets and personal data before a provider sees them | [Privacy and masking](/operators/privacy) |
| Keep short-lived copies of request content for debugging | [Captured content](/operators/captured-content) |
| Get told when a quota runs out or a credential fails | [Alerts](/operators/alerts) |
| Send events to Pulse or a webhook | [Events](/operators/events) |
| Manage configuration as code | [Terraform](/operators/terraform) |

## Administer

| Task | Guide |
|---|---|
| Invite people, create API tokens, set up single sign-on | [People, API tokens and sign-in](/operators/access) |
| See who changed what | [Audit log](/operators/audit) |
| Check the server's health and compatibility | [Diagnostics](/operators/diagnostics) |
| Back up and restore | [Backup and restore](/operators/backup) |
| Upgrade and roll back | [Upgrades and rollback](/operators/upgrades) |
| Metrics and capacity | [Monitoring and capacity](/operators/monitoring) |
| Something is wrong | [Troubleshooting](/operators/troubleshooting) |
| Move from Quota Harbor | [Migrating from Quota Harbor](/operators/migrate-quota-harbor) |

## How the pieces fit

If the words _client_, _plan_, _model name_ or _fallback group_ are new, read [Concepts](/guide/concepts) first. In
short: a **client** sends a request with its access token; the client's **routing plan** turns the requested model
into a **model name**; the model name points at a **fallback group**, the ordered list of **accounts** and models
that can serve it; Harbor Pilot tries them in order until one answers.
