-- Audit log retention (issue #84).
--
-- The audit table stays append-only for everything recent. This replaces the
-- trigger that refused every DELETE with one that refuses a DELETE of any row
-- younger than 30 days, judged by SQLite's own clock and the row's own
-- timestamp, so no handler, however compromised, can erase recent history; only
-- rows older than that hard floor can go, which is what the retention pruning
-- (default 180 days, never less than 30) deletes. UPDATE stays refused for all
-- rows. The floor is the same constant the server's configuration enforces as
-- the minimum retention (auth.AuditFloorDays); a test keeps the two equal.
DROP TRIGGER audit_log_no_delete;

CREATE TRIGGER audit_log_no_delete_recent
BEFORE DELETE ON audit_log
WHEN OLD.at > CAST(strftime('%s', 'now') AS INTEGER) - 30 * 86400
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only: rows younger than 30 days cannot be deleted');
END;
