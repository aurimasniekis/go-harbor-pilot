-- Managed configuration objects. Every kind (workspace, project, provider,
-- provider_account, subscription_entitlement, pool, model_alias,
-- routing_plan, client_binding, …) shares one table with a typed JSON spec
-- validated in Go. This gives uniform revisions, ETags, drift tracking,
-- audit, export/import and reference integrity.
CREATE TABLE resources (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    spec          TEXT NOT NULL,              -- canonical JSON
    revision      INTEGER NOT NULL DEFAULT 1,
    managed_by    TEXT NOT NULL,              -- ui | api | terraform | cli | import | demo
    drift_state   TEXT NOT NULL DEFAULT 'in_sync', -- in_sync | drifted (terraform-managed edited elsewhere)
    drifted_at    INTEGER,
    drifted_by    TEXT,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    created_by    TEXT NOT NULL,
    updated_by    TEXT NOT NULL,
    config_revision INTEGER NOT NULL,
    UNIQUE (kind, name)
) STRICT;
CREATE INDEX resources_kind ON resources(kind, id);

-- Directed references between resources (from depends on to). Deleting a
-- referenced resource is refused while references exist.
CREATE TABLE resource_refs (
    from_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    to_id   TEXT NOT NULL REFERENCES resources(id),
    field   TEXT NOT NULL,
    PRIMARY KEY (from_id, to_id, field)
) STRICT;
CREATE INDEX resource_refs_to ON resource_refs(to_id);

-- Write-only secret values (envelope-encrypted). Never returned by the API.
CREATE TABLE resource_secrets (
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    field       TEXT NOT NULL,
    envelope    TEXT NOT NULL,
    fingerprint TEXT NOT NULL,                -- short non-reversible hint (first 8 of HMAC)
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (resource_id, field)
) STRICT;

-- Immutable history of every resource revision (spec only, no secrets).
CREATE TABLE resource_history (
    resource_id   TEXT NOT NULL,
    revision      INTEGER NOT NULL,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    spec          TEXT,                       -- NULL for deletions
    operation     TEXT NOT NULL,              -- create | update | delete
    managed_by    TEXT NOT NULL,
    actor_id      TEXT NOT NULL,
    at            INTEGER NOT NULL,
    config_revision INTEGER NOT NULL,
    PRIMARY KEY (resource_id, revision)
) STRICT;

-- Routing plan policy versions: draft -> published -> superseded.
CREATE TABLE policy_versions (
    id            TEXT PRIMARY KEY,           -- pvr_
    plan_id       TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('draft','published','superseded','archived')),
    document      TEXT NOT NULL,              -- canonical JSON plan document
    validation    TEXT NOT NULL DEFAULT '{}', -- last validation report
    note          TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    published_by  TEXT,
    published_at  INTEGER,
    rollback_of   TEXT,                       -- version id this publish restored
    UNIQUE (plan_id, version)
) STRICT;
CREATE UNIQUE INDEX policy_versions_one_published ON policy_versions(plan_id) WHERE status = 'published';

-- Gateway credentials bound to client bindings. Only verifiers are stored.
CREATE TABLE gateway_credentials (
    id            TEXT PRIMARY KEY,           -- crd_
    prefix        TEXT NOT NULL UNIQUE,
    verifier      BLOB NOT NULL,
    binding_id    TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    scopes        TEXT NOT NULL,              -- JSON array
    parent_id     TEXT REFERENCES gateway_credentials(id) ON DELETE CASCADE,
    bound_refs    TEXT NOT NULL DEFAULT '{}', -- trusted external refs bound at mint time
    managed_by    TEXT NOT NULL,
    expires_at    INTEGER,
    revoked_at    INTEGER,
    revoke_reason TEXT,
    created_by    TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER,
    last_used_ip  TEXT
) STRICT;
CREATE INDEX gateway_credentials_binding ON gateway_credentials(binding_id);
CREATE INDEX gateway_credentials_parent ON gateway_credentials(parent_id);

-- Idempotency keys for create operations (24h).
CREATE TABLE idempotency_keys (
    key          TEXT NOT NULL,
    principal_id TEXT NOT NULL,
    operation    TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status       INTEGER NOT NULL,
    response     TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (principal_id, operation, key)
) STRICT;
CREATE INDEX idempotency_keys_created ON idempotency_keys(created_at);
