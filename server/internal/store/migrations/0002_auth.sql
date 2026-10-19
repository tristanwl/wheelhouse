-- Accounts, sessions and the audit log (Phase 2, item 2).
--
-- Times are Unix seconds (UTC). Ids are 128-bit random values as 32 lowercase hex
-- characters, so an id in a URL says nothing about how many accounts exist.
--
-- Nothing in here is reversible: there is no "down". Passwords are stored only
-- as Argon2id hashes (the parameters are part of the encoded value), session
-- tokens only as their SHA-256, so neither column is a secret column in the
-- secrets.Cipher sense: there is nothing to decrypt, and a stolen database
-- yields neither a password nor a usable session.

CREATE TABLE users (
    id                  TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 32),
    -- Unique ignoring case ("Alice" and "alice" are one account). The server
    -- restricts names to ASCII letters, digits and . _ @ -, so NOCASE (which
    -- folds ASCII only) is exact.
    username            TEXT    NOT NULL COLLATE NOCASE CHECK (length(username) BETWEEN 3 AND 64),
    -- Encoded Argon2id hash: $argon2id$v=19$m=...,t=...,p=...$salt$hash
    password_hash       TEXT    NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    role                TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
    disabled            INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    -- Set by an admin reset or `wheelhouse reset-password`: the server lets this
    -- account do nothing but change its password until it has.
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
    created_at          INTEGER NOT NULL,
    password_changed_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE UNIQUE INDEX users_username ON users (username);

-- The last enabled administrator cannot be deleted, demoted or disabled. The
-- code checks this too (in the same transaction, so the error is friendly); the
-- triggers are what holds if some other code path, or a person with the
-- database open, gets it wrong.
CREATE TRIGGER users_keep_last_admin_delete
BEFORE DELETE ON users
WHEN OLD.role = 'admin' AND OLD.disabled = 0
 AND NOT EXISTS (SELECT 1 FROM users WHERE role = 'admin' AND disabled = 0 AND id <> OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'last admin');
END;

CREATE TRIGGER users_keep_last_admin_update
BEFORE UPDATE OF role, disabled ON users
WHEN OLD.role = 'admin' AND OLD.disabled = 0 AND (NEW.role <> 'admin' OR NEW.disabled <> 0)
 AND NOT EXISTS (SELECT 1 FROM users WHERE role = 'admin' AND disabled = 0 AND id <> OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'last admin');
END;

CREATE TABLE sessions (
    id         TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 32),
    -- SHA-256 of the random 256-bit token in the cookie. The token itself is
    -- never stored; the CSRF token is derived from it, so it is not stored either.
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    -- Absolute expiry (created_at + 30 days); the idle limit is checked against last_seen.
    expires_at INTEGER NOT NULL,
    ip         TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT ''
) STRICT, WITHOUT ROWID;

CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

-- Security-relevant events. Rows are only ever inserted: the triggers refuse to
-- change or remove one (retention pruning arrives with its own migration, which
-- relaxes the delete trigger for old rows only). No secrets, passwords, tokens or
-- request bodies go in; actor_name is a snapshot so the record outlives the
-- account, which is why actor_id is not a foreign key.
CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at         INTEGER NOT NULL,
    actor_id   TEXT,
    actor_name TEXT    NOT NULL CHECK (length(actor_name) <= 64),
    action     TEXT    NOT NULL CHECK (length(action) BETWEEN 1 AND 64),
    target     TEXT    NOT NULL DEFAULT '' CHECK (length(target) <= 256),
    address    TEXT    NOT NULL DEFAULT '' CHECK (length(address) <= 64),
    detail     TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(detail) AND length(detail) <= 4096)
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);

CREATE TRIGGER audit_log_no_update
BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;

CREATE TRIGGER audit_log_no_delete
BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
