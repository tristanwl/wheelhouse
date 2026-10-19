//go:build !unix

package runlock

import "os"

// lockFile has no flock here: the lock is not held and Acquire reports it.
func lockFile(*os.File) (string, error) {
	return "this platform has no advisory file locks", nil
}
