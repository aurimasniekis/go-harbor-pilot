-- Capacity reservations taken before dispatch and settled afterwards.
CREATE TABLE reservations (
    id            TEXT PRIMARY KEY,           -- rsv_
    request_id    TEXT NOT NULL,
    attempt_id    TEXT,
    account_id    TEXT NOT NULL,
    binding_id    TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('active','committed','released','expired')),
    est_cost_e10  INTEGER NOT NULL DEFAULT 0,
    currency      TEXT,
    created_at    INTEGER NOT NULL,
    lease_until   INTEGER NOT NULL,
    settled_at    INTEGER,
    settle_reason TEXT
) STRICT;
CREATE INDEX reservations_active_account ON reservations(account_id) WHERE status = 'active';
CREATE INDEX reservations_active_binding ON reservations(binding_id) WHERE status = 'active';
CREATE INDEX reservations_lease ON reservations(lease_until) WHERE status = 'active';

-- Append-only accounting ledger. Rows are never updated; corrections are new
-- rows. idempotency_key makes retried writes harmless.
CREATE TABLE ledger (
    id              TEXT PRIMARY KEY,         -- led_
    at              INTEGER NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('usage','unknown_usage','correction','external_observed')),
    request_id      TEXT,
    attempt_id      TEXT,
    session_id      TEXT,
    workspace_id    TEXT,
    project_id      TEXT,
    run_ref         TEXT,
    binding_id      TEXT,
    account_id      TEXT,
    provider_id     TEXT,
    model           TEXT,
    billing         TEXT,                     -- metered_api | subscription | free | local
    input_tokens    INTEGER,                  -- NULL = unknown
    output_tokens   INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    reasoning_tokens   INTEGER,
    usage_source    TEXT NOT NULL,            -- upstream_reported | estimated | unknown | provider_reported | client_reported
    cost_e10        INTEGER,                  -- NULL = no cost figure
    currency        TEXT,
    cost_kind       TEXT NOT NULL,            -- estimated_api_charge | list_equivalent | actual_api_charge | none
    price_version   TEXT,
    corrects_id     TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    provenance      TEXT NOT NULL DEFAULT '{}'
) STRICT;
CREATE INDEX ledger_at ON ledger(at);
CREATE INDEX ledger_account_at ON ledger(account_id, at);
CREATE INDEX ledger_binding_at ON ledger(binding_id, at);
CREATE INDEX ledger_project_at ON ledger(project_id, at);
CREATE INDEX ledger_request ON ledger(request_id);

-- Conversation sessions observed by the gateway.
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,             -- ses_
    binding_id  TEXT NOT NULL,
    key_hash    TEXT NOT NULL,                -- HMAC of the session key (never the raw header)
    source      TEXT NOT NULL,                -- bound_ref | harness_header | client_hint
    confidence  TEXT NOT NULL,                -- high | medium | low
    harness     TEXT,
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    requests    INTEGER NOT NULL DEFAULT 0,
    UNIQUE (binding_id, key_hash)
) STRICT;
CREATE INDEX sessions_last_seen ON sessions(last_seen);

-- Logical client requests.
CREATE TABLE requests (
    id                 TEXT PRIMARY KEY,      -- req_
    created_at         INTEGER NOT NULL,
    finished_at        INTEGER,
    binding_id         TEXT,
    credential_id      TEXT,
    workspace_id       TEXT,
    project_id         TEXT,
    trusted_refs       TEXT NOT NULL DEFAULT '{}',
    claims             TEXT NOT NULL DEFAULT '{}',   -- untrusted client hints, recorded with provenance
    session_id         TEXT,
    session_confidence TEXT NOT NULL DEFAULT 'unknown',
    harness            TEXT,
    harness_confidence TEXT,
    harness_version    TEXT,
    inbound_protocol   TEXT NOT NULL,
    stream             INTEGER NOT NULL,
    requested_model    TEXT,
    alias_id           TEXT,
    resolution         TEXT,
    plan_id            TEXT,
    plan_version       INTEGER,
    required_caps      TEXT NOT NULL DEFAULT '[]',
    status             TEXT NOT NULL,         -- in_progress | ok | error | blocked | canceled
    error_kind         TEXT,
    error_message      TEXT,
    stop_reason        TEXT,
    ttft_ms            INTEGER,
    duration_ms        INTEGER,
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    final_account_id   TEXT,
    final_provider_id  TEXT,
    final_model        TEXT,
    mode               TEXT,
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    reasoning_tokens   INTEGER,
    usage_source       TEXT,
    cost_e10           INTEGER,
    currency           TEXT,
    cost_kind          TEXT,
    tools_offered      INTEGER NOT NULL DEFAULT 0,
    tool_calls         INTEGER NOT NULL DEFAULT 0,
    tool_results       INTEGER NOT NULL DEFAULT 0,
    tool_result_errors INTEGER NOT NULL DEFAULT 0,
    tool_names         TEXT NOT NULL DEFAULT '[]',
    losses             TEXT NOT NULL DEFAULT '[]',
    decision           TEXT NOT NULL DEFAULT '{}',
    bytes_out          INTEGER NOT NULL DEFAULT 0,
    client_ip          TEXT,
    user_agent         TEXT
) STRICT;
CREATE INDEX requests_created ON requests(created_at);
CREATE INDEX requests_binding ON requests(binding_id, created_at);
CREATE INDEX requests_session ON requests(session_id, created_at);
CREATE INDEX requests_project ON requests(project_id, created_at);
CREATE INDEX requests_account ON requests(final_account_id, created_at);

-- Upstream attempts of a logical request.
CREATE TABLE attempts (
    id                TEXT PRIMARY KEY,       -- att_
    request_id        TEXT NOT NULL,
    n                 INTEGER NOT NULL,
    started_at        INTEGER NOT NULL,
    finished_at       INTEGER,
    account_id        TEXT NOT NULL,
    provider_id       TEXT NOT NULL,
    model             TEXT NOT NULL,
    upstream_protocol TEXT,
    mode              TEXT,
    status            TEXT NOT NULL,          -- ok | error | canceled
    http_status       INTEGER,
    error_kind        TEXT,
    error_message     TEXT,
    executed          INTEGER NOT NULL DEFAULT 0, -- 1 = upstream may have processed it
    ttft_ms           INTEGER,
    duration_ms       INTEGER,
    failover_reason   TEXT,
    retry_after_s     INTEGER,
    input_tokens      INTEGER,
    output_tokens     INTEGER,
    unknown_events    TEXT NOT NULL DEFAULT '[]'
) STRICT;
CREATE INDEX attempts_request ON attempts(request_id, n);
CREATE INDEX attempts_account ON attempts(account_id, started_at);
