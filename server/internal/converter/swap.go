package converter

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxBackupNames bounds the ".bak", ".bak.1", ... names tried for one file.
const maxBackupNames = 100

var errBackupExists = errors.New("backup name taken")

func backupName(src string, n int) string {
	if n == 0 {
		return src + bakSuffix
	}
	return src + bakSuffix + "." + strconv.Itoa(n)
}

// isBackupNameOf reports whether b is one of the names backupName gives src.
func isBackupNameOf(src, b string) bool {
	if filepath.Dir(src) != filepath.Dir(b) {
		return false
	}
	rest, ok := strings.CutPrefix(b, src+bakSuffix)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(rest, "."))
	return strings.HasPrefix(rest, ".") && err == nil && n > 0 && n < maxBackupNames && strconv.Itoa(n) == rest[1:]
}

// createBackup keeps the original at backup without ever replacing an existing
// file. It first tries a hard link (atomic, fails if the name exists, and leaves
// src in place so a crash cannot strand it); filesystems without hard links
// (some NFS/SMB mounts) fall back to an Lstat check followed by a rename, which
// has a tiny race window between the check and the rename. linked reports which
// was used.
func createBackup(src, backup string) (linked bool, err error) {
	err = os.Link(src, backup)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrExist) {
		return false, errBackupExists
	}
	if _, lerr := os.Lstat(backup); lerr == nil {
		return false, errBackupExists
	} else if !errors.Is(lerr, fs.ErrNotExist) {
		return false, lerr
	}
	return false, os.Rename(src, backup)
}

// renameNoClobber renames from to a name that must not exist, with the same link
// first, check-then-rename fallback as createBackup.
func renameNoClobber(from, to string) error {
	err := os.Link(from, to)
	if err == nil {
		return os.Remove(from)
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, lerr := os.Lstat(to); lerr == nil {
		return fs.ErrExist
	} else if !errors.Is(lerr, fs.ErrNotExist) {
		return lerr
	}
	return os.Rename(from, to)
}

// recordSwap saves the swap about to start; an error means it must not start.
func (q *JobQueue) recordSwap(j *job, path, backup string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.swapPath, j.swapBackup = path, backup
	if err := q.persist(j); err != nil {
		j.swapPath, j.swapBackup = "", ""
		q.cfg.Logger.Error("could not record a conversion swap", "job", j.ID, "error", err)
		return err
	}
	return nil
}

func (q *JobQueue) clearSwap(j *job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.swapPath, j.swapBackup = "", ""
	q.save(j)
}

// swap replaces src with the converted tempPath, keeping the original at the
// first free backup name (".bak", then ".bak.1", ...). An existing backup is
// never overwritten, so converting a file twice keeps both originals. The
// recorded swap (database) lets a restart restore the original if the process
// dies between the two steps. On error the original is in place and tempPath is
// left for the caller to remove.
func (q *JobQueue) swap(j *job, src, tempPath string) (string, error) {
	var backup string
	var linked bool
	for n := 0; ; n++ {
		if n >= maxBackupNames {
			return "", fail("Too many backups of this file already exist beside it; remove old .bak files and try again")
		}
		backup = backupName(src, n)
		if _, err := os.Lstat(backup); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fail("Could not check for an existing backup")
		}
		if err := q.recordSwap(j, src, backup); err != nil {
			return "", fail("Could not record the swap, so the original was not touched")
		}
		l, err := createBackup(src, backup)
		if errors.Is(err, errBackupExists) {
			continue
		}
		if err != nil {
			q.clearSwap(j)
			return "", fail("Could not move the original to its backup")
		}
		linked = l
		break
	}
	var err error
	if linked {
		err = os.Rename(tempPath, src) // atomic; src is never missing
	} else {
		err = renameNoClobber(tempPath, src)
	}
	if err != nil {
		if linked {
			if a, e1 := os.Stat(src); e1 == nil {
				if b, e2 := os.Stat(backup); e2 == nil && os.SameFile(a, b) {
					_ = os.Remove(backup)
				}
			}
		} else {
			_ = renameNoClobber(backup, src)
		}
		q.clearSwap(j)
		return "", fail("Could not move the converted file into place")
	}
	q.clearSwap(j)
	return backup, nil
}

// pendingSwaps returns the jobs that had a swap recorded when the process
// stopped.
func (q *JobQueue) pendingSwaps() []*job {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []*job
	for _, j := range q.jobs {
		if j.swapPath != "" || j.swapBackup != "" {
			out = append(out, j)
		}
	}
	return out
}

// pathInRoots reports whether p (which need not exist) lies inside a root, by
// resolving its directory.
func pathInRoots(roots []string, p string) bool {
	if p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return false
	}
	full := filepath.Join(dir, filepath.Base(p))
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		if rr, err := filepath.EvalSymlinks(filepath.Clean(root)); err == nil && within(rr, full) {
			return true
		}
	}
	return false
}

// restoreSwap undoes a swap that was recorded but not finished, using only the
// recorded paths, which are validated again against the media roots in force.
// Whatever happens, the record is cleared afterwards.
func (q *JobQueue) restoreSwap(j *job) {
	q.mu.Lock()
	path, backup := j.swapPath, j.swapBackup
	q.mu.Unlock()
	msg := q.restoreSwapFiles(path, backup)
	q.mu.Lock()
	defer q.mu.Unlock()
	j.swapPath, j.swapBackup = "", ""
	if msg != "" {
		j.Log = tail(j.Log+msg+"\n", maxLogBytes)
		q.cfg.Logger.Info("conversion swap recovery", "job", j.ID, "result", msg)
	}
	q.save(j)
}

func (q *JobQueue) restoreSwapFiles(path, backup string) string {
	roots := q.roots()
	if !isBackupNameOf(path, backup) || !pathInRoots(roots, path) || !pathInRoots(roots, backup) {
		return "Ignored a recorded swap whose paths are not valid"
	}
	bi, err := os.Lstat(backup)
	if err != nil || !bi.Mode().IsRegular() {
		return ""
	}
	si, err := os.Lstat(path)
	switch {
	case err == nil && os.SameFile(si, bi):
		// The backup is a hard link of the untouched original: the swap never
		// happened.
		_ = os.Remove(backup)
		return "Removed the unused backup link of an interrupted swap; the original is in place"
	case err == nil:
		return "The swap had completed before the interruption; the original is kept at " + backup
	case !errors.Is(err, fs.ErrNotExist):
		return ""
	}
	if err := renameNoClobber(backup, path); err != nil {
		return "Could not restore the original from " + backup
	}
	return "Restored the original from " + backup + " after an interrupted swap"
}
