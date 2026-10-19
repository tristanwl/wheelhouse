//go:build unix

package runlock

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes flock(2) LOCK_EX|LOCK_NB. A reason is returned, with no error,
// when this filesystem cannot lock at all.
func lockFile(f *os.File) (reason string, err error) {
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // a file descriptor fits an int
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return "", ErrHeld
	case errors.Is(err, syscall.ENOLCK), errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP),
		errors.Is(err, syscall.ENOSYS), errors.Is(err, syscall.EINVAL):
		return "the filesystem of the configuration directory does not support file locks (" + err.Error() + ")", nil
	}
	return "", err
}
