# Monitoring and capacity

Harbor Pilot gives you three things to watch from outside: health endpoints, Prometheus metrics and structured logs.
Inside the app, [Home](/operators/home) and [alerts](/operators/alerts) cover quotas, budgets and failing accounts;
this page is about the server itself.

## Health

| Endpoint | Answers 200 when |
|---|---|
| `GET /healthz` | The process is up. Use it for liveness. |
| `GET /readyz` | Both databases are reachable and startup recovery has finished. Use it for readiness and load balancers. |

Neither needs a token. The container image's health check runs `harbor-pilot healthcheck --url
http://127.0.0.1:8080/readyz`; you can use the same command anywhere `curl` isn't available.

## Metrics

`GET /metrics` serves Prometheus metrics.

| Metric | Labels | Meaning |
|---|---|---|
| `hp_gateway_requests_total` | `protocol`, `outcome` | Requests from clients, by inbound protocol and outcome. |
| `hp_gateway_attempts_total` | `executor`, `outcome` | Upstream attempts, by executor kind and outcome class. More attempts than requests means fallback is working hard. |
| `hp_gateway_request_duration_seconds` | `protocol` | Total request duration (histogram). |
| `hp_gateway_ttft_seconds` | `executor` | Time to first token (histogram). |
| `hp_gateway_inflight_requests` | | Requests in flight now. |
| `hp_reservations_total` | `outcome` | Budget and concurrency reservations, by outcome. A rise in refusals means limits are biting. |
| `hp_telemetry_dropped_total` | `pipeline` | Request records dropped under back-pressure (see below). Should stay at 0. |
| `hp_outbox_pending` | `consumer` | Events not yet delivered, per relay and alerting. A steady rise means a destination is down. |
| `hp_deliveries_total` | `channel`, `outcome` | Event and alert delivery attempts. |
| `hp_compat_breakers` | `state` | [Circuit breakers](/operators/diagnostics) by state. Anything open deserves a look. |
| `hp_config_revision` | | The active configuration revision. Changes when anyone changes configuration. |
| `hp_http_requests_total` | `route`, `class` | All HTTP requests, by route pattern and status class. |

No label holds a secret, a token or prompt content.

### Access to `/metrics`

- Without configuration, `/metrics` answers only **direct loopback** clients: a request from `127.0.0.1` or `::1` with
  no `X-Forwarded-For`, `Forwarded` or `X-Real-IP` header, and only when no `server.trusted_proxies` are set. Behind a
  reverse proxy on the same host every client looks like loopback, so forwarded requests never qualify.
- Otherwise, set `server.metrics_token_file` to a file holding a token, and have Prometheus send it:

```yaml
scrape_configs:
  - job_name: harbor-pilot
    scheme: https
    metrics_path: /metrics
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/harbor-pilot-metrics.token
    static_configs:
      - targets: ["pilot.example.com"]
```

### Suggested alerts

| Condition | Why |
|---|---|
| `/readyz` failing | The server can't serve. |
| `increase(hp_telemetry_dropped_total[15m]) > 0` | Request records are being dropped: the database can't keep up. |
| `hp_outbox_pending` growing for 30 minutes | An event relay or alert channel is failing. |
| `hp_compat_breakers{state!="closed"} > 0` | A protocol path is paused. |
| Ratio of failed `hp_gateway_requests_total` rising | Clients are seeing errors. |

## Logs

Logs are structured JSON on stderr, one object per line. Useful messages:

| Message | When |
|---|---|
| `gateway.request.completed` | Once per request: route, model, account, attempts, usage, latency and intent. Never the prompt. |
| `db.migration.backup`, `db.migration.applied` | At startup, when migrations run (see [Upgrades](/operators/upgrades)). |
| `setup.required` | No administrator exists yet. Run `harbor-pilot admin bootstrap`. |

Set `HP_LOG_LEVEL=debug` for more detail while you investigate. Debug logs still leave out secrets and prompt content.

Every API error carries a `request_id`, which is also in the log line and the [audit log](/operators/audit) entry.

## Capacity

Harbor Pilot is one process on SQLite in WAL mode. On a request it authenticates, routes from an in-memory snapshot,
runs one small reservation transaction and dispatches. Request records, captured content and events are written in
the background through bounded queues, so they never hold a request up.

### Measured overhead

These numbers come from the load tests in the repository. The gateway ran in-process on loopback in front of a
synthetic upstream that answered immediately, so they measure Harbor Pilot's own overhead, not provider latency.
Measured on 2026-11-05 on an Apple M5 Pro (15 cores), Go 1.27.3, at `HP_LOAD=1` scale.

| Scenario | Load | Result |
|---|---|---|
| Streaming throughput | 3,200 streamed requests, 64 concurrent clients | About 2,550 requests/s. Latency p50 22 ms, p95 54 ms, p99 77 ms. First byte p50 5 ms. Every request billed exactly once, no reservation left open. |
| Slow clients | 256 clients reading one line every 40 ms (streams held about 1.1 s) beside 1,600 fast requests | Fast requests p95 19 ms, against 17 ms with no slow clients. All slow streams completed. |
| Telemetry back-pressure | 2,400 requests with a one-slot request-record queue | All requests succeeded and accounting was complete. 2,820 request records were dropped and counted in `telemetry_dropped`. |

The last row is deliberate: under extreme pressure Harbor Pilot drops *request records* rather than slow requests
down. Accounting (reservations and the spend ledger) is never dropped.

### What limits a real deployment

- **Upstream latency and provider rate limits** dominate end-to-end time.
- **SQLite's single writer** serializes reservations and ledger writes. The runs above didn't saturate it. At
  thousands of requests per second across all clients, watch `hp_telemetry_dropped_total` and request latency.
- **CLI bridges** run one vendor process per request, bounded by each account's maximum concurrency and by how many
  processes the host can run.

**One instance per data directory.** There is no multi-writer high availability: a second server on the same data
directory refuses to start. Recovery is by restoring a [backup](/operators/backup).

### Load test your own server

```sh
make load                                        # the repository's load tests at full scale

go run ./cmd/hp-loadgen --url https://pilot.example.com --token-file ~/.hp-token \
  --model coding/default --concurrency 32 --requests 1000 --stream
go run ./cmd/hp-loadgen ... --read-delay 50ms    # simulate slow clients
```

`hp-loadgen` reads the access token from a file or the environment, never from arguments. It prints throughput,
latency and first-byte percentiles, and exits non-zero if any request fails. Point it only at accounts whose usage you
are prepared to pay for, or at a local or synthetic upstream.
