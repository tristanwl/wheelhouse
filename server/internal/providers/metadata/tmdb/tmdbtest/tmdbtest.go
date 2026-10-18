// Package tmdbtest is a stand-in for TMDB served over real HTTP, for tests and
// for black-box runs (point the app at it with TMDB_BASE_URL = URL + "/3"). A
// missing or wrong key is a 401 with a JSON body, as at TMDB. It serves the same
// data as lib/providers/metadata/__fixtures__/fake-tmdb.ts and tmdb-server.ts.
package tmdbtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// GoodKey is the one API key the fake accepts.
const GoodKey = "tmdb-key-0123456789abcdef"

// Options say how the fake behaves.
type Options struct {
	// MovieIDs are returned by every list endpoint; defaults to 101, 102, 103.
	MovieIDs []int
	// Delay holds every answer back this long (a hanging upstream when large).
	Delay time.Duration
}

// Movie is the list entry for an id.
func Movie(id int) map[string]any {
	return map[string]any{
		"id":           id,
		"title":        fmt.Sprintf("Example Movie %d", id),
		"overview":     "A made-up plot.",
		"release_date": "2024-01-01",
		"poster_path":  fmt.Sprintf("/poster%d.jpg", id),
		"vote_average": 7.5,
	}
}

// MovieDetail is the /movie/{id} answer for an id.
func MovieDetail(id int) map[string]any {
	d := Movie(id)
	d["genres"] = []map[string]any{{"id": 1, "name": "Drama"}}
	d["runtime"] = 100
	d["backdrop_path"] = "/backdrop.jpg"
	d["videos"] = map[string]any{"results": []any{}}
	return d
}

// Server is a running fake.
type Server struct {
	// URL is the host; the API root is URL + "/3".
	URL string

	srv *httptest.Server
	mu  sync.Mutex
	req []*url.URL
}

// APIRoot is the value for the TMDB base URL setting.
func (s *Server) APIRoot() string { return s.URL + "/3" }

// Requests returns the request URLs seen so far, in arrival order.
func (s *Server) Requests() []*url.URL {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*url.URL(nil), s.req...)
}

// Paths returns just the paths of Requests.
func (s *Server) Paths() []string {
	var out []string
	for _, u := range s.Requests() {
		out = append(out, u.Path)
	}
	return out
}

// Close stops the server (idempotent); its URL then refuses connections.
func (s *Server) Close() { s.srv.Close() }

var detailPath = regexp.MustCompile(`^/movie/(\d+)$`)

// Start runs a fake TMDB until the test ends or Close is called.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()
	ids := opts.MovieIDs
	if ids == nil {
		ids = []int{101, 102, 103}
	}
	s := &Server{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := *r.URL
		s.mu.Lock()
		s.req = append(s.req, &u)
		s.mu.Unlock()
		if opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-r.Context().Done():
				return
			}
		}
		send := func(status int, body any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		}
		if r.URL.Query().Get("api_key") != GoodKey {
			send(http.StatusUnauthorized, map[string]any{"status_code": 7, "status_message": "Invalid API key."})
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/3")
		if path == "/configuration" {
			send(http.StatusOK, map[string]any{"images": map[string]any{"base_url": "http://image.example.com"}})
			return
		}
		if m := detailPath.FindStringSubmatch(path); m != nil {
			var id int
			_, _ = fmt.Sscanf(m[1], "%d", &id)
			send(http.StatusOK, MovieDetail(id))
			return
		}
		// Any list endpoint.
		results := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			results = append(results, Movie(id))
		}
		send(http.StatusOK, map[string]any{"results": results, "total_pages": 1})
	}))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}
