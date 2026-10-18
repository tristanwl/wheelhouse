//go:build unix

package converter

import (
	"os"
	"syscall"
)

// LinkCount is how many directory entries point at the file's data. A library
// file with more than one is often also the copy a torrent client is seeding.
// ok is false when the file cannot be read (the conversion itself reports
// that).
func LinkCount(path string) (n uint64, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false
	}
	return uint64(st.Nlink), true //nolint:unconvert // Nlink is uint32 or uint64 by platform
}
