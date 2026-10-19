// Command wheelhouse is the Wheelhouse server.
//
//	wheelhouse               run the server
//	wheelhouse healthcheck   exit 0 if the local server answers /healthz (for HEALTHCHECK)
//	wheelhouse version       print the version (also --version)
//	wheelhouse backup FILE   write a consistent copy of the database to FILE
//	wheelhouse rekey         re-encrypt every stored secret under a new master key
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/app"
	"github.com/tristanwl/wheelhouse/server/internal/codecs"
	"github.com/tristanwl/wheelhouse/server/internal/config"
	"github.com/tristanwl/wheelhouse/server/internal/converter"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-version":
			fmt.Println("wheelhouse", version.Version)
			return
		case "healthcheck":
			if err := healthcheck(os.Getenv, http.DefaultTransport); err != nil {
				fmt.Fprintln(os.Stderr, "unhealthy:", err)
				os.Exit(1)
			}
			return
		case "backup", "rekey":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			e := cliEnv{getenv: os.Getenv, configDir: codecs.DefaultConfigDir, stdout: os.Stdout}
			var err error
			if os.Args[1] == "backup" {
				err = runBackup(ctx, e, os.Args[2:])
			} else {
				err = runRekey(ctx, e, os.Args[2:])
			}
			stop()
			if err != nil {
				fmt.Fprintf(os.Stderr, "wheelhouse %s: %v\n", os.Args[1], err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "usage: %s [healthcheck | version | backup <file> | rekey]\n", os.Args[0])
			os.Exit(2)
		}
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// healthcheck asks the server on this machine, at the address it was told to
// listen on, whether it is healthy. It needs no curl or shell.
func healthcheck(getenv func(string) string, rt http.RoundTripper) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.DialAddr()+"/healthz", nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return errors.New("the server does not answer")
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz answered %d", res.StatusCode)
	}
	return nil
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
	log.Info("listening", "addr", ln.Addr().String(), "version", version.Version)

	handler := app.Handler(app.Options{
		Logger:    log,
		Source:    core.NewProcessEnvConfigSource(),
		Context:   ctx,
		Converter: app.ConverterOptions{MediaRoots: converter.RootsFromEnv(os.Getenv)},
	})
	return app.Serve(ctx, ln, handler, log)
}
