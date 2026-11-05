-- Intent and its provenance per request.
ALTER TABLE requests ADD COLUMN intent TEXT;
ALTER TABLE requests ADD COLUMN intent_source TEXT;          -- declared | rule | classifier
ALTER TABLE requests ADD COLUMN intent_detail TEXT NOT NULL DEFAULT '{}'; -- classifier version, latency, outcome
-- Conversation history deduplication.
ALTER TABLE requests ADD COLUMN new_messages INTEGER NOT NULL DEFAULT 0;
ALTER TABLE requests ADD COLUMN repeated_messages INTEGER NOT NULL DEFAULT 0;
ALTER TABLE requests ADD COLUMN unique_tool_results INTEGER NOT NULL DEFAULT 0;
ALTER TABLE requests ADD COLUMN occurrence TEXT NOT NULL DEFAULT 'uncertain'; -- unique | uncertain
CREATE INDEX requests_intent ON requests(intent, created_at);

-- Message fingerprints already seen in a session (hashes only, never content).
CREATE TABLE session_messages (
    session_id       TEXT NOT NULL,
    fingerprint      TEXT NOT NULL,
    first_request_id TEXT NOT NULL,
    first_seen       INTEGER NOT NULL,
    PRIMARY KEY (session_id, fingerprint)
) STRICT, WITHOUT ROWID;
