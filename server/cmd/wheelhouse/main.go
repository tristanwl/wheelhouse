// Command wheelhouse is the Wheelhouse server.
//
//	wheelhouse               run the server
//	wheelhouse healthcheck   exit 0 if the local server answers /healthz (for HEALTHCHECK)
//	wheelhouse version       print the version (also --version)
//	wheelhouse backup FILE   write a consistent copy of the database to FILE
//	wheelhouse rekey         re-encrypt every stored secret under a new master key
//	wheelhouse reset-password USER   set a one-time password for USER (recovery)
//	wheelhouse forget-secrets --yes  discard stored credentials after the master key is lost
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
	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/config"
	"github.com/tristanwl/wheelhouse/server/internal/converter"
	"github.com/tristanwl/wheelhouse/server/internal/instances"
	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
	"github.com/tristanwl/wheelhouse/server/internal/providers/all"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
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
		case "backup", "rekey", "reset-password", "forget-secrets":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			e := cliEnv{getenv: os.Getenv, configDir: config.ConfigDir(os.Getenv), stdout: os.Stdout}
			var err error
			switch os.Args[1] {
			case "backup":
				err = runBackup(ctx, e, os.Args[2:])
			case "rekey":
				err = runRekey(ctx, e, os.Args[2:])
			case "forget-secrets":
				err = runForgetSecrets(ctx, e, os.Args[2:])
			default:
				err = runResetPassword(ctx, e, os.Args[2:])
			}
			stop()
			if err != nil {
				fmt.Fprintf(os.Stderr, "wheelhouse %s: %v\n", os.Args[1], err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "usage: %s [healthcheck | version | backup <file> | rekey | reset-password <user> | forget-secrets --yes]\n", os.Args[0])
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
	proxies, err := config.TrustedProxies(os.Getenv)
	if err != nil {
		return err
	}
	configDir := config.ConfigDir(os.Getenv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Storage first: a server that cannot open its database or key must not
	// listen, because every route but health and setup needs accounts. Each of
	// these errors names the problem and what to do (wrong file mode, missing
	// volume, a database from a newer version, a key that does not match).
	db, err := store.Open(ctx, config.DBPath(os.Getenv, configDir))
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Warn("closing the database", "error", err)
		}
	}()
	loaded, err := secrets.Load(secrets.LoadOptions{Getenv: os.Getenv, ConfigDir: configDir, Generate: true})
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}
	cipher, err := db.UnlockSecrets(ctx, loaded, true)
	if err != nil {
		if errors.Is(err, store.ErrWrongKey) {
			return fmt.Errorf("secret key: %w. Put the original key back (%s). If it is lost for good, `wheelhouse forget-secrets --yes` discards the stored provider credentials and keeps the accounts; see docs/operations.md, \"Key loss\"", err, loaded.Source)
		}
		return fmt.Errorf("secret key: %w", err)
	}
	accounts := auth.New(db, auth.Options{ConfigDir: configDir, Logger: log})
	if err := accounts.PrepareSetup(ctx); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}

	denied, err := core.ParseDeniedNetworks(os.Getenv("WHEELHOUSE_DENIED_NETWORKS"))
	if err != nil {
		return fmt.Errorf("WHEELHOUSE_DENIED_NETWORKS: %w", err)
	}
	core.SetDeniedNetworks(denied)
	registry, err := all.Registry()
	if err != nil {
		return fmt.Errorf("providers: %w", err)
	}
	stored := instances.New(db, cipher, registry, instances.Options{Environment: core.NewProcessEnvConfigSource(), Logger: log})
	network, err := netaccess.New(ctx, db, proxies)
	if err != nil {
		return err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("listening", "addr", ln.Addr().String(), "version", version.Version)

	handler := app.Handler(app.Options{
		Logger:    log,
		Config:    stored,
		Context:   ctx,
		Converter: app.ConverterOptions{MediaRoots: converter.RootsFromEnv(os.Getenv), ConfigDir: configDir},
		Auth:      accounts,
		Network:   network,
	})
	return app.Serve(ctx, ln, handler, log)
}
