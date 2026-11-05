# Capacity and load testing

Harbor Pilot is a single process on SQLite in WAL mode. On the request path it does authentication, routing from the in-memory snapshot, one small reservation transaction and dispatch. Activity, traces and events are written in the background through bounded queues.

## Measured gateway overhead
These figures come from the load tests in `internal/acceptance/load_test.go`. The gateway runs in-process on loopback in front of a synthetic upstream that answers immediately, so they measure Harbor Pilot's own overhead, not provider latency.

The tests ran on 2026-11-05 on an Apple M5 Pro (15 cores), with Go 1.27.3, at `HP_LOAD=1` scale:

| Scenario | Load | Result |
|---|---|---|
| Streaming throughput | 3,200 streamed requests, 64 concurrent clients | About 2,550 requests/s. Latency p50 22 ms, p95 54 ms, p99 77 ms. First byte p50 5 ms. Every request was billed exactly once, and no reservation was left open. |
| Slow clients | 256 clients reading one line every 40 ms (streams held about 1.1 s) alongside 1,600 fast requests | Fast requests p95 19 ms, against a 17 ms baseline with no slow clients. All slow streams completed. |
| Telemetry back-pressure | 2,400 requests with a one-slot activity queue | All requests succeeded. Accounting was complete. 2,820 activity records were dropped and counted, and `telemetry_dropped` reports the gap. |

What limits a real deployment:
- **Upstream latency and provider rate limits** dominate end-to-end time.
- **SQLite's single writer** serializes reservations and ledger writes. The runs above did not saturate it, but writer latency was not measured separately. At very high sustained rates (thousands of requests per second across all clients), watch the database metrics on `/metrics`.
- **CLI bridges** run one vendor process per request. Their per-account concurrency limits (`max_concurrency`) bound them, along with the host's capacity to run those processes.

The supported topology is one instance per data directory. There is no multi-writer high availability; recovery is by restoring a backup (see [backup-restore.md](backup-restore.md)).

## Running the load tests
```
make load                            # HP_LOAD=1 go test -run TestLoad ./internal/acceptance
go test -run TestLoad ./internal/acceptance   # quick regression scale (runs in the normal suite)
```

To test a running server, use `hp-loadgen`. It reads the gateway token from a file or an environment variable, never from arguments:
```
go run ./cmd/hp-loadgen --url https://pilot.example.com --token-file ~/.hp-token \
  --model coding/default --concurrency 32 --requests 1000 --stream
go run ./cmd/hp-loadgen ... --read-delay 50ms   # simulate slow clients
```
It prints throughput and latency and first-byte percentiles, and exits non-zero if any request fails. Point it only at accounts whose usage you are prepared to pay for, or at a local or synthetic upstream.
