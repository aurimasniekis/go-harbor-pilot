# First run

After [installing](/guide/install-docker) Harbor Pilot and opening the setup link from `harbor-pilot admin bootstrap`,
you have an administrator account and an empty instance. This page takes you from there to a working route: one
provider, one fallback group, one model name, one plan and one client.

<Shot name="login" alt="Harbor Pilot sign-in page with email and password fields" caption="Sign in with the account you created from the setup link" />

## 1. Connect a provider

Open **Providers** and choose **Connect a provider**. Pick what you are connecting: the Anthropic, OpenAI or Gemini
API, an OpenAI-compatible service (OpenRouter, DeepSeek, vLLM and others), Ollama, or a subscription through the
Claude Code or Codex CLI bridge.

<Shot name="provider-connect" alt="Connect a provider dialog with provider types, a name, and the API key with options to paste it, read it from the server environment or add it later" caption="Connect a provider and its first account in one step" />

For an API key you have three choices:

- **Paste it.** It is stored encrypted with the master key and never shown again.
- **From the server environment.** Harbor Pilot reads it from a variable when it needs it, for example
  `env:HP_SECRET_ANTHROPIC_API_KEY`. Only variables with an allowed prefix (by default `HP_SECRET_`) can be used.
- **Add later.** The provider is created without an account.

The key becomes the provider's first account. Its panel opens with a **Test connection** check, which proves the
address and credential work. It does not prove model access, quota or billing.

<Shot name="providers" alt="Providers page listing Anthropic, OpenAI and local providers with their accounts, status, requests and cost" caption="Each account shows an honest status: Connection OK, Key rejected, Unreachable, Not tested…" />

More in [Providers and accounts](/operators/providers) and [Subscriptions and CLI bridges](/operators/subscriptions).

## 2. Create a fallback group and a model name

Open **Routing**.

1. On **Fallback groups**, add a group (for example `coding-default`) and list the account and model pairs to try in
   order, such as your Anthropic account with `claude-sonnet-4-5`, then an OpenAI account with a GPT model.
2. On **Model names**, add a name your tools will ask for (for example `coding/default`) and point it at the group.

<Shot name="fallback-groups" alt="Fallback groups page listing coding-default, coding-fast and local-private with their members" caption="Fallback groups list who to try, in order" />

## 3. Create and publish a plan

On **Plans**, create a plan (for example `team-default`). Plans are versioned: you edit a draft, check it and publish
it.

In the first draft:

- Set the **default model name** to `coding/default`, so any requested model goes there.
- Optionally map patterns to other model names (`gpt-*` → `coding/fast`).
- Choose what happens when an attempt fails (try another account, another model, another provider) and whether a paid
  API key may be used as a fallback.

Publish the draft. The plan's **Flow** tab now says in plain words where each requested model goes.

<Shot name="plan" alt="A published routing plan with its flow: claude models to Team key 1 then two more, gpt models to OpenAI team" caption="The Flow tab of a published plan" />

Before publishing, **Test a request** shows what a sample request would do. The check also warns when a version would
block most or all traffic. More in [Routing plans](/operators/routing).

## 4. Add a client and connect a tool

Open **Clients** and choose **Add client**. Give it a name, pick the tool (Claude Code, Codex, OpenCode, Pi or another
API client) and the plan you just published.

<Shot name="client-add" alt="Add client dialog with a name, a short name, a choice of tool and a routing plan" caption="A client ties a tool to a routing plan" />

Creating the client also creates its first access token and shows it once, together with the setup steps for the tool
you picked. Continue with [Connect your first tool](/guide/first-tool).

## 5. Before you rely on it

- Set up [backups](/operators/backup) and keep the master key somewhere safe.
- Invite your team under **Settings → People**, or connect [single sign-on](/operators/access).
- Create an API token for Terraform or CI under **Settings → API tokens**.
- Add [budgets](/operators/limits) and [alerts](/operators/alerts) for the limits you care about.

The **Finish setting up** card on Home tracks the last three.
