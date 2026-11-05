-- Platform core: key/value, server keys, configuration revisions, audit log,
-- transactional outbox and per-consumer delivery state.

CREATE TABLE kv (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- Named random server keys (token pepper, CSRF/session HMAC, webhook secret
-- derivation). Values are envelope-encrypted under the master KEK.
CREATE TABLE server_keys (
    purpose    TEXT PRIMARY KEY,
    envelope   TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    rotated_at INTEGER
) STRICT;

-- Every committed control-plane change bumps the global revision.
CREATE TABLE config_revisions (
    revision    INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at  INTEGER NOT NULL,
    actor_id    TEXT NOT NULL,
    actor_kind  TEXT NOT NULL,
    source      TEXT NOT NULL,           -- ui | api | terraform | cli | import | system
    summary     TEXT NOT NULL,
    request_id  TEXT
) STRICT;

CREATE TABLE audit_log (
    id          TEXT PRIMARY KEY,
    at          INTEGER NOT NULL,
    actor_id    TEXT NOT NULL,
    actor_kind  TEXT NOT NULL,           -- user | service | system | anonymous
    action      TEXT NOT NULL,           -- e.g. plan.publish, trace.content.read
    target_kind TEXT,
    target_id   TEXT,
    outcome     TEXT NOT NULL,           -- success | denied | error
    ip          TEXT,
    user_agent  TEXT,
    request_id  TEXT,
    details     TEXT NOT NULL DEFAULT '{}' -- JSON, redacted
) STRICT;
CREATE INDEX audit_log_at ON audit_log(at);
CREATE INDEX audit_log_target ON audit_log(target_kind, target_id, at);
CREATE INDEX audit_log_action ON audit_log(action, at);

-- Domain events written in the same transaction as the state change.
CREATE TABLE event_outbox (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,   -- evt_ ULID, idempotency key downstream
    type         TEXT NOT NULL,          -- harborpilot.<area>.<entity>_<action>.v1
    subject      TEXT NOT NULL,          -- evt.harborpilot.<area>.<entity>.<action>
    occurred_at  INTEGER NOT NULL,
    severity     TEXT NOT NULL DEFAULT 'info',
    correlation_id TEXT,
    dedupe_key   TEXT,
    payload      TEXT NOT NULL           -- JSON, allowlisted fields only
) STRICT;
CREATE INDEX event_outbox_occurred ON event_outbox(occurred_at);

-- Delivery state per (consumer, event). Consumers are relays (pulse:<dst>,
-- webhook:<dst>) and the notification rule engine.
CREATE TABLE event_deliveries (
    id            TEXT PRIMARY KEY,
    consumer      TEXT NOT NULL,
    event_seq     INTEGER NOT NULL REFERENCES event_outbox(seq) ON DELETE CASCADE,
    status        TEXT NOT NULL,         -- pending | sending | delivered | deferred | dead | skipped
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    lease_until   INTEGER,
    last_error    TEXT,
    last_status   INTEGER,
    delivered_at  INTEGER,
    created_at    INTEGER NOT NULL,
    UNIQUE(consumer, event_seq)
) STRICT;
CREATE INDEX event_deliveries_due ON event_deliveries(consumer, status, next_attempt_at);

-- Consumer cursors over the outbox (fan-out happens lazily per consumer).
CREATE TABLE event_cursors (
    consumer   TEXT PRIMARY KEY,
    last_seq   INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
