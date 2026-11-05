-- Scopes for listing, quotas and bulk deletion.
ALTER TABLE traces ADD COLUMN plan_id TEXT;
ALTER TABLE traces ADD COLUMN policy_id TEXT;
ALTER TABLE traces ADD COLUMN binding_id TEXT;
ALTER TABLE traces ADD COLUMN session_id TEXT;
CREATE INDEX traces_plan ON traces(plan_id, expires_at);
CREATE INDEX traces_session ON traces(session_id);
