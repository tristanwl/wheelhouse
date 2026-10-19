package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"github.com/tristanwl/wheelhouse/server/internal/secrets"
)

// SecretColumn names a column that holds secrets.Cipher envelopes (a BLOB), so
// Rekey can find and re-encrypt it. A value is sealed with
// secrets.AD(Table, id, Column), where id is the row's IDColumn as text.
type SecretColumn struct {
	Table    string
	IDColumn string
	Column   string
}

// secretColumns lists every column that stores a secret. The migration that
// adds such a column adds it here too, and TestSecretColumnsExist fails if the
// two disagree.
var secretColumns = []SecretColumn{
	// The sealed JSON object of an instance's secret settings (0003_instances.sql).
	{Table: "instances", IDColumn: "id", Column: "secrets"},
}

// SecretColumns returns the registered secret columns.
func SecretColumns() []SecretColumn { return append([]SecretColumn(nil), secretColumns...) }

const (
	metaKeyCheck    = "secret_key_check"
	keyCheckPlain   = "wheelhouse secret key check"
	nextKeySuffix   = ".next"
	firstKeyVersion = 1
)

// keyCheckAD binds the check value to its meta row.
var keyCheckAD = secrets.AD("meta", metaKeyCheck, "value")

// Errors from the secret key handling. None contains key material.
var (
	// ErrWrongKey means the master key does not match this database: another
	// key file, or the database was restored without its key.
	ErrWrongKey = errors.New("the secret key does not match this database (a different key file, or a restored database without its key)")
	// ErrNoKeyCheck means no secret was ever stored, so there is nothing to
	// check a key against.
	ErrNoKeyCheck = errors.New("this database has no secret key record yet")
)

// ErrKeyChanged means the key record in the database is not the one this
// process unlocked at start: another process ran `rekey` or `forget-secrets`
// against the same database while the server was up (the configuration lock
// prevents that, this is the second line of defence). Anything this process
// sealed now would be sealed under a key the database no longer uses and lost
// at the next start, so it must not seal until it is restarted.
var ErrKeyChanged = errors.New("the secret key of the database was changed by another process while Wheelhouse was running (rekey or forget-secrets); restart Wheelhouse so it uses the new key")

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (c SecretColumn) validate() error {
	for _, s := range []string{c.Table, c.IDColumn, c.Column} {
		if !identifier.MatchString(s) {
			return fmt.Errorf("store: %q is not a valid table or column name", s)
		}
	}
	return nil
}

// UnlockSecrets returns the Cipher for the master key l.Key against this
// database. The database keeps one encrypted check value in meta
// (secret_key_check) whose envelope header says which key version it is under,
// and which only the right key opens, so a wrong or swapped key is an
// ErrWrongKey now instead of garbage later.
//
// With create, the first call on a database with no check value seals one under
// key version 1. For a generated key file, a rekey that stopped between its
// commit and the file swap is finished here: if secret.key does not match but
// secret.key.next does, the next key is promoted; if secret.key matches and a
// secret.key.next is left over, that rekey never committed and the file is
// removed.
func (db *DB) UnlockSecrets(ctx context.Context, l secrets.Loaded, create bool) (*secrets.Cipher, error) {
	c, err := db.unlock(ctx, l.Key, create)
	if l.Source != secrets.SourceGenerated || l.Path == "" {
		return c, err
	}
	staged := l.Path + nextKeySuffix
	if _, serr := os.Lstat(staged); serr != nil {
		return c, err
	}
	switch {
	case err == nil:
		_ = os.Remove(staged)
	case errors.Is(err, ErrWrongKey):
		nk, rerr := secrets.ReadKeyFile(staged)
		if rerr != nil {
			return nil, err
		}
		nc, uerr := db.unlock(ctx, nk, false)
		if uerr != nil {
			return nil, err
		}
		if perr := secrets.PromoteKeyFile(staged, l.Path); perr != nil {
			return nil, perr
		}
		return nc, nil
	}
	return c, err
}

func (db *DB) unlock(ctx context.Context, key secrets.Key, create bool) (*secrets.Cipher, error) {
	env, err := metaBlob(ctx, db.w.QueryRowContext, metaKeyCheck) // the writer's view is always current
	if errors.Is(err, ErrNoMeta) {
		if !create {
			return nil, ErrNoKeyCheck
		}
		c, cerr := secrets.NewCipher(key, firstKeyVersion)
		if cerr != nil {
			return nil, cerr
		}
		sealed, cerr := c.Seal([]byte(keyCheckPlain), keyCheckAD)
		if cerr != nil {
			return nil, cerr
		}
		// Another process may have won the race; keep whichever got in first.
		if _, cerr := db.Exec(ctx, "INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING", metaKeyCheck, sealed); cerr != nil {
			return nil, cerr
		}
		return db.unlock(ctx, key, false)
	}
	if err != nil {
		return nil, err
	}
	return openCheck(key, env)
}

// CheckKey reports ErrKeyChanged when the key check in the database is no
// longer the one c was unlocked from (a different key version, or one c does
// not open). tx, when not nil, is the transaction to read in (the writer's, so
// the answer is current and cannot change before the caller's write commits);
// otherwise the read pool answers.
func (db *DB) CheckKey(ctx context.Context, tx *sql.Tx, c *secrets.Cipher) error {
	qr := db.r.QueryRowContext
	if tx != nil {
		qr = tx.QueryRowContext
	}
	env, err := metaBlob(ctx, qr, metaKeyCheck)
	if err != nil {
		return err
	}
	if v, verr := secrets.EnvelopeVersion(env); verr != nil || v != c.Version() {
		return ErrKeyChanged
	}
	if _, err := currentOpens(c, env); err != nil {
		return ErrKeyChanged
	}
	return nil
}

func openCheck(key secrets.Key, env []byte) (*secrets.Cipher, error) {
	version, err := secrets.EnvelopeVersion(env)
	if err != nil {
		return nil, fmt.Errorf("the secret key record in this database is damaged: %w", err)
	}
	c, err := secrets.NewCipher(key, version)
	if err != nil {
		return nil, err
	}
	if pt, err := c.Open(env, keyCheckAD); err != nil || string(pt) != keyCheckPlain {
		return nil, ErrWrongKey
	}
	return c, nil
}

// Rekey re-encrypts every value in the registered secret columns from the
// current key to next and records the new key version, all in one transaction:
// if anything fails (a value that does not decrypt, a crash) the database is
// untouched and the old key still works. It returns the new Cipher and how many
// values it re-encrypted. current must be the Cipher UnlockSecrets returned;
// a stale one (another rekey ran since) is ErrWrongKey.
func (db *DB) Rekey(ctx context.Context, current *secrets.Cipher, next secrets.Key) (*secrets.Cipher, int, error) {
	return db.RekeyColumns(ctx, current, next, secretColumns)
}

// RekeyColumns is Rekey over an explicit set of columns (tests use it with their
// own tables).
func (db *DB) RekeyColumns(ctx context.Context, current *secrets.Cipher, next secrets.Key, cols []SecretColumn) (*secrets.Cipher, int, error) {
	if current.Version() == ^uint32(0) {
		return nil, 0, errors.New("key version overflow")
	}
	for _, c := range cols {
		if err := c.validate(); err != nil {
			return nil, 0, err
		}
	}
	nextC, err := secrets.NewCipher(next, current.Version()+1)
	if err != nil {
		return nil, 0, err
	}
	count := 0
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		count = 0
		// The writer lock is held from here: nothing can change under us.
		env, err := metaBlob(ctx, tx.QueryRowContext, metaKeyCheck)
		if err != nil {
			return err
		}
		if _, err := currentOpens(current, env); err != nil {
			return err
		}
		for _, col := range cols {
			n, err := rekeyColumn(ctx, tx, col, current, nextC)
			if err != nil {
				return err
			}
			count += n
		}
		sealed, err := nextC.Seal([]byte(keyCheckPlain), keyCheckAD)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE meta SET value = ? WHERE key = ?", sealed, metaKeyCheck)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return nextC, count, nil
}

func currentOpens(c *secrets.Cipher, env []byte) ([]byte, error) {
	pt, err := c.Open(env, keyCheckAD)
	if err != nil || string(pt) != keyCheckPlain {
		return nil, ErrWrongKey
	}
	return pt, nil
}

func rekeyColumn(ctx context.Context, tx *sql.Tx, col SecretColumn, from, to *secrets.Cipher) (int, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT "%s", "%s" FROM "%s" WHERE "%s" IS NOT NULL`, col.IDColumn, col.Column, col.Table, col.Column))
	if err != nil {
		return 0, err
	}
	var ids []any
	var items []secrets.Item
	for rows.Next() {
		var id any
		var env []byte
		if err := rows.Scan(&id, &env); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		items = append(items, secrets.Item{AD: secrets.AD(col.Table, idText(id), col.Column), Envelope: env})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	out, err := secrets.Reencrypt(from, to, items)
	if err != nil {
		return 0, fmt.Errorf("%s.%s: %w", col.Table, col.Column, err)
	}
	q := fmt.Sprintf(`UPDATE "%s" SET "%s" = ? WHERE "%s" = ?`, col.Table, col.Column, col.IDColumn) //nolint:gosec // identifiers are validated against ^[a-z][a-z0-9_]*$ by SecretColumn.validate
	for i, env := range out {
		if _, err := tx.ExecContext(ctx, q, env, ids[i]); err != nil {
			return 0, err
		}
	}
	return len(out), nil
}

// idText is the id as it enters the associated data: text as is, numbers in
// decimal.
func idText(id any) string {
	switch v := id.(type) {
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// RekeyFiles is Rekey for the generated key file: it stages a new random key as
// <key file>.next, re-encrypts everything in one transaction, then renames the
// staged file over the key file. A crash before the commit leaves the old key
// in force (UnlockSecrets removes the leftover staged file); a crash after it
// is finished by UnlockSecrets at the next start, because the staged key is the
// one the database now needs. It returns the new Cipher and the count.
func (db *DB) RekeyFiles(ctx context.Context, current *secrets.Cipher, l secrets.Loaded) (*secrets.Cipher, int, error) {
	return db.rekeyFiles(ctx, current, l, secretColumns)
}

func (db *DB) rekeyFiles(ctx context.Context, current *secrets.Cipher, l secrets.Loaded, cols []SecretColumn) (*secrets.Cipher, int, error) {
	if l.Source != secrets.SourceGenerated || l.Path == "" {
		return nil, 0, errors.New("the key is supplied by the environment; use a new key file instead")
	}
	staged := l.Path + nextKeySuffix
	nk, err := stageKey(staged)
	if err != nil {
		return nil, 0, err
	}
	c, n, err := db.RekeyColumns(ctx, current, nk, cols)
	if err != nil {
		_ = os.Remove(staged)
		return nil, 0, err
	}
	if err := secrets.PromoteKeyFile(staged, l.Path); err != nil {
		return nil, 0, fmt.Errorf("the secrets were re-encrypted but the new key could not replace %s; it is at %s, and the next start will finish the swap: %w", l.Path, staged, err)
	}
	return c, n, nil
}

// RekeyToFile re-encrypts under a new random key written to newKeyPath (mode
// 0600, refusing to overwrite). For keys the operator supplies (a key file
// mount or the environment): the operator then installs the new key where the
// old one was.
func (db *DB) RekeyToFile(ctx context.Context, current *secrets.Cipher, newKeyPath string) (*secrets.Cipher, int, error) {
	nk, err := stageKey(newKeyPath)
	if err != nil {
		return nil, 0, err
	}
	c, n, err := db.Rekey(ctx, current, nk)
	if err != nil {
		_ = os.Remove(newKeyPath)
		return nil, 0, err
	}
	return c, n, nil
}

func stageKey(path string) (secrets.Key, error) {
	k, err := secrets.GenerateKeyFile(path)
	if errors.Is(err, fs.ErrExist) {
		return secrets.Key{}, fmt.Errorf("%s already exists; remove it (a leftover from an earlier rekey?) or choose another path", path)
	}
	return k, err
}

// ForgetSecrets is the recovery from a master key that is lost for good: every
// registered secret column is set to NULL (the values cannot be decrypted by
// anyone, so they are discarded, not guessed) and the key check is replaced by
// one sealed under key, which becomes the database's key. The caller must have
// seen ErrWrongKey first; this refuses to run when key already opens the
// database, so it cannot be used to throw away secrets that are still readable.
// It returns the new Cipher and how many stored values were discarded. Users,
// sessions and the audit log are not touched: passwords are hashed, not
// encrypted, so they never depended on the key.
func (db *DB) ForgetSecrets(ctx context.Context, key secrets.Key) (*secrets.Cipher, int, error) {
	return db.forgetSecrets(ctx, key, secretColumns)
}

func (db *DB) forgetSecrets(ctx context.Context, key secrets.Key, cols []SecretColumn) (*secrets.Cipher, int, error) {
	for _, c := range cols {
		if err := c.validate(); err != nil {
			return nil, 0, err
		}
	}
	var next *secrets.Cipher
	count := 0
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		count = 0
		version := uint32(firstKeyVersion)
		env, err := metaBlob(ctx, tx.QueryRowContext, metaKeyCheck)
		switch {
		case errors.Is(err, ErrNoMeta):
			// Nothing was ever sealed: there is nothing to forget.
		case err != nil:
			return err
		default:
			if _, err := openCheck(key, env); err == nil {
				return errors.New("the current key opens this database; there is nothing to forget")
			}
			old, err := secrets.EnvelopeVersion(env)
			if err != nil {
				return fmt.Errorf("the secret key record in this database is damaged: %w", err)
			}
			version = old + 1
		}
		for _, col := range cols {
			q := fmt.Sprintf(`UPDATE "%s" SET "%s" = NULL WHERE "%s" IS NOT NULL`, col.Table, col.Column, col.Column) //nolint:gosec // identifiers are validated against ^[a-z][a-z0-9_]*$ by SecretColumn.validate
			res, err := tx.ExecContext(ctx, q)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			count += int(n)
		}
		c, err := secrets.NewCipher(key, version)
		if err != nil {
			return err
		}
		sealed, err := c.Seal([]byte(keyCheckPlain), keyCheckAD)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", metaKeyCheck, sealed); err != nil {
			return err
		}
		next = c
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return next, count, nil
}
