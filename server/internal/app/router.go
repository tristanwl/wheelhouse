package app

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Level is who may call a route. The Guard enforces it on every request
// (guard.go): there is no way to register a route that skips it.
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
	guard  *Guard
	routes []Route
}

// NewRouter returns an empty router whose routes are enforced by g. A nil Guard
// refuses every route that is not public.
func NewRouter(g *Guard) *Router { return &Router{mux: http.NewServeMux(), guard: g} }

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
	r.mux.Handle(pattern, r.guard.authorize(pattern, level, h))
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

// IsAPI reports whether a pattern is under /api (for tests and docs).
func IsAPI(pattern string) bool {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = pattern
	}
	return path == "/api" || strings.HasPrefix(path, "/api/")
}
