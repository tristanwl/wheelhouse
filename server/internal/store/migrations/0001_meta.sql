-- Key/value facts about this database itself (not user data). Today the only
-- row is secret_key_check, the encrypted canary that tells which master key
-- version this database is under (see secrets.go).
CREATE TABLE meta (
    key   TEXT NOT NULL PRIMARY KEY,
    value ANY  NOT NULL
) STRICT, WITHOUT ROWID;
