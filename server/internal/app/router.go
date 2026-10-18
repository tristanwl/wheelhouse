package app

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Level is who may call a route. Authentication arrives in Phase 2; until then
// every level is treated as public, but each route already says what it needs,
// so turning enforcement on is one change in Router.authorize.
type Level string

// The access levels.
const (
	// Public routes need no session: health, login, first-run setup.
	Public Level = "public"
	// Authenticated routes need a signed-in user (either role).
	Authenticated Level = "authenticated"
	// Admin routes need an administrator: configuration, users, sessions.
	Admin Level = "admin"
)

// Valid reports whether l is one of the three levels.
func (l Level) Valid() bool {
	return l == Public || l == Authenticated || l == Admin
}

// Route is one registered route: a ServeMux pattern and its access level.
type Route struct {
	Pattern string
	Level   Level
}

// Router is the only way routes are registered. Every route must name its
// access level, so none can be added without a decision, and every handler
// goes through authorize.
type Router struct {
	mux    *http.ServeMux
	routes []Route
}

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{mux: http.NewServeMux()} }

// Handle registers h for a ServeMux pattern ("GET /api/status", "/") at an
// access level. It panics on an invalid level or a duplicate pattern, which are
// programming errors found at startup (and by the tests).
func (r *Router) Handle(pattern string, level Level, h http.Handler) {
	if !level.Valid() {
		panic(fmt.Sprintf("route %q has no valid access level (%q)", pattern, level))
	}
	if slices.ContainsFunc(r.routes, func(x Route) bool { return x.Pattern == pattern }) {
		panic(fmt.Sprintf("route %q registered twice", pattern))
	}
	r.routes = append(r.routes, Route{Pattern: pattern, Level: level})
	r.mux.Handle(pattern, authorize(level, h))
}

// HandleFunc is Handle for a function.
func (r *Router) HandleFunc(pattern string, level Level, h http.HandlerFunc) {
	r.Handle(pattern, level, h)
}

// Routes lists every registered route in registration order.
func (r *Router) Routes() []Route { return slices.Clone(r.routes) }

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

type levelKey struct{}

// LevelOf returns the access level of the route serving the request.
func LevelOf(ctx context.Context) Level {
	l, _ := ctx.Value(levelKey{}).(Level)
	return l
}

// authorize is the single server-side authorization hook. It does not refuse
// anything yet: there are no users, so every level is allowed. Phase 2 checks
// the session against level here.
func authorize(level Level, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), levelKey{}, level)))
	})
}

// IsAPI reports whether a pattern is under /api (for tests and docs).
func IsAPI(pattern string) bool {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = pattern
	}
	return path == "/api" || strings.HasPrefix(path, "/api/")
}
