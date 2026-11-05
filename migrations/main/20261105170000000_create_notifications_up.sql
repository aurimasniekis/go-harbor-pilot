-- Notification state per (rule, channel, transition) for hysteresis,
-- grouping and recovery.
CREATE TABLE notification_state (
    rule_id      TEXT NOT NULL,
    channel_id   TEXT NOT NULL,
    dedupe_key   TEXT NOT NULL,
    state        TEXT NOT NULL,               -- active | resolved
    last_sent_at INTEGER,
    suppressed   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (rule_id, channel_id, dedupe_key)
) STRICT;

-- Rendered notifications and their delivery state. Bodies are built from
-- allowlisted event fields only (no prompts or secrets) and are bounded.
CREATE TABLE notifications (
    id              TEXT PRIMARY KEY,        -- nfy_
    rule_id         TEXT,
    channel_id      TEXT NOT NULL,
    event_seq       INTEGER,
    event_id        TEXT,
    kind            TEXT NOT NULL,           -- alert | recovery | digest | test
    dedupe_key      TEXT,
    severity        TEXT NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,
    status          TEXT NOT NULL,           -- pending | digest | sending | sent | digested | dead
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    lease_until     INTEGER,
    last_error      TEXT,
    last_status     INTEGER,
    sent_at         INTEGER,
    created_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX notifications_due ON notifications(channel_id, status, next_attempt_at);
CREATE INDEX notifications_created ON notifications(created_at);
