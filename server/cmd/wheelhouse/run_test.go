package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A client that finds the port open must find the setup token file already
// written: the acceptance harness (and an operator) reads it right after the
// server answers. The server therefore writes it before it binds.
func TestSetupTokenIsWrittenBeforeListening(t *testing.T) {
	dir := t.TempDir()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	t.Setenv("WHEELHOUSE_CONFIG_DIR", dir)
	t.Setenv("WHEELHOUSE_DB", filepath.Join(dir, "wheelhouse.db"))
	t.Setenv("WHEELHOUSE_LISTEN", addr)

	bound := make(chan bool, 1)
	orig := listen
	listen = func(ctx context.Context, a string) (net.Listener, error) {
		_, statErr := os.Stat(filepath.Join(dir, "setup-token"))
		bound <- statErr == nil
		return orig(ctx, a)
	}
	t.Cleanup(func() { listen = orig })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	select {
	case present := <-bound:
		if !present {
			t.Error("the server opened its listener before <config>/setup-token existed")
		}
	case err := <-done:
		t.Fatalf("server stopped before listening: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the server never opened its listener")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stopping: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not stop")
	}
}
