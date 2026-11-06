-- Trusted references (mint-time bound_refs and the client's external_refs)
-- indexed per ledger row and per request, so usage and activity can be
-- filtered and grouped by any reference key (D-041). Client headers are
-- claims and never land here.

-- One row per (ledger row, trusted ref). Written in the ledger row's
-- transaction. The primary key serves "key = ? AND value = ? AND at in range".
CREATE TABLE ledger_refs (
    key       TEXT NOT NULL,
    value     TEXT NOT NULL,
    at        INTEGER NOT NULL,              -- copy of ledger.at
    ledger_id TEXT NOT NULL,
    PRIMARY KEY (key, value, at, ledger_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX ledger_refs_ledger ON ledger_refs(ledger_id, key, value);

-- One row per (request, trusted ref). Written with the request row by the
-- telemetry writer, so it has the same coverage as requests.
CREATE TABLE request_refs (
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    request_id TEXT NOT NULL,
    PRIMARY KEY (key, value, request_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX request_refs_request ON request_refs(request_id, key);

-- Backfill from the refs already stored on request rows. A value that isn't
-- a JSON object (empty, malformed, an array) indexes nothing, as do non-text
-- and empty values. The CASE guards json_each itself: a WHERE clause is not
-- guaranteed to run before the table-valued function.
INSERT OR IGNORE INTO request_refs(key, value, request_id)
    SELECT j.key, j.value, r.id
    FROM requests r,
         json_each(CASE WHEN json_valid(r.trusted_refs) AND json_type(r.trusted_refs) = 'object' THEN r.trusted_refs ELSE '{}' END) j
    WHERE j.type = 'text' AND j.value <> '';
INSERT OR IGNORE INTO ledger_refs(key, value, at, ledger_id)
    SELECT j.key, j.value, l.at, l.id
    FROM ledger l JOIN requests r ON r.id = l.request_id,
         json_each(CASE WHEN json_valid(r.trusted_refs) AND json_type(r.trusted_refs) = 'object' THEN r.trusted_refs ELSE '{}' END) j
    WHERE j.type = 'text' AND j.value <> '';
