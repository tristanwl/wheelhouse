// Package fakeqbittorrent is a stand-in for qBittorrent's WebUI API. It behaves
// like the real thing: a login sets a session cookie, anything without it is a
// 403, and repeated bad logins ban the client (403 on login). v4 answers login
// with 200 "Ok."/"Fails."; v5 with 204 and 401.
//
// testdata/ holds byte-for-byte copies of
// acceptance/fakes/qbittorrent: sanitized responses
// captured from real qBittorrent v5.2.4 and v4.6.7 containers, with no network
// and no real content. The v5 torrent list is assembled from the real objects
// seen for each state (identities replaced). No real run produced
// "downloading", "uploading", "forcedDL", "forcedMetaDL", "error" or
// "missingFiles" (they need peers or a failing disk), so tests cover those by
// hand. For tests only; nothing in the server binary imports it.
package fakeqbittorrent

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

// Credentials the fake accepts.
const (
	GoodUser     = "admin"
	GoodPassword = "test-password-0123456789"
)

// maxFailures: qBittorrent bans an IP after this many failed logins.
const maxFailures = 5

// Version is the qBittorrent major version to imitate.
type Version string

// The versions with captured fixtures.
const (
	V4 Version = "v4"
	V5 Version = "v5"
)

// Scenario picks the torrent list.
type Scenario string

// The scenarios.
const (
	Active Scenario = "active"
	Idle   Scenario = "idle"
)

// Obj is a JSON object, as the fixtures decode.
type Obj = map[string]any

// State is the live data the fake answers with. Tests change it through Edit.
type State struct {
	Torrents []Obj
	Transfer Obj
	// RefuseSessions simulates broken session handling: logins succeed but the
	// cookie is never accepted.
	RefuseSessions bool
}

// Request is one call the fake received.
type Request struct {
	Method string
	Path   string
	Cookie string
}

type loginSpec struct {
	Status    int     `json:"status"`
	Body      string  `json:"body"`
	SetCookie *string `json:"setCookie"`
}

type authSpec struct {
	Login struct {
		OK    loginSpec `json:"ok"`
		Wrong loginSpec `json:"wrong"`
	} `json:"login"`
	NoCookie struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	} `json:"noCookie"`
}

// Server is a running fake.
type Server struct {
	// URL is the base URL to configure.
	URL string

	ts         *httptest.Server
	spec       authSpec
	banned     authSpecBan
	cookieName string
	versionTxt string

	mu       sync.Mutex
	sessions map[string]bool
	state    State
	logins   int
	failures int
	requests []Request
}

type authSpecBan struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// Fixture returns one of the embedded fixture files.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic("fakeqbittorrent: " + err.Error())
	}
	return b
}

func decode(name string, out any) {
	if err := json.Unmarshal(Fixture(name), out); err != nil {
		panic("fakeqbittorrent: " + name + ": " + err.Error())
	}
}

// Start runs the fake until the test ends.
func Start(t testing.TB, version Version, scenario Scenario) *Server {
	t.Helper()
	s := &Server{sessions: map[string]bool{}}

	var auth map[string]json.RawMessage
	decode("auth.json", &auth)
	if err := json.Unmarshal(auth[string(version)], &s.spec); err != nil {
		panic("fakeqbittorrent: auth.json: " + err.Error())
	}
	if err := json.Unmarshal(auth["banned"], &s.banned); err != nil {
		panic("fakeqbittorrent: auth.json: " + err.Error())
	}
	s.cookieName = "SID"
	if sc := s.spec.Login.OK.SetCookie; sc != nil {
		s.cookieName, _, _ = strings.Cut(*sc, "=")
	}
	var versions map[Version]struct {
		Version string `json:"version"`
	}
	decode("versions.json", &versions)
	s.versionTxt = versions[version].Version

	decode("transfer-"+string(version)+".json", &s.state.Transfer)
	s.state.Torrents = []Obj{}
	if scenario != Idle {
		decode("torrents-"+string(version)+".json", &s.state.Torrents)
	}

	s.ts = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Close stops the server early (to test an unreachable service).
func (s *Server) Close() { s.ts.CloseClientConnections(); s.ts.Close() }

// Edit changes the live data under the server's lock.
func (s *Server) Edit(f func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.state)
}

// ExpireSessions forgets every session, as a qBittorrent restart (or its
// one-hour timeout) would.
func (s *Server) ExpireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.sessions)
}

// Logins counts successful logins.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// Requests returns what the fake received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func send(w http.ResponseWriter, status int, body, contentType string) {
	if body != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

const plain = "text/plain; charset=UTF-8"

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Cookie: r.Header.Get("Cookie")})

	if r.URL.Path == "/api/v2/auth/login" && r.Method == http.MethodPost {
		_ = r.ParseForm()
		switch {
		case s.failures >= maxFailures:
			send(w, s.banned.Status, s.banned.Body, plain)
		case r.PostForm.Get("username") == GoodUser && r.PostForm.Get("password") == GoodPassword:
			s.logins++
			raw := make([]byte, 16)
			_, _ = rand.Read(raw)
			sid := base64.RawURLEncoding.EncodeToString(raw)
			s.sessions[sid] = true
			w.Header().Set("Set-Cookie", s.cookieName+"="+sid+"; HttpOnly; path=/")
			send(w, s.spec.Login.OK.Status, s.spec.Login.OK.Body, plain)
		default:
			s.failures++
			send(w, s.spec.Login.Wrong.Status, s.spec.Login.Wrong.Body, plain)
		}
		return
	}

	var sid string
	for part := range strings.SplitSeq(r.Header.Get("Cookie"), ";") {
		if name, value, ok := strings.Cut(strings.TrimSpace(part), "="); ok && name == s.cookieName {
			sid = value
		}
	}
	if s.state.RefuseSessions || sid == "" || !s.sessions[sid] {
		send(w, s.spec.NoCookie.Status, s.spec.NoCookie.Body, plain)
		return
	}

	switch r.URL.Path {
	case "/api/v2/app/version":
		send(w, http.StatusOK, s.versionTxt, plain)
	case "/api/v2/transfer/info":
		b, _ := json.Marshal(s.state.Transfer)
		send(w, http.StatusOK, string(b), "application/json")
	case "/api/v2/torrents/info":
		b, _ := json.Marshal(s.state.Torrents)
		send(w, http.StatusOK, string(b), "application/json")
	default:
		send(w, http.StatusNotFound, "Not Found", plain)
	}
}
