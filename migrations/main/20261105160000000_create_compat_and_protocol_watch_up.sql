-- Compatibility circuit breakers, scoped by provider and protocol path.
CREATE TABLE compat_breakers (
    key            TEXT PRIMARY KEY,          -- <provider_id>|<inbound>><upstream>
    provider_id    TEXT NOT NULL,
    path           TEXT NOT NULL,
    state          TEXT NOT NULL,             -- closed | open | half_open
    override       TEXT NOT NULL DEFAULT 'auto', -- auto | force_open | force_closed
    override_reason TEXT,
    opened_at      INTEGER,
    retry_at       INTEGER,
    cooldown_ms    INTEGER NOT NULL DEFAULT 0,
    failures       INTEGER NOT NULL DEFAULT 0,
    last_class     TEXT,
    last_reason    TEXT,
    updated_at     INTEGER NOT NULL
) STRICT;

-- Observed protocol changes (structure only, never content values).
CREATE TABLE protocol_changes (
    id             TEXT PRIMARY KEY,          -- pch_
    fingerprint    TEXT NOT NULL UNIQUE,
    kind           TEXT NOT NULL,             -- new_event | new_field | new_enum_value | type_change | expected_absent | unsupported_sequence | schema_rejection | parse_failure | restored
    direction      TEXT NOT NULL,             -- request | response
    protocol       TEXT NOT NULL,
    provider_id    TEXT,
    endpoint       TEXT,
    adapter_version TEXT NOT NULL,
    client_harness TEXT,
    client_version TEXT,
    path           TEXT NOT NULL,
    detail         TEXT NOT NULL DEFAULT '{}',
    confidence     TEXT NOT NULL,             -- high | medium | low
    impact         TEXT NOT NULL,             -- breaking | additive | unknown
    first_seen     INTEGER NOT NULL,
    last_seen      INTEGER NOT NULL,
    count          INTEGER NOT NULL DEFAULT 1
) STRICT;
CREATE INDEX protocol_changes_last_seen ON protocol_changes(last_seen);

-- Deduplicated work items, one per change fingerprint.
CREATE TABLE work_items (
    id             TEXT PRIMARY KEY,          -- wki_
    change_id      TEXT NOT NULL UNIQUE,
    title          TEXT NOT NULL,
    diff           TEXT NOT NULL DEFAULT '{}',
    evidence       TEXT NOT NULL DEFAULT '[]',  -- request ids (metadata references only)
    routes         TEXT NOT NULL DEFAULT '[]',
    action         TEXT NOT NULL DEFAULT '{}',
    status         TEXT NOT NULL DEFAULT 'new', -- new | acknowledged | investigating | fix_available | verified | resolved | ignored
    issue_url      TEXT,
    commit_ref     TEXT,
    release_ref    TEXT,
    fixture        TEXT,
    fixture_reviewed INTEGER NOT NULL DEFAULT 0,
    notes          TEXT,
    revision       INTEGER NOT NULL DEFAULT 1,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;
CREATE INDEX work_items_status ON work_items(status, updated_at);
