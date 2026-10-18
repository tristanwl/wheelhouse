// Command wheelhouse is the Wheelhouse server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/tristanwl/wheelhouse/server/internal/app"
	"github.com/tristanwl/wheelhouse/server/internal/config"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("listening", "addr", ln.Addr().String())

	return app.Serve(ctx, ln, app.Handler(app.Options{Logger: log}), log)
}
