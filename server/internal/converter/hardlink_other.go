//go:build !unix

package converter

// LinkCount is not available off Unix; the check is then skipped.
func LinkCount(string) (uint64, bool) { return 0, false }
