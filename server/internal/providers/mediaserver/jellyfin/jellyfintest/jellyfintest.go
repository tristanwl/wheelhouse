// Package jellyfintest is a stand-in for Jellyfin that behaves like the real
// one: keys are checked (only in the Authorization header, as in 12.x) and a
// missing or wrong one gets an empty 401. The fixtures in testdata were
// captured from a real Jellyfin 12.2.0 (official Docker image), sanitized, and
// are byte for byte the files in lib/providers/media-server/__fixtures__/jellyfin
// (a test keeps them in sync). info-public-setup.json is /System/Info/Public
// before the setup wizard ran; sessions-idle.json is one connected client that
// is not playing anything; sessions-playing.json is one client playing a film.
// It is the Go port of that directory's server.ts.
package jellyfintest

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
)

//go:embed testdata/*
var fixtures embed.FS

// GoodKey is the one API key the fake accepts. Version is the fixtures' version.
const (
	GoodKey = "jellyfin-key-0123456789"
	Version = "12.2.0"
)

// Fixture returns a file from testdata.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// Sessions says which /Sessions answer the fake gives.
type Sessions string

// The /Sessions scenarios.
const (
	None    Sessions = "none"    // no sessions
	Idle    Sessions = "idle"    // one connected client, not playing
	Playing Sessions = "playing" // one client playing
	Mixed   Sessions = "mixed"   // one of each
)

// Scenario says how the fake behaves.
type Scenario struct {
	// SetupCompleted is whether the first-run wizard has been completed.
	SetupCompleted bool
	Sessions       Sessions
	// AuthedFailure makes the key-protected endpoints fail with this status
	// instead of answering.
	AuthedFailure int
}

// Request is what the fake saw.
type Request struct {
	Path string
	// Key is the Token from `Authorization: MediaBrowser ... Token="..."`, ""
	// when absent.
	Key string
	// EmbyToken is the legacy X-Emby-Token header; Jellyfin 12 ignores it.
	EmbyToken string
}

// Server is a running fake.
type Server struct {
	URL string

	srv *httptest.Server
	mu  sync.Mutex
	req []Request
}

// Requests returns the requests seen so far, in arrival order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.req...)
}

// Paths returns just the paths of Requests.
func (s *Server) Paths() []string {
	var out []string
	for _, r := range s.Requests() {
		out = append(out, r.Path)
	}
	return out
}

// Close stops the server (idempotent); its URL then refuses connections.
func (s *Server) Close() { s.srv.Close() }

var (
	mediaBrowser = regexp.MustCompile(`^MediaBrowser\s`)
	tokenPart    = regexp.MustCompile(`Token="([^"]*)"`)
)

// keyFrom pulls the key out of `Authorization: MediaBrowser Client="...", Token="..."`.
func keyFrom(header string) string {
	if !mediaBrowser.MatchString(header) {
		return ""
	}
	if m := tokenPart.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return ""
}

func sessionList(kind Sessions) []byte {
	var idle, playing []json.RawMessage
	_ = json.Unmarshal(Fixture("sessions-idle.json"), &idle)
	_ = json.Unmarshal(Fixture("sessions-playing.json"), &playing)
	list := []json.RawMessage{}
	switch kind {
	case Idle:
		list = idle
	case Playing:
		list = playing
	case Mixed:
		list = append(append(list, idle...), playing...)
	}
	out, _ := json.Marshal(list)
	return out
}

// Start runs a fake Jellyfin until the test ends or Close is called.
func Start(t testing.TB, sc Scenario) *Server {
	t.Helper()
	s := &Server{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := keyFrom(r.Header.Get("Authorization"))
		s.mu.Lock()
		s.req = append(s.req, Request{Path: r.URL.Path, Key: key, EmbyToken: r.Header.Get("X-Emby-Token")})
		s.mu.Unlock()

		send := func(body []byte) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(body)
		}
		if r.URL.Path == "/System/Info/Public" {
			if sc.SetupCompleted {
				send(Fixture("info-public.json"))
			} else {
				send(Fixture("info-public-setup.json"))
			}
			return
		}
		if sc.AuthedFailure != 0 {
			w.WriteHeader(sc.AuthedFailure)
			return
		}
		if key != GoodKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			send(Fixture("info.json"))
		case "/Sessions":
			send(sessionList(sc.Sessions))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}
