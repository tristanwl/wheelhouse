package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every route the app registers has an explicit, valid access level. This is
// the enumerating test the security requirements ask for: a route added without
// a decision cannot be registered (Router.Handle panics), and this test lists
// them all so a review sees each one.
func TestEveryRouteHasAnAccessLevel(t *testing.T) {
	routes := Routes(Options{}).Routes()
	if len(routes) == 0 {
		t.Fatal("no routes registered")
	}
	seen := map[string]Level{}
	for _, r := range routes {
		if !r.Level.Valid() {
			t.Errorf("route %q has no valid access level (%q)", r.Pattern, r.Level)
		}
		seen[r.Pattern] = r.Level
	}
	// Spelled out on purpose: adding or removing a route is a visible change here,
	// and Phase 2 reviews each level. Everything is public until authentication exists.
	want := map[string]Level{
		"GET /healthz":       Public,
		"GET /api/features":  Public,
		"GET /api/status":    Public,
		"GET /api/downloads": Public,
		"/api":               Public,
		"/api/":              Public,
		"/":                  Public,
	}
	for p, l := range want {
		if seen[p] != l {
			t.Errorf("route %q: level %q, want %q", p, seen[p], l)
		}
	}
	for p := range seen {
		if _, ok := want[p]; !ok {
			t.Errorf("route %q is registered but not listed in this test; list it with its level", p)
		}
	}
}

func TestRegisteringWithoutALevelPanics(t *testing.T) {
	for _, l := range []Level{"", "everyone", "ADMIN"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("level %q was accepted", l)
				}
			}()
			NewRouter().HandleFunc("GET /api/x", l, func(http.ResponseWriter, *http.Request) {})
		}()
	}
	r := NewRouter()
	r.HandleFunc("GET /api/x", Public, func(http.ResponseWriter, *http.Request) {})
	defer func() {
		if recover() == nil {
			t.Error("a duplicate pattern was accepted")
		}
	}()
	r.HandleFunc("GET /api/x", Admin, func(http.ResponseWriter, *http.Request) {})
}

// Every request reaches its handler through the authorization hook, which knows
// the route's level.
func TestHandlersSeeTheirLevel(t *testing.T) {
	r := NewRouter()
	var got []Level
	for i, l := range []Level{Public, Authenticated, Admin} {
		path := "GET /api/level" + string(rune('a'+i))
		r.HandleFunc(path, l, func(_ http.ResponseWriter, req *http.Request) {
			got = append(got, LevelOf(req.Context()))
		})
	}
	for _, p := range []string{"/api/levela", "/api/levelb", "/api/levelc"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	if !slices.Equal(got, []Level{Public, Authenticated, Admin}) {
		t.Fatalf("levels seen by handlers: %v", got)
	}
}

// The mux is private to the router: no file of the package may build another
// one, or register a route around the level requirement.
func TestNoRouteBypassesTheRouter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "router.go" {
			continue
		}
		tree, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "NewServeMux" {
				t.Errorf("%s: builds its own ServeMux; register routes with Router.Handle", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

// Everything, the API routes and the 404s too, sits behind the middleware.
func TestAPIRoutesAreBehindTheSecurityMiddleware(t *testing.T) {
	h := Handler(Options{})
	for _, p := range []string{"/healthz", "/api/features", "/api/status", "/api/downloads", "/api/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Request-Id"} {
			if rec.Header().Get(header) == "" {
				t.Errorf("%s: no %s", p, header)
			}
		}
		if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %q %q", p, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
		}
	}
}

func TestIsAPI(t *testing.T) {
	for p, want := range map[string]bool{
		"GET /api/status": true, "/api": true, "/api/": true, "GET /healthz": false, "/": false, "/apix": false,
	} {
		if IsAPI(p) != want {
			t.Errorf("IsAPI(%q) = %v", p, !want)
		}
	}
}
