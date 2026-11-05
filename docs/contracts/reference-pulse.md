# Reference: Pulse event contract and Harbor Pilot mapping

- **Source:** `aurimasniekis/go-pulse` @ `c4689c8` (v0.1.5). License Apache-2.0.
- **Importable packages:** only `github.com/aurimasniekis/go-pulse/pkg/pulse` and `pkg/pulse/envelope`.
- **Harbor Pilot dependency:** `github.com/aurimasniekis/go-pulse v0.1.5`. Only the outbox relay in
  `internal/events/relay/pulse` imports it, and the relay is optional (it starts disabled).

Facts are verified-in-source unless marked.

## 1. Go SDK

| API | Signature / behaviour | Source |
|---|---|---|
| `pulse.Connect(url string, opts ...Option) (*Client, error)` | sets `nats.MaxReconnects(-1)` and `nats.ReconnectWait(1s)` | `pkg/pulse/client.go:116,149-150` |
| Connect options | `WithCredsFile(path)`, `WithCreds([]byte)` (credentials required), `WithCAFile(path)`, `WithCA(pem)`, `WithSource(s)` (default `ce-source`), `WithName(s)`, `WithStream(s)`, `WithNATSOptions(...nats.Option)` | `client.go:62-93` |
| `(*Client).Publish(ctx, subject, typ string, data any, opts ...PublishOption) (*PubAck, error)` | JetStream publish that waits for the ack; `data` may be `[]byte`, `string`, `json.RawMessage`, `nil` or any JSON-marshalable value | `client.go:270` |
| `PubAck` | `{ID string; Stream string; Sequence uint64; Duplicate bool}` | `client.go:207-211` |
| Publish options | `WithID(id)` (sets `Nats-Msg-Id`), `WithTime(t)` (sets `ce-time`), `WithContentType(ct)`, `WithEventSource(s)` (per-message `ce-source`), `WithHeader(k,v)`, `WithBlob(ref)` | `client.go:227-249` |
| Default ID | ULID (`envelope.NewID`) when `WithID` is not given | agent report |
| Consuming | ordered consumer by default; `Durable(name)` gives an explicit-ack consumer whose redelivery backoff starts at 1 s, doubles, and caps at 5 min | `pkg/pulse/subscribe.go` (agent report) |

## 2. Envelope (`pkg/pulse/envelope/envelope.go`)

| Header | Constant | Meaning |
|---|---|---|
| `Nats-Msg-Id` | `HeaderID` | idempotency key (JetStream dedup) |
| `ce-type` | `HeaderType` | event type, e.g. `home.door.opened.v1`; empty means untyped |
| `ce-source` | `HeaderSource` | producing tool |
| `ce-time` | `HeaderTime` | RFC 3339 nano timestamp, UTC |
| `content-type` | `HeaderContentType` | e.g. `application/json` |
| `pulse-blob`, `pulse-blob-size`, `pulse-blob-sha256` | `HeaderBlob*` | Object Store reference for payloads over 1 MiB |

- `ce-specversion` 1.0 is implied (agent report).
- Unknown headers pass through as `Event.Extra` (agent report).

**Subject rule** (`ValidateSubject`, `envelope.go:52-69`):
- At most `MaxSubjectLen` (256) bytes.
- Must start with `evt.`.
- At least 4 tokens (`evt.<domain>.<entity>.<action>`), each matching `[a-z0-9_-]+`.

The SDK, CLI and HTTP API all reject anything else.

## 3. Stream (`internal/streams/events.go:18-50`)

| Setting | Value |
|---|---|
| Name / subjects | `EVENTS` / `evt.>` |
| Retention, storage | `LimitsPolicy`, `FileStorage`, `MaxAge 0` (keep), `MaxBytes` configured, `DiscardOld` |
| Max message size | `1 << 20` (1 MiB) |
| Duplicate window | **2 min** |
| Compression / replicas / max consumers | S2 / 1 / 256 |

## 4. Authentication and grants (`internal/natsauth`; agent report, read in source)

- Decentralised JWT: operator, then the `APP` account, then a scoped signing key per client.
- Each client gets a `.creds` file whose user JWT is tagged `pulse-client:<id>`. The inbox is `_INBOX_<id>`.
- Grant actions: `publish`, `subscribe`, `request`, `reply`, `durable`, `blobs`.
- Clients are managed through the pulsed admin API: `GET/POST /clients`, `PUT /clients/{id}/grants`,
  `/rotate`, `/revoke`, `/kick`.
- **Pitfall:** a publish outside the client's grants shows up as a ~5 s timeout, not an immediate error.
  Harbor Pilot treats a publish timeout as retryable but reports the "possible missing grant" hint in
  delivery history.

## 5. HTTP publish alternative (`docs/API.md`, doc-claim)

- **Single event:** `POST /api/v1/publish/{subject}` with `Authorization: Bearer pulse_<prefix>_<secret>`.
  Headers: `Ce-Type`, `Ce-Source`, and `Idempotency-Key` (becomes `Nats-Msg-Id`).
  Returns 201 `{stream, seq, id, duplicate}`.
- **Batch:** `POST /api/v1/publish` with up to 100 events.
- **Harbor Pilot use:** **not used.** The relay uses the Go SDK over NATS. HTTP publish is documented here only
  as an option for deployments without NATS reachability. Adding it requires a new decision.

## 6. Delivery semantics (doc-claim, `docs/GOAL.md`)

> "No exactly-once delivery beyond JetStream's 2-minute de-duplication window on `Nats-Msg-Id`."

Consumers must deduplicate by event ID. Harbor Pilot promises **at-least-once**.

## 7. Harbor Pilot mapping (decision D-021)

| Item | Value |
|---|---|
| Subject | `evt.harborpilot.<area>.<entity>.<action>`, e.g. `evt.harborpilot.routing.account.switched` |
| `ce-type` | `harborpilot.<area>.<entity>_<action>.v<N>`, e.g. `harborpilot.routing.account_switched.v1` |
| `Nats-Msg-Id` | the event's ULID from `event_outbox.id`, stable across retries (`pulse.WithID`) |
| `ce-time` | the event's `occurred_at` (`pulse.WithTime`), not the publish time |
| `ce-source` | `harbor-pilot/<instance_id>` (`pulse.WithSource` at connect) |
| `content-type` | `application/json` |
| Body | `{"schema_version":1,"event_id","occurred_at","correlation_id","subject":{…ids…},"data":{…redacted…}}`; schemas live in `events/` |
| Size guard | serialized body capped at **256 KiB**. Oversized events are truncated by schema rules (drop optional lists, set `truncated:true`), never blob-offloaded. `pulse-blob` is not used, so no `blobs` grant is needed |
| NATS grant | `publish:evt.harborpilot.>` |
| Config | `events.pulse.{enabled,url,creds_ref,ca_ref,source}`; credentials are secret refs, never literals |

Relay behaviour (copies the producer pattern seen in the HallPass/Porch outbox relays, re-implemented):
1. Insert into `event_outbox` inside the domain transaction.
2. Relay after commit, publishing in FIFO order within a partition. A round stops at the first failure.
3. Backoff is `2^(n-1)` seconds with ±50 % jitter, capped at 5 min.
4. Dead-letter after N attempts or a max age (configurable). Defaults: 50 attempts / 24 h.
5. A row is marked delivered only after the `PubAck`. `PubAck.Duplicate=true` also counts as delivered.
6. A NATS outage never blocks the gateway: the relay is a separate goroutine, the outbox is bounded by
   retention, and the overflow policy is documented in `../ARCHITECTURE.md`.

**Tests:** relay tests run against an **embedded** `nats-server/v2` with JetStream, configured like
`EventsConfig` (2-min dup window, 1 MiB limit). Live verification against a real pulsed is opt-in
(`scripts/live/pulse.sh`).
