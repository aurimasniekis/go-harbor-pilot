# Screenshot coverage inventory

> Every screenshot on the website is a real Harbor Pilot UI capture made by `make screenshots`
> (`scripts/screenshots.sh`). The script builds the binary and starts a fresh demo instance on 127.0.0.1 with its
> synthetic dataset (synthetic providers served by the instance itself, `example.test` people, `[synthetic]` prompts),
> then `web/scripts/screenshots.mjs` signs in, seeds the states the plain demo lacks through the API and the instance's
> own gateway (declared subscription windows, a captured-content grant and a masked capture, an alert channel with
> messages to a local receiver, a relay to an unreachable local port, a paused protocol path, a draft plan that blocks
> traffic, run-scoped and API tokens, a backup) and captures each shot at 1440×900 in the dark and light themes, one
> right after the other. Shots marked "also on a phone" are captured at 390×844 too. Nothing outside 127.0.0.1 is
> contacted. Relative times reflect the moment of capture. Tokens visible in reveal dialogs belong to the throwaway
> instance, which is deleted afterwards. No placeholders or mock-ups.
>
> A row is `current` only when its images were regenerated after the last change to that UI area.
> `site/scripts/check-shots.mjs` (part of `pnpm build`) fails when a page uses a shot that is not listed here, when a
> `current` shot's files are missing, or when a listed shot is not shown on any page.

| Shot name | Page(s) | UI area | Status |
|---|---|---|---|
| `login` | First run | Sign-in page | current |
| `home` | Home and what needs attention, Operator guides, Site landing, Try the demo, What is Harbor Pilot? | Home: what needs attention, 24-hour figures, closest limits, latest requests (also on a phone) | current |
| `palette` | Operator guides, Try the demo | Command palette searching for "budget" | current |
| `requests` | Requests and "why this route", Try the demo | Requests list with tiles and filters (also on a phone) | current |
| `requests-sessions` | Claude Code, Requests and "why this route" | Requests grouped by session | current |
| `request` | Concepts, Connect your first tool, Requests and "why this route", Site landing | A request that failed over: "why this route", route, attempts, candidates (also on a phone) | current |
| `request-replay` | Requests and "why this route" | Replay with current config drawer with a result | current |
| `request-run-refs` | Agent orchestrators | A run token's request with its trusted references under Verified context | stale: regenerate (the demo client now pins spaceman.fleet) |
| `spend` | Site landing, Spend | Spend over time, cost categories kept separate | stale |
| `spend-breakdown` | Spend | Spend breakdown by client, project and model | current |
| `limits` | Limits: quotas and budgets, Site landing | Limits overview: accounts, tightest windows, budgets (also on a phone) | current |
| `limits-tracking` | Limits: quotas and budgets | Quota tracking: trackers and the accounts they route for | current |
| `quota-forecast` | Limits: quotas and budgets | A quota window's history and forecast | current |
| `budget-add` | Limits: quotas and budgets | Add budget dialog | current |
| `providers` | First run, Providers and accounts | Providers with their accounts | current |
| `provider-connect` | First run, Providers and accounts | Connect a provider dialog | current |
| `account-test` | Providers and accounts | Account drawer with a passing connection test | current |
| `subscription-add` | Subscriptions and CLI bridges | Add a subscription dialog | current |
| `subscriptions` | Subscriptions and CLI bridges | Editing a subscription with its declared usage windows | current |
| `clients` | Clients and access tokens, Connect your tools | Clients list (also on a phone) | current |
| `client` | Agent orchestrators, Clients and access tokens, Connect your first tool, Site landing | A client page: facts, activity, tokens | current |
| `client-add` | Clients and access tokens, First run | Add client dialog | current |
| `client-setup` | Claude Code, Clients and access tokens, Connect your first tool, Connect your tools | Set up a tool with Claude Code selected | current |
| `client-setup-codex` | Codex | Set up a tool with Codex selected | current |
| `token-create` | Clients and access tokens | Create access token dialog with permissions | current |
| `token-created` | Clients and access tokens | Access token created: the token shown once with the tool's steps | current |
| `token-replace` | Clients and access tokens | Token rotation: replacement created, revoke the old one | current |
| `routing` | Routing plans | Routing plans list | current |
| `plan` | Concepts, First run, Routing plans, Site landing | A plan's Flow tab | current |
| `plan-versions` | Routing plans | A plan's History tab | current |
| `plan-simulator` | Routing plans | Test a request against a plan | current |
| `plan-editor` | Routing plans | Draft editor, Models and fallback tab | current |
| `plan-rules` | Routing plans | Draft editor, Rules tab with a decision table that blocks | current |
| `plan-publish` | Routing plans | Publish dialog with the blocks-traffic warning | current |
| `model-names` | Routing plans | Model names tab | current |
| `fallback-groups` | First run, Routing plans | Fallback groups tab | current |
| `fallback-group-add` | Routing plans | Add fallback group dialog | current |
| `privacy` | Privacy and masking, Site landing | Masking policies | current |
| `privacy-policy` | Masking: guarantees and limits, Privacy and masking | Add masking policy dialog | current |
| `privacy-test` | Masking: guarantees and limits, Privacy and masking | Trying a masking policy on sample text, with a result | current |
| `capture-rules` | Captured content | Content capture rules | current |
| `captured` | Captured content | Captured content list, with a read grant | current |
| `captured-trace` | Captured content | One opened masked capture | current |
| `alerts` | Alerts | Alert channels | current |
| `alert-channel` | Alerts | Add alert channel dialog | current |
| `alert-rule` | Alerts | Add alert rule dialog | current |
| `alert-history` | Alerts | Alert history with alerts, a recovery and test messages | current |
| `relays` | Events: Pulse and signed webhooks | Event relays list | current |
| `relay` | Events: Pulse and signed webhooks | A relay page with a failed delivery | current |
| `event-stream` | Event catalog, Events: Pulse and signed webhooks | Event stream | current |
| `people` | People, API tokens and sign-in | People | current |
| `people-invite` | People, API tokens and sign-in | Invite user dialog | current |
| `account` | People, API tokens and sign-in | My account: profile, password, sessions | current |
| `api-tokens` | Control-plane API, People, API tokens and sign-in, Terraform | API tokens: automation accounts | current |
| `api-token-issued` | People, API tokens and sign-in | API token issued dialog | current |
| `sso` | People, API tokens and sign-in | Sign-in and SSO settings | current |
| `sso-add` | People, API tokens and sign-in | Add identity provider dialog | current |
| `audit` | Audit log | Audit log | current |
| `diagnostics` | Compatibility matrix, Diagnostics | Diagnostics with a protocol path paused by hand | current |
| `system` | Backup and restore, Secrets and the master key | System and backups with one backup | current |
