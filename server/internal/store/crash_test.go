package store

import (
	"bufio"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMain doubles as the crash helper: the tests below re-run this test binary
// as a child that writes to the database and then is killed with SIGKILL, the
// way a power cut or an OOM kill ends a process, with no chance to clean up.
func TestMain(m *testing.M) {
	if path := os.Getenv("WHEELHOUSE_STORE_CRASH_HELPER"); path != "" {
		os.Exit(crashHelper(path, os.Getenv("WHEELHOUSE_STORE_CRASH_MODE")))
	}
	os.Exit(m.Run())
}

func crashHelper(path, mode string) int {
	db, err := Open(bgCtx(), path)
	if err != nil {
		println("helper:", err.Error())
		return 1
	}
	say := func(s string) { println(s) } // stderr is unbuffered
	err = db.Tx(bgCtx(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(bgCtx(), "CREATE TABLE IF NOT EXISTS t (v TEXT)")
		return err
	})
	if err != nil {
		println("helper:", err.Error())
		return 1
	}
	if _, err := db.Exec(bgCtx(), "INSERT INTO t VALUES ('committed')"); err != nil {
		println("helper:", err.Error())
		return 1
	}
	switch mode {
	case "mid-transaction":
		// Insert a lot, flush pages to the WAL with a big write, and never commit.
		tx, err := db.w.BeginTx(bgCtx(), nil)
		if err != nil {
			println("helper:", err.Error())
			return 1
		}
		for range 5000 {
			if _, err := tx.ExecContext(bgCtx(), "INSERT INTO t VALUES (hex(randomblob(512)))"); err != nil {
				println("helper:", err.Error())
				return 1
			}
		}
		say("ready")
		time.Sleep(time.Minute) // killed here, transaction open
	case "after-commit":
		say("ready") // committed, then killed without Close (the WAL is not checkpointed)
		time.Sleep(time.Minute)
	}
	return 0
}

func runCrash(t *testing.T, path, mode string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$") //nolint:gosec // re-running the test binary
	cmd.Env = append(os.Environ(), "WHEELHOUSE_STORE_CRASH_HELPER="+path, "WHEELHOUSE_STORE_CRASH_MODE="+mode)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if line := sc.Text(); line == "ready" || len(line) > 7 && line[:7] == "helper:" {
				ready <- line
				return
			}
		}
		ready <- "eof"
	}()
	select {
	case line := <-ready:
		if line != "ready" {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("helper: %s", line)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("helper never got ready")
	}
	if err := cmd.Process.Kill(); err != nil { // SIGKILL
		t.Fatal(err)
	}
	_ = cmd.Wait()
}

func TestCrashMidTransactionLeavesNoTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	runCrash(t, path, "mid-transaction")

	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Log("no -wal left by the killed process:", err)
	}
	db := openAt(t, path) // recovers the WAL
	if n := count(t, db, "t"); n != 1 {
		t.Errorf("%d rows after the crash, want only the committed one", n)
	}
	var ok string
	if err := db.QueryRow(ctx(t), "PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		t.Errorf("integrity_check = %q, %v", ok, err)
	}
	if got := appliedVersions(t, db); len(got) != SchemaVersion() {
		t.Errorf("applied %v", got)
	}
	// And the database is fully usable again.
	mustExec(t, db, "INSERT INTO t VALUES ('after')")
	if n := count(t, db, "t"); n != 2 {
		t.Errorf("%d rows", n)
	}
}

func TestCommittedDataSurvivesKill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	runCrash(t, path, "after-commit")
	db := openAt(t, path)
	if n := count(t, db, "t"); n != 1 {
		t.Errorf("%d rows, the committed row is missing", n)
	}
	var ok string
	if err := db.QueryRow(ctx(t), "PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		t.Errorf("integrity_check = %q, %v", ok, err)
	}
}

// A rolled-back transaction (the in-process version of the same property).
func TestRolledBackTransactionLeavesNoTrace(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "CREATE TABLE t (v TEXT)")
	tx, err := db.w.BeginTx(ctx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		if _, err := tx.ExecContext(ctx(t), "INSERT INTO t VALUES (hex(randomblob(256)))"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "t"); n != 0 {
		t.Errorf("%d rows after a rollback", n)
	}
}
