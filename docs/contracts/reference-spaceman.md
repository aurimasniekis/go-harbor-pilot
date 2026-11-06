# Reference: SpaceMan, and the proposed consumer contract

- **Source:** `aurimasniekis/go-spaceman` @ `e4a915a`. License Apache-2.0. Not released (README).
- **Role:** SpaceMan is **one consumer** of Harbor Pilot. It is not a dependency, and Harbor Pilot's identity
  model does not depend on it. The integration uses only generic primitives: client binding, gateway
  credential, project, and bound external references.
- **Scope rule:** we do not modify SpaceMan. The changes it would need are listed in §4 as proposals only.

## 1. What SpaceMan does today (verified-in-source)

**Architecture.**
- The central `spaceman` server owns state, API and UI.
- `spaceman-agent` runs on each node, dials central over WebSocket, and launches harnesses **interactively**
  in herdr-core terminals.
- Stack: Go 1.27.3, huma v2 API, casbin, OIDC, SQLite (modernc) with goose, embedded NATS + go-pulse.
- UI: React 19 + Mantine 8 (agent report).

**IDs** (`internal/ids/ids.go`). Each ID is a prefix plus a ULID. The prefixes are part of SpaceMan's public
contract and are never reused.

| Prefix | Entity | Prefix | Entity |
|---|---|---|---|
| `spc` | space | `run` | run |
| `ins` | agent instance | `ses` | harness conversation lineage |
| `rol` | space role | `nod` | node |
| `acc` | account | `tsk` | task |
| `wsp` | workspace | `rep` | repository |
| `rcr` | run credential | `lse` | lease |

SpaceMan has **no "project" entity**. A space is the unit of work.

**Harness launch** (`internal/harness/claude.go`, `codex.go`, `harness.go`, `internal/agent/helpers.go`, `exec.go`):
- **Claude Code.** The only secret env allowed is `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`
  (`claude.go:18,99`). The config dir is `CLAUDE_CONFIG_DIR` per space/instance. `--session-id <uuid>` is
  chosen by SpaceMan. `--mcp-config <run>/mcp.json --strict-mcp-config` loads only the SpaceMan bridge
  (agent report).
- **Codex.** Uses a per-instance `CODEX_HOME` and `-c mcp_servers.spaceman.*`. API keys are written to
  `auth.json`, because interactive Codex ignores `OPENAI_API_KEY` (ADR-0054, doc-claim). A ChatGPT login is a
  per-node `auth.json` with a lock (ADR-0053, doc-claim).
- **Environment scrubbing.** The launch shim strips every inherited variable that starts with `CLAUDE_CODE_`,
  `ANTHROPIC_` or `CODEX_SANDBOX` (`internal/agent/helpers.go:60`), then applies a 0600 secrets file.
  **Consequence:** an `ANTHROPIC_BASE_URL` placed in ordinary launch env is removed. A gateway URL must travel
  through the secret-env path.
- **Non-secret env** for every run (`internal/harness/harness.go:186-189`): `SPACEMAN_RUN_ID`, `SPACEMAN_SPACE_ID`,
  `HALLPASS_SESSION=<run_id>`, `SPACEMAN_MEMORY_DIR`.
- **Run credential.** `SPACEMAN_RUN_TOKEN` (`exec.go:637`) authenticates the agent to SpaceMan. It is not
  something Harbor Pilot can verify.

**Account kinds** (`internal/accounts/accounts.go:51-56`):

| Kind | Harness | Delivery |
|---|---|---|
| `claude_oauth_token` (from `claude setup-token`) | claude-code | env `CLAUDE_CODE_OAUTH_TOKEN` |
| `anthropic_api_key` | claude-code | env `ANTHROPIC_API_KEY` |
| `openai_api_key` | codex | env `OPENAI_API_KEY` |
| `codex_chatgpt_login` | codex | file (`CODEX_AUTH_JSON` payload key, max 64 KiB) |

- Spaces bind accounts. There is **no rotation and no quota awareness** (agent report).
- Usage is parsed from Claude transcripts only. Codex usage is reported as "unavailable", never zero.
- Budgets (`budget_tokens_24h`, cost) are soft admission checks (`limits/admit.go`, agent report).

**Gateway integration today:** none. "Gateway" in SpaceMan means its MCP gateway (`internal/gateway`).

**Events** (`internal/outbox/catalog.go`):
- Subjects are `evt.spaceman.<area>.<entity>.<action>`; the CloudEvents type is
  `spaceman.<area>.<entity>_<action>.v<N>`. The grant is `publish:evt.spaceman.>`.
- Run events: `fleet.run.launched`, `fleet.run.started`, `fleet.run.ended`, all v1.
  - Payload: `space_id, run_id, node_id, kind` (control/worker/kb/dreamer/diagnostic), `role, harness, instance_id,
    lease_generation`.
  - `launched` adds `adopted`. `ended` adds `state` (exited/failed/lost), `end_reason`, `exit_code`.
  - Prompts, error text and terminal output are never included.

**Threat-model fact** (SpaceMan LIMITATIONS, doc-claim): agents have shell access and can reach the network
directly. Anything in the run's env, a gateway token included, is readable by the agent.

## 2. Proposed contract (Harbor Pilot side, implemented)

The design is inferred from the facts above; `TestScenarioN_SpaceManAndStandaloneClientCoexist` covers it.

1. **A SpaceMan installation is a client binding.**
   - An operator creates a binding `spaceman-<instance>`.
   - The binding has a service principal, a routing plan, an optional project, permissions and limits, and
     the permission `credentials.mint_child`.
   - SpaceMan central holds that binding's **service credential**. Agents never see it.
2. **Run-scoped child credentials.** At dispatch, SpaceMan central calls
   `POST /api/v1/client-bindings/{id}/child-credentials` with:
   ```json
   {"external_refs": {"spaceman.space": "spc_…", "spaceman.run": "run_…", "spaceman.instance": "ins_…"},
    "project_id": "prj_…", "expected_harness": "claude-code", "ttl_seconds": 43200,
    "limits": {"max_concurrency": 2, "budget": {"amount": "5.00", "currency": "USD"}},
    "allowed_aliases": ["coding/default", "coding/fast"]}
   ```
   - The response is `{credential_id, token, expires_at}`. The token is shown once.
   - The child credential can only **narrow** its parent: aliases, budget, TTL and permissions are never
     wider.
   - `external_refs` are **trusted**, because they were bound at mint time by an authenticated principal.
     They become accounting dimensions: project → run → session → request → attempts.
3. **Revocation.**
   - `DELETE /api/v1/credentials/{id}` revokes a child.
   - TTL expiry is a backstop.
   - Optionally, a Harbor Pilot Pulse consumer revokes children on `evt.spaceman.fleet.run.ended`. This needs
     an explicitly configured subscription and is off by default.
4. **Untrusted correlation hints.** These are used only for display and session grouping, never for
   authorization or the billing scope:
   - `X-HP-External-Ref` and `X-HP-Session` (sent via Claude `ANTHROPIC_CUSTOM_HEADERS` or Codex `http_headers`).
   - Native `x-claude-code-session-id`, which equals SpaceMan's `--session-id`.

   If a hint disagrees with a bound ref, the **bound ref wins** and the mismatch is recorded.
5. **Read APIs.**
   - `GET /api/v1/usage?external_ref=spaceman.run:run_…`: tokens and costs per model, with provenance and
     unknown/partial flags. This fills SpaceMan's Codex usage gap.
   - `GET /api/v1/quota/headroom?binding=…`: lets SpaceMan refuse a launch early.
6. **Events out:**
   - `evt.harborpilot.accounting.run_usage.finalized` (when a child credential expires or is revoked).
   - `evt.harborpilot.quota.window.exhausted`.
   - `evt.harborpilot.routing.request.blocked`.

   SpaceMan can ingest these through its existing Pulse inbound subscriptions.
7. **Isolation (scenario N).** A standalone client and SpaceMan runs must not share sessions, accounting rows
   or traces unless they share a binding. Tests assert that a token from binding A cannot read or affect B.

## 3. Harness wiring for SpaceMan runs (inferred)

| Harness | Values SpaceMan would deliver (secret env unless noted) |
|---|---|
| Claude Code | `ANTHROPIC_BASE_URL=<hp>/` (Anthropic Messages namespace), `ANTHROPIC_AUTH_TOKEN=<child token>`, optional `ANTHROPIC_CUSTOM_HEADERS="X-HP-External-Ref: run_…"` |
| Codex | CLI overrides `-c model_provider=harbor -c model_providers.harbor.base_url=<hp>/v1 -c model_providers.harbor.wire_api=responses -c model_providers.harbor.env_key=HARBOR_PILOT_TOKEN`, plus secret env `HARBOR_PILOT_TOKEN=<child token>` |

## 4. Changes SpaceMan would need (proposal only, not implemented)

1. A new account kind, e.g. `harbor_pilot_gateway`, holding the binding's service credential. The credential
   stays on central and is never delivered to nodes.
2. A mint at dispatch, with the child token delivered via secret env. Extend `secretEnv` in
   `internal/harness/claude.go` to allow `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` and
   `ANTHROPIC_CUSTOM_HEADERS`.
3. Codex launch: add the `-c model_providers.harbor.*` overrides and the `HARBOR_PILOT_TOKEN` secret env.
4. Revoke on run end (or rely on TTL).
5. Optional: use the Harbor Pilot usage API as the authoritative usage source in admission checks.

The Terraform example `examples/spaceman` (provider repo) creates only the Harbor Pilot side: project, binding,
plan and service credential.
