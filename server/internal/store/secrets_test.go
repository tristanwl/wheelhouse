package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/secrets"
)

// The tests use their own table; the real secret columns arrive with the
// features that store secrets.
var testCols = []SecretColumn{{Table: "test_secrets", IDColumn: "id", Column: "api_key"}, {Table: "test_secrets", IDColumn: "id", Column: "password"}}

func mustKey(t *testing.T) secrets.Key {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func unlock(t *testing.T, db *DB, k secrets.Key) *secrets.Cipher {
	t.Helper()
	c, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: k, Source: secrets.SourceEnv}, true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func createSecretsTable(t *testing.T, db *DB) {
	t.Helper()
	mustExec(t, db, "CREATE TABLE test_secrets (id TEXT PRIMARY KEY, api_key BLOB, password BLOB)")
}

// putSecret stores plaintext sealed for its row and field.
func putSecret(t *testing.T, db *DB, c *secrets.Cipher, id, field, plaintext string) {
	t.Helper()
	env, err := c.Seal([]byte(plaintext), secrets.AD("test_secrets", id, field))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, fmt.Sprintf(`INSERT INTO test_secrets (id, %[1]s) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET %[1]s = excluded.%[1]s`, field), id, env)
}

func getSecret(t *testing.T, db *DB, c *secrets.Cipher, id, field string) (string, error) {
	t.Helper()
	var env []byte
	if err := db.QueryRow(ctx(t), fmt.Sprintf("SELECT %s FROM test_secrets WHERE id = ?", field), id).Scan(&env); err != nil {
		t.Fatal(err)
	}
	pt, err := c.Open(env, secrets.AD("test_secrets", id, field))
	return string(pt), err
}

func TestSecretColumnsExist(t *testing.T) {
	db := openTemp(t)
	for _, c := range SecretColumns() {
		if err := c.validate(); err != nil {
			t.Error(err)
		}
		var typ string
		err := db.QueryRow(ctx(t), "SELECT type FROM pragma_table_info(?) WHERE name = ?", c.Table, c.Column).Scan(&typ)
		if err != nil || typ != "BLOB" {
			t.Errorf("%s.%s: type %q, %v (a secret column must be a BLOB)", c.Table, c.Column, typ, err)
		}
		if err := db.QueryRow(ctx(t), "SELECT type FROM pragma_table_info(?) WHERE name = ?", c.Table, c.IDColumn).Scan(&typ); err != nil {
			t.Errorf("%s.%s: %v", c.Table, c.IDColumn, err)
		}
	}
	if err := (SecretColumn{Table: `x"; DROP TABLE meta; --`, IDColumn: "id", Column: "c"}).validate(); err == nil {
		t.Error("an unsafe identifier was accepted")
	}
}

func TestUnlockCreatesVersionOneAndChecksTheKey(t *testing.T) {
	db := openTemp(t)
	key := mustKey(t)
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: key}, false); !errors.Is(err, ErrNoKeyCheck) {
		t.Fatalf("before create: %v", err)
	}
	c := unlock(t, db, key)
	if c.Version() != 1 {
		t.Errorf("version %d", c.Version())
	}
	// Same key: same cipher version. Wrong key: refused.
	if c2, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: key}, false); err != nil || c2.Version() != 1 {
		t.Fatalf("second unlock: %v", err)
	}
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: mustKey(t)}, true); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	// The check value is an envelope, not a plaintext marker.
	raw, err := metaBlob(ctx(t), db.QueryRow, metaKeyCheck)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(keyCheckPlain)) {
		t.Error("the key check is stored in plaintext")
	}
	// A damaged record is reported, not accepted.
	mustExec(t, db, "UPDATE meta SET value = x'00' WHERE key = ?", metaKeyCheck)
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: key}, true); err == nil {
		t.Error("a damaged key record was accepted")
	}
}

func TestRekeyReencryptsEverythingAtomically(t *testing.T) {
	db := openTemp(t)
	createSecretsTable(t, db)
	old := mustKey(t)
	c1 := unlock(t, db, old)
	want := map[string]string{}
	for i := range 25 {
		id := fmt.Sprintf("inst-%d", i)
		putSecret(t, db, c1, id, "api_key", "canary-key-"+id)
		want[id+"/api_key"] = "canary-key-" + id
		if i%2 == 0 { // some rows have both fields, some one
			putSecret(t, db, c1, id, "password", "canary-pass-"+id)
			want[id+"/password"] = "canary-pass-" + id
		}
	}
	before := map[string][]byte{}
	rows, _ := db.Query(ctx(t), "SELECT id, api_key FROM test_secrets")
	for rows.Next() {
		var id string
		var env []byte
		_ = rows.Scan(&id, &env)
		before[id] = env
	}
	_ = rows.Close()

	next := mustKey(t)
	c2, n, err := db.RekeyColumns(ctx(t), c1, next, testCols)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) || c2.Version() != 2 {
		t.Errorf("re-encrypted %d (want %d), version %d", n, len(want), c2.Version())
	}
	for k, v := range want {
		id, field, _ := strings.Cut(k, "/")
		got, err := getSecret(t, db, c2, id, field)
		if err != nil || got != v {
			t.Errorf("%s: %q, %v", k, got, err)
		}
		if _, err := getSecret(t, db, c1, id, field); err == nil {
			t.Errorf("%s still opens with the old key", k)
		}
	}
	rows, _ = db.Query(ctx(t), "SELECT id, api_key FROM test_secrets")
	for rows.Next() {
		var id string
		var env []byte
		_ = rows.Scan(&id, &env)
		if bytes.Equal(env, before[id]) {
			t.Errorf("%s kept its old ciphertext", id)
		}
	}
	_ = rows.Close()

	// The database now answers to the new key only.
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: old}, false); !errors.Is(err, ErrWrongKey) {
		t.Errorf("old key after rekey: %v", err)
	}
	if c, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: next}, false); err != nil || c.Version() != 2 {
		t.Errorf("new key: %v", err)
	}
	// A stale cipher (version 1) cannot rekey again.
	if _, _, err := db.RekeyColumns(ctx(t), c1, mustKey(t), testCols); !errors.Is(err, ErrWrongKey) {
		t.Errorf("stale cipher: %v", err)
	}
	// And a second rotation works from the new one.
	c3, _, err := db.RekeyColumns(ctx(t), c2, mustKey(t), testCols)
	if err != nil || c3.Version() != 3 {
		t.Fatalf("second rekey: %v", err)
	}
	if got, err := getSecret(t, db, c3, "inst-0", "api_key"); err != nil || got != "canary-key-inst-0" {
		t.Errorf("after two rekeys: %q, %v", got, err)
	}
}

// If one value cannot be decrypted, nothing at all is changed.
func TestRekeyIsAllOrNothing(t *testing.T) {
	db := openTemp(t)
	createSecretsTable(t, db)
	key := mustKey(t)
	c1 := unlock(t, db, key)
	for i := range 10 {
		putSecret(t, db, c1, fmt.Sprintf("r%d", i), "api_key", fmt.Sprintf("secret-%d", i))
	}
	// Corrupt the last row the rekey would reach.
	mustExec(t, db, "UPDATE test_secrets SET api_key = x'0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' WHERE id = 'r9'")

	_, _, err := db.RekeyColumns(ctx(t), c1, mustKey(t), testCols)
	if err == nil {
		t.Fatal("a rekey over a corrupt value succeeded")
	}
	// Still under the old key, every intact value readable, version unchanged.
	if c, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: key}, false); err != nil || c.Version() != 1 {
		t.Fatalf("after the failed rekey: %v", err)
	}
	for i := range 9 {
		got, err := getSecret(t, db, c1, fmt.Sprintf("r%d", i), "api_key")
		if err != nil || got != fmt.Sprintf("secret-%d", i) {
			t.Errorf("r%d: %q, %v", i, got, err)
		}
	}
}

func TestRekeyRollsBackOnDatabaseError(t *testing.T) {
	db := openTemp(t)
	createSecretsTable(t, db)
	c1 := unlock(t, db, mustKey(t))
	putSecret(t, db, c1, "a", "api_key", "one")
	// A trigger makes the first UPDATE fail after the key check was read.
	mustExec(t, db, "CREATE TRIGGER nope BEFORE UPDATE ON test_secrets BEGIN SELECT RAISE(ABORT, 'no'); END")
	if _, _, err := db.RekeyColumns(ctx(t), c1, mustKey(t), testCols); err == nil {
		t.Fatal("expected an error")
	}
	if got, err := getSecret(t, db, c1, "a", "api_key"); err != nil || got != "one" {
		t.Errorf("%q, %v", got, err)
	}
	if v, _ := checkVersion(t, db); v != 1 {
		t.Errorf("key version %d after a failed rekey", v)
	}
}

// checkVersion reads the version of the stored key check.
func checkVersion(t *testing.T, db *DB) (uint32, error) {
	t.Helper()
	raw, err := metaBlob(ctx(t), db.QueryRow, metaKeyCheck)
	if err != nil {
		return 0, err
	}
	return secrets.EnvelopeVersion(raw)
}

func TestRekeyWithNoSecretsStored(t *testing.T) {
	db := openTemp(t)
	c1 := unlock(t, db, mustKey(t))
	next := mustKey(t)
	c2, n, err := db.Rekey(ctx(t), c1, next) // the real, currently empty, column list
	if err != nil || n != 0 || c2.Version() != 2 {
		t.Fatalf("%d, %v, %v", n, c2, err)
	}
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: next}, false); err != nil {
		t.Errorf("new key: %v", err)
	}
}

func TestRekeyHandlesNumericIDsAndEmptyColumns(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE nums (id INTEGER PRIMARY KEY, token BLOB)")
	c1 := unlock(t, db, mustKey(t))
	env, _ := c1.Seal([]byte("tok"), secrets.AD("nums", "7", "token"))
	mustExec(t, db, "INSERT INTO nums (id, token) VALUES (7, ?)", env)
	mustExec(t, db, "INSERT INTO nums (id, token) VALUES (8, NULL)") // unset: skipped
	cols := []SecretColumn{{Table: "nums", IDColumn: "id", Column: "token"}}
	c2, n, err := db.RekeyColumns(ctx(t), c1, mustKey(t), cols)
	if err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	var got []byte
	_ = db.QueryRow(ctx(t), "SELECT token FROM nums WHERE id = 7").Scan(&got)
	if pt, err := c2.Open(got, secrets.AD("nums", "7", "token")); err != nil || string(pt) != "tok" {
		t.Errorf("%q, %v", pt, err)
	}
}

// A value copied to another row does not open: the row id is bound in.
func TestSecretIsBoundToItsRowAndField(t *testing.T) {
	db := openTemp(t)
	createSecretsTable(t, db)
	c := unlock(t, db, mustKey(t))
	putSecret(t, db, c, "radarr", "api_key", "radarr-key")
	putSecret(t, db, c, "sonarr", "api_key", "sonarr-key")
	mustExec(t, db, "UPDATE test_secrets SET api_key = (SELECT api_key FROM test_secrets WHERE id = 'radarr') WHERE id = 'sonarr'")
	if got, err := getSecret(t, db, c, "sonarr", "api_key"); err == nil {
		t.Errorf("a value moved to another row opened as %q", got)
	}
	mustExec(t, db, "UPDATE test_secrets SET password = api_key WHERE id = 'radarr'")
	if got, err := getSecret(t, db, c, "radarr", "password"); err == nil {
		t.Errorf("a value moved to another field opened as %q", got)
	}
}

func TestRekeyFilesGeneratedKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful")
	}
	cfg := t.TempDir()
	db := openAt(t, filepath.Join(cfg, testDBName))
	createSecretsTable(t, db)
	l, err := secrets.Load(secrets.LoadOptions{Getenv: func(string) string { return "" }, ConfigDir: cfg, Generate: true})
	if err != nil {
		t.Fatal(err)
	}
	c1, err := db.UnlockSecrets(ctx(t), l, true)
	if err != nil {
		t.Fatal(err)
	}
	putSecret(t, db, c1, "x", "api_key", "canary-x")

	oldRaw, _ := os.ReadFile(l.Path)
	c2, n, err := db.rekeyFiles(ctx(t), c1, l, testCols) // the test table is not a registered column
	if err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	if got, err := getSecret(t, db, c2, "x", "api_key"); err != nil || got != "canary-x" {
		t.Errorf("%q, %v", got, err)
	}
	newRaw, _ := os.ReadFile(l.Path)
	if len(newRaw) != secrets.KeySize || bytes.Equal(oldRaw, newRaw) {
		t.Fatal("the key file was not replaced by a new key")
	}
	if st, _ := os.Stat(l.Path); st.Mode().Perm() != 0o600 {
		t.Errorf("new key file mode %04o", st.Mode().Perm())
	}
	if _, err := os.Stat(l.Path + ".next"); err == nil {
		t.Error("the staged key file was left behind")
	}
	if c2.Version() != 2 {
		t.Errorf("version %d", c2.Version())
	}
	// A restart: load the key again and unlock.
	l2, err := secrets.Load(secrets.LoadOptions{Getenv: func(string) string { return "" }, ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := db.UnlockSecrets(ctx(t), l2, false); err != nil || c.Version() != 2 {
		t.Fatalf("restart: %v", err)
	}
	// An operator-supplied key cannot be replaced in place.
	if _, _, err := db.RekeyFiles(ctx(t), c2, secrets.Loaded{Key: l2.Key, Source: secrets.SourceEnv}); err == nil {
		t.Error("RekeyFiles accepted an environment key")
	}
}

// Crash between the commit and the key file swap: the next start finishes it.
func TestUnlockFinishesInterruptedRekey(t *testing.T) {
	cfg := t.TempDir()
	db := openAt(t, filepath.Join(cfg, testDBName))
	l, err := secrets.Load(secrets.LoadOptions{Getenv: func(string) string { return "" }, ConfigDir: cfg, Generate: true})
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := db.UnlockSecrets(ctx(t), l, true)

	// Stage and commit, but "crash" before the rename.
	staged := l.Path + ".next"
	nk, err := secrets.GenerateKeyFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Rekey(ctx(t), c1, nk); err != nil {
		t.Fatal(err)
	}
	// secret.key still holds the old key, which no longer matches.
	if _, err := db.UnlockSecrets(ctx(t), l, false); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if _, err := os.Stat(staged); err == nil {
		t.Error("the staged key is still there after recovery")
	}
	l2, _ := secrets.Load(secrets.LoadOptions{Getenv: func(string) string { return "" }, ConfigDir: cfg})
	if !l2.Key.Equal(nk) {
		t.Error("secret.key is not the new key")
	}
	if c, err := db.UnlockSecrets(ctx(t), l2, false); err != nil || c.Version() != 2 {
		t.Errorf("after recovery: %v", err)
	}
}

// Crash after staging but before the commit: the old key stays, the leftover goes.
func TestUnlockDiscardsAbortedRekey(t *testing.T) {
	cfg := t.TempDir()
	db := openAt(t, filepath.Join(cfg, testDBName))
	l, _ := secrets.Load(secrets.LoadOptions{Getenv: func(string) string { return "" }, ConfigDir: cfg, Generate: true})
	if _, err := db.UnlockSecrets(ctx(t), l, true); err != nil {
		t.Fatal(err)
	}
	staged := l.Path + ".next"
	if _, err := secrets.GenerateKeyFile(staged); err != nil {
		t.Fatal(err)
	}
	if c, err := db.UnlockSecrets(ctx(t), l, false); err != nil || c.Version() != 1 {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(staged); err == nil {
		t.Error("the aborted rekey's staged key was not removed")
	}
	// RekeyFiles refuses to run over a leftover it cannot explain.
	if _, err := secrets.GenerateKeyFile(staged); err != nil {
		t.Fatal(err)
	}
	c1, _ := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: l.Key}, false)
	if _, _, err := db.RekeyFiles(ctx(t), c1, l); err == nil {
		t.Error("RekeyFiles ran over an existing staged key")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Error("RekeyFiles deleted a staged key it did not create")
	}
}

func TestRekeyToFile(t *testing.T) {
	db := openTemp(t)
	c1 := unlock(t, db, mustKey(t))
	out := filepath.Join(t.TempDir(), "new.key")
	c2, _, err := db.RekeyToFile(ctx(t), c1, out)
	if err != nil || c2.Version() != 2 {
		t.Fatal(err)
	}
	k, err := secrets.ReadKeyFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: k}, false); err != nil {
		t.Errorf("the written key does not open the database: %v", err)
	}
	// It never overwrites, and a failed rekey removes only its own file.
	if _, _, err := db.RekeyToFile(ctx(t), c2, out); err == nil {
		t.Error("overwrote an existing file")
	}
	if _, err := secrets.ReadKeyFile(out); err != nil {
		t.Errorf("the existing file was damaged: %v", err)
	}
	stale := filepath.Join(t.TempDir(), "n2.key")
	if _, _, err := db.RekeyToFile(ctx(t), c1, stale); err == nil { // c1 is stale
		t.Fatal("stale cipher accepted")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a failed rekey left a key file behind")
	}
}

// The canary: plaintext secrets must never reach the database file, its
// write-ahead log or a backup.
func TestPlaintextNeverReachesDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), testDBName)
	db := openAt(t, path)
	createSecretsTable(t, db)
	c := unlock(t, db, mustKey(t))

	canaries := []string{"CANARY-APIKEY-7f3a9c1e", "CANARY-PASSWORD-b82d04aa", "CANARY-REPLACED-5c6e1f90"}
	putSecret(t, db, c, "radarr", "api_key", canaries[0])
	putSecret(t, db, c, "radarr", "password", canaries[1])
	putSecret(t, db, c, "radarr", "api_key", canaries[2]) // "replace" overwrites the first
	for i := range 300 {                                  // plenty of churn so pages are reused
		putSecret(t, db, c, fmt.Sprintf("x%d", i), "api_key", fmt.Sprintf("CANARY-FILLER-%04d", i))
	}
	mustExec(t, db, "DELETE FROM test_secrets WHERE id LIKE 'x%'")

	// While open (so the -wal holds the pages) ...
	live := readAll(t, path)
	if _, ok := live[filepath.Base(path)+"-wal"]; !ok {
		t.Fatal("no -wal file to scan")
	}
	// ... after a backup ...
	bk := filepath.Join(t.TempDir(), "backup.db")
	if err := db.Backup(ctx(t), bk); err != nil {
		t.Fatal(err)
	}
	// ... after a rekey ...
	if _, _, err := db.RekeyColumns(ctx(t), c, mustKey(t), testCols); err != nil {
		t.Fatal(err)
	}
	live2 := readAll(t, path)
	// ... and after a clean close and checkpoint.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	closed := readAll(t, path)
	backup := readAll(t, bk)

	for set, files := range map[string]map[string][]byte{"live": live, "after rekey": live2, "closed": closed, "backup": backup} {
		for _, needle := range append(canaries, "CANARY-FILLER", "CANARY-") {
			if hits := containsAny(files, needle); len(hits) > 0 {
				t.Errorf("%s: plaintext %q found in %v", set, needle, hits)
			}
		}
	}
	// The scan itself works: a plaintext value is found where it was put.
	plain := openAt(t, filepath.Join(t.TempDir(), "plain.db"))
	mustExec(t, plain, "CREATE TABLE leak (v TEXT)")
	mustExec(t, plain, "INSERT INTO leak VALUES (?)", canaries[0])
	if hits := containsAny(readAll(t, plain.Path()), canaries[0]); len(hits) == 0 {
		t.Fatal("the canary scan cannot see plaintext: it proves nothing")
	}
}

// secure_delete: a deleted value is overwritten, not left in a free page.
func TestDeletedValuesAreOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), testDBName)
	db := openAt(t, path)
	mustExec(t, db, "CREATE TABLE junk (v BLOB)")
	marker := bytes.Repeat([]byte("DELETED-MARKER-"), 20)
	mustExec(t, db, "INSERT INTO junk VALUES (?)", marker)
	mustExec(t, db, "DELETE FROM junk")
	mustTx(t, db, func(tx *sql.Tx) error { return nil })
	if _, err := db.w.ExecContext(ctx(t), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if hits := containsAny(readAll(t, path), "DELETED-MARKER-"); len(hits) > 0 {
		t.Errorf("deleted data is still in %v", hits)
	}
}
