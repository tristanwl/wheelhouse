package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	ms, err := loadMigrations(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || SchemaVersion() != len(ms) {
		t.Fatalf("%d migrations, SchemaVersion %d", len(ms), SchemaVersion())
	}
	if ms[0].name != "0001_meta" {
		t.Errorf("first migration is %s", ms[0].name)
	}
}

func TestLoadMigrationsRejectsBadSets(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	for name, fsys := range map[string]fstest.MapFS{
		"gap":          {"m/0001_a.sql": file("select 1"), "m/0003_c.sql": file("select 1")},
		"starts at 2":  {"m/0002_a.sql": file("select 1")},
		"duplicate":    {"m/0001_a.sql": file("select 1"), "m/0001_b.sql": file("select 1")},
		"bad name":     {"m/1_a.sql": file("select 1")},
		"upper case":   {"m/0001_A.sql": file("select 1")},
		"not sql":      {"m/0001_a.txt": file("select 1")},
		"no separator": {"m/0001.sql": file("select 1")},
	} {
		if _, err := loadMigrations(fsys, "m"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func appliedVersions(t *testing.T, db *DB) []int {
	t.Helper()
	rows, err := db.Query(ctx(t), "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := Open(ctx(t), path)
	if err != nil {
		t.Fatal(err)
	}
	var first, name, sum string
	if err := db.QueryRow(ctx(t), "SELECT applied_at, name, sha256 FROM schema_migrations WHERE version = 1").Scan(&first, &name, &sum); err != nil {
		t.Fatal(err)
	}
	if name != "0001_meta" || len(sum) != 64 {
		t.Errorf("recorded %q %q", name, sum)
	}
	mustExec(t, db, "INSERT INTO meta (key, value) VALUES ('keep', 'me')")
	_ = db.Close()

	for range 3 {
		db2 := openAt(t, path)
		if got := appliedVersions(t, db2); len(got) != SchemaVersion() {
			t.Fatalf("applied %v", got)
		}
		var again string
		if err := db2.QueryRow(ctx(t), "SELECT applied_at FROM schema_migrations WHERE version = 1").Scan(&again); err != nil || again != first {
			t.Errorf("migration 1 was re-applied: %q vs %q (%v)", again, first, err)
		}
		if v, err := db2.GetMeta(ctx(t), "keep"); err != nil || v != "me" {
			t.Errorf("data lost: %v, %v", v, err)
		}
		if v, err := db2.Version(ctx(t)); err != nil || v != SchemaVersion() {
			t.Errorf("Version = %d, %v", v, err)
		}
		_ = db2.Close()
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := Open(ctx(t), path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, "INSERT INTO schema_migrations (version, name, sha256, applied_at) VALUES (?, 'from_the_future', 'x', 'now')", SchemaVersion()+1)
	_ = db.Close()

	_, err = Open(ctx(t), path)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v", err)
	}
	mustContain(t, err, "newer")
	// Nothing was changed by the refusal, and a backup can still be taken of it.
	db2 := openAt(t, path, Options{Existing: true})
	if got := appliedVersions(t, db2); len(got) != SchemaVersion()+1 {
		t.Errorf("applied %v", got)
	}
	if err := db2.Backup(ctx(t), filepath.Join(t.TempDir(), "b.db")); err != nil {
		t.Errorf("backup of a newer database: %v", err)
	}
}

func TestRefusesChangedOrDamagedHistory(t *testing.T) {
	for name, stmt := range map[string]string{
		"edited migration": "UPDATE schema_migrations SET sha256 = 'tampered' WHERE version = 1",
		"renamed":          "UPDATE schema_migrations SET name = '0001_other' WHERE version = 1",
		"gap in history":   "UPDATE schema_migrations SET version = 3 WHERE version = 1",
	} {
		path := filepath.Join(t.TempDir(), "w.db")
		db, err := Open(ctx(t), path)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, stmt)
		_ = db.Close()
		if _, err := Open(ctx(t), path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMigrationsApplyInOrderAndAllOrNothing(t *testing.T) {
	db := openTemp(t)
	// Start from the real set, then add migrations of our own on top.
	real, _ := loadMigrations(migrationFS, "migrations")
	base := len(real)
	mk := func(v int, sql string) migration {
		name := fmt.Sprintf("%04d_test", v)
		return migration{version: v, name: name, sql: sql, sum: name}
	}
	ms := append(append([]migration{}, real...),
		mk(base+1, "CREATE TABLE a (id INTEGER PRIMARY KEY); INSERT INTO a VALUES (1);"),
		mk(base+2, "CREATE TABLE b (id INTEGER PRIMARY KEY, a INTEGER REFERENCES a(id))"))

	if err := db.migrateWith(ctx(t), ms); err != nil {
		t.Fatal(err)
	}
	if got := appliedVersions(t, db); len(got) != base+2 || got[base+1] != base+2 {
		t.Fatalf("applied %v", got)
	}
	if count(t, db, "a") != 1 {
		t.Error("migration 2 did not run")
	}

	// A failing migration after a good one: neither is applied.
	bad := append(append([]migration{}, ms...),
		mk(base+3, "CREATE TABLE c (id INTEGER)"),
		mk(base+4, "CREATE TABLE d (id INTEGER); THIS IS NOT SQL"))
	err := db.migrateWith(ctx(t), bad)
	mustContain(t, err, fmt.Sprintf("%04d_test", base+4))
	if got := appliedVersions(t, db); len(got) != base+2 {
		t.Errorf("a failed upgrade left %v applied", got)
	}
	for _, table := range []string{"c", "d"} {
		var n int
		if err := db.QueryRow(ctx(t), "SELECT count(*) FROM sqlite_master WHERE name = ?", table).Scan(&n); err != nil || n != 0 {
			t.Errorf("table %s survived the failed upgrade", table)
		}
	}
	// The database still works and the good set still applies.
	if err := db.migrateWith(ctx(t), ms); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStartsMigrateOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	dbs := make(chan *DB, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := Open(ctx(t), path)
			if err != nil {
				errs <- err
				return
			}
			dbs <- db
		}()
	}
	wg.Wait()
	close(errs)
	close(dbs)
	for err := range errs {
		t.Error(err)
	}
	var one *DB
	for db := range dbs {
		one = db
		t.Cleanup(func() { _ = db.Close() })
	}
	if one != nil {
		if got := appliedVersions(t, one); len(got) != SchemaVersion() {
			t.Errorf("applied %v", got)
		}
	}
}

func TestMigrationFailureLeavesNoPartialSchema(t *testing.T) {
	// The same property through the public path: a database with a leftover
	// table that makes migration 1 fail must come out unchanged.
	path := filepath.Join(t.TempDir(), "w.db")
	db := openAt(t, path, Options{Existing: false})
	_ = db.Close()
	raw := openAt(t, path, Options{Existing: true})
	mustTx(t, raw, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx(t), "DELETE FROM schema_migrations")
		return err
	})
	_ = raw.Close()
	// meta already exists, so migration 1 cannot be applied again.
	if _, err := Open(ctx(t), path); err == nil {
		t.Fatal("re-applying over an existing table must fail")
	}
	check := openAt(t, path, Options{Existing: true})
	if got := appliedVersions(t, check); len(got) != 0 {
		t.Errorf("a failed migration was recorded: %v", got)
	}
}
