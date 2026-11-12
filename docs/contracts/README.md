# Contracts

Harbor Pilot's own published contracts, and the verified external contracts it depends on.

## Harbor Pilot's contracts

| File | Subject |
|---|---|
| [gateway.md](gateway.md) | Data plane: endpoints, token transport, hints, pipeline, streaming, translation |
| [events.md](events.md) | Event envelope, signatures and catalog (generated from `internal/events/catalog.go`) |
| [config.md](config.md) | Managed configuration: authority, revisions, import/export, the Terraform-facing contract |

The control-plane API contract is the generated [`api/openapi.yaml`](../../api/openapi.yaml).

## External references

| File | Subject | Kind |
|---|---|---|
| [reference-quota-harbor.md](reference-quota-harbor.md) | Reused packages, importer schema, UI stack and tokens | Reused code (same owner) |
| [reference-hallpass-auth.md](reference-hallpass-auth.md) | Human authentication parameters re-implemented | Patterns only |
| [reference-porch-oidc.md](reference-porch-oidc.md) | Porch OpenID Provider contract for SSO | Integration |
| [reference-pulse.md](reference-pulse.md) | Pulse/NATS event contract and the subject mapping | Integration (imported SDK) |
| [reference-spaceman.md](reference-spaceman.md) | SpaceMan and the consumer contract | Integration (consumer) |
| [reference-magpie-observations.md](reference-magpie-observations.md) | Design observations from a clean-room study | Design notes only |
| [harness-wire-facts.md](harness-wire-facts.md) | How Claude Code, Codex, OpenCode and Pi reach a custom gateway | Integration (clients) |

## Snapshots

| Source | Commit or version | Checked |
|---|---|---|
| aurimasniekis/go-quota-harbor | `1bf2acb` | 2026-11-05 |
| aurimasniekis/go-hallpass | `cc83dba` (v0.7.4) | 2026-11-05 |
| aurimasniekis/go-porch | `d92cdb0` (v0.3.1) | 2026-11-05 |
| aurimasniekis/go-pulse | `c4689c8` (v0.1.5) | 2026-11-05 |
| aurimasniekis/go-spaceman | `e4a915a` | 2026-11-05 |
| yetone/magpie | `7c1dc19` | 2026-11-09 (prompt-cache handling in the CLI bridge; earlier sections at `bd0f338`, 2026-11-05) |
| Claude Code CLI | 2.1.318, 2.1.321, 2.1.322 | 2026-11-09 (prompt caching, harness-wire-facts §1.4); 2.1.322 2026-11-12 (session entries, §1.5) |

When a source moves past its snapshot, re-verify a fact before relying on it, then update the row.

## Labels

Every factual claim carries one:

- **verified-in-source**: read at the snapshot above (path given), or seen in a local binary's own output
  (`claude --help`).
- **doc-claim**: stated in a README, ADR, guide or comment, not confirmed by running or tracing code.
- **inferred**: our reading of how pieces combine; confirm when implementing.

Verified-in-source describes code, not a live deployment. A live result goes into
[`../COMPATIBILITY.md`](../COMPATIBILITY.md) only after a recorded live run, with date, version and script.

## Rules

1. Never invent endpoints, headers, claims or flags. Untraceable facts are labelled `doc-claim` or `inferred`, or
   left out.
2. Reference repositories are read-only; changes an integration would need are written here as proposals.
3. Reference projects are named only in `docs/` and the named integrations (Pulse relay, Porch-compatible OIDC, Quota
   Harbor importer, SpaceMan examples). Code never carries "ported from" comments.
4. The Magpie study is clean-room input: no code, fixtures or captured payloads are copied.
