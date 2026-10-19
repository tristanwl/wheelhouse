package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// wantLevels is the table of every route and its access level, spelled out on
// purpose: adding, removing or re-levelling a route is a visible change here,
// and a reviewer sees each decision. acceptance/routes.ts holds the same table
// (TestAcceptanceRouteTableMatches keeps the two equal).
var wantLevels = map[string]Level{
	// Public: health, setup, login, the UI and the JSON 404s.
	"GET /healthz":         Public,
	"GET /api/auth/setup":  Public,
	"POST /api/auth/setup": Public,
	"POST /api/auth/login": Public,
	"/api":                 Public,
	"/api/":                Public,
	"/":                    Public,

	// Any signed-in account: what the app shows, library management (users
	// manage media, so deletes and conversions are here), and the own account.
	"GET /api/features":                                      Authenticated,
	"GET /api/status":                                        Authenticated,
	"GET /api/downloads":                                     Authenticated,
	"GET /api/library":                                       Authenticated,
	"GET /api/storage":                                       Authenticated,
	"GET /api/search":                                        Authenticated,
	"GET /api/add-meta":                                      Authenticated,
	"POST /api/add":                                          Authenticated,
	"GET /api/library/{instanceId}/{id}/details":             Authenticated,
	"GET /api/library/{instanceId}/{id}/seasons":             Authenticated,
	"DELETE /api/library/{instanceId}/{id}":                  Authenticated,
	"DELETE /api/library/{instanceId}/{id}/seasons/{season}": Authenticated,
	"GET /api/explore":                                       Authenticated,
	"GET /api/explore/{id}":                                  Authenticated,

	"GET /api/codec-analysis":                         Authenticated,
	"GET /api/codec-analysis/settings":                Authenticated,
	"GET /api/codec-analysis/encoders":                Authenticated,
	"GET /api/codec-analysis/convert":                 Authenticated,
	"POST /api/codec-analysis/convert":                Authenticated,
	"GET /api/codec-analysis/convert/{jobId}":         Authenticated,
	"POST /api/codec-analysis/convert/{jobId}/cancel": Authenticated,

	"POST /api/auth/logout":          Authenticated,
	"GET /api/auth/session":          Authenticated,
	"POST /api/auth/password":        Authenticated,
	"GET /api/auth/sessions":         Authenticated,
	"DELETE /api/auth/sessions/{id}": Authenticated,

	// Administrators: configuration, accounts, other people's sessions, the audit log.
	"PUT /api/codec-analysis/settings":      Admin,
	"GET /api/users":                        Admin,
	"POST /api/users":                       Admin,
	"PATCH /api/users/{id}":                 Admin,
	"POST /api/users/{id}/password":         Admin,
	"DELETE /api/users/{id}":                Admin,
	"GET /api/users/{id}/sessions":          Admin,
	"DELETE /api/users/{id}/sessions":       Admin,
	"GET /api/audit":                        Admin,
	"GET /api/admin/providers":              Admin,
	"GET /api/admin/instances":              Admin,
	"POST /api/admin/instances":             Admin,
	"POST /api/admin/instances/test":        Admin,
	"PUT /api/admin/instances/{id}":         Admin,
	"DELETE /api/admin/instances/{id}":      Admin,
	"POST /api/admin/instances/{id}/import": Admin,
	"GET /api/admin/features":               Admin,
	"PUT /api/admin/features":               Admin,
	"GET /api/admin/network":                Admin,
	"PUT /api/admin/network":                Admin,
	"GET /api/admin/converter":              Admin,
	"PUT /api/admin/converter":              Admin,
}

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
	for p, l := range wantLevels {
		if seen[p] != l {
			t.Errorf("route %q: level %q, want %q", p, seen[p], l)
		}
	}
	for p := range seen {
		if _, ok := wantLevels[p]; !ok {
			t.Errorf("route %q is registered but not listed in this test; list it with its level", p)
		}
	}
}

// acceptance/routes.ts lists every route with its level for the black-box
// suite's enumerating tests. It is parsed here so the two tables cannot drift.
func TestAcceptanceRouteTableMatches(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "acceptance", "routes.ts"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\{ route: "([^"]+)", level: "([a-z]+)" \}`)
	found := map[string]Level{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		found[m[1]] = Level(m[2])
	}
	if len(found) == 0 {
		t.Fatal("no ROUTE_LEVELS entries found in acceptance/routes.ts")
	}
	for p, l := range wantLevels {
		if found[p] != l {
			t.Errorf("acceptance/routes.ts ROUTE_LEVELS: %q has level %q, the server says %q", p, found[p], l)
		}
	}
	for p := range found {
		if _, ok := wantLevels[p]; !ok {
			t.Errorf("acceptance/routes.ts lists %q, which the server does not register", p)
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
			NewRouter(nil).HandleFunc("GET /api/x", l, func(http.ResponseWriter, *http.Request) {})
		}()
	}
	r := NewRouter(nil)
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
	acc := newAccounts(t)
	r := NewRouter(&Guard{Auth: acc.Svc})
	var got []Level
	for i, l := range []Level{Public, Authenticated, Admin} {
		path := "GET /api/level" + string(rune('a'+i))
		r.HandleFunc(path, l, func(_ http.ResponseWriter, req *http.Request) {
			got = append(got, LevelOf(req.Context()))
		})
	}
	for _, p := range []string{"/api/levela", "/api/levelb", "/api/levelc"} {
		acc.As(r).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
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
