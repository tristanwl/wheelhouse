package radarr_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/radarr"
)

func serve(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestEmptyObjectFloodIsRefusedBeforeItIsDecoded is the reviewer's finding: a
// body of about 8 MB of "{}," elements is cheap on the wire and made the old
// decoder allocate gigabytes. It must now be refused by the element limit, with
// a bounded memory cost.
func TestEmptyObjectFloodIsRefusedBeforeItIsDecoded(t *testing.T) {
	body := append(append([]byte("["), bytes.Repeat([]byte("{},"), 8<<20/3)...), []byte("{}]")...)
	c := radarr.New(radarr.Config{URL: serve(t, body), APIKey: "example-key"}, "radarr", mm.ArrOptions{})

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := c.Movies(ctx)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "too large or too deeply nested") {
		t.Fatalf("error = %v", err)
	}
	// TotalAlloc counts everything allocated in the call: the body itself and
	// its copies are a few times 8 MB; the old decode was about 250 times that.
	got := after.TotalAlloc - before.TotalAlloc
	t.Logf("allocated %d MiB for the refused 8 MB body", got>>20)
	if got > 100<<20 {
		t.Errorf("the refused body still cost %d MiB of allocations", got>>20)
	}
}

func TestDeeplyNestedBodyIsRefused(t *testing.T) {
	body := append(bytes.Repeat([]byte("["), 5000), bytes.Repeat([]byte("]"), 5000)...)
	c := radarr.New(radarr.Config{URL: serve(t, body), APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	if _, err := c.Movies(ctx); err == nil || !strings.Contains(err.Error(), "too large or too deeply nested") {
		t.Errorf("error = %v", err)
	}
}

// movieJSON is a realistic Radarr movie: the fields the app reads plus the
// artwork, ratings and file details a real library carries.
func movieJSON(i int) string {
	return fmt.Sprintf(`{"id":%[1]d,"title":"Example Movie %[1]d","originalTitle":"Example Movie %[1]d","sortTitle":"example movie %[1]d","year":2001,"monitored":true,`+
		`"path":"/movies/Example Movie %[1]d (2001)","rootFolderPath":"/movies","added":"2024-01-02T03:04:05Z","sizeOnDisk":4294967296,"hasFile":true,"tmdbId":%[1]d,`+
		`"images":[{"coverType":"poster","url":"/MediaCover/%[1]d/poster.jpg","remoteUrl":"https://image.example.com/t/p/original/%[1]d.jpg"},{"coverType":"fanart","url":"/MediaCover/%[1]d/fanart.jpg","remoteUrl":"https://image.example.com/t/p/original/f%[1]d.jpg"}],`+
		`"ratings":{"imdb":{"votes":1000,"value":7.1,"type":"user"},"tmdb":{"votes":2000,"value":7.4,"type":"user"}},"genres":["Drama","Comedy"],"tags":[],`+
		`"alternateTitles":[{"sourceType":"tmdb","movieMetadataId":%[1]d,"title":"Alt %[1]d"}],"overview":"%[2]s",`+
		`"movieFile":{"id":%[1]d,"movieId":%[1]d,"relativePath":"Example Movie %[1]d (2001).mkv","path":"/movies/Example Movie %[1]d (2001)/Example Movie %[1]d (2001).mkv","size":4294967296,`+
		`"dateAdded":"2024-01-02T03:04:05Z","quality":{"quality":{"id":7,"name":"Bluray-1080p","source":"bluray","resolution":1080},"revision":{"version":1,"real":0,"isRepack":false}},`+
		`"mediaInfo":{"videoCodec":"h264","audioCodec":"AAC","audioChannels":5.1,"resolution":"1920x1080","runTime":"1:45:00"},"languages":[{"id":1,"name":"English"}]}}`,
		i, "Lorem ipsum dolor sit amet.")
}

// TestARealisticLargeLibraryStillDecodes: the limits must not reject a genuine
// library. The size is WHEELHOUSE_BIG_FIXTURE movies (default 5000, so the
// ordinary run stays quick; 50000 was used for the numbers in the PR).
func TestARealisticLargeLibraryStillDecodes(t *testing.T) {
	n := 5000
	if v, err := strconv.Atoi(os.Getenv("WHEELHOUSE_BIG_FIXTURE")); err == nil && v > 0 {
		n = v
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		b.WriteString(movieJSON(i))
	}
	b.WriteByte(']')
	size := b.Len()
	c := radarr.New(radarr.Config{URL: serve(t, []byte(b.String())), APIKey: "example-key"}, "radarr", mm.ArrOptions{})
	b.Reset()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	movies, err := c.Movies(ctx)
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil || len(movies) != n {
		t.Fatalf("%d movies, error %v", len(movies), err)
	}
	t.Logf("%d movies, %d MiB of JSON: decoded in %v, %d MiB allocated in total", n, size>>20, took, (after.TotalAlloc-before.TotalAlloc)>>20)
}
