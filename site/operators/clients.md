# Clients and access tokens

A **client** is one thing that sends requests to Harbor Pilot: Claude Code on a laptop, Codex in CI, an agent
orchestrator, a notebook using an SDK. Each client has its own routing plan, limits and access tokens, and every
request is recorded against it. Give each tool, machine or automation its own client so you can see, limit and revoke
it on its own.

<Shot name="clients" alt="Clients list with four clients, each showing its tool, routing plan, status, requests, failures and cost in the last 24 hours" caption="Clients: one row per tool, machine or automation" />

**Configure → Clients** lists them with their tool and plan, status, and requests, failures and cost over the last 24
hours.

## Add a client

<Shot name="client-add" alt="Add client dialog with name, short name, tool cards for Claude Code, Codex, OpenCode, Pi and Other API client, and the routing plan" caption="Add client: name it, pick the tool and the plan" />

**+ Add client**, then:

1. **Name** it, for example "Laptop — Claude Code". The **Short name** (lowercase) follows the name until you edit it,
   and is what appears in the request list and in Terraform.
2. Pick the **Tool**: **Claude Code**, **Codex**, **OpenCode**, **Pi**, or **Other API client** (any OpenAI, Anthropic
   or Gemini SDK). The tool chooses the setup instructions and labels reports; a different tool still works with the
   same token.
3. Pick the **Routing plan**, which decides which accounts and models serve this client
   ([Routing plans](/operators/routing)).
4. **Create client and token**. The access token is shown once (see below).

## Connect the tool

**Set up a tool** (on the Clients list or a client's page) walks through the setup for a client: choose the client and,
optionally, a model; pick the tool; then follow the steps. Each tool's steps keep the token out of configuration
files: for Claude Code the token goes into a file only you can read, and `settings.json` points `apiKeyHelper` at it.
**Use the harbor-pilot CLI** does the same with one command.

<Shot name="client-setup" alt="Set up a tool page with the client and model, the tool picker, and Claude Code steps that save the token to a private file and point apiKeyHelper at it" caption="Set up a tool: the steps for Claude Code" />

Each tool has its own guide: [Claude Code](/tools/claude-code), [Codex](/tools/codex), [OpenCode](/tools/opencode),
[Pi](/tools/pi), [SDKs and API clients](/tools/sdks) and [agent orchestrators](/tools/orchestrators).

## A client's page

<Shot name="client" alt="Client page for Claude Code on a laptop with its tool, routing plan, project, requests, failures and cost, and the latest requests" caption="A client: facts, then Activity, Access tokens and Settings" />

The top shows the **Tool**, **Routing plan**, **Project**, and **Requests 24h**, **Failed 24h** and **Cost 24h**. Three
tabs follow:

- **Activity**: the latest requests; **See all in Requests** opens the full list filtered to this client.
- **Access tokens**: the client's tokens.
- **Settings**: everything below.

The ⋯ menu turns the client off or on, or deletes it. A client that is off is refused at the gateway, and its page
says **This client is off**.

## Access tokens

Tools authenticate with an access token that starts with `hpk_`. A client can have several, for example one per
machine.

**Create token** asks for:

- **Token name**.
- **Expires**: **7 days**, **30 days**, **90 days** (the default), **1 year**, **Custom…** (a number of days) or
  **Never expires**.
- **This token can**: its permissions. By default **Send requests** and **List models**. Click **Change** to add:

| Permission | Allows |
|---|---|
| **Send requests** | Call the model endpoints |
| **List models** | See which model names it may ask for |
| **Read its own usage** | Requests and costs of this client only |
| **Create run tokens** | Short-lived tokens for orchestrated runs |

**Access token created** shows the token **once**. Copy it into the tool now; Harbor Pilot stores only a keyed hash (HMAC-SHA256)
of it and can't show it again.

The tokens table lists each token's permissions, when it was last used and when it expires. **Show revoked** includes
revoked ones. Tokens an orchestrator minted for a single run appear as **Run token**s under the token that created
them; see [agent orchestrators](/tools/orchestrators).

### Rotate a token

1. On the token's ⋯ menu, choose **Replace with a new token…**. It creates a new token with the same name.
2. Give the new token to the tool.
3. Click **Revoke the old token now**, in the same dialog.

Until step 3 both tokens work, so the tool never stops.

### Revoke a token

**Revoke** on the token's ⋯ menu. Tools using it stop working immediately, and run tokens created from it are revoked
too.

## Settings

The **Settings** tab, with **Discard** and **Save** at the bottom:

**Basics**

- **Name**, **Tool**, **Routing plan**.
- **Default project**: usage is attributed to this project.
- **Owner**: **No owner**, **A person** or **An automation account**.

**Limits**

- **Requests per minute** and **Requests at once** (empty means unlimited).
- **Largest request** in bytes (empty means the server default).
- **Allowed models**: the model names this client may ask for. Empty means any model its plan routes.

These sit on top of the plan, the accounts' own limits and any [budgets](/operators/limits#budgets).

**Advanced**: what Harbor Pilot trusts from the tool

- **Group requests into sessions**: use the tool's session headers to group requests into
  [sessions](/operators/requests#sessions). It only affects grouping, never access.
- **Let the tool choose the project** and **Projects the tool may choose**: honour the `X-HP-Project` header, but only
  for the projects listed.
- **Trust the purpose the tool declares**: use the `X-HP-Intent` header as the request's intent, which
  [routing rules](/operators/routing#rules-and-decision-tables) can test. Otherwise it is recorded as reported, not
  trusted.
- **References**: trusted references for reporting, one `key=value` per line, such as
  `git.repo=example.com/acme/web`.

Anything a tool sends that its client does not trust is still recorded under **Reported by the tool** on the request
page, but routing and reports don't rely on it.

## On a phone

<Shot name="clients" mobile alt="Clients list on a phone with each client as a card showing its tool, plan, status, requests, failures and cost" caption="Clients on a 390 px screen" />

## Related

- [Routing plans](/operators/routing)
- [Requests](/operators/requests)
- [Terraform](/operators/terraform): manage clients as code.
