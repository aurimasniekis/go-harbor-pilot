-- Accounts an administrator pre-provisioned for single sign-on: the first
-- verified sign-in from this provider with the account's email links it,
-- then the marker is cleared. Unmarked accounts are never linked by email.
ALTER TABLE users ADD COLUMN sso_link_provider TEXT;
