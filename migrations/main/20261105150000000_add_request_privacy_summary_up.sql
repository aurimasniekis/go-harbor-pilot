-- Per-request masking summary (rule names and counts only, never values).
ALTER TABLE requests ADD COLUMN privacy TEXT NOT NULL DEFAULT '{}';
