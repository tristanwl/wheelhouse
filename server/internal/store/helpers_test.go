package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

const testDBName = "wheelhouse.db"

func bgCtx() context.Context { return context.Background() }

// openAt opens (and cleans up) a database at path.
func openAt(t *testing.T, path string, opts ...Options) *DB {
	t.Helper()
	db, err := Open(ctx(t), path, opts...)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openTemp opens a fresh database in a private temporary directory.
func openTemp(t *testing.T) *DB {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "wheelhouse.db"))
}

func mustExec(t *testing.T, db *DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx(t), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(ctx(t), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func mustTx(t *testing.T, db *DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := db.Tx(ctx(t), fn); err != nil {
		t.Fatal(err)
	}
}

// readAll returns the bytes of the database file and its -wal and -shm
// companions, whichever exist.
func readAll(t *testing.T, path string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		b, err := os.ReadFile(path + suffix)
		if err == nil {
			out[filepath.Base(path)+suffix] = b
		}
	}
	return out
}

func containsAny(files map[string][]byte, needle string) []string {
	var hits []string
	for name, b := range files {
		if bytes.Contains(b, []byte(needle)) {
			hits = append(hits, name)
		}
	}
	return hits
}

func mustContain(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", parts)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not mention %q", err, p)
		}
	}
}
