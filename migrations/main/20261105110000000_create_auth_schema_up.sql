-- Human users, linked external identities, web sessions, login throttling,
-- one-time setup/recovery/invite tokens, service principals and their API
-- tokens, OIDC provider configuration and transient OIDC flow state.

CREATE TABLE users (
    id             TEXT PRIMARY KEY,            -- usr_
    email          TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name   TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
    role_source    TEXT NOT NULL DEFAULT 'local', -- local | oidc:<provider_id>
    password_hash  TEXT,                        -- NULL = no local password (SSO only)
    password_set_at INTEGER,
    disabled_at    INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    last_login_at  INTEGER,
    revision       INTEGER NOT NULL DEFAULT 1
) STRICT;

-- Explicit, audited grants that roles never imply (trace content access).
CREATE TABLE user_grants (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission TEXT NOT NULL,
    granted_by TEXT NOT NULL,
    granted_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, permission)
) STRICT;

CREATE TABLE user_identities (
    provider_id TEXT NOT NULL,                  -- oid_
    issuer      TEXT NOT NULL,
    subject     TEXT NOT NULL,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email_at_link TEXT,
    linked_at   INTEGER NOT NULL,
    last_seen_at INTEGER,
    PRIMARY KEY (issuer, subject)
) STRICT;
CREATE INDEX user_identities_user ON user_identities(user_id);

CREATE TABLE web_sessions (
    id_hash            TEXT PRIMARY KEY,        -- sha256 of the cookie token
    display_id         TEXT NOT NULL UNIQUE,    -- wss_ (shown in UI, used for revocation)
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_secret        BLOB NOT NULL,
    auth_method        TEXT NOT NULL,           -- password | oidc | breakglass | setup
    auth_provider      TEXT,
    limited            INTEGER NOT NULL DEFAULT 0,
    auth_at            INTEGER NOT NULL,
    created_at         INTEGER NOT NULL,
    last_seen_at       INTEGER NOT NULL,
    idle_expires_at    INTEGER NOT NULL,
    absolute_expires_at INTEGER NOT NULL,
    ip                 TEXT,
    user_agent         TEXT,
    revoked_at         INTEGER,
    revoke_reason      TEXT
) STRICT;
CREATE INDEX web_sessions_user ON web_sessions(user_id);
CREATE INDEX web_sessions_expiry ON web_sessions(absolute_expires_at);

CREATE TABLE login_throttle (
    key          TEXT PRIMARY KEY,              -- acct:<email> | ip:<prefix>
    failures     INTEGER NOT NULL,
    last_failure_at INTEGER NOT NULL,
    blocked_until INTEGER NOT NULL
) STRICT;

-- One-time tokens: first-admin setup, break-glass recovery, invitations,
-- password resets. Only the hash is stored.
CREATE TABLE one_time_tokens (
    token_hash  TEXT PRIMARY KEY,
    purpose     TEXT NOT NULL CHECK (purpose IN ('setup','recovery','invite','reset')),
    user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
    email       TEXT,
    role        TEXT,
    created_by  TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    used_at     INTEGER
) STRICT;

-- Persistent security alerts that must be acknowledged (break-glass use).
CREATE TABLE security_alerts (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    message     TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    acknowledged_at INTEGER,
    acknowledged_by TEXT
) STRICT;

-- Automation identities (Terraform, CI, integrations).
CREATE TABLE service_principals (
    id           TEXT PRIMARY KEY,              -- pri_
    name         TEXT NOT NULL UNIQUE,
    description  TEXT NOT NULL DEFAULT '',
    permissions  TEXT NOT NULL,                 -- JSON array of permission strings
    disabled_at  INTEGER,
    managed_by   TEXT NOT NULL DEFAULT 'ui',
    created_by   TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    revision     INTEGER NOT NULL DEFAULT 1
) STRICT;

-- Control-plane API tokens for service principals (hpa_<prefix>_<secret>).
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,              -- crd_
    prefix       TEXT NOT NULL UNIQUE,
    verifier     BLOB NOT NULL,                 -- HMAC-SHA256(pepper, secret)
    principal_id TEXT NOT NULL REFERENCES service_principals(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    expires_at   INTEGER,
    revoked_at   INTEGER,
    created_by   TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    last_used_ip TEXT
) STRICT;
CREATE INDEX api_tokens_principal ON api_tokens(principal_id);

CREATE TABLE oidc_providers (
    id             TEXT PRIMARY KEY,            -- oid_
    slug           TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL,
    issuer         TEXT NOT NULL,
    client_id      TEXT NOT NULL,
    client_secret  TEXT,                        -- envelope (crypto.Seal) or NULL for public clients
    client_secret_ref TEXT,                     -- env:NAME | file:/path alternative
    scopes         TEXT NOT NULL,               -- JSON array
    groups_claim   TEXT NOT NULL DEFAULT '',    -- dotted path, e.g. porch.groups
    role_mappings  TEXT NOT NULL DEFAULT '[]',  -- JSON [{group, role}] evaluated in order
    default_role   TEXT,                        -- role when no mapping matches (NULL = deny)
    provisioning   TEXT NOT NULL CHECK (provisioning IN ('preprovisioned','jit')),
    allowed_domains TEXT NOT NULL DEFAULT '[]', -- JSON, restricts JIT
    allowed_algs   TEXT NOT NULL DEFAULT '["ES256","RS256","EdDSA"]',
    allow_private_network INTEGER NOT NULL DEFAULT 0, -- issuer on a private/loopback address
    enabled        INTEGER NOT NULL DEFAULT 1,
    managed_by     TEXT NOT NULL DEFAULT 'ui',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    revision       INTEGER NOT NULL DEFAULT 1
) STRICT;

-- Transient OIDC authorization state (single use, 10 minutes).
CREATE TABLE oidc_auth_state (
    state_hash    TEXT PRIMARY KEY,
    provider_id   TEXT NOT NULL,
    browser_hash  TEXT NOT NULL,                -- binds the flow to the initiating browser
    nonce_hash    TEXT NOT NULL,
    pkce_verifier TEXT NOT NULL,                -- sealed
    purpose       TEXT NOT NULL,                -- login | link | test
    link_user_id  TEXT,
    return_to     TEXT NOT NULL DEFAULT '/',
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE auth_settings (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    password_login   INTEGER NOT NULL DEFAULT 1,  -- 0 = SSO only (recovery still works)
    idle_timeout_s   INTEGER NOT NULL DEFAULT 1800,
    absolute_timeout_s INTEGER NOT NULL DEFAULT 43200,
    updated_at       INTEGER NOT NULL,
    revision         INTEGER NOT NULL DEFAULT 1
) STRICT;
INSERT INTO auth_settings(id, updated_at) VALUES (1, 0);
