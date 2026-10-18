// Package middleware holds the HTTP middleware every request passes through:
// request IDs, security headers, access logging and panic recovery. Later
// steps (sessions, CSRF, rate limits) extend this chain.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one is outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-Id"

type ctxKey struct{}

// RequestID returns the ID of the request, or "" outside the middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// A client-supplied ID is only kept when it is short and made of safe
// characters, so it cannot inject anything into logs or headers.
var validID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func newID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error (it crashes the program instead).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// WithRequestID gives every request an ID: the client's X-Request-Id when it is
// well formed, otherwise a random one. It is stored in the context and echoed
// in the response.
func WithRequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validID.MatchString(id) {
				id = newID()
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}

// ContentSecurityPolicy allows only same-origin resources, plus images from two
// hosts: the posters and backdrops the UI shows. TMDB's image CDN serves Explore
// and the Radarr posters; Sonarr's poster URLs point at TheTVDB's artwork host.
// Nothing else is allowed (no wildcard, no bare scheme), and the UI loads no
// script, style, font, frame or fetch from outside. There is no 'unsafe-inline'
// and no 'unsafe-eval': scripts and styles must be files.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data: https://image.tmdb.org https://artworks.thetvdb.com; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// SecurityHeaders sets the headers every response carries, errors included.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", ContentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			next.ServeHTTP(w, r)
		})
	}
}

// recorder remembers the status and size of a response. Unwrap keeps
// http.ResponseController features (flushing, deadlines) working.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// AccessLog logs one line per request: method, path, status, size, duration,
// client address and request ID. It never logs the query string, any header or
// any body, because those carry tokens and API keys. The path is logged as a
// quoted attribute, so control characters cannot forge log lines.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &recorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			log.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote", r.RemoteAddr),
				slog.String("request_id", RequestID(r.Context())),
			)
		})
	}
}

// Recover turns a panic in a handler into a generic 500. The response says
// nothing about the cause. The log records the request ID, the type of the
// panic value and the stack, but not the value itself, which may hold user
// input or a secret.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(v)
				}
				log.LogAttrs(r.Context(), slog.LevelError, "panic in handler",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("request_id", RequestID(r.Context())),
					slog.String("panic_type", fmt.Sprintf("%T", v)),
					slog.String("stack", string(debug.Stack())),
				)
				h := w.Header()
				h.Set("Content-Type", "application/json")
				h.Set("Cache-Control", "no-store")
				h.Del("Content-Length")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Internal server error"})
			}()
			next.ServeHTTP(w, r)
		})
	}
}
