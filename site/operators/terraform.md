# Terraform

The Harbor Pilot Terraform provider manages configuration as code: providers and accounts, fallback groups, model
names, routing plans and their versions, clients and their tokens, quota tracking and budgets, masking policies and
capture rules, alert channels and rules, event relays, automation accounts and sign-in settings.

It talks only to the control-plane API (`/api/v1`), with an API token, like any other client. It never reads the
database, and it never manages runtime data: requests, usage, captured content and alert history stay out of it.

Its acceptance tests run each resource and the seven example scenarios against a real `harbor-pilot` server built from
the same release line, so its status is **contract-tested**.

## Install

The provider is on the Terraform Registry as
[`aurimasniekis/harbor-pilot`](https://registry.terraform.io/providers/aurimasniekis/harbor-pilot/latest). You need
Terraform 1.0 or later; write-only secrets need 1.11, and the ephemeral token needs 1.10.

```hcl
terraform {
  required_providers {
    harborpilot = {
      source  = "aurimasniekis/harbor-pilot"
      version = "~> 0.1"
    }
  }
}
```

Then run `terraform init`. Use the provider version that matches your server's minor version: provider 0.1 works with
Harbor Pilot 0.1.

### Build it from source

To work on the provider, or to run an unreleased version, you need Go 1.27:

```sh
git clone https://github.com/aurimasniekis/terraform-provider-harbor-pilot.git
cd terraform-provider-harbor-pilot
make build          # writes ./bin/terraform-provider-harbor-pilot
```

Tell Terraform to use the local build, in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "aurimasniekis/harbor-pilot" = "/path/to/terraform-provider-harbor-pilot/bin"
  }
  direct {}
}
```

With `dev_overrides` you skip `terraform init` for this provider; Terraform prints a warning on every run that the
override is active.

## Authenticate

The provider uses an **automation account** and its API token, never a person's session.

1. In Harbor Pilot, open **Settings → API tokens → New automation account**.
2. Choose the **Terraform** preset: it reads and changes configuration, publishes plans, sets secrets and SSO. Or pick
   **Custom** and give only what your configuration manages.
3. Issue a token (for example named `terraform-ci`, expiring in 90 days) and copy it. It is shown once.

<Shot name="api-tokens" alt="Settings, API tokens tab, with the demo-terraform automation account marked Terraform, its Change permissions and its creation time" caption="An automation account for Terraform, with its own API tokens" />

Then configure the provider through the environment, the same variables the `harbor-pilot` CLI reads:

```sh
export HP_SERVER=https://pilot.example.com
export HP_API_TOKEN_FILE=~/.config/harbor-pilot/terraform.token   # or HP_API_TOKEN
```

```hcl
provider "harborpilot" {}
```

Or explicitly:

```hcl
provider "harborpilot" {
  url          = "https://pilot.example.com"
  token_file   = "/run/secrets/harbor-pilot-token"
  ca_cert_file = "/etc/ssl/internal-ca.pem"
  timeout      = "30s"
}
```

| Argument | Default | Meaning |
|---|---|---|
| `url` | `$HP_SERVER` | Server origin. |
| `token_file` | `$HP_API_TOKEN_FILE` | File holding the API token (`hpa_…`). Preferred. |
| `token` | `$HP_API_TOKEN` | The token itself. A token written here ends up in plan files. |
| `ca_cert_file` | system roots | PEM bundle to verify the server certificate. |
| `insecure_skip_verify` | `false` | Skip TLS verification. For tests only. |
| `timeout` | `30s` | Per-request timeout, at most `5m`. |

The token and request and response bodies are never logged.

## What it manages

Each resource is one Harbor Pilot object. The middle column is the name you see in the UI.

| Resource | In the UI |
|---|---|
| `harborpilot_workspace`, `harborpilot_project` | Settings → Workspaces & projects |
| `harborpilot_provider` | Providers |
| `harborpilot_provider_account` | A provider's accounts |
| `harborpilot_subscription_entitlement` | Subscriptions |
| `harborpilot_pool` | Routing → Fallback groups |
| `harborpilot_model_alias` | Routing → Model names |
| `harborpilot_routing_plan` | Routing → Plans |
| `harborpilot_routing_plan_version` | A plan's version: draft, validate, publish |
| `harborpilot_client_binding` | Clients |
| `harborpilot_gateway_credential` | A client's access tokens |
| `harborpilot_quota_monitor` | Limits → quota tracking |
| `harborpilot_budget` | Limits → budgets (also one-time budgets, `period = "once"`) |
| `harborpilot_privacy_policy` | Privacy → Masking |
| `harborpilot_trace_policy` | Privacy → Content capture |
| `harborpilot_event_destination` | Alerts → Event relays |
| `harborpilot_notification_channel`, `harborpilot_notification_rule` | Alerts → Channels, Rules |
| `harborpilot_service_principal`, `harborpilot_api_token` | Settings → API tokens |
| `harborpilot_auth_settings`, `harborpilot_oidc_provider` | Settings → Sign-in & SSO |

There is a data source for each kind, looked up by name or id, plus `harborpilot_quota_adapters` (the quota tracking
adapters this server supports) and `harborpilot_event_catalog` (every event kind).

## How specs work

Most resources take a `spec`: a JSON document, written with `jsonencode`, in the same shape the API takes.

```hcl
resource "harborpilot_provider" "anthropic" {
  name = "anthropic"
  spec = jsonencode({ type = "anthropic", display_name = "Anthropic" })
}

resource "harborpilot_provider_account" "team" {
  name = "anthropic-team"
  spec = jsonencode({
    provider_id    = harborpilot_provider.anthropic.id
    billing        = "metered_api"
    credential_ref = "env:HP_SECRET_ANTHROPIC_API_KEY" # resolved on the server, never in state
    enabled        = true
  })
}
```

- **Defaults.** Properties you leave out are sent empty (an empty string or list, `0`, `false`). `enabled` must always
  be set. The server validates every spec, and an error names the exact property, for example `spec.members[0].model`.
- **No phantom diffs.** The server stores a canonical form (defaults added, lists sorted). The provider remembers it
  from the last apply, so a second `terraform plan` is empty.
- **Drift.** Objects Terraform manages are marked **Managed by Terraform** in the UI ("edits here are reverted by the
  next apply"). If someone changes one in the UI, the API or the CLI anyway, the object is marked **Changed outside
  Terraform**, and the next plan shows the difference until Terraform writes it again.
- **No silent overwrites.** Updates and deletes send `If-Match` with the revision Terraform last read. If the object
  changed in between, the server refuses (HTTP 412) and the provider says so. Run a new plan.
- **References.** A delete waits up to two minutes while other objects still refer to the one being deleted, then
  lists what still refers to it.
- **Import** works by id or by name: `terraform import harborpilot_provider.anthropic anthropic`.

### Routing plan versions

`harborpilot_routing_plan` is the plan's name and settings. Its routing logic lives in `harborpilot_routing_plan_version`,
in `document`. With `publish = true` (the default), applying validates and publishes the version. Published versions
are immutable, so changing `document` publishes a new version. If someone publishes another version in the UI, the
next plan offers to publish this one again: a rollback to what the code says.

```hcl
resource "harborpilot_routing_plan_version" "default" {
  plan_id = harborpilot_routing_plan.default.id
  note    = "Subscription first, API fallback"
  document = jsonencode({
    default_alias_id = harborpilot_model_alias.coding.id
    fallback = {
      steps               = ["retry", "alternate_account", "stop"]
      max_attempts        = 3
      allow_paid_fallback = true
    }
    privacy = {
      policy_id       = harborpilot_privacy_policy.strict.id
      trace_policy_id = harborpilot_trace_policy.three_days.id
    }
  })
}
```

### One-time budgets

A budget with `period = "once"` is a total that never resets. Managing one needs a provider release that knows
`starts_at` and `ends_at`; an older provider or server rejects `period = "once"` with a field error.

```hcl
resource "harborpilot_budget" "trial" {
  name = "trial-credit"
  spec = jsonencode({
    scope     = "project"
    scope_id  = harborpilot_project.checkout.id
    period    = "once"
    limit     = "10"
    currency  = "USD"
    starts_at = "2026-11-01T00:00:00Z" # optional; empty counts from creation
    ends_at   = "2027-03-31T23:59:59Z" # optional; empty never ends
    enabled   = true
  })
}
```

- Leave `timezone` out: it must be empty for one-time budgets.
- With `starts_at` empty, spend counts from when the budget was created, so a replacement starts a new count. Set
  `starts_at` when the count must survive a replacement.
- **Start over** in the UI (or `POST /api/v1/budgets/{id}/reset`) changes `starts_at` on the server, which shows as
  drift; the next plan offers to set it back. Change `starts_at` in your configuration instead.

## Secrets stay out of state

Terraform's `sensitive` flag hides a value in output but still writes it to state. The provider avoids putting
secrets in state at all:

1. **Secret references (preferred).** Fields such as `credential_ref`, `api_key_ref`, `secret_ref` and `creds_ref`
   name an environment variable or a file on the **server**: `env:HP_SECRET_ANTHROPIC_API_KEY` or
   `file:/run/secrets/anthropic`. The server reads it when it needs it; the secret never passes through Terraform.
   Environment variables need an allowed prefix, by default `HP_SECRET_`.
2. **Write-only arguments (Terraform 1.11+).** `secrets_wo` (and `client_secret_wo` on OIDC providers) is sent to the
   server but never stored in plan or state. Terraform can't see changes to a write-only value, so bump
   `secrets_wo_version` to rotate.

   ```hcl
   resource "harborpilot_provider_account" "stored" {
     name = "anthropic-stored-key"
     spec = jsonencode({
       provider_id = harborpilot_provider.anthropic.id
       billing     = "metered_api"
       enabled     = true
     })
     secrets_wo         = { credential = var.anthropic_api_key } # an ephemeral or sensitive variable
     secrets_wo_version = 1                                      # bump to rotate
   }
   ```
3. **Tokens.** `harborpilot_gateway_credential` and `harborpilot_api_token` issue tokens but do not keep them in state
   unless you set `store_token_in_state = true`. Issue the token you hand to a person with `harbor-pilot token issue`
   instead.
4. **Data sources** never return secret values, only which secrets are set.

### Ephemeral gateway token

`harborpilot_gateway_token` (Terraform 1.10+) issues a short-lived gateway token for a client while Terraform runs and
revokes it when the run ends. Use it to hand a token to another provider or a write-only argument without it ever
reaching state.

```hcl
ephemeral "harborpilot_gateway_token" "relay" {
  binding_id  = harborpilot_client_binding.relay.id
  name        = "relay"
  ttl_seconds = 900 # default 3600, at most 86400
}

resource "harborpilot_notification_channel" "relay" {
  name = "relay-webhook"
  spec = jsonencode({
    type        = "webhook"
    webhook_url = "https://relay.example.com/notify"
    enabled     = true
  })
  secrets_wo         = { secret = ephemeral.harborpilot_gateway_token.relay.token }
  secrets_wo_version = 1
}
```

## Examples

The provider repository's `examples/scenarios` has seven complete configurations. Each is applied to a real server by
the acceptance tests.

| Scenario | What it sets up |
|---|---|
| `01-personal-claude-code` | One Claude subscription through the Claude Code bridge, its usage windows tracked, exposed to Claude Code. |
| `02-shared-subscription-pools` | Several tools sharing Claude and ChatGPT subscription pools. |
| `03-project-budgets-api-fallback` | Project budgets, with metered API keys as the fallback. |
| `04-privacy-3-day-traces` | Required masking with custom rules and secret values, and masked captures kept 3 days. |
| `05-availability-first-passthrough` | A plan that prefers staying up: native passthrough with pre-authorized losses. |
| `06-pulse-events-notifications` | A Pulse relay, a signed webhook relay and alert rules. |
| `07-spaceman-integration` | Run-scoped tokens for the SpaceMan agent orchestrator. |

Every resource also has a small example under `examples/resources/`.

## Related

- [People, API tokens and sign-in](/operators/access): automation accounts and their permissions.
- [Secrets and the master key](/security/secrets): how stored secrets and references work.
- [Control-plane API](/reference/api): the schemas `spec` follows.
