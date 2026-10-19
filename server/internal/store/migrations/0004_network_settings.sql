-- Local-network bypass and trusted proxies (Phase 2, item 5).
--
-- One row, id = 1, created here with the bypass off and no ranges, so a fresh or
-- upgraded installation behaves as before until an administrator turns it on.
-- The two lists are JSON arrays of CIDR strings in canonical form (the server
-- parses, masks and unmaps every entry before it writes); the CHECKs hold the
-- shape if some other code path, or a person with the database open, gets it
-- wrong. Nothing here is secret: addresses and ranges only.

CREATE TABLE network_settings (
    id              INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    -- 1 = a request from inside bypass_cidrs is treated as a signed-in user.
    bypass_enabled  INTEGER NOT NULL DEFAULT 0 CHECK (bypass_enabled IN (0, 1)),
    bypass_cidrs    TEXT    NOT NULL DEFAULT '[]'
                    CHECK (json_valid(bypass_cidrs) AND json_type(bypass_cidrs) = 'array' AND json_array_length(bypass_cidrs) <= 64),
    -- Peers whose X-Forwarded-For / Forwarded / X-Real-IP / X-Forwarded-Proto
    -- are believed, in addition to WHEELHOUSE_TRUSTED_PROXIES.
    trusted_proxies TEXT    NOT NULL DEFAULT '[]'
                    CHECK (json_valid(trusted_proxies) AND json_type(trusted_proxies) = 'array' AND json_array_length(trusted_proxies) <= 64),
    updated_at      INTEGER NOT NULL,
    CHECK (bypass_enabled = 0 OR json_array_length(bypass_cidrs) > 0)
) STRICT;

INSERT INTO network_settings (id, updated_at) VALUES (1, 0);
