package app

import (
	"net/http/httptest"
	"testing"
)

// TestHTTPFuzzFixtureIsWired keeps the fuzz target from being vacuous: with the
// fixture's credentials the real routes answer, so the invariants are checked
// against live handlers, not against a wall of 401s.
func TestHTTPFuzzFixtureIsWired(t *testing.T) {
	up := startUpstreams(t)
	fx, err := newHTTPFixture(up)
	if err != nil {
		t.Fatal(err)
	}
	defer fx.close()
	for _, c := range []struct {
		method, target string
		who            who
		want           int
	}{
		{"GET", "/api/status", adminOK, 200},
		{"GET", "/api/status", userOK, 200},
		{"GET", "/api/status", nobody, 401},
		{"GET", "/api/library", adminOK, 200},
		{"GET", "/api/downloads", adminOK, 200},
		{"GET", "/api/explore?category=popular", adminOK, 200},
		{"GET", "/api/codec-analysis?kind=movies", adminOK, 200},
		{"GET", "/api/admin/instances", adminOK, 200},
		{"GET", "/api/admin/instances", userOK, 403},
		{"GET", "/api/admin/network", adminOK, 200},
		{"GET", "/api/admin/converter", adminOK, 200},
		{"GET", "/api/users", adminOK, 200},
		{"GET", "/api/audit", adminOK, 200},
		{"POST", "/api/users", adminNoCSRF, 403},
		{"GET", "/healthz", nobody, 200},
		{"GET", "/", nobody, 200},
	} {
		req, ok := fx.buildRequest(c.method, c.target, "", nil, c.who, 0)
		if !ok {
			t.Fatalf("%s %s: request refused", c.method, c.target)
		}
		rec := httptest.NewRecorder()
		fx.h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s as %s: %d, want %d: %.200s", c.method, c.target, c.who, rec.Code, c.want, rec.Body.String())
		}
	}
}
