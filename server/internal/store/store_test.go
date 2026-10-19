package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestOpenCreatesPrivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wheelhouse.db")
	db := openAt(t, path)
	mustExec(t, db, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	mustExec(t, db, "INSERT INTO t (v) VALUES ('x')")
	if runtime.GOOS == "windows" {
		return
	}
	// The main file, the write-ahead log and the shared-memory index all hold
	// data; none may be readable by anyone else.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		st, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("%s: %v", suffix, err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %04o, want 0600", filepath.Base(path+suffix), st.Mode().Perm())
		}
	}
}

func TestOpenSetsPragmas(t *testing.T) {
	db := openTemp(t)
	want := map[string]string{
		"journal_mode":   "wal",
		"foreign_keys":   "1",
		"busy_timeout":   "5000",
		"synchronous":    "2", // FULL
		"secure_delete":  "1",
		"trusted_schema": "0",
	}
	for pragma, v := range want {
		var got string
		// The writer has them all; the reader pool has the same except journal/synchronous being a file/writer concern.
		if err := db.w.QueryRowContext(ctx(t), "PRAGMA "+pragma).Scan(&got); err != nil || got != v {
			t.Errorf("writer %s = %q (%v), want %q", pragma, got, err, v)
		}
	}
	for _, pragma := range []string{"foreign_keys", "secure_delete"} {
		var got string
		if err := db.QueryRow(ctx(t), "PRAGMA "+pragma).Scan(&got); err != nil || got != "1" {
			t.Errorf("reader %s = %q (%v)", pragma, got, err)
		}
	}
	var ver string
	if err := db.QueryRow(ctx(t), "SELECT sqlite_version()").Scan(&ver); err != nil || ver == "" {
		t.Fatal(ver, err)
	}
}

func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	db := openTemp(t)
	// Hold several reader connections at once so the pool opens more than one.
	var conns []*sql.Conn
	for range readers {
		c, err := db.r.Conn(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		var fk, qo int
		if err := c.QueryRowContext(ctx(t), "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Errorf("reader %d foreign_keys = %d, %v", i, fk, err)
		}
		if err := c.QueryRowContext(ctx(t), "PRAGMA query_only").Scan(&qo); err != nil || qo != 1 {
			t.Errorf("reader %d query_only = %d, %v", i, qo, err)
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

func TestReadersCannotWrite(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE t (v TEXT)")
	rows, err := db.Query(ctx(t), "INSERT INTO t VALUES ('x')")
	if err == nil {
		_ = rows.Close()
		t.Fatal("a write through the read pool succeeded")
	}
	if count(t, db, "t") != 0 {
		t.Error("the row was written")
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE parent (id INTEGER PRIMARY KEY)")
	mustExec(t, db, "CREATE TABLE child (id INTEGER PRIMARY KEY, p INTEGER NOT NULL REFERENCES parent(id))")
	if _, err := db.Exec(ctx(t), "INSERT INTO child (p) VALUES (42)"); err == nil {
		t.Fatal("a child row with no parent was accepted")
	}
}

func TestOpenRefusesBadLocations(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(ctx(t), filepath.Join(dir, "missing", "w.db"))
	mustContain(t, err, "does not exist")

	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx(t), filepath.Join(file, "w.db"))
	mustContain(t, err, "not a directory")

	_, err = Open(ctx(t), dir) // a directory where the file should be
	mustContain(t, err, "not a regular file")
}

func TestOpenRefusesWorldWritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx(t), filepath.Join(dir, "w.db"))
	mustContain(t, err, "writable by everyone", "chmod")
	// A sticky world-writable directory (like /tmp) is acceptable.
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	openAt(t, filepath.Join(dir, "w.db"))
}

func TestOpenRefusesWrongFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful")
	}
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := Open(ctx(t), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o666, 0o601} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		_, err := Open(ctx(t), path)
		mustContain(t, err, "chmod 600")
		_, err = Open(ctx(t), path, Options{Existing: true})
		mustContain(t, err, "chmod 600")
	}
	if err := os.Chmod(path, 0o400); err != nil { // read-only for the owner is not "too open"
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	openAt(t, path)
}

func TestOpenExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	_, err := Open(ctx(t), path, Options{Existing: true})
	mustContain(t, err, "no database")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("Existing created the file")
	}
	// It does not migrate either.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openAt(t, path, Options{Existing: true})
	var n int
	if err := db.QueryRow(ctx(t), "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil || n != 0 {
		t.Errorf("an Existing open changed an empty database: %d tables, %v", n, err)
	}
}

func TestPathsWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my config #1 ?x=1&_pragma=journal_mode(DELETE) %41")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "w d?b#.db")
	db := openAt(t, path)
	mustExec(t, db, "CREATE TABLE t (v)")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the database is not at the requested path: %v", err)
	}
	var mode string
	if err := db.w.QueryRowContext(ctx(t), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v: the path leaked into the DSN", mode, err)
	}
}

func TestTxCommitRollbackPanic(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE t (v TEXT)")

	mustTx(t, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx(t), "INSERT INTO t VALUES ('committed')")
		return err
	})
	boom := errors.New("boom")
	err := db.Tx(ctx(t), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx(t), "INSERT INTO t VALUES ('rolled back')"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		_ = db.Tx(ctx(t), func(tx *sql.Tx) error {
			_, _ = tx.ExecContext(ctx(t), "INSERT INTO t VALUES ('panicked')")
			panic("oops")
		})
	}()
	if n := count(t, db, "t"); n != 1 {
		t.Errorf("%d rows, want only the committed one", n)
	}
	// The writer is usable after both.
	mustExec(t, db, "INSERT INTO t VALUES ('after')")
}

func TestReadTxSnapshot(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE t (v TEXT)")
	mustExec(t, db, "INSERT INTO t VALUES ('a')")
	err := db.ReadTx(ctx(t), func(tx *sql.Tx) error {
		var before, after int
		if err := tx.QueryRowContext(ctx(t), "SELECT count(*) FROM t").Scan(&before); err != nil {
			return err
		}
		mustExec(t, db, "INSERT INTO t VALUES ('b')") // a write while the reader is mid-snapshot
		if err := tx.QueryRowContext(ctx(t), "SELECT count(*) FROM t").Scan(&after); err != nil {
			return err
		}
		if before != 1 || after != 1 {
			t.Errorf("snapshot changed: %d then %d", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "t"); n != 2 {
		t.Errorf("%d rows", n)
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE a (n INTEGER)")
	mustExec(t, db, "CREATE TABLE b (n INTEGER)")
	const writers, perWriter, readersN = 8, 40, 6

	var wg sync.WaitGroup
	errs := make(chan error, writers+readersN)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				n := w*perWriter + i
				// Two inserts in one transaction: readers must never see one without the other.
				if err := db.Tx(ctx(t), func(tx *sql.Tx) error {
					if _, err := tx.ExecContext(ctx(t), "INSERT INTO a VALUES (?)", n); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx(t), "INSERT INTO b VALUES (?)", n)
					return err
				}); err != nil {
					errs <- fmt.Errorf("writer %d: %w", w, err)
					return
				}
			}
		}()
	}
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for r := range readersN {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				var na, nb int
				if err := db.ReadTx(ctx(t), func(tx *sql.Tx) error {
					if err := tx.QueryRowContext(ctx(t), "SELECT count(*) FROM a").Scan(&na); err != nil {
						return err
					}
					return tx.QueryRowContext(ctx(t), "SELECT count(*) FROM b").Scan(&nb)
				}); err != nil {
					errs <- fmt.Errorf("reader %d: %w", r, err)
					return
				}
				if na != nb {
					errs <- fmt.Errorf("reader %d saw a=%d b=%d: a torn transaction", r, na, nb)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := count(t, db, "a"); n != writers*perWriter {
		t.Errorf("a has %d rows, want %d", n, writers*perWriter)
	}
}

// Two handles on one file (the server and a CLI command in another process
// behave the same way) must queue on the file lock, not fail with "database is
// locked".
func TestTwoHandlesShareTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	a, b := openAt(t, path), openAt(t, path)
	mustExec(t, a, "CREATE TABLE t (who TEXT, n INTEGER)")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, h := range []struct {
		name string
		db   *DB
	}{{"a", a}, {"b", b}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				if _, err := h.db.Exec(ctx(t), "INSERT INTO t VALUES (?, ?)", h.name, i); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := count(t, a, "t"); n != 200 {
		t.Errorf("%d rows", n)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db := openTemp(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx(t), "SELECT 1"); err == nil {
		t.Error("a closed database accepted a statement")
	}
	// Closing the last connection checkpoints and removes the WAL.
	if _, err := os.Stat(db.Path() + "-wal"); err == nil {
		t.Error("the -wal file was left behind after Close")
	}
}

func TestMeta(t *testing.T) {
	db := openTemp(t)
	if _, err := db.GetMeta(ctx(t), "nope"); !errors.Is(err, ErrNoMeta) {
		t.Fatalf("err = %v", err)
	}
	if err := db.SetMeta(ctx(t), "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(ctx(t), "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, err := db.GetMeta(ctx(t), "k"); err != nil || v != "v2" {
		t.Fatalf("%v, %v", v, err)
	}
	if err := db.SetMeta(ctx(t), "n", 7); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.GetMeta(ctx(t), "n"); v != int64(7) {
		t.Errorf("n = %#v", v)
	}
	if _, err := db.Exec(ctx(t), "INSERT INTO meta (key, value) VALUES ('null', NULL)"); err == nil {
		t.Error("NULL value accepted")
	}
	if strings.Contains(db.Path(), "..") {
		t.Error("Path is not clean")
	}
}
