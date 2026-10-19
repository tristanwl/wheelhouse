package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

func newEnv(t *testing.T, vars map[string]string) (cliEnv, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return cliEnv{getenv: func(k string) string { return vars[k] }, configDir: t.TempDir(), stdout: out}, out
}

func TestBackupCommand(t *testing.T) {
	e, out := newEnv(t, nil)
	ctx := context.Background()
	db, err := store.Open(ctx, e.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO meta (key, value) VALUES ('k', 'live')"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // still open: the server is running

	dest := filepath.Join(t.TempDir(), "b.db")
	if err := runBackup(ctx, e, []string{dest}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "master key is not included") {
		t.Errorf("output: %q", out.String())
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dest); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %04o", st.Mode().Perm())
		}
	}
	bk, err := store.Open(ctx, dest, store.Options{Existing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bk.Close() }()
	if v, err := bk.GetMeta(ctx, "k"); err != nil || v != "live" {
		t.Errorf("%v, %v", v, err)
	}

	// Refuses to overwrite, and says so.
	err = runBackup(ctx, e, []string{dest})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("overwrite: %v", err)
	}
}

func TestBackupCommandDoesNotCreateOrMigrate(t *testing.T) {
	e, _ := newEnv(t, nil)
	dest := filepath.Join(t.TempDir(), "b.db")
	err := runBackup(context.Background(), e, []string{dest})
	if err == nil || !strings.Contains(err.Error(), "no database") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(e.dbPath()); err == nil {
		t.Error("backup created a database")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("backup created a file")
	}
}

func TestBackupCommandUsage(t *testing.T) {
	e, _ := newEnv(t, nil)
	for _, args := range [][]string{nil, {}, {"a", "b"}, {""}, {"--help"}} {
		if err := runBackup(context.Background(), e, args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestBackupUsesWheelhouseDB(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "elsewhere.db")
	e, _ := newEnv(t, map[string]string{"WHEELHOUSE_DB": custom})
	db, err := store.Open(context.Background(), custom)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := runBackup(context.Background(), e, []string{filepath.Join(t.TempDir(), "b.db")}); err != nil {
		t.Fatal(err)
	}
}

func TestRekeyCommandWithNothingStored(t *testing.T) {
	e, out := newEnv(t, nil)
	ctx := context.Background()
	if err := runRekey(ctx, e, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Re-encrypted 0 secret(s); the key is now version 2.") {
		t.Errorf("output: %q", out.String())
	}
	// The generated key exists, is private, and matches the database.
	loaded, err := secrets.Load(secrets.LoadOptions{Getenv: e.getenv, ConfigDir: e.configDir})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, e.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if c, err := db.UnlockSecrets(ctx, loaded, false); err != nil || c.Version() != 2 {
		t.Fatalf("%v", err)
	}
	// Again: version 3.
	out.Reset()
	if err := runRekey(ctx, e, nil); err != nil || !strings.Contains(out.String(), "version 3") {
		t.Errorf("%v %q", err, out.String())
	}
}

func TestRekeyCommandNeverPrintsKeys(t *testing.T) {
	e, out := newEnv(t, nil)
	if err := runRekey(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(e.configDir, secrets.GeneratedKeyName))
	for _, bad := range []string{string(raw), base64.StdEncoding.EncodeToString(raw), base64.RawStdEncoding.EncodeToString(raw)} {
		if strings.Contains(out.String(), bad) {
			t.Error("the key was printed")
		}
	}
}

func TestRekeyCommandWithOperatorKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opKey := filepath.Join(dir, "operator.key")
	k, _ := secrets.GenerateKeyFile(opKey)
	_ = k
	e, out := newEnv(t, map[string]string{secrets.EnvKeyFile: opKey})

	// Without --new-key-out the command explains and changes nothing.
	err := runRekey(ctx, e, nil)
	if err == nil || !strings.Contains(err.Error(), "--new-key-out") {
		t.Fatalf("err = %v", err)
	}
	newKey := filepath.Join(dir, "new.key")
	if err := runRekey(ctx, e, []string{"--new-key-out", newKey}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "version 2") || !strings.Contains(out.String(), newKey) {
		t.Errorf("output: %q", out.String())
	}
	// The operator installs the new key; the database opens with it, not the old one.
	db, err := store.Open(ctx, e.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	nk, err := secrets.ReadKeyFile(newKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnlockSecrets(ctx, secrets.Loaded{Key: nk}, false); err != nil {
		t.Errorf("new key: %v", err)
	}
	if _, err := db.UnlockSecrets(ctx, secrets.Loaded{Key: k}, false); err == nil {
		t.Error("the old key still opens the database")
	}
	// The operator's own key file was not touched, and no generated key appeared.
	if _, err := secrets.ReadKeyFile(opKey); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(e.configDir, secrets.GeneratedKeyName)); err == nil {
		t.Error("a generated key was created next to an operator key")
	}
}

func TestRekeyCommandRefusals(t *testing.T) {
	ctx := context.Background()
	e, _ := newEnv(t, nil)
	for _, args := range [][]string{{"extra"}, {"--bogus"}, {"--new-key-out"}} {
		if err := runRekey(ctx, e, args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
	// --new-key-out with the generated key is refused.
	if err := runRekey(ctx, e, []string{"--new-key-out", filepath.Join(t.TempDir(), "x")}); err == nil || !strings.Contains(err.Error(), "only for keys supplied") {
		t.Errorf("generated + out: %v", err)
	}
	// A key that does not match the database is refused with a clear error.
	if err := runRekey(ctx, e, nil); err != nil {
		t.Fatal(err)
	}
	other := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	e2 := cliEnv{getenv: func(k string) string {
		if k == secrets.EnvKey {
			return other
		}
		return ""
	}, configDir: e.configDir, stdout: &bytes.Buffer{}}
	err := runRekey(ctx, e2, []string{"--new-key-out", filepath.Join(t.TempDir(), "n.key")})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("wrong key: %v", err)
	}
}

func TestStorageCommandsDoNotRunAtServerStart(t *testing.T) {
	// The server does not open the database or create a key yet: nothing in
	// run() references them. This guards the "no behaviour change" promise.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	run := string(src[strings.Index(string(src), "func run("):])
	for _, forbidden := range []string{"store.", "secrets.", "cliEnv"} {
		if strings.Contains(run, forbidden) {
			t.Errorf("run() uses %s", forbidden)
		}
	}
}
