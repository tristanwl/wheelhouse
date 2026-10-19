package app

import (
	"context"
	"net/http"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/downloads"
	"github.com/tristanwl/wheelhouse/server/internal/features"
	"github.com/tristanwl/wheelhouse/server/internal/status"
	"github.com/tristanwl/wheelhouse/server/internal/version"
)

// registerCore registers the routes that need no provider.
func registerCore(r *Router, d deps) {
	// Public, like login and setup: the allow-list for authentication. The
	// answer does not need the database, but reports it: a store that does not
	// answer makes the status "degraded" (503, so the container health check
	// notices), and nothing else about the failure is said.
	r.HandleFunc("GET /healthz", Public, func(w http.ResponseWriter, req *http.Request) {
		status, code := "ok", http.StatusOK
		if d.auth != nil {
			ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
			defer cancel()
			if d.auth.Healthy(ctx) != nil {
				status, code = "degraded", http.StatusServiceUnavailable
			}
		}
		writeJSON(w, code, map[string]string{"status": status, "version": version.Version})
	})
}

// registerStack registers the routes that describe the configured stack:
// features, status and downloads.
func registerStack(r *Router, d deps) {
	r.HandleFunc("GET /api/features", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		resp, err := features.Of(req.Context(), d.rt, features.Environment{Ffmpeg: d.ffmpeg()})
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	r.HandleFunc("GET /api/status", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		resp, err := status.Compute(req.Context(), d.rt, d.now())
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	r.HandleFunc("GET /api/downloads", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		resp, err := downloads.Compute(req.Context(), d.rt)
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
}
