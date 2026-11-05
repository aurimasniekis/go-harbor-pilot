# Contracts

This directory records **verified external contracts** that Harbor Pilot depends on or integrates with,
plus Harbor Pilot's own published contracts (gateway, events, configuration, Terraform) as they are
written.

## Index

| File | Subject | Kind |
|---|---|---|
| [reference-quota-harbor.md](reference-quota-harbor.md) | Quota Harbor: reused packages, schema for the importer, UI stack and design tokens | Reference (reused code, same owner) |
| [reference-hallpass-auth.md](reference-hallpass-auth.md) | Human authentication parameters that are re-implemented | Reference (patterns only) |
| [reference-porch-oidc.md](reference-porch-oidc.md) | Porch OpenID Provider contract for SSO | Integration (external system) |
| [reference-pulse.md](reference-pulse.md) | Pulse/NATS event contract and Harbor Pilot's subject mapping | Integration (imported SDK) |
| [reference-spaceman.md](reference-spaceman.md) | SpaceMan today and the proposed consumer contract | Integration (consumer) |
| [reference-magpie-observations.md](reference-magpie-observations.md) | Design observations from an isolated lab study (clean room) | Design notes only |
| [harness-wire-facts.md](harness-wire-facts.md) | How Claude Code, Codex, OpenCode and Pi reach a custom gateway | Integration (clients) |

Harbor Pilot's own contracts are added here as they are implemented:
`gateway.md`, `api.md`, `events/`, `config.md` and `terraform.md`.

## Reference snapshots

| Repo | Commit | Checked |
|---|---|---|
| aurimasniekis/go-quota-harbor | `1bf2acb` | 2026-11-05 |
| aurimasniekis/go-hallpass | `cc83dba` (tag v0.7.4) | 2026-11-05 |
| aurimasniekis/go-porch | `d92cdb0` (tag v0.3.1) | 2026-11-05 |
| aurimasniekis/go-pulse | `c4689c8` (tag v0.1.5) | 2026-11-05 |
| aurimasniekis/go-spaceman | `e4a915a` | 2026-11-05 |
| yetone/magpie | `bd0f338` | 2026-11-05 |
| Claude Code CLI | 2.1.318 (local binary) | 2026-11-05 |

## Verification legend

Every factual claim carries one of these labels.

- **verified-in-source**: read in the referenced repo at the commit above (file path given), or observed
  from a locally installed binary's own output (e.g. `claude --help`).
- **doc-claim**: stated in a README, ADR, guide or code comment but not confirmed by executing or tracing code.
- **inferred**: our reading of how pieces combine; it needs confirmation at implementation time.

A **verified-in-source** fact describes what the code does. It does **not** prove the behaviour of a live
deployment. Live behaviour goes in [`../COMPATIBILITY.md`](../COMPATIBILITY.md) only after an opt-in live
verification run (`scripts/live/`), recorded with date and version.

## Evidence policy

1. Never invent endpoints, headers, claims or flags. If a fact cannot be traced to source or binary
   output, label it `doc-claim` or `inferred`, or leave it out.
2. Reference repositories are read-only. Changes they would need for an integration are written down
   here as proposals, never applied.
3. Reference projects are named only in `docs/` and in named integration features (Pulse publisher,
   Porch OIDC compatibility, Quota Harbor importer, SpaceMan example). Code never carries
   "ported from" comments.
4. The Magpie study is a clean-room design input. No code, fixtures or captured payloads are copied.
   Harbor Pilot's fixtures are written from scratch or are synthetic.
5. When a reference repo moves past the commit above, re-verify before relying on a changed fact, then
   update the table.
