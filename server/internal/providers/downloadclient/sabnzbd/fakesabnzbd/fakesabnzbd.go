// Package fakesabnzbd is a stand-in for SABnzbd's /api endpoint, replaying
// sanitized real responses (testdata/ holds byte-for-byte copies of
// acceptance/fakes/sabnzbd, captured from SABnzbd
// 4.5). It behaves like SABnzbd 4.5: a wrong key is a 403 with a plain-text
// body. For tests only; nothing in the server binary imports it.
package fakesabnzbd

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

// GoodKey is the API key the fake accepts.
const GoodKey = "test-key-0123456789"

// Scenario picks the queue the fake serves.
type Scenario string

// The scenarios.
const (
	Idle   Scenario = "idle"
	Active Scenario = "active"
	Paused Scenario = "paused"
)

// Request is one call the fake received.
type Request struct {
	Path  string
	Query url.Values
}

// Server is a running fake.
type Server struct {
	// URL is the base URL to configure.
	URL string

	ts       *httptest.Server
	scenario Scenario
	mu       sync.Mutex
	requests []Request
}

// Fixture returns one of the embedded fixture files.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic("fakesabnzbd: " + err.Error())
	}
	return b
}

// Start runs the fake until the test ends.
func Start(t testing.TB, scenario Scenario) *Server {
	t.Helper()
	s := &Server{scenario: scenario}
	s.ts = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Close stops the server early (to test an unreachable service).
func (s *Server) Close() { s.ts.Close() }

// Requests returns what the fake received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	s.requests = append(s.requests, Request{Path: r.URL.Path, Query: q})
	s.mu.Unlock()

	if r.URL.Path != "/api" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if q.Get("apikey") != GoodKey {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("API Key Incorrect"))
		return
	}
	var name string
	switch q.Get("mode") {
	case "queue":
		name = "queue-" + string(s.scenario) + ".json"
	case "history":
		name = "history.json"
		if s.scenario == Idle {
			name = "history-idle.json"
		}
	case "get_config":
		name = "servers.json"
	case "warnings":
		name = "warnings.json"
	case "version":
		name = "version.json"
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":false,"error":"not implemented: ` + url.QueryEscape(q.Get("mode")) + `"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(Fixture(name))
}
