-- Latest observed state of each quota bucket. owner_key is the quota
-- monitor id for adapter observations, or the provider account id for
-- observations derived from gateway responses.
CREATE TABLE quota_buckets (
    owner_key          TEXT NOT NULL,
    bucket_key         TEXT NOT NULL,
    monitor_id         TEXT,
    account_id         TEXT,                  -- linked provider account (routing health)
    source             TEXT NOT NULL,         -- adapter | response_headers
    spec               TEXT NOT NULL,         -- JSON bucket spec (scope, kind, unit, window, reset semantics)
    limit_value        REAL,                  -- NULL = unknown (never zero by default)
    used_value         REAL,
    remaining_value    REAL,
    remaining_fraction REAL,
    limit_e10          INTEGER,
    used_e10           INTEGER,
    remaining_e10      INTEGER,
    reset_at           INTEGER,
    source_time        INTEGER,
    observed_at        INTEGER NOT NULL,
    quality            TEXT NOT NULL,
    PRIMARY KEY (owner_key, bucket_key)
) STRICT;
CREATE INDEX quota_buckets_account ON quota_buckets(account_id);

-- Observation history used for burn rates and forecasts.
CREATE TABLE quota_observations (
    id                 INTEGER PRIMARY KEY,
    owner_key          TEXT NOT NULL,
    bucket_key         TEXT NOT NULL,
    observed_at        INTEGER NOT NULL,
    limit_value        REAL,
    used_value         REAL,
    remaining_value    REAL,
    reset_at           INTEGER,
    quality            TEXT NOT NULL
) STRICT;
CREATE INDEX quota_observations_bucket ON quota_observations(owner_key, bucket_key, observed_at);
CREATE INDEX quota_observations_at ON quota_observations(observed_at);

-- Poll scheduling and monitor credential health.
CREATE TABLE quota_poll_state (
    monitor_id           TEXT PRIMARY KEY,
    last_attempt_at      INTEGER,
    last_success_at      INTEGER,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    next_attempt_at      INTEGER,
    error_kind           TEXT,
    error_message        TEXT,
    error_http_status    INTEGER,
    auth_state           TEXT NOT NULL DEFAULT 'unknown',
    cooldown_until       INTEGER,
    last_latency_ms      INTEGER,
    masked_identity      TEXT,
    notes                TEXT NOT NULL DEFAULT '[]'
) STRICT;

-- OAuth tokens obtained by in-app login for monitors, sealed with the
-- master key.
CREATE TABLE quota_oauth_tokens (
    monitor_id TEXT PRIMARY KEY,
    envelope   TEXT NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- Usage and charges reported by provider usage/admin APIs. Rows of
-- different breakdowns describe the same usage and are never summed.
CREATE TABLE external_usage (
    monitor_id     TEXT NOT NULL,
    breakdown      TEXT NOT NULL,
    dimensions     TEXT NOT NULL,             -- canonical JSON of the dimension values
    interval_start INTEGER NOT NULL,
    interval_end   INTEGER NOT NULL,
    requests       INTEGER,
    input_tokens   INTEGER,
    output_tokens  INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    actual_cost_e10    INTEGER,
    estimated_cost_e10 INTEGER,
    currency       TEXT,
    partial        INTEGER NOT NULL DEFAULT 0,
    quality        TEXT NOT NULL,
    observed_at    INTEGER NOT NULL,
    PRIMARY KEY (monitor_id, breakdown, dimensions, interval_start, interval_end)
) STRICT;
CREATE INDEX external_usage_interval ON external_usage(interval_start);

-- Budgets need the scope of in-flight reservations.
ALTER TABLE reservations ADD COLUMN project_id TEXT;
ALTER TABLE reservations ADD COLUMN workspace_id TEXT;

-- Budget threshold state per period (for one warning per period).
CREATE TABLE budget_state (
    budget_id     TEXT NOT NULL,
    period_start  INTEGER NOT NULL,
    warned_at     INTEGER,
    exceeded_at   INTEGER,
    PRIMARY KEY (budget_id, period_start)
) STRICT;
