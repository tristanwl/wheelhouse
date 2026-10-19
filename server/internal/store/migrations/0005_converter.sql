-- Converter settings and the persisted conversion queue (Phase 2, item 8;
-- issues #31 and #33).
--
-- converter_settings is one row, id = 1. media_roots is NULL until an
-- administrator saves roots (then WHEELHOUSE_MEDIA_ROOTS, then the built-in
-- /movies and /tv decide); a JSON array of absolute paths once set, validated by
-- the server before it writes. include_hdr is off by default: HDR and Dolby
-- Vision files are not conversion candidates. Nothing here is secret.

CREATE TABLE converter_settings (
    id         INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    media_roots TEXT   CHECK (media_roots IS NULL OR
                       (json_valid(media_roots) AND json_type(media_roots) = 'array'
                        AND json_array_length(media_roots) BETWEEN 1 AND 32)),
    include_hdr INTEGER NOT NULL DEFAULT 0 CHECK (include_hdr IN (0, 1)),
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO converter_settings (id, updated_at) VALUES (1, 0);

-- The conversion queue. seq orders the jobs (oldest first, so queued jobs resume
-- in the order they were added). The row is the job as the API shows it plus
-- what is needed to run it again after a restart; nothing in it is trusted when
-- it is read back: the server re-validates the enums, and resolves the path
-- against the current media roots, before it runs a job.
--
--   path     the resolved real path that was inside the media roots when the job
--            was queued (what the job will touch).
--   params   JSON object with the user-visible request fields (filePath, title,
--            originalSize, backend, targetFamily), kept for display and replay.
--   owner    the user id that started it, or 'bypass' (local-network bypass) or
--            'cli'; owner_name is the name at that time, kept for display.
--   ack_*    the explicit acknowledgements the request carried.
CREATE TABLE conversion_jobs (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT    NOT NULL UNIQUE,
    instance_id  TEXT    NOT NULL,
    item_id      INTEGER NOT NULL,
    path         TEXT    NOT NULL,
    params       TEXT    NOT NULL CHECK (json_valid(params) AND json_type(params) = 'object'),
    hdr          TEXT    NOT NULL DEFAULT 'none'
                 CHECK (hdr IN ('none', 'hdr10', 'hlg', 'dolby-vision', 'unknown')),
    state        TEXT    NOT NULL
                 CHECK (state IN ('queued', 'probing', 'running', 'success', 'failed', 'canceled')),
    progress     INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error        TEXT    NOT NULL DEFAULT '',
    new_size     INTEGER NOT NULL DEFAULT 0,
    backup_path  TEXT    NOT NULL DEFAULT '',
    log          TEXT    NOT NULL DEFAULT '',
    owner        TEXT    NOT NULL,
    owner_name   TEXT    NOT NULL DEFAULT '',
    ack_hardlink INTEGER NOT NULL DEFAULT 0 CHECK (ack_hardlink IN (0, 1)),
    ack_hdr      INTEGER NOT NULL DEFAULT 0 CHECK (ack_hdr IN (0, 1)),
    created_at   INTEGER NOT NULL,
    started_at   INTEGER NOT NULL DEFAULT 0,
    finished_at  INTEGER NOT NULL DEFAULT 0
) STRICT;

-- One active job per file, enforced by the database as well as by the queue:
-- two ffmpeg jobs on one path would race on the same temp and backup names.
CREATE UNIQUE INDEX conversion_jobs_one_active_per_path
    ON conversion_jobs (path) WHERE state IN ('queued', 'probing', 'running');
CREATE INDEX conversion_jobs_finished ON conversion_jobs (finished_at);
