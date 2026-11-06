# Agent orchestrators

An orchestrator such as [SpaceMan](https://github.com/aurimasniekis/go-spaceman) starts many agent runs, each one a
coding tool with its own task. Giving every run the same long-lived token makes it impossible to tell runs apart and
dangerous to leak one. Harbor Pilot instead lets the orchestrator create a **run token** for each run: short-lived,
narrower than its parent, and carrying references that identify the run.

::: info Status
Run tokens are contract-tested (`TestMintChildConfinesBoundRefs` and the SpaceMan acceptance scenario). SpaceMan does
not create run tokens yet; this page describes the Harbor Pilot side, which any orchestrator can use.
:::

## How it works

```mermaid
sequenceDiagram
  participant O as Orchestrator
  participant HP as Harbor Pilot
  participant A as Agent run (Claude Code, Codex…)
  O->>HP: POST /v1/harbor/credentials<br/>parent token (Create run tokens)<br/>bound_refs {spaceman.run: run_…}
  HP-->>O: 201 {credential, token}
  O->>A: start with ANTHROPIC_BASE_URL + run token
  A->>HP: /v1/messages with the run token
  Note over HP: request tagged with the run,<br/>routed by the client's plan
  O->>HP: DELETE /v1/harbor/credentials/{id} when the run ends
```

1. Create a client for the orchestrator, for example `spaceman-runs`, with a routing plan, a project and limits.
2. Create one token for it with the **Create run tokens** permission (`tokens:mint`), and keep that token on the
   orchestrator's central server. Agents never see it.
3. When a run starts, the orchestrator creates a run token and hands it to that run only.
4. When the run ends, the orchestrator revokes it. The expiry is a backstop.

## Create a run token

```sh
curl https://pilot.example.com/v1/harbor/credentials \
  -H "Authorization: Bearer $PARENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "run run_01JD…",
    "scopes": ["gateway:invoke"],
    "ttl_seconds": 7200,
    "bound_refs": {"spaceman.run": "run_01JD…", "session": "run_01JD…"}
  }'
```

The answer is `201 Created` with the credential's details and its token, shown this once:

```json
{
  "credential": {
    "id": "crd_…",
    "binding_id": "cbn_…",
    "parent_id": "crd_…",
    "scopes": ["gateway:invoke"],
    "bound_refs": {"spaceman.space": "spc_…", "spaceman.run": "run_01JD…", "session": "run_01JD…"},
    "expires_at": "…"
  },
  "token": "hpk_…"
}
```

| Field | Meaning |
|---|---|
| `name` | Required, 1–80 characters. Shown on the client's **Access tokens** tab. |
| `scopes` | A subset of the parent's, never `tokens:mint`. Defaults to sending requests and listing models. |
| `ttl_seconds` | Lifetime. Defaults to 12 hours, at most 7 days, and never longer than the parent's. |
| `bound_refs` | References tied to the token, as `key: value` strings. |

A run token belongs to the same client as its parent, so it uses the same plan, limits and allowed models. It can't
create further tokens.

## Bound references

References bound at creation are **trusted**: an authenticated orchestrator set them, and the agent can't change them.
Harbor Pilot records them on every request the token makes.

| Key | Effect |
|---|---|
| `run`, `spaceman.run` or `ci.job` | The run reference on each usage entry, so spend adds up per run. |
| `session` | Groups the run's requests into one session, with high confidence. Find its session id with `ref=session:<value>`. |
| `project_id` | Charges the run to a project. It must be the client's project or one of its allowed projects. |
| anything else | Recorded as a trusted reference for reporting, for example `spaceman.space`. |

Every trusted reference, whatever its key, can filter and group usage and activity (see below).

A run token inherits the parent's references and the references configured on the client (under the client's
**References** setting). It can add to them, but it can't override either.

Headers the agent sends, such as `X-HP-Session` or `X-HP-External-Ref`, are hints. They are recorded as claims next to
the trusted references, and never decide access or billing.

<Shot name="request-run-refs" alt="A request made with a run token of the spaceman-runs client, with Verified context open: client, project, workspace, protocol and required capabilities, then the trusted references spaceman.run run_synthetic_2, spaceman.space spc_synthetic and spaceman.task tsk_synthetic_7" caption="A run token's request: its trusted references under Verified context" />

## Hand the token to the run

Give the run its token like any other tool, in the run's environment:

```sh
# Claude Code
ANTHROPIC_BASE_URL=https://pilot.example.com
ANTHROPIC_AUTH_TOKEN=hpk_…            # the run token

# Codex
HARBOR_PILOT_TOKEN=hpk_…
codex -c model_provider=harbor-pilot \
  -c model_providers.harbor-pilot.base_url=https://pilot.example.com/v1 \
  -c model_providers.harbor-pilot.wire_api=responses \
  -c model_providers.harbor-pilot.env_key=HARBOR_PILOT_TOKEN …
```

An agent with shell access can read anything in its environment, the run token included. That is why run tokens are
short-lived, limited to sending requests, and tied to one run: a leaked run token is worth one client's plan for a few
hours, and its requests are all labelled with the run.

## Revoke it

```sh
curl -X DELETE https://pilot.example.com/v1/harbor/credentials/crd_… \
  -H "Authorization: Bearer $PARENT_TOKEN"
```

Either the parent token or the run token itself may revoke a run token; the answer is `204 No Content`. Any other
token gets `404`.

## See the runs

Runs show up like any other client's traffic: under the orchestrator's client, in **Requests**, and in **Spend**
broken down by client and project.

To tell runs apart, filter and group by their trusted references through the API, with an API token (`hpa_…`) that has `usage.read`
and `activity.read`:

```sh
# Per-run totals in one space, one group per run (paged with cursor and limit)
curl -sS "https://pilot.example.com/api/v1/usage/summary?ref=spaceman.space:spc_alpha&group_by=ref:spaceman.run" \
  -H "Authorization: Bearer $API_TOKEN"

# One run's requests
curl -sS "https://pilot.example.com/api/v1/activity/requests?ref=spaceman.run:run_a" -H "Authorization: Bearer $API_TOKEN"

# The session id of a run's bound session
curl -sS "https://pilot.example.com/api/v1/activity/sessions?ref=session:run_a" -H "Authorization: Bearer $API_TOKEN"
```

Only trusted references count: headers an agent sends never move usage between runs. Add `binding_id=` when other
clients could use the same reference values. See [Usage and activity by reference](/reference/api#usage-and-activity-by-reference).

<Shot name="client" alt="A client page with recent requests, its access tokens and the setup steps for its tool" caption="The orchestrator's client: its requests and tokens" />

The demo has a `spaceman-runs` client with the **Create run tokens** permission and a `spaceman.space` reference, if you
want to try this locally. See [Try the demo](/guide/demo).

## With Terraform

The Terraform provider can create the Harbor Pilot side (project, client, plan and the parent token); its
`examples/scenarios/07-spaceman-integration` shows a complete setup. See
[Terraform](/operators/terraform).
