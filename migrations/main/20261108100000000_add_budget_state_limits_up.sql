-- Limit in force when each threshold event fired, so raising a budget's
-- limit re-arms its warning and exceeded events within the same window
-- (D-044). Rows written before this migration have NULL limits and re-arm
-- once.
ALTER TABLE budget_state ADD COLUMN warned_limit_e10 INTEGER;
ALTER TABLE budget_state ADD COLUMN exceeded_limit_e10 INTEGER;
