package radarr_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/radarr"
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

// withLibraryCap sets the Radarr and Sonarr list cap for one test, with the
// operator's log going to the returned buffer.
func withLibraryCap(t *testing.T, bytesCap int64) *lockedBuf {
	t.Helper()
	was := core.LibraryListBytes()
	core.SetLibraryListBytes(bytesCap)
	logs := &lockedBuf{}
	core.SetDiagnostics(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() {
		core.SetLibraryListBytes(was)
		core.SetDiagnostics(nil)
	})
	return logs
}

func library(n int) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		b.WriteString(movieJSON(i))
	}
	b.WriteByte(']')
	return b.Bytes()
}

// Issue #88: the cap is configurable. A library over it is refused with a
// sentence a user can act on (no setting, no variable), and the administrator's
// numbers and the setting to change are in the server log.
func TestAnOversizedLibraryIsRefusedWithAnActionableNoticeAndALogLine(t *testing.T) {
	body := library(2000) // about 2 MiB
	if len(body) < 1<<20 {
		t.Fatalf("fixture is only %d bytes", len(body))
	}
	logs := withLibraryCap(t, 1<<20)
	c := radarr.New(radarr.Config{URL: serve(t, body), APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	_, err := c.Movies(ctx)
	if err == nil {
		t.Fatal("a library over the cap was accepted")
	}
	const want = "radarr: the library is larger than Wheelhouse is set up to read; an administrator can raise the limit (see the documentation)"
	if err.Error() != want {
		t.Errorf("notice = %q", err)
	}
	for _, leak := range []string{"WHEELHOUSE", "MIB", "MiB", "environment", "bytes"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the notice names %q", leak)
		}
	}
	log := logs.String()
	for _, need := range []string{"WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB", "limitMiB=1", "responseMiB=", "percentOfLimit=", "service=radarr", "Large libraries"} {
		if !strings.Contains(log, need) {
			t.Errorf("the log line lacks %q:\n%s", need, log)
		}
	}
	if strings.Contains(log, "example-key") {
		t.Error("the log line holds the key")
	}

	// Raising the cap past the response makes the same library load.
	core.SetLibraryListBytes(8 << 20)
	got, err := c.Movies(ctx)
	if err != nil || len(got) != 2000 {
		t.Fatalf("with the cap raised: %d movies, %v", len(got), err)
	}
}

// A server that does not announce its length (chunked) is cut at the cap and
// reported the same way.
func TestAnOversizedChunkedLibraryIsRefusedToo(t *testing.T) {
	body := library(2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for len(body) > 0 { // flushed in pieces: no Content-Length
			n := min(len(body), 64<<10)
			_, _ = w.Write(body[:n])
			w.(http.Flusher).Flush()
			body = body[n:]
		}
	}))
	t.Cleanup(srv.Close)
	logs := withLibraryCap(t, 1<<20)
	c := radarr.New(radarr.Config{URL: srv.URL, APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	_, err := c.Movies(ctx)
	if err == nil || !strings.Contains(err.Error(), "the library is larger than Wheelhouse is set up to read") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(logs.String(), "percentOfLimit=") {
		t.Errorf("log = %s", logs.String())
	}
}

// Raising the byte cap changes nothing else: the element, value, depth and
// string limits still refuse a flood, however large the cap.
func TestARaisedCapKeepsEveryShapeLimit(t *testing.T) {
	withLibraryCap(t, 512<<20)
	flood := append(append([]byte("["), bytes.Repeat([]byte("{},"), 300_000)...), []byte("{}]")...)
	c := radarr.New(radarr.Config{URL: serve(t, flood), APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	if _, err := c.Movies(ctx); err == nil || !strings.Contains(err.Error(), "too large or too deeply nested") {
		t.Errorf("the element limit no longer applies with a raised cap: %v", err)
	}
	deep := append(bytes.Repeat([]byte("["), 5000), bytes.Repeat([]byte("]"), 5000)...)
	c = radarr.New(radarr.Config{URL: serve(t, deep), APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	if _, err := c.Movies(ctx); err == nil || !strings.Contains(err.Error(), "too large or too deeply nested") {
		t.Errorf("the depth limit no longer applies with a raised cap: %v", err)
	}
	long := []byte(`[{"title":"` + strings.Repeat("x", 2<<20) + `"}]`)
	c = radarr.New(radarr.Config{URL: serve(t, long), APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	if _, err := c.Movies(ctx); err == nil || !strings.Contains(err.Error(), "too large or too deeply nested") {
		t.Errorf("the string limit no longer applies with a raised cap: %v", err)
	}
}

func TestTheDefaultCapIs96MiB(t *testing.T) {
	if got := core.LibraryListBytes(); got != 96<<20 {
		t.Errorf("default cap = %d", got)
	}
	var _ io.Writer = &lockedBuf{}
}
