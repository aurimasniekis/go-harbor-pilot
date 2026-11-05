# Demo Mode

`harbor-pilot serve --demo [--demo-clock 2026-11-05T12:00:00Z]` serves a deterministic, clearly synthetic dataset.

- **Separate files.** Demo mode uses its own database files (`demo.db`, `demo-traces.db`) and a throwaway master key (`demo-master.key`) in the data directory. It never reads production credentials.
- **Synthetic identities.** All seeded identities use the reserved `example.test` domain:
  - `admin@example.test` (admin)
  - `viewer@example.test` (viewer)
  - Both use the password `harbor-pilot-demo`. This credential is intentionally public and works only in demo databases.
  - A service principal `demo-terraform`, marked `managed_by=terraform`.
- **Frozen clock.** `--demo-clock` freezes time, so UI output and end-to-end tests are reproducible.
- **Banner.** The UI shows a persistent "Demo data" banner.

Later phases add synthetic clients, providers, accounts, routing plans and gateway traffic through the same real code paths, with no special-case UI.

The demo administrator's password is published (`admin@example.test` / `harbor-pilot-demo`), so demo mode refuses to listen on anything but a loopback address. Pass `--demo-allow-public` (or set `demo.allow_public`) only on an isolated network.
