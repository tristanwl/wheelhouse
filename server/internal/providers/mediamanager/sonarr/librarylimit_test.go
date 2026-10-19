package sonarr_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/sonarr"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Issue #88 for Sonarr: the same configurable cap and the same notice.
func TestAnOversizedSeriesListIsRefusedAndTheCapCanBeRaised(t *testing.T) {
	var body bytes.Buffer
	body.WriteByte('[')
	for i := 1; i <= 3000; i++ {
		if i > 1 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":%d,"title":"Example Show %d","overview":"%s"}`, i, i, strings.Repeat("lorem ", 100))
	}
	body.WriteByte(']')
	if body.Len() < 1<<20 {
		t.Fatalf("fixture is only %d bytes", body.Len())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body.Bytes())
	}))
	t.Cleanup(srv.Close)

	was := core.LibraryListBytes()
	logs := &lockedBuf{}
	core.SetDiagnostics(slog.New(slog.NewTextHandler(logs, nil)))
	core.SetLibraryListBytes(1 << 20)
	t.Cleanup(func() { core.SetLibraryListBytes(was); core.SetDiagnostics(nil) })

	c := sonarr.New(sonarr.Config{URL: srv.URL, APIKey: "example-key"}, "sonarr", mm.ArrOptions{})
	_, err := c.Series(t.Context())
	const want = "sonarr: the library is larger than Wheelhouse is set up to read; an administrator can raise the limit (see the documentation)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(logs.String(), "WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB") {
		t.Errorf("log = %s", logs.String())
	}
	core.SetLibraryListBytes(16 << 20)
	if got, err := c.Series(t.Context()); err != nil || len(got) != 3000 {
		t.Fatalf("with the cap raised: %d, %v", len(got), err)
	}
}
