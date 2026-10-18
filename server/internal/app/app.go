// Package app wires the handlers and middleware into the server and runs it.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/features"
	"github.com/tristanwl/wheelhouse/server/internal/middleware"
	"github.com/tristanwl/wheelhouse/server/internal/ui"
)

// Options are the parts of the app that tests replace.
type Options struct {
	Logger *slog.Logger
	// UI serves every non-API path. Defaults to the embedded UI.
	UI http.Handler
	// Ffmpeg reports whether ffmpeg works on the host. Defaults to a cached probe.
	Ffmpeg func() bool
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
}

// Routes returns the route table without middleware.
func Routes(o Options) http.Handler {
	if o.UI == nil {
		o.UI = ui.Handler()
	}
	if o.Ffmpeg == nil {
		o.Ffmpeg = features.HasFfmpeg
	}
	mux := http.NewServeMux()

	// Public: the allow-list for authentication, once there is any.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/features", func(w http.ResponseWriter, _ *http.Request) {
		// No providers exist in the skeleton, so no instances are configured.
		writeJSON(w, http.StatusOK, features.Compute(nil, features.Environment{Ffmpeg: o.Ffmpeg()}))
	})

	// Anything else under /api is a JSON 404, never the UI.
	mux.HandleFunc("/api", apiNotFound)
	mux.HandleFunc("/api/", apiNotFound)

	mux.Handle("/", o.UI)
	return mux
}

// Handler returns the full handler: routes wrapped in the middleware chain.
func Handler(o Options) http.Handler {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return middleware.Chain(Routes(o),
		middleware.WithRequestID(),
		middleware.SecurityHeaders(),
		middleware.AccessLog(o.Logger),
		middleware.Recover(o.Logger),
	)
}

// shutdownTimeout is how long in-flight requests get to finish after SIGTERM.
const shutdownTimeout = 10 * time.Second

// Serve serves h on ln until ctx is cancelled, then shuts down gracefully.
// It returns nil after a clean shutdown.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close()
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
