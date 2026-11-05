# AGENTS.md: working in go-harbor-pilot

Harbor Pilot is a self-hosted LLM gateway and policy control plane: a single Go binary on SQLite, with an embedded React UI. Read `docs/PRODUCT.md` and `docs/ARCHITECTURE.md` before changing anything structural.

## Ground rules
- **Truthful status.** Never mark a capability `live-verified` without a recorded run, giving date, version and script. Mocks and fixtures earn at most `contract-tested`. Keep `docs/TRACEABILITY.md`, `docs/COMPATIBILITY.md` and `docs/LIMITATIONS.md` current in the same change.
- **No other project names in code.** Comments, identifiers and strings must not reference external or reference projects. The exceptions are named integration features: the Pulse relay, Porch-compatible OIDC, the Quota Harbor importer, and the SpaceMan examples. Reference material lives in `docs/contracts/`.
- **Clean-room protocol work.** Fixtures under `testdata/protocol/` are synthetic, written from public API documentation. Never paste captured real prompts or third-party fixtures.
- **Secrets never leave the secret path.** Don't log them, put them in errors, events, metric labels or test output. Use `credentials.Secret`. Read CLI secrets from stdin, a file or the environment, never from argv.
- **Hot path stays lean.** Gateway request handling must not block on telemetry, traces, events or notifications. Use the bounded channels, or the outbox inside the accounting transaction.
- **Database is the configuration authority.** Every mutation goes through `internal/controlplane` services: validate, check `If-Match`, write, audit, outbox, recompile the snapshot.

## Commands
```
make check        # go vet, golangci-lint, gofmt, go test -race, web typecheck/lint/test/build
make test-go      # go test ./...
make fuzz-smoke   # each fuzz target for 30s
make gen          # regenerate api/openapi.yaml and web types (commit the result)
make demo         # serve --demo on 127.0.0.1:8080
make e2e          # Playwright against the demo binary
```

## Conventions
- **Go version and dependencies.** Go 1.27. Use the standard library where practical. Tests use stdlib `testing` with table-driven `t.Run` subtests, `httptest` fakes and `clock.Fake`. No testify, no mock generators.
- **SQL and storage.** SQL is hand-written in `internal/store`. Migrations are append-only: name them `migrations/<db>/<17-digit timestamp>_<snake>_up.sql`, and never edit one that has shipped.
- **Data formats.** Timestamps are unix milliseconds in UTC. Money is `money.Amount` (1e-10 units) plus a currency.
- **IDs.** Prefixed ULIDs from `internal/platform/ids`, for example `prv_`, `acc_`, `pln_`, `req_`.
- **API.** Huma operations must declare an `x-permission`, return the error envelope `{"error":{"code","message","request_id","details?"}}`, use cursor pagination, and set an `ETag` header carrying the object revision.
- **UI.** Keep the existing stack: CSS Modules plus `web/src/styles/tokens.css`, TanStack, ECharts, lucide-react. Don't add component or CSS libraries.
- **Commits.** Conventional Commits.
