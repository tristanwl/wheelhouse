-- Host allow-list for the local-network bypass (Phase 2 item 10, DNS rebinding).
--
-- A JSON array of canonical host names ("nas.example.lan", or "*.example.lan"):
-- requests whose Host is one of these, an IP literal, localhost or a
-- single-label name may get the bypass. The server validates every entry before
-- it writes; the CHECK holds the shape. Existing rows start with an empty list.

ALTER TABLE network_settings ADD COLUMN allowed_hosts TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(allowed_hosts) AND json_type(allowed_hosts) = 'array' AND json_array_length(allowed_hosts) <= 64);
