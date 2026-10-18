// Package plextest is a stand-in for Plex that behaves like the real one:
// tokens are checked and a missing or wrong one gets a 401 with an HTML page.
// It serves the sanitized real responses in testdata (byte for byte the same
// files as lib/providers/media-server/__fixtures__/plex; a test keeps them in
// sync). It is the Go port of that directory's server.ts.
package plextest

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

//go:embed testdata/*
var fixtures embed.FS

// GoodToken is the one token the fake accepts.
const GoodToken = "plex-token-0123456789"

// Fixture returns a file from testdata.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// Scenario says how the fake behaves.
type Scenario struct {
	Claimed bool
	// Active serves two playing sessions; otherwise none.
	Active bool
	// AuthedFailure makes the token-protected endpoints fail with this status
	// instead of answering.
	AuthedFailure int
}

// Request is what the fake saw.
type Request struct {
	Path string
	// Token is the X-Plex-Token header, "" when absent.
	Token string
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

// Start runs a fake Plex until the test ends or Close is called.
func Start(t testing.TB, sc Scenario) *Server {
	t.Helper()
	s := &Server{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Plex-Token")
		s.mu.Lock()
		s.req = append(s.req, Request{Path: r.URL.Path, Token: token})
		s.mu.Unlock()

		json := func(body []byte) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}
		if r.URL.Path == "/identity" {
			if sc.Claimed {
				json(Fixture("identity-claimed.json"))
			} else {
				json(Fixture("identity-unclaimed.json"))
			}
			return
		}
		if sc.AuthedFailure != 0 {
			w.WriteHeader(sc.AuthedFailure)
			return
		}
		if token != GoodToken {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write(Fixture("unauthorized.html"))
			return
		}
		switch strings.TrimSuffix(r.URL.Path, "/") {
		case "":
			json(Fixture("root.json"))
		case "/status/sessions":
			if sc.Active {
				json(Fixture("sessions-active.json"))
			} else {
				json(Fixture("sessions-idle.json"))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}
