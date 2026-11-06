# Developer documentation

These files are for people working on Harbor Pilot's code. Operator documentation (installing, configuring,
connecting tools, security model, reference) is the website in [`site/`](../site), published at
https://aurimasniekis.github.io/go-harbor-pilot/.

| File | What it holds | Keep current when |
|---|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | What Harbor Pilot does, the evidence vocabulary, topology, packages, domain model, request lifecycle, storage, capacity | Structure changes |
| [DECISIONS.md](DECISIONS.md) | Decisions D-001 to D-039 and their reasons | A decision is made or superseded (add an entry; never reuse an id) |
| [THREAT_MODEL.md](THREAT_MODEL.md) | Assets, actors, threats, mitigations, residual risk | A trust boundary or mitigation changes |
| [LIMITATIONS.md](LIMITATIONS.md) | Deliberate limits and external blockers | Behaviour changes a limit (also `site/security/limitations.md`) |
| [COMPATIBILITY.md](COMPATIBILITY.md) | Status of every protocol, translation, harness, executor and quota adapter, with evidence | A status changes (also `site/reference/compatibility.md`) |
| [TRACEABILITY.md](TRACEABILITY.md) | Every requirement with its status and evidence | A requirement's status or evidence changes |
| [SCREENSHOTS.md](SCREENSHOTS.md) | Every documentation screenshot: where it is shown, what it captures, whether it is current | A UI area changes (recapture with `make screenshots`) or a page adds or drops a shot |
| [contracts/](contracts/README.md) | Harbor Pilot's gateway, event and configuration contracts, and verified external references | A contract changes |

Status words are defined once, in ARCHITECTURE.md ("Evidence vocabulary"). Never mark anything `live-verified`
without a recorded run (date, version, script).

The control-plane API is described by the generated [`api/openapi.yaml`](../api/openapi.yaml) (`make gen`).
