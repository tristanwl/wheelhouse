// Package store is Wheelhouse's SQLite database: opening it safely, forward-only
// migrations, online backups and the encryption glue for secret columns.
//
// The driver is modernc.org/sqlite (pure Go, no CGO). A DB holds two connection
// pools over one file in WAL mode: a writer limited to one connection (SQLite
// allows one writer at a time anyway, and a single connection means writers
// queue in Go instead of failing with SQLITE_BUSY) and a pool of read-only
// connections, which never block the writer in WAL mode. Every connection has
// foreign_keys on, a busy timeout and secure_delete on, so deleted secrets are
// overwritten, not left in free pages.
//
// Nothing in the server opens the database yet; the commands in cmd/wheelhouse
// and the tests are its only users until the accounts and stored-configuration
// work lands (docs/wheelhouse-phase2-plan.md).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"modernc.org/sqlite"
)

const (
	// busyTimeout is how long a connection waits for a lock another process
	// (the CLI while the server runs) or connection holds.
	busyTimeout = 5 * time.Second
	// readers is the size of the read connection pool.
	readers = 4
)

func init() {
	// Our DSNs are built here from constants and an escaped path, but refuse a
	// _pragma that smuggles in a second statement all the same.
	sqlite.StrictPragmas(true)
}

// Options changes how Open behaves. The zero value creates the database if
// needed and migrates it to the schema this binary knows.
type Options struct {
	// Existing opens a database that must already exist and does not create,
	// migrate or refuse it for being older or newer than this binary. It is for
	// backing up whatever is there (a backup before an upgrade is the common
	// case). Stored data can only be used through a DB opened without it.
	Existing bool
}

// DB is an open database. It is safe for concurrent use.
type DB struct {
	path string
	w    *sql.DB // one connection: every write
	r    *sql.DB // read-only connections

	closeOnce sync.Once
	closeErr  error
}

// Open opens (creating it with mode 0600 if absent) the database at path and
// applies pending migrations in one transaction.
//
// It refuses a path whose directory is missing, is not a directory or is
// writable by everyone, a path that is not a regular file, and a file that
// group or others can access (so a database copied around with the wrong mode
// is noticed, not silently used). It refuses a database whose schema is newer
// than this binary with ErrSchemaTooNew.
func Open(ctx context.Context, path string, opts ...Options) (*DB, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := checkLocation(path, !o.Existing); err != nil {
		return nil, err
	}

	w, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	r, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(readers)
	r.SetMaxIdleConns(readers)
	r.SetConnMaxLifetime(0)

	db := &DB{path: path, w: w, r: r}
	fail := func(err error) (*DB, error) {
		_ = db.Close()
		return nil, err
	}
	// Opening the writer applies the pragmas and creates the file and its WAL.
	if err := pingRetrying(ctx, w); err != nil {
		return fail(fmt.Errorf("opening %s: %w", path, err))
	}
	if err := checkFile(path); err != nil { // also covers the file SQLite just made
		return fail(err)
	}
	var mode string
	if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fail(err)
	}
	if mode != "wal" {
		return fail(fmt.Errorf("%s: the database is not in WAL mode (journal_mode=%s); is the filesystem a network share?", path, mode))
	}
	if !o.Existing {
		if err := db.migrate(ctx); err != nil {
			return fail(err)
		}
	}
	return db, nil
}

// pingRetrying opens the first connection. Switching a new file to WAL needs an
// exclusive lock, and SQLite does not always honour the busy timeout for that
// one step (two processes starting on an empty database at once), so a busy
// answer is retried for a few seconds.
func pingRetrying(ctx context.Context, w *sql.DB) error {
	deadline := time.Now().Add(busyTimeout)
	for {
		err := w.PingContext(ctx)
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func isBusy(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
			return true
		}
	}
	return false
}

// dsn builds the connection string. The path is URL-escaped, so it can neither
// add a query parameter nor end the path early.
func dsn(path string, writer bool) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	q := url.Values{}
	pragmas := []string{
		fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()),
		"foreign_keys(1)",
		"secure_delete(1)",
		"trusted_schema(0)",
		"cell_size_check(1)",
	}
	if writer {
		pragmas = append(pragmas, "journal_mode(WAL)", "synchronous(FULL)")
		// BEGIN IMMEDIATE: take the write lock up front, so a transaction that
		// reads then writes can never fail half way with SQLITE_BUSY_SNAPSHOT.
		q.Set("_txlock", "immediate")
	} else {
		pragmas = append(pragmas, "query_only(1)")
	}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	q.Set("_defensive", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

// Path is the absolute path of the database file.
func (db *DB) Path() string { return db.path }

// Close closes both pools. Closing the last connection of a WAL database
// checkpoints and removes the -wal file. It is safe to call more than once.
func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		db.closeErr = errors.Join(db.r.Close(), db.w.Close())
	})
	return db.closeErr
}

// Exec runs a statement that changes data, on the writer.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.w.ExecContext(ctx, query, args...)
}

// Query runs a read on the read-only pool. The caller closes the rows. A
// statement that would write fails (query_only).
func (db *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.r.QueryContext(ctx, query, args...)
}

// QueryRow runs a single-row read on the read-only pool.
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return db.r.QueryRowContext(ctx, query, args...)
}

// Tx runs fn in a write transaction (BEGIN IMMEDIATE) on the writer and commits
// when it returns nil. An error or a panic rolls back. fn must use tx only: the
// writer is a single connection, so calling db.Exec or db.Tx from inside fn
// waits for itself and deadlocks until ctx ends.
func (db *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			err = errors.Join(err, rerr)
		}
		return err
	}
	return tx.Commit()
}

// ReadTx runs fn in one read transaction on the read-only pool: every read in
// fn sees the same snapshot, whatever the writer commits meanwhile.
func (db *DB) ReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a read transaction has nothing to commit
	return fn(tx)
}

// checkLocation checks the directory (and the file, if it exists) before
// SQLite touches them. With create, a missing file is created empty with mode
// 0600 so SQLite, which copies the main file's mode to the -wal and -shm files,
// never makes a world-readable one.
func checkLocation(path string, create bool) error {
	dir := filepath.Dir(path)
	st, err := os.Stat(dir) //nolint:gosec // the operator-configured database directory
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the directory %s does not exist (is the config volume mounted?)", dir)
	case err != nil:
		return err
	case !st.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o002 != 0 && st.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("the directory %s is writable by everyone (mode %04o); anyone could replace the database; run: chmod o-w %s", dir, st.Mode().Perm(), dir)
	}

	err = checkFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return fmt.Errorf("no database at %s", path)
		}
		f, cerr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the operator-configured database path
		if cerr == nil {
			return f.Close() // closed before SQLite opens it: closing a descriptor drops POSIX locks
		}
		if !errors.Is(cerr, fs.ErrExist) {
			return cerr
		}
		return checkFile(path) // lost a race with another process; check what it made
	}
	return err
}

// checkFile requires path to be a regular file that only its owner can access.
func checkFile(path string) error {
	st, err := os.Stat(path) //nolint:gosec // the operator-configured database path; follows a symlink to the real file
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the database %s has mode %04o; it holds credentials, so only the owner may have access; run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	return nil
}
