# Privacy and masking

A **masking policy** decides what Harbor Pilot removes from a request before a provider sees it. Credentials, your
own secret values and, if you want, personal data are replaced with placeholders such as `HPSEC_…`. When the reply
comes back, the originals can be put back, so your tool sees the real values while the provider only ever saw
placeholders.

Masking is part of a **routing plan**. The plan names a masking policy and, separately, a content capture rule (see
[Captured content](/operators/captured-content)). Every client on that plan gets both.

<Shot name="privacy" alt="Privacy page with the Masking tab open, listing the mask-secrets policy, what happens when masking can't run, what it masks and the plan that uses it" caption="Privacy → Masking: each policy, what it masks and which plans use it" />

The line under the page title tells you how far masking reaches right now, for example *Masking is on for 1 of 4
clients. Content capture is on for Private only (kept 3 days, masked).* The table shows, per policy:

| Column | Meaning |
|---|---|
| **When masking can't run** | What happens to a request the policy can't be applied to (see below). |
| **Masks** | A summary of the detectors that are on. |
| **Unmask in replies** | Whether original values are put back in the reply. |
| **Used by** | The plans that use the policy. A policy no plan uses masks nothing. |

::: info Status
Masking is **contract-tested**: detectors, rule precedence, placeholders, restoring across stream chunks and guarded
tool-argument restore all have tests (see [Compatibility](/reference/compatibility)). Read
[Masking: guarantees and limits](/security/masking) before you rely on it for regulated data.
:::

## How a request is masked

```mermaid
flowchart LR
  A[Request from a client] --> B{Plan has a<br/>masking policy?}
  B -- no --> U[Sent as is]
  B -- yes --> C[Detectors and custom rules<br/>scan the text]
  C --> D{Any match with<br/>action Block?}
  D -- yes --> X[Request refused]
  D -- no --> E[Matches replaced with<br/>HPSEC_ placeholders]
  E --> P[Provider]
  P --> R[Reply]
  R --> F{Unmask in replies?}
  F -- yes --> G[Placeholders from this<br/>session put back]
  F -- no --> H[Reply as is]
```

When several rules match the same text, **Block** wins over an exception, an exception wins over **Mask**, and
**Mask** wins over **Warn**. An exception never overrides a block.

## Add a masking policy

**Privacy → Masking → Add masking policy.**

<Shot name="privacy-policy" alt="Add masking policy dialog with a short name, the choice of what happens when masking can't run, and detector tables for secrets and personal data with an action per detector" caption="A masking policy: what to do when masking can't run, then which detectors to use" />

1. Give it a **Short name** (used in plans and the API) and a **Description**.
2. Choose **When masking can't run** (next section).
3. Pick detectors and their actions.
4. Optionally add configured secret values, custom rules and exceptions.
5. Decide whether replies are unmasked.
6. Save, then open your plan in **Routing** and choose the policy on the plan's **Privacy** tab. Publish the plan.

### When masking can't run

Masking can fail to run, for example on a byte-for-byte passthrough route (see [Diagnostics](/operators/diagnostics)),
or when stored secret values can't be read. The policy says what happens then:

| Choice | What happens |
|---|---|
| **Block the request** | Recommended. Nothing leaves Harbor Pilot unmasked. The client gets an error. |
| **Send unmasked and flag it** | Keeps tools working during an incident. Each such request is marked in **Requests**, so you can find them afterwards. |
| **Send anyway** | Not recommended. The request goes out unmasked without a mark. |

Passthrough forwards the client's original bytes, so it can't mask. A plan switches a route to passthrough only if its
compatibility settings pre-authorize losing masking *and* its policy is not **Block the request**. Otherwise that
candidate is skipped. See [Diagnostics](/operators/diagnostics#what-a-plan-does-while-a-breaker-is-open).

### Detectors

Each detector has an action: **Mask**, **Block** or **Warn**. **Select all** and **none** work per table.

**Secrets and credentials**

| Detector | Catches |
|---|---|
| Private keys | PEM private key blocks |
| AWS access keys, AWS secret keys | AWS credential pairs |
| GitHub tokens | Classic tokens and fine-grained `github_pat_…` tokens |
| Anthropic, OpenAI, Google API, Stripe and Slack keys | The vendors' key formats |
| JWTs | JSON Web Tokens |
| Secret assignments | Values after `password=`, `api_key:`, `token=` and similar |
| Credential URLs | The password part of `scheme://user:password@host` |
| Harbor Pilot tokens | Harbor Pilot's own gateway and API tokens |
| Configured secret values | The exact values you list in the policy (below) |

**Personal data (optional)**

| Detector | Catches |
|---|---|
| Email addresses | `name@example.com` |
| Phone numbers | Numbers with at least 9 digits, with an optional `+` country code |
| Credit card numbers | Card numbers that pass the Luhn check |
| IBANs | Account numbers that pass the mod-97 check |
| IP addresses | IPv4 addresses |

**Heuristics**

| Detector | Catches |
|---|---|
| High-entropy strings | Long random-looking strings. It **warns** by default, because it also matches hashes and IDs. |

A **Warn** match is recorded on the request but nothing is changed.

### Configured secret values

List exact values that must never leave, one per line, at least 6 characters each: internal hostnames, a database
password, a customer's name. You can:

- **Store values (encrypted).** They are sealed with the master key and never shown again. Later edits offer **Keep
  the stored values** (you see only a fingerprint), **Replace the stored values** or **Remove**.
- **Read from an environment variable or file.** For example `file:/run/secrets/mask-values`, one value per line, or
  `env:HP_SECRET_MASK_VALUES`. Only variables with an allowed prefix (by default `HP_SECRET_`) can be used.
- **None.**

If the values can't be read when a request arrives, that is a case of "masking can't run".

### Custom rules

**Custom rules** add your own patterns. Each has a **Name**, a **Pattern** (Go RE2 syntax), the **Part to mask** (the
whole match, `0`, or a capture group `1`–`9`) and an **Action**: **Mask**, **Block the request** or **Warn only**.

For example, mask only the number in `CUST-123456`:

| Name | Pattern | Part to mask | Action |
|---|---|---|---|
| `customer-id` | `CUST-([0-9]{6,})` | 1 | Mask |

### Exceptions

**Exceptions** stop a detection from being masked, for example a documentation hostname that looks like a credential
URL. The **Pattern** must match the *whole* detection, and **Only for** limits the exception to some detectors.
Exceptions never override a block.

## Putting values back in replies

**Put the original values back in text returned to the same client** turns placeholders in the reply back into the
original values. Only placeholders created for the same client and session are restored. A placeholder that came from
another session stays a placeholder. Restoring works on streamed replies too, even when a placeholder is split across
chunks.

### Tool-call arguments

**Also put them back in tool-call arguments** is off by default. Turn it on only when a tool really needs the real
value, for example a deploy tool that needs the hostname. The dialog shows a warning, because this hands secrets to
whatever runs the tool. You must name:

- **Tools allowed to receive originals** (required, comma separated). Other tools get placeholders.
- **Argument fields**: only these fields are restored. Leave empty for all fields of the listed tools.
- **Hosts allowed inside URLs**: a value inside a URL is restored only if the URL points at one of these hosts. Values
  are never restored inside URLs to hosts you didn't list.

## Try a policy on sample text

**Privacy → Try a policy on sample text** (and the same panel inside the policy dialog) runs a policy against text you
paste and shows what would be masked, blocked or warned, and what the provider would see. Nothing is sent to a provider, and
the sample is neither stored nor logged. Use synthetic samples where you can. Trying a saved policy that has stored secret values needs the `secrets.write` permission,
because the result would reveal whether a value matches.

## With Terraform

```hcl
resource "harborpilot_privacy_policy" "strict" {
  name = "strict"
  spec = jsonencode({
    description       = "Mask credentials, keys and customer identifiers"
    required          = true # "Block the request" when masking can't run
    rules             = [{ name = "customer-id", pattern = "CUST-[0-9]{6,}", action = "mask" }]
    allow             = [{ name = "docs-host", pattern = "docs\\.corp\\.example\\.com" }]
    secret_values_ref = "file:/etc/harbor-pilot/secrets/mask-values.txt"
  })
}
```

See [Terraform](/operators/terraform).

## Related

- [Masking: guarantees and limits](/security/masking): what masking can and can't promise.
- [Captured content](/operators/captured-content): keeping masked copies of requests for debugging.
- [Routing plans](/operators/routing): where a policy is attached.
