// Package runlock keeps a running server and the commands that rewrite its
// secrets or its key (rekey, forget-secrets) from running at the same time.
//
// The server holds an exclusive advisory lock on <config dir>/wheelhouse.lock
// for its whole life; those commands take the same lock and refuse when they
// cannot. The lock belongs to the open file, not to the file's existence or
// contents, so the kernel releases it when the process exits for any reason
// (a clean stop, a crash, SIGKILL, the container being removed): a lock file
// left behind is harmless and never needs cleaning up. The PID written into it
// is only a hint for the refusal message and is never trusted.
//
// On Linux and the BSDs the lock is flock(2). Where the filesystem cannot lock
// (some network shares, odd FUSE mounts) or the platform has no flock, Acquire
// does not fail: the Lock reports Degraded, the server logs a warning, and the
// commands print one: the protection is then only the operator's care, as it
// was before the lock existed (docs/operations.md, "Locking").
package runlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileName is the lock file in the config directory.
const FileName = "wheelhouse.lock"

// ErrHeld means another process holds the lock: a Wheelhouse server is running
// on this config directory (or a command that rewrites secrets is).
var ErrHeld = errors.New("another Wheelhouse process is using this configuration directory")

// Lock is a held lock. The zero value and nil are valid and hold nothing.
type Lock struct {
	f        *os.File
	degraded string
}

// lockFunc is lockFile; a variable so a test can play a filesystem without locks.
var lockFunc = lockFile

// Path is where the lock file of configDir is.
func Path(configDir string) string { return filepath.Join(configDir, FileName) }

// Acquire takes the exclusive lock without waiting. It returns an error that
// wraps ErrHeld when someone else has it. When locking is not possible here it
// returns a Lock whose Degraded text says why, and no error.
func Acquire(configDir string) (*Lock, error) {
	path := Path(configDir)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // <config dir>/wheelhouse.lock
	if err != nil {
		// A read-only open is enough for flock, so a command run as another user
		// still sees a server's lock.
		var rerr error
		f, rerr = os.Open(path) //nolint:gosec // same file
		if rerr != nil {
			if _, serr := os.Stat(configDir); errors.Is(serr, os.ErrNotExist) { //nolint:gosec // the operator-configured config directory
				return nil, fmt.Errorf("the directory %s does not exist (is the config volume mounted?)", configDir)
			}
			return nil, fmt.Errorf("cannot open the lock file %s: %w", path, err)
		}
	}
	reason, err := lockFunc(f)
	switch {
	case err == nil && reason == "":
		stamp(f)
		return &Lock{f: f}, nil
	case err == nil:
		_ = f.Close()
		return &Lock{degraded: reason}, nil
	case errors.Is(err, ErrHeld):
		hint := holder(f)
		_ = f.Close()
		return nil, fmt.Errorf("%w%s", ErrHeld, hint)
	default:
		_ = f.Close()
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
}

// Unlocked is a Lock that holds nothing and says why: what Acquire returns on a
// filesystem without locks. Tests use it to play that filesystem.
func Unlocked(reason string) *Lock { return &Lock{degraded: reason} }

// Degraded is empty when the lock is really held, and otherwise the reason it
// is not (the filesystem or platform does not support locks).
func (l *Lock) Degraded() string {
	if l == nil {
		return ""
	}
	return l.degraded
}

// Release drops the lock. It is safe to call twice and on nil.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	return f.Close() // closing the descriptor releases the flock
}

// stamp records our PID for the refusal message of whoever fails to lock later.
func stamp(f *os.File) {
	if f.Truncate(0) == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
}

// holder describes the process in the file, if the file says one.
func holder(f *os.File) string {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	if pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n]))); err == nil && pid > 0 {
		return fmt.Sprintf(" (process %d)", pid)
	}
	return ""
}
