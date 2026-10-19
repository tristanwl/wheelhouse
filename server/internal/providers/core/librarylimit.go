package core

import (
	"log/slog"
	"sync/atomic"
)

// The cap on what a Radarr or Sonarr list may weigh is an operator setting
// (WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB, default 96, 16 to 512; issue #88). It is
// read once at start and applies to every client. Only the byte cap moves: the
// element, value, depth and string limits and the decode gate of DecodeJSON stay
// as they are, so a raised cap never lets a hostile body turn into more memory
// than the shape limits allow.

// LibrarySizeSetting is the variable the log line names so an administrator
// knows what to change. It is never put in text a user reads.
const LibrarySizeSetting = "WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB"

var (
	libraryListBytes atomic.Int64
	diagnostics      atomic.Pointer[slog.Logger]
)

func init() { libraryListBytes.Store(LargeListBytes) }

// SetLibraryListBytes sets the response cap of the Radarr and Sonarr list
// endpoints. n must be positive; the server validates the operator's value
// (config.MaxLibraryResponseMiB) before calling it.
func SetLibraryListBytes(n int64) {
	if n > 0 {
		libraryListBytes.Store(n)
	}
}

// LibraryListBytes is the response cap of the Radarr and Sonarr list endpoints.
func LibraryListBytes() int64 { return libraryListBytes.Load() }

// SetDiagnostics sets where the provider layer writes operator-only lines (the
// ones that name settings and sizes, which notices for users never do). Without
// one they are dropped.
func SetDiagnostics(l *slog.Logger) { diagnostics.Store(l) }

func diag() *slog.Logger {
	if l := diagnostics.Load(); l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}
