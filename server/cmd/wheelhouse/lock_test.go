package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/runlock"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// liveServer runs the real run() in this process on a temporary directory and
// returns when it is listening. stop shuts it down and waits.
type liveServer struct {
	dir  string
	logs *syncBuffer
	stop func() error
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startLiveServer(t *testing.T, dir string) *liveServer {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	t.Setenv("WHEELHOUSE_CONFIG_DIR", dir)
	t.Setenv("WHEELHOUSE_DB", filepath.Join(dir, "wheelhouse.db"))
	t.Setenv("WHEELHOUSE_LISTEN", addr)

	up := make(chan struct{}, 1)
	orig := listen
	listen = func(ctx context.Context, a string) (net.Listener, error) {
		ln, err := orig(ctx, a)
		if err == nil {
			up <- struct{}{}
		}
		return ln, err
	}
	t.Cleanup(func() { listen = orig })

	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.NewTextHandler(logs, nil))) }()
	select {
	case <-up:
	case err := <-done:
		cancel()
		t.Fatalf("the server stopped before listening: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("the server never listened")
	}
	s := &liveServer{dir: dir, logs: logs}
	var once sync.Once
	var stopErr error
	s.stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(30 * time.Second):
				stopErr = errors.New("the server did not stop")
			}
		})
		return stopErr
	}
	t.Cleanup(func() { _ = s.stop() })
	return s
}

func cliFor(dir string, out io.Writer) cliEnv {
	return cliEnv{getenv: func(k string) string {
		if k == "WHEELHOUSE_DB" {
			return filepath.Join(dir, "wheelhouse.db")
		}
		return ""
	}, configDir: dir, stdout: out}
}

func keyVersion(t *testing.T, dir string) uint32 {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, "wheelhouse.db"), store.Options{Existing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	v, err := db.GetMeta(ctx, "secret_key_check")
	if err != nil {
		t.Fatal(err)
	}
	ver, err := secrets.EnvelopeVersion(v.([]byte))
	if err != nil {
		t.Fatal(err)
	}
	return ver
}

// Issue #83: a running server holds the configuration lock, and the commands
// that rewrite the key or the secrets refuse, changing nothing.
func TestKeyCommandsRefuseWhileAServerRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no advisory locks on this platform")
	}
	dir := t.TempDir()
	srv := startLiveServer(t, dir)
	ctx := context.Background()
	before := keyVersion(t, dir)
	keyBefore, err := os.ReadFile(filepath.Join(dir, secrets.GeneratedKeyName)) //nolint:gosec // test directory
	if err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	e := cliFor(dir, out)
	for name, run := range map[string]func() error{
		"rekey":          func() error { return runRekey(ctx, e, nil) },
		"forget-secrets": func() error { return runForgetSecrets(ctx, e, []string{"--yes"}) },
	} {
		err := run()
		if err == nil {
			t.Fatalf("%s ran against a live server", name)
		}
		for _, want := range []string{"refusing to run", "server is running", "Stop the server", "throwaway container", name} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal should say %q: %v", name, want, err)
			}
		}
	}
	if out.Len() != 0 {
		t.Errorf("a refused command printed %q", out.String())
	}
	if v := keyVersion(t, dir); v != before {
		t.Errorf("the key version moved from %d to %d", before, v)
	}
	if keyAfter, _ := os.ReadFile(filepath.Join(dir, secrets.GeneratedKeyName)); !bytes.Equal(keyAfter, keyBefore) { //nolint:gosec // test directory
		t.Error("the key file changed")
	}

	// A backup is safe online and neither needs nor takes the lock.
	dest := filepath.Join(t.TempDir(), "online.db")
	if err := runBackup(ctx, e, []string{dest}); err != nil {
		t.Fatalf("backup against a live server: %v", err)
	}
	// reset-password does not touch secrets or the key either.
	if err := runResetPassword(ctx, e, []string{"nobody"}); err == nil || !strings.Contains(err.Error(), "no user named") {
		t.Errorf("reset-password against a live server: %v", err)
	}

	// After a clean shutdown the same commands work.
	if err := srv.stop(); err != nil {
		t.Fatal(err)
	}
	if err := runRekey(ctx, e, nil); err != nil {
		t.Fatalf("rekey after the server stopped: %v", err)
	}
	if v := keyVersion(t, dir); v != before+1 {
		t.Errorf("key version %d after rekey, want %d", v, before+1)
	}
}

// A second server on the same directory is refused too: two processes would
// each hold their own idea of the key and of the setup token.
func TestSecondServerOnTheSameDirectoryIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no advisory locks on this platform")
	}
	dir := t.TempDir()
	startLiveServer(t, dir)
	err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, runlock.ErrHeld) || !strings.Contains(err.Error(), "same /config volume") {
		t.Fatalf("second server: %v", err)
	}
}

// What a crashed or killed server leaves behind is a lock file with an old PID.
// It must neither stop the next server nor the commands.
func TestStaleLockFileAfterACrashBlocksNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(runlock.Path(dir), []byte("424242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := runRekey(ctx, cliFor(dir, io.Discard), nil); err != nil {
		t.Fatalf("rekey with a stale lock file: %v", err)
	}
	srv := startLiveServer(t, dir) // and a server starts over it
	if err := runRekey(ctx, cliFor(dir, io.Discard), nil); err == nil {
		t.Error("rekey must be refused again once the server holds the lock")
	}
	if err := srv.stop(); err != nil {
		t.Fatal(err)
	}
	if err := runForgetSecrets(ctx, cliFor(dir, io.Discard), []string{"--yes"}); err == nil || !strings.Contains(err.Error(), "nothing to forget") {
		t.Errorf("forget-secrets after the server stopped should get to its own checks: %v", err)
	}
}

// Where the filesystem cannot lock, the server starts anyway and says so.
func TestServerWarnsWhenTheFilesystemCannotLock(t *testing.T) {
	orig := acquireLock
	t.Cleanup(func() { acquireLock = orig })
	acquireLock = func(string) (*runlock.Lock, error) { return runlock.Unlocked("test: no locks"), nil }
	dir := t.TempDir()
	srv := startLiveServer(t, dir)
	if !strings.Contains(srv.logs.String(), "does not hold the configuration lock") {
		t.Errorf("no warning in the log:\n%s", srv.logs.String())
	}
	out := &bytes.Buffer{}
	if err := runRekey(context.Background(), cliFor(dir, out), nil); err != nil {
		t.Fatalf("degraded rekey: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING: cannot check whether a server is running") {
		t.Errorf("no warning on the command line: %q", out.String())
	}
}
