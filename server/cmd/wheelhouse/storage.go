package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/config"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// cliEnv is what the storage commands need from the outside, so tests can point
// them at a temporary directory.
type cliEnv struct {
	getenv    func(string) string
	configDir string // holds the generated secret.key and the default database
	stdout    io.Writer
}

func (e cliEnv) dbPath() string { return config.DBPath(e.getenv, e.configDir) }

// runBackup implements `wheelhouse backup <file>`: a consistent copy of the
// database, made while the server runs. It never creates the database, never
// migrates it (so a backup before an upgrade is of what is there) and never
// overwrites a file.
func runBackup(ctx context.Context, e cliEnv, args []string) error {
	if len(args) != 1 || args[0] == "" || args[0][0] == '-' {
		return errors.New("usage: wheelhouse backup <file>")
	}
	db, err := store.Open(ctx, e.dbPath(), store.Options{Existing: true})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.Backup(ctx, args[0]); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.stdout, "Backup written to %s (mode 0600).\n", args[0])
	_, _ = fmt.Fprintln(e.stdout, "WARNING: the master key is NOT in this backup. Provider API keys and passwords in it are encrypted with that key")
	_, _ = fmt.Fprintln(e.stdout, "and can never be recovered without it. Keep a copy of the key (secret.key in the config directory, or the key you")
	_, _ = fmt.Fprintln(e.stdout, "supplied) somewhere safe and separate from your backups. See docs/operations.md, \"Key loss\".")
	return nil
}

// runResetPassword implements `wheelhouse reset-password <user>`: the recovery
// path for a locked-out administrator. Being able to run it means having a shell
// in the container (and so the volume), which is the proof of ownership. It sets
// a generated one-time password, which the account must change at its next
// login, ends the account's sessions, records the reset in the audit log as
// done by "cli", and prints the password once.
func runResetPassword(ctx context.Context, e cliEnv, args []string) error {
	if len(args) != 1 || args[0] == "" || args[0][0] == '-' {
		return errors.New("usage: wheelhouse reset-password <user>")
	}
	path := e.dbPath()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no database at %s: has the server been started once, and is this the right container?", path)
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	pw, err := auth.New(db, auth.Options{}).ResetPasswordCLI(ctx, args[0])
	if errors.Is(err, auth.ErrNotFound) {
		return fmt.Errorf("there is no user named %q", args[0])
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.stdout, "The password of %q was reset. Sign in with this one-time password (shown once, not stored):\n\n    %s\n\n", args[0], pw)
	_, _ = fmt.Fprintln(e.stdout, "Wheelhouse will ask for a new password at that login. The account's sessions were ended.")
	return nil
}

// runRekey implements `wheelhouse rekey [--new-key-out <file>]`: it generates a
// new master key (version n+1) and re-encrypts every stored secret under it in
// one transaction. Stop the server first and take a backup.
//
// A key Wheelhouse generated (secret.key in the config directory) is replaced in
// place. A key the operator supplies (WHEELHOUSE_SECRET_KEY_FILE or
// WHEELHOUSE_SECRET_KEY) cannot be replaced from here, so the new key is written
// to --new-key-out for the operator to install. The key is never printed.
func runRekey(ctx context.Context, e cliEnv, args []string) error {
	fs := flag.NewFlagSet("rekey", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("new-key-out", "", "write the new key to this file (required when the key is supplied by the environment)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New("usage: wheelhouse rekey [--new-key-out <file>]")
	}

	// This command owns the database and the key, so it creates them when they
	// do not exist yet (rekeying an installation with no secrets is a no-op that
	// still moves the key version on).
	db, err := store.Open(ctx, e.dbPath())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	loaded, err := secrets.Load(secrets.LoadOptions{Getenv: e.getenv, ConfigDir: e.configDir, Generate: true})
	if err != nil {
		return err
	}
	current, err := db.UnlockSecrets(ctx, loaded, true)
	if err != nil {
		return err
	}

	var next *secrets.Cipher
	var n int
	if loaded.Source == secrets.SourceGenerated {
		if *out != "" {
			return errors.New("--new-key-out is only for keys supplied by WHEELHOUSE_SECRET_KEY_FILE or WHEELHOUSE_SECRET_KEY; this installation uses the generated " + secrets.GeneratedKeyName)
		}
		next, n, err = db.RekeyFiles(ctx, current, loaded)
	} else {
		if *out == "" {
			return fmt.Errorf("the key comes from %s, which this command cannot replace: run again with --new-key-out <file>, then install that file as the new key", loaded.Source)
		}
		next, n, err = db.RekeyToFile(ctx, current, *out)
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.stdout, "Re-encrypted %d secret(s); the key is now version %d.\n", n, next.Version())
	if loaded.Source == secrets.SourceGenerated {
		_, _ = fmt.Fprintf(e.stdout, "The new key replaced %s.\n", loaded.Path)
	} else {
		_, _ = fmt.Fprintf(e.stdout, "The new key is in %s (32 raw bytes, mode 0600). Install it as your %s (or its base64 as %s) before starting the server: the old key no longer opens the database. Delete %s afterwards.\n",
			*out, secrets.EnvKeyFile, secrets.EnvKey, *out)
	}
	_, _ = fmt.Fprintln(e.stdout, "Old backups need the old key.")
	return nil
}

// runForgetSecrets implements `wheelhouse forget-secrets --yes`: the recovery
// from a master key that is lost for good. The server refuses to start when the
// key does not match the database (ErrWrongKey); the stored provider credentials
// cannot be read by anyone, so this discards them and makes the key that is
// configured now the database's key. Accounts and sessions are untouched. It
// refuses when the configured key does work, so it cannot destroy readable
// secrets, and without --yes.
func runForgetSecrets(ctx context.Context, e cliEnv, args []string) error {
	fs := flag.NewFlagSet("forget-secrets", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "confirm that the stored provider credentials are discarded")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || !*yes {
		return errors.New("usage: wheelhouse forget-secrets --yes   (discards every stored provider API key and password; only for a master key that is lost for good, see docs/operations.md)")
	}
	path := e.dbPath()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no database at %s", path)
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	loaded, err := secrets.Load(secrets.LoadOptions{Getenv: e.getenv, ConfigDir: e.configDir, Generate: true})
	if err != nil {
		return err
	}
	_, n, err := db.ForgetSecrets(ctx, loaded.Key)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.stdout, "Discarded %d stored secret value(s). The key from %s is now this database's key.\n", n, loaded.Source)
	_, _ = fmt.Fprintln(e.stdout, "Accounts and sessions were not touched. Enter the credentials of your services again in Wheelhouse, and keep a copy of the key somewhere safe.")
	return nil
}
