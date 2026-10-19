package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestBackupCopiesEverythingWithMode0600(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	for i := range 200 {
		mustExec(t, db, "INSERT INTO t (v) VALUES (?)", i)
	}
	mustExec(t, db, "INSERT INTO meta (key, value) VALUES ('k', 'v')")

	bdir := t.TempDir()
	dest := filepath.Join(bdir, "backup.db")
	if err := db.Backup(ctx(t), dest); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dest); st.Mode().Perm() != 0o600 {
			t.Errorf("backup has mode %04o", st.Mode().Perm())
		}
	}
	if names := listDir(t, bdir); len(names) != 1 {
		t.Errorf("leftover files: %v", names)
	}

	// A restore is a file copy: open the backup as a database.
	restored := openAt(t, dest)
	if n := count(t, restored, "t"); n != 200 {
		t.Errorf("restored %d rows", n)
	}
	if v, err := restored.GetMeta(ctx(t), "k"); err != nil || v != "v" {
		t.Errorf("meta: %v, %v", v, err)
	}
	var ok string
	if err := restored.QueryRow(ctx(t), "PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		t.Errorf("integrity_check = %q, %v", ok, err)
	}
	if got := appliedVersions(t, restored); len(got) != SchemaVersion() {
		t.Errorf("applied %v", got)
	}
}

func TestBackupNeverOverwrites(t *testing.T) {
	db := openTemp(t)
	dest := filepath.Join(t.TempDir(), "precious.db")
	if err := os.WriteFile(dest, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := db.Backup(ctx(t), dest)
	mustContain(t, err, "already exists")
	if b, _ := os.ReadFile(dest); string(b) != "do not touch" {
		t.Error("an existing file was overwritten")
	}
	if names := listDir(t, filepath.Dir(dest)); len(names) != 1 {
		t.Errorf("leftover files: %v", names)
	}
	// Twice in a row: the second is refused.
	d2 := filepath.Join(t.TempDir(), "b.db")
	if err := db.Backup(ctx(t), d2); err != nil {
		t.Fatal(err)
	}
	mustContain(t, db.Backup(ctx(t), d2), "already exists")
	// Not even through a symlink.
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(dest, link); err == nil {
		mustContain(t, db.Backup(ctx(t), link), "already exists")
		if b, _ := os.ReadFile(dest); string(b) != "do not touch" {
			t.Error("a symlink target was overwritten")
		}
	}
}

func TestBackupRefusesBadDestinations(t *testing.T) {
	db := openTemp(t)
	mustContain(t, db.Backup(ctx(t), filepath.Join(t.TempDir(), "no", "such", "dir", "b.db")), "does not exist")
	mustContain(t, db.Backup(ctx(t), db.Path()), "already exists")
	mustContain(t, db.Backup(ctx(t), db.Path()+"-wal"), "")
}

func TestBackupFailureLeavesNothing(t *testing.T) {
	db := openTemp(t)
	bdir := t.TempDir()
	// A cancelled context aborts the backup; no file, final or temporary, remains.
	cctx, stop := context.WithCancel(context.Background())
	stop()
	if err := db.Backup(cctx, filepath.Join(bdir, "b.db")); err == nil {
		t.Fatal("a cancelled backup succeeded")
	}
	if names := listDir(t, bdir); len(names) != 0 {
		t.Errorf("leftover files: %v", names)
	}
}

// A backup taken while another goroutine keeps committing two-row transactions
// is a consistent snapshot: it never contains half a transaction.
func TestBackupIsConsistentUnderWrites(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE a (n INTEGER)")
	mustExec(t, db, "CREATE TABLE b (n INTEGER)")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = db.Tx(ctx(t), func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx(t), "INSERT INTO a VALUES (?)", i); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx(t), "INSERT INTO b VALUES (?)", i)
				return err
			})
		}
	}()
	bdir := t.TempDir()
	for i := range 5 {
		dest := filepath.Join(bdir, "b"+string(rune('0'+i))+".db")
		if err := db.Backup(ctx(t), dest); err != nil {
			t.Fatal(err)
		}
		bk := openAt(t, dest, Options{Existing: true})
		na, nb := count(t, bk, "a"), count(t, bk, "b")
		if na != nb {
			t.Errorf("backup %d is torn: a=%d b=%d", i, na, nb)
		}
		var ok string
		if err := bk.QueryRow(ctx(t), "PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
			t.Errorf("integrity_check = %q, %v", ok, err)
		}
		_ = bk.Close()
	}
	close(stop)
	wg.Wait()
}

func TestBackupOfAnotherProcessDatabase(t *testing.T) {
	// The CLI opens the running server's database from a second handle.
	path := filepath.Join(t.TempDir(), "w.db")
	server := openAt(t, path)
	mustExec(t, server, "CREATE TABLE t (v TEXT)")
	mustExec(t, server, "INSERT INTO t VALUES ('live')")
	cli := openAt(t, path, Options{Existing: true})
	dest := filepath.Join(t.TempDir(), "b.db")
	if err := cli.Backup(ctx(t), dest); err != nil {
		t.Fatal(err)
	}
	bk := openAt(t, dest, Options{Existing: true})
	if n := count(t, bk, "t"); n != 1 {
		t.Errorf("%d rows in the backup", n)
	}
}

// If something creates the destination while the backup is being made, the
// backup must fail rather than replace it.
func TestBackupNeverReplacesAFileThatAppearsMeanwhile(t *testing.T) {
	db := openTemp(t)
	dest := filepath.Join(t.TempDir(), "raced.db")
	beforePlace = func() { _ = os.WriteFile(dest, []byte("created meanwhile"), 0o600) }
	t.Cleanup(func() { beforePlace = func() {} })
	mustContain(t, db.Backup(ctx(t), dest), "already exists")
	if b, _ := os.ReadFile(dest); string(b) != "created meanwhile" {
		t.Error("the file that appeared was replaced")
	}
	if names := listDir(t, filepath.Dir(dest)); len(names) != 1 {
		t.Errorf("leftover files: %v", names)
	}
}
