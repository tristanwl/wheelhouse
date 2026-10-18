package mmtest

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// GoodKey is the API key the fakes accept by default.
const GoodKey = "test-key-0123456789"

// Request is one request the fake received.
type Request struct {
	Method string
	// Path is below the path prefix, if any: "/api/v3/movie".
	Path  string
	Query url.Values
	// Header is the request's headers (X-Api-Key included, so a test can check it).
	Header http.Header
	// Body is the decoded JSON body, the raw text if it was not JSON, or nil.
	Body any
}

type options struct {
	apiKey       string
	delay        time.Duration
	prefix       string
	health       []Doc
	hasHealth    bool
	movies       []Doc
	hasMovies    bool
	series       []Doc
	hasSeries    bool
	rootFolders  []Doc
	hasRoots     bool
	episodeFiles []Doc
	hasEFiles    bool
	episodes     []Doc
	hasEpisodes  bool
}

// Option configures a fake.
type Option func(*options)

// WithAPIKey sets the key the fake accepts; any other gets HTTP 401, as in the
// real apps.
func WithAPIKey(key string) Option { return func(o *options) { o.apiKey = key } }

// WithDelay delays every response, so concurrent calls overlap.
func WithDelay(d time.Duration) Option { return func(o *options) { o.delay = d } }

// WithPathPrefix serves everything under a prefix ("/radarr"), as when the app
// sits behind a reverse proxy at a sub-path. Requests outside it get 404.
func WithPathPrefix(prefix string) Option {
	return func(o *options) { o.prefix = "/" + strings.Trim(prefix, "/") }
}

// WithHealth replaces the health issues.
func WithHealth(issues []Doc) Option {
	return func(o *options) { o.health, o.hasHealth = issues, true }
}

// WithRootFolders replaces the root folders.
func WithRootFolders(roots []Doc) Option {
	return func(o *options) { o.rootFolders, o.hasRoots = roots, true }
}

// WithMovies replaces the Radarr library (nil means an empty library).
func WithMovies(movies []Doc) Option {
	return func(o *options) { o.movies, o.hasMovies = movies, true }
}

// WithSeries replaces the Sonarr library.
func WithSeries(series []Doc) Option {
	return func(o *options) { o.series, o.hasSeries = series, true }
}

// WithEpisodeFiles replaces Sonarr's episode files.
func WithEpisodeFiles(files []Doc) Option {
	return func(o *options) { o.episodeFiles, o.hasEFiles = files, true }
}

// WithEpisodes replaces Sonarr's episodes.
func WithEpisodes(episodes []Doc) Option {
	return func(o *options) { o.episodes, o.hasEpisodes = episodes, true }
}

type result struct {
	status int
	body   any // nil: empty body
	found  bool
}

func ok(body any) result       { return result{status: 200, body: body, found: true} }
func created(body any) result  { return result{status: 201, body: body, found: true} }
func empty() result            { return result{status: 200, found: true} }
func notFound() result         { return result{status: 404, body: Doc{"message": "NotFound"}, found: true} }
func unhandled() result        { return result{} }
func accepted(body any) result { return result{status: 202, body: body, found: true} }

// Fake is a stand-in for Sonarr's or Radarr's API replaying the sanitized real
// responses. State is mutable: adding or deleting an item changes what the list
// returns.
type Fake struct {
	// URL is the base URL to configure (it includes the path prefix).
	URL string

	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []Request
	route  func(Request) result
	state  state
	nextID int
}

type state struct {
	movies, series, rootFolders, episodeFiles, episodes []Doc
}

// Requests returns a copy of the request log.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// Last returns the most recent request.
func (f *Fake) Last() Request {
	r := f.Requests()
	if len(r) == 0 {
		return Request{}
	}
	return r[len(r)-1]
}

// Find returns the first request with the method and path, and whether there was one.
func (f *Fake) Find(method, path string) (Request, bool) {
	for _, r := range f.Requests() {
		if r.Method == method && r.Path == path {
			return r, true
		}
	}
	return Request{}, false
}

// Count says how many requests of this method and path (no query) arrived.
func (f *Fake) Count(method, path string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// Close stops the server. Calling it twice is fine.
func (f *Fake) Close() { f.srv.Close() }

// Movies returns a copy of Radarr's current library.
func (f *Fake) Movies() []Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Copy(f.state.movies)
}

// SeriesDocs returns a copy of Sonarr's current library.
func (f *Fake) SeriesDocs() []Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Copy(f.state.series)
}

// EpisodeFiles returns a copy of Sonarr's current episode files.
func (f *Fake) EpisodeFiles() []Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Copy(f.state.episodeFiles)
}

func start(t testing.TB, o *options, route func(*Fake, Request) result, st state) *Fake {
	t.Helper()
	f := &Fake{state: st, nextID: 1000}
	f.route = func(r Request) result { return route(f, r) }
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if o.prefix != "" {
			rest, found := strings.CutPrefix(path, o.prefix+"/")
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			path = "/" + rest
		}
		raw, _ := io.ReadAll(r.Body)
		var body any
		if len(raw) > 0 {
			if json.Unmarshal(raw, &body) != nil {
				body = string(raw)
			}
		}
		req := Request{Method: r.Method, Path: path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.mu.Unlock()
		if o.delay > 0 {
			time.Sleep(o.delay)
		}

		if r.Header.Get("X-Api-Key") != o.apiKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
			return
		}
		f.mu.Lock()
		res := f.route(req)
		var encoded []byte
		if res.body != nil {
			encoded, _ = json.Marshal(res.body)
		}
		f.mu.Unlock()
		if !res.found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if encoded != nil {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(res.status)
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(f.srv.Close)
	f.URL = f.srv.URL + o.prefix
	return f
}

func newOptions(opts []Option) *options {
	o := &options{apiKey: GoodKey}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func pick(has bool, override []Doc, fixture string) []Doc {
	if has {
		return Copy(override)
	}
	return FixtureDocs(fixture)
}

func (f *Fake) newID() int {
	f.nextID++
	return f.nextID - 1
}

var (
	movieIDPath  = regexp.MustCompile(`^/api/v3/movie/(\d+)$`)
	seriesIDPath = regexp.MustCompile(`^/api/v3/series/(\d+)$`)
	fileIDPath   = regexp.MustCompile(`^/api/v3/episodefile/(\d+)$`)
	tvdbTerm     = regexp.MustCompile(`^tvdb:(\d+)$`)
)

func idOf(re *regexp.Regexp, path string) (int, bool) {
	m := re.FindStringSubmatch(path)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

func bodyDoc(r Request) Doc {
	d, _ := r.Body.(map[string]any)
	if d == nil {
		return Doc{}
	}
	return maps.Clone(d) // never the logged request itself
}

func indexOfID(docs []Doc, id int) int {
	return slices.IndexFunc(docs, func(d Doc) bool { return Int(d, "id") == id })
}

// StartRadarr starts a stand-in for Radarr's v5 API.
func StartRadarr(t testing.TB, opts ...Option) *Fake {
	t.Helper()
	o := newOptions(opts)
	st := state{
		movies:      pick(o.hasMovies, o.movies, "radarr/movies.json"),
		rootFolders: pick(o.hasRoots, o.rootFolders, "radarr/rootfolder.json"),
	}
	health := pick(o.hasHealth, o.health, "radarr/health.json")
	return start(t, o, func(f *Fake, r Request) result {
		movieID, hasMovieID := idOf(movieIDPath, r.Path)
		switch r.Method {
		case http.MethodGet:
			switch r.Path {
			case "/api/v3/system/status":
				return ok(FixtureDoc("radarr/system-status.json"))
			case "/api/v3/health":
				return ok(health)
			case "/api/v3/queue":
				return ok(FixtureDoc("radarr/queue.json"))
			case "/api/v3/rootfolder":
				return ok(f.state.rootFolders)
			case "/api/v3/qualityprofile":
				return ok(FixtureDocs("radarr/qualityprofile.json"))
			case "/api/v3/indexer":
				return ok(FixtureDocs("radarr/indexer.json"))
			case "/api/v3/tag":
				return ok([]Doc{{"id": 1, "label": "example"}})
			case "/api/v3/movie":
				return ok(f.state.movies)
			case "/api/v3/movie/lookup":
				// A term lookup returns the library's own record (with an id) for
				// movies that are in it. The tmdb-id lookup never does: real Radarr
				// leaves id off.
				inLib := FixtureDoc("radarr/lookup-in-library.json")
				hit := inLib
				for _, m := range f.state.movies {
					if Int(m, "tmdbId") == Int(inLib, "tmdbId") {
						hit = m
					}
				}
				return ok([]Doc{hit, FixtureDoc("radarr/lookup-new.json")})
			case "/api/v3/movie/lookup/tmdb":
				tmdbID, _ := strconv.Atoi(r.Query.Get("tmdbId"))
				for _, m := range f.state.movies {
					if Int(m, "tmdbId") == tmdbID {
						return ok(m)
					}
				}
				fresh := FixtureDoc("radarr/lookup-new.json")
				fresh["tmdbId"] = tmdbID
				return ok(fresh)
			}
			if hasMovieID {
				if i := indexOfID(f.state.movies, movieID); i >= 0 {
					return ok(f.state.movies[i])
				}
				return notFound()
			}
		case http.MethodPost:
			switch r.Path {
			case "/api/v3/movie":
				doc := bodyDoc(r)
				doc["id"] = f.newID()
				f.state.movies = append(f.state.movies, doc)
				return created(doc)
			case "/api/v3/command":
				return created(Doc{"id": 1, "name": bodyDoc(r)["name"], "status": "queued"})
			}
		case http.MethodDelete:
			if hasMovieID {
				f.state.movies = slices.DeleteFunc(f.state.movies, func(d Doc) bool { return Int(d, "id") == movieID })
				// Radarr answers a delete with 200 and an empty body, not 204.
				return empty()
			}
		}
		return unhandled()
	}, st)
}

// StartSonarr starts a stand-in for Sonarr's v4 API.
func StartSonarr(t testing.TB, opts ...Option) *Fake {
	t.Helper()
	o := newOptions(opts)
	st := state{
		series:       pick(o.hasSeries, o.series, "sonarr/series.json"),
		rootFolders:  pick(o.hasRoots, o.rootFolders, "sonarr/rootfolder.json"),
		episodeFiles: pick(o.hasEFiles, o.episodeFiles, "sonarr/episodefile.json"),
		episodes:     pick(o.hasEpisodes, o.episodes, "sonarr/episode.json"),
	}
	health := pick(o.hasHealth, o.health, "sonarr/health.json")
	forSeries := func(docs []Doc, r Request) []Doc {
		id, _ := strconv.Atoi(r.Query.Get("seriesId"))
		out := []Doc{}
		for _, d := range docs {
			if Int(d, "seriesId") == id {
				out = append(out, d)
			}
		}
		return out
	}
	return start(t, o, func(f *Fake, r Request) result {
		seriesID, hasSeriesID := idOf(seriesIDPath, r.Path)
		fileID, hasFileID := idOf(fileIDPath, r.Path)
		switch r.Method {
		case http.MethodGet:
			switch r.Path {
			case "/api/v3/system/status":
				return ok(FixtureDoc("sonarr/system-status.json"))
			case "/api/v3/health":
				return ok(health)
			case "/api/v3/queue":
				return ok(FixtureDoc("sonarr/queue.json"))
			case "/api/v3/rootfolder":
				return ok(f.state.rootFolders)
			case "/api/v3/qualityprofile":
				return ok(FixtureDocs("sonarr/qualityprofile.json"))
			case "/api/v3/indexer":
				return ok(FixtureDocs("sonarr/indexer.json"))
			case "/api/v3/tag":
				return ok([]Doc{{"id": 1, "label": "example"}})
			case "/api/v3/series":
				return ok(f.state.series)
			case "/api/v3/series/lookup":
				term := r.Query.Get("term")
				var known Doc
				var tvdb string
				if m := tvdbTerm.FindStringSubmatch(term); m != nil {
					tvdb = m[1]
					for _, s := range f.state.series {
						if strconv.Itoa(Int(s, "tvdbId")) == tvdb {
							known = s
						}
					}
				}
				if known != nil {
					return ok([]Doc{known})
				}
				fresh := FixtureDoc("sonarr/lookup-new.json")
				if tvdb != "" {
					n, _ := strconv.Atoi(tvdb)
					fresh["tvdbId"] = n
				}
				return ok([]Doc{fresh})
			case "/api/v3/episodefile":
				return ok(forSeries(f.state.episodeFiles, r))
			case "/api/v3/episode":
				return ok(forSeries(f.state.episodes, r))
			}
			if hasSeriesID {
				if i := indexOfID(f.state.series, seriesID); i >= 0 {
					return ok(f.state.series[i])
				}
				return notFound()
			}
		case http.MethodPost:
			switch r.Path {
			case "/api/v3/series":
				doc := bodyDoc(r)
				doc["id"] = f.newID()
				f.state.series = append(f.state.series, doc)
				return created(doc)
			case "/api/v3/command":
				return created(Doc{"id": 1, "name": bodyDoc(r)["name"], "status": "queued"})
			}
		case http.MethodPut:
			if hasSeriesID {
				doc := bodyDoc(r)
				for i, s := range f.state.series {
					if Int(s, "id") == seriesID {
						f.state.series[i] = doc
					}
				}
				return accepted(doc)
			}
		case http.MethodDelete:
			if hasSeriesID {
				f.state.series = slices.DeleteFunc(f.state.series, func(d Doc) bool { return Int(d, "id") == seriesID })
				return empty()
			}
			if hasFileID {
				f.state.episodeFiles = slices.DeleteFunc(f.state.episodeFiles, func(d Doc) bool { return Int(d, "id") == fileID })
				return empty()
			}
		}
		return unhandled()
	}, st)
}
