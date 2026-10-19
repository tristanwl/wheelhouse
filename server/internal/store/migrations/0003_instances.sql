-- Stored configuration (Phase 2, item 4): provider instances and feature toggles.
--
-- An instance is one configured copy of a provider (Radarr, Radarr 4K, ...).
-- The settings that are not secret (URL, user name) are JSON in `config`. The
-- secret ones (API key, password, token) are one JSON object sealed with
-- secrets.Cipher in `secrets`, with associated data ("instances", id, "secrets"),
-- so a value cannot be moved to another instance. `secrets` is a registered
-- secret column (store.SecretColumns): `wheelhouse rekey` re-encrypts it and
-- `wheelhouse forget-secrets` sets it to NULL (a NULL means "the key was lost";
-- the instance then reports its credentials as missing).
--
-- Ids are chosen by the administrator: lowercase letters, digits and hyphens,
-- starting with the provider id ("radarr", "radarr-4k"). They appear in URLs.
-- The server enforces the id rule; the checks here are the backstop.

CREATE TABLE instances (
    id          TEXT    NOT NULL PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 64 AND id NOT GLOB '*[^a-z0-9-]*'),
    provider_id TEXT    NOT NULL CHECK (length(provider_id) BETWEEN 1 AND 64),
    name        TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    enabled     INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    -- Non-secret settings, a JSON object.
    config      TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(config) AND json_type(config) = 'object'),
    -- The secret settings as one sealed JSON object (BLOB); NULL when there are none.
    secrets     BLOB,
    -- Display order: the order instances were added in.
    position    INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX instances_position ON instances (position);

-- A feature an administrator switched off or on. A feature with no row is on.
-- (The Dashboard cannot be switched off; the server refuses, and ignores a row.)
CREATE TABLE feature_settings (
    feature_id TEXT    NOT NULL PRIMARY KEY CHECK (length(feature_id) BETWEEN 1 AND 64),
    enabled    INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
