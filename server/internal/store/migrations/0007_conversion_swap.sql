-- Record of a file swap in progress (security review, item 10).
--
-- Before the converter moves an original aside, it writes the exact file path and
-- the exact backup path it is about to use here, and clears both once the swap
-- has finished or been rolled back. At start the server restores a stranded
-- original only for a job that still has these set, using these recorded paths
-- (re-validated against the media roots in force). It never looks for files
-- named *.bak. Rows written before this migration have both empty: nothing is
-- ever restored for them.
ALTER TABLE conversion_jobs ADD COLUMN swap_path   TEXT NOT NULL DEFAULT '';
ALTER TABLE conversion_jobs ADD COLUMN swap_backup TEXT NOT NULL DEFAULT '';
