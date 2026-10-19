package runlock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSecondHolderIsRefusedUntilTheFirstReleases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no flock")
	}
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil || first.Degraded() != "" {
		t.Fatalf("first: %v %q", err, first.Degraded())
	}
	_, err = Acquire(dir)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second holder: %v", err)
	}
	if !strings.Contains(err.Error(), "process "+strconv.Itoa(os.Getpid())) {
		t.Errorf("the refusal should name the holder: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil { // twice is fine
		t.Fatal(err)
	}
	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("after a clean release: %v", err)
	}
	_ = again.Release()
}

func TestAStaleFileLocksNothing(t *testing.T) {
	dir := t.TempDir()
	// What a crashed server leaves behind: the file, with a PID that is long gone.
	if err := os.WriteFile(Path(dir), []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("a leftover file must not refuse: %v", err)
	}
	if b, _ := os.ReadFile(Path(dir)); strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("the file was not restamped: %q", b)
	}
	_ = l.Release()
	// Garbage in the file does not matter either.
	if err := os.WriteFile(Path(dir), []byte("not a pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err = Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Release()
}

func TestLockFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes")
	}
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()
	if st, err := os.Stat(Path(dir)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", st, err)
	}
}

func TestMissingDirectoryIsAnError(t *testing.T) {
	if _, err := Acquire(filepath.Join(t.TempDir(), "nope")); err == nil || errors.Is(err, ErrHeld) {
		t.Fatalf("%v", err)
	}
}

func TestAFilesystemWithoutLocksDegradesInsteadOfFailing(t *testing.T) {
	orig := lockFunc
	t.Cleanup(func() { lockFunc = orig })
	lockFunc = func(*os.File) (string, error) { return "no locks here", nil }
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Degraded() != "no locks here" {
		t.Errorf("degraded = %q", l.Degraded())
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	var nilLock *Lock
	if nilLock.Degraded() != "" || nilLock.Release() != nil {
		t.Error("nil lock")
	}
}

// A process that is killed without any chance to clean up (the crash case)
// frees the lock: the kernel does it, so no stale-file logic can be wrong.
func TestKilledHolderFreesTheLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no flock")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$") //nolint:gosec // re-executes this test binary
	cmd.Env = append(os.Environ(), "RUNLOCK_HELPER_DIR="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == "held" {
				line <- "held"
				return
			}
		}
		line <- ""
	}()
	select {
	case s := <-line:
		if s != "held" {
			t.Fatal("the helper never took the lock")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for the helper")
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("while the other process lives: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("after the holder was killed: %v", err)
	}
	_ = l.Release()
}

// TestHelperHoldLock is the child of TestKilledHolderFreesTheLock.
func TestHelperHoldLock(t *testing.T) {
	dir := os.Getenv("RUNLOCK_HELPER_DIR")
	if dir == "" {
		t.Skip("helper")
	}
	if _, err := Acquire(dir); err != nil {
		_, _ = os.Stdout.WriteString("error " + err.Error() + "\n")
		os.Exit(1)
	}
	_, _ = os.Stdout.WriteString("held\n")
	time.Sleep(time.Minute)
}
