-- Short-lived trace content. Lives in its own database file (traces.db) so it
-- can be excluded from ordinary backups and vacuumed independently.

-- One data-encryption key per (trace policy, retention bucket). Destroying the
-- row is the cryptographic-erasure step: ciphertext sealed with it becomes
-- unrecoverable even if residual pages survive in the file, WAL or backups
-- that did not capture this row.
CREATE TABLE trace_keys (
    id          TEXT PRIMARY KEY,
    bucket      TEXT NOT NULL,           -- <policy_id>|<expires_day>
    kek_id      TEXT NOT NULL,
    wrapped_dek BLOB NOT NULL,
    expires_at  INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    UNIQUE(bucket)
) STRICT;
CREATE INDEX trace_keys_expiry ON trace_keys(expires_at);

CREATE TABLE traces (
    id           TEXT PRIMARY KEY,       -- trc_
    request_id   TEXT NOT NULL,
    key_id       TEXT NOT NULL,
    mode         TEXT NOT NULL,          -- masked_diagnostic | masked_full | unredacted
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    size_bytes   INTEGER NOT NULL,
    truncated    INTEGER NOT NULL DEFAULT 0,
    sections     TEXT NOT NULL           -- JSON list of captured section names
) STRICT;
CREATE INDEX traces_request ON traces(request_id);
CREATE INDEX traces_expiry ON traces(expires_at);

CREATE TABLE trace_parts (
    trace_id   TEXT NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
    section    TEXT NOT NULL,            -- inbound_request, upstream_request, stream_events, ...
    ord        INTEGER NOT NULL,
    ciphertext BLOB NOT NULL,
    PRIMARY KEY (trace_id, section, ord)
) STRICT;

-- Search index over masked text only. Rows are deleted together with traces.
CREATE VIRTUAL TABLE trace_search USING fts5(trace_id UNINDEXED, body, tokenize = 'unicode61');
