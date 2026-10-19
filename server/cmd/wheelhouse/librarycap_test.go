package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/config"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// An invalid cap stops the server at start, naming the variable and the range.
func TestInvalidLibraryResponseCapRefusesToStart(t *testing.T) {
	for _, v := range []string{"15", "513", "0", "-1", "big", "96.5", "96MiB"} {
		t.Setenv("WHEELHOUSE_CONFIG_DIR", t.TempDir())
		t.Setenv("WHEELHOUSE_LISTEN", "127.0.0.1:39518")
		t.Setenv("WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB", v)
		err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err == nil || !strings.Contains(err.Error(), "WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB") || !strings.Contains(err.Error(), "16 to 512") {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// A valid setting reaches the provider layer; the default is 96 MiB.
func TestLibraryResponseCapIsAppliedAtStart(t *testing.T) {
	was := core.LibraryListBytes()
	t.Cleanup(func() { core.SetLibraryListBytes(was) })

	srv := startLiveServer(t, t.TempDir())
	if got := core.LibraryListBytes(); got != 96<<20 {
		t.Errorf("default cap = %d MiB", got>>20)
	}
	if err := srv.stop(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB", "200")
	srv = startLiveServer(t, t.TempDir())
	if got := core.LibraryListBytes(); got != 200<<20 {
		t.Errorf("cap = %d MiB, want 200", got>>20)
	}
	if !strings.Contains(srv.logs.String(), "WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB") {
		t.Errorf("the log does not say the cap was changed: %s", srv.logs.String())
	}
	if err := srv.stop(); err != nil {
		t.Fatal(err)
	}
}

// The provider layer names the variable in its log line; it must be the one the
// configuration reads.
func TestTheLogNamesTheVariableTheServerReads(t *testing.T) {
	if core.LibrarySizeSetting != config.EnvMaxLibraryResponseMiB {
		t.Errorf("%q != %q", core.LibrarySizeSetting, config.EnvMaxLibraryResponseMiB)
	}
}
