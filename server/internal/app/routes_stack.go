package app

import (
	"net/http"

	"github.com/tristanwl/wheelhouse/server/internal/downloads"
	"github.com/tristanwl/wheelhouse/server/internal/features"
	"github.com/tristanwl/wheelhouse/server/internal/status"
)

// registerCore registers the routes that need no provider.
func registerCore(r *Router, _ deps) {
	// Public: the allow-list for authentication, once there is any.
	r.HandleFunc("GET /healthz", Public, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// registerStack registers the routes that describe the configured stack:
// features, status and downloads.
func registerStack(r *Router, d deps) {
	r.HandleFunc("GET /api/features", Public, func(w http.ResponseWriter, req *http.Request) {
		resp, err := features.Of(req.Context(), d.rt, features.Environment{Ffmpeg: d.ffmpeg()})
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	r.HandleFunc("GET /api/status", Public, func(w http.ResponseWriter, req *http.Request) {
		resp, err := status.Compute(req.Context(), d.rt, d.now())
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	r.HandleFunc("GET /api/downloads", Public, func(w http.ResponseWriter, req *http.Request) {
		resp, err := downloads.Compute(req.Context(), d.rt)
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
}
