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
	"github.com/tristanwl/wheelhouse/server/internal/providers/all"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/ui"
)

// Options are the parts of the app that tests replace.
type Options struct {
	Logger *slog.Logger
	// UI serves every non-API path. Defaults to the embedded UI.
	UI http.Handler
	// Ffmpeg reports whether ffmpeg works on the host. Defaults to a cached probe.
	Ffmpeg func() bool
	// Runtime turns configured instances into clients. Defaults to a runtime
	// over every provider and Source.
	Runtime *core.Runtime
	// Source is where instances come from when Runtime is nil. Defaults to an
	// empty source (nothing configured); main passes the process environment.
	Source core.ConfigSource
	// Now is the clock for the 24 h warning rule. Defaults to time.Now.
	Now func() time.Time
	// Context bounds background work (running conversions are killed when it is
	// cancelled). Defaults to context.Background.
	Context context.Context
	// Converter configures the codec routes; the zero value uses the defaults.
	Converter ConverterOptions
}

// emptySource configures nothing.
type emptySource struct{}

func (emptySource) Instances(context.Context) ([]core.Instance, error) { return nil, nil }

// deps is what the route groups share.
type deps struct {
	rt     *core.Runtime
	ffmpeg func() bool
	now    func() time.Time
	ctx    context.Context
	conv   ConverterOptions
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

// internalError answers a generic 500 for a failure that is not the caller's:
// never the error text, which could carry configuration.
func internalError(w http.ResponseWriter) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
}

// Routes builds the router: every route, with its access level. It panics if
// the provider registry cannot be built, which only a coding error causes.
func Routes(o Options) *Router {
	if o.UI == nil {
		o.UI = ui.Handler()
	}
	if o.Ffmpeg == nil {
		o.Ffmpeg = features.HasFfmpeg
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Runtime == nil {
		if o.Source == nil {
			o.Source = emptySource{}
		}
		rt, err := all.NewRuntime(o.Source)
		if err != nil {
			panic("providers: " + err.Error())
		}
		o.Runtime = rt
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	d := deps{rt: o.Runtime, ffmpeg: o.Ffmpeg, now: o.Now, ctx: o.Context, conv: o.Converter}

	r := NewRouter()
	registerCore(r, d)
	registerStack(r, d)
	registerCodecs(r, d)

	// Anything else under /api is a JSON 404, never the UI.
	r.HandleFunc("/api", Public, apiNotFound)
	r.HandleFunc("/api/", Public, apiNotFound)

	r.Handle("/", Public, o.UI)
	return r
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
