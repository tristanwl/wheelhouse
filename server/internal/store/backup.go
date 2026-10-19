package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a consistent copy of the database to dest while the database
// stays in use (SQLite's VACUUM INTO takes a read snapshot; writers carry on).
// The copy is a complete database file: restoring is stopping the server and
// copying it over wheelhouse.db (and removing any -wal and -shm files).
//
// dest must not exist (a backup never overwrites anything), its directory must
// exist, and it is created with mode 0600. It is built under a temporary name
// in the same directory and linked into place only when it is complete and
// flushed, so a crash never leaves a partial file under the final name.
//
// Secrets in the copy are still encrypted. The master key is not part of it:
// keep that separately, a backup stored next to its key is only as safe as the
// place they share.
func (db *DB) Backup(ctx context.Context, dest string) error {
	dest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists; a backup never overwrites a file", dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if dest == db.path || strings.HasPrefix(dest, db.path+"-") {
		return errors.New("the backup cannot be the database itself")
	}
	dir := filepath.Dir(dest)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("the directory %s does not exist", dir)
	}

	// VACUUM INTO accepts an existing target only when it is empty, so create
	// it ourselves with the mode we want.
	tmp, err := os.CreateTemp(dir, ".wheelhouse-backup-*.tmp") // mode 0600
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, "VACUUM INTO ?", tmpName); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	if err := syncFile(tmpName); err != nil {
		return err
	}
	beforePlace()
	// Link, not rename: link fails if dest appeared meanwhile, rename would replace it.
	if err := os.Link(tmpName, dest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists; a backup never overwrites a file", dest)
		}
		// Some filesystems (FUSE, network shares) have no hard links; fall back
		// to a rename after checking again that nothing is there.
		if _, serr := os.Lstat(dest); !errors.Is(serr, fs.ErrNotExist) {
			return fmt.Errorf("%s already exists; a backup never overwrites a file", dest)
		}
		if rerr := os.Rename(tmpName, dest); rerr != nil {
			return fmt.Errorf("could not move the finished backup into place: %w", rerr)
		}
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return err
	}
	syncDirOf(dest)
	return nil
}

// beforePlace is a test hook: it runs after the backup is complete and before
// it is moved to its final name, the window in which another process could
// create that name.
var beforePlace = func() {}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // the backup we just wrote
	if err != nil {
		return err
	}
	serr := f.Sync()
	return errors.Join(serr, f.Close())
}

func syncDirOf(path string) {
	if d, err := os.Open(filepath.Dir(path)); err == nil { //nolint:gosec // the backup directory
		_ = d.Sync()
		_ = d.Close()
	}
}
