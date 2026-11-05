-- Latest "Test connection" result per provider account. Messages never
-- contain credentials (they are scrubbed before they are stored).
CREATE TABLE account_checks (
    account_id  TEXT PRIMARY KEY,
    checked_at  INTEGER NOT NULL,
    status      TEXT NOT NULL,
    message     TEXT NOT NULL,
    http_status INTEGER,
    latency_ms  INTEGER NOT NULL DEFAULT 0
) STRICT;
