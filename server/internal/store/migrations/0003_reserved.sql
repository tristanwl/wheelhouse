-- Reserved: version 3 is assigned to Phase 2 item 4 (stored configuration), which
-- is built in parallel with item 5. Migrations may not have gaps, so this empty
-- placeholder lets 0004_network_settings.sql load before item 4 lands.
--
-- WHOEVER MERGES SECOND: delete this file when the real 0003 is on main (a
-- duplicate version fails every start and test loudly, so it cannot be missed).
-- No release has been cut with Phase 2 schema, so nothing needs migrating off it.

SELECT 1;
