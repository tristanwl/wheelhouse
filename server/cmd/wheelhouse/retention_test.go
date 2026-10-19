package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// An invalid retention is a start-up error naming the variable and the allowed
// range, before anything is opened.
func TestInvalidAuditRetentionRefusesToStart(t *testing.T) {
	for _, v := range []string{"29", "0", "-5", "3651", "half a year", "180.5"} {
		dir := t.TempDir()
		t.Setenv("WHEELHOUSE_CONFIG_DIR", dir)
		t.Setenv("WHEELHOUSE_LISTEN", "127.0.0.1:39517")
		t.Setenv("WHEELHOUSE_AUDIT_RETENTION_DAYS", v)
		err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err == nil || !strings.Contains(err.Error(), "WHEELHOUSE_AUDIT_RETENTION_DAYS") || !strings.Contains(err.Error(), "30 to 3650") {
			t.Errorf("%q: %v", v, err)
		}
		if _, statErr := store.Open(context.Background(), filepath.Join(dir, "wheelhouse.db"), store.Options{Existing: true}); statErr == nil {
			t.Errorf("%q: a database was created before the setting was refused", v)
		}
	}
}

// At start the server prunes what is older than the retention (180 days by
// default) and keeps the rest.
func TestServerPrunesOldAuditRowsAtStart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, "wheelhouse.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for action, age := range map[string]int64{"ancient": 400, "old": 200, "recent": 100, "fresh": 1} {
		if _, err := db.Exec(ctx, `INSERT INTO audit_log (at, actor_name, action) VALUES (?, 'test', ?)`, now-age*86400, action); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	srv := startLiveServer(t, dir)
	deadline := time.Now().Add(20 * time.Second)
	for {
		actions := auditActions(t, dir)
		if actions["audit.prune"] == 1 {
			if actions["ancient"] != 0 || actions["old"] != 0 || actions["recent"] != 1 || actions["fresh"] != 1 {
				t.Fatalf("after the prune: %v", actions)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no prune at start: %v\n%s", actions, srv.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := srv.stop(); err != nil {
		t.Fatal(err)
	}
}

func auditActions(t *testing.T, dir string) map[string]int {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, "wheelhouse.db"), store.Options{Existing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(ctx, `SELECT action FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out[a]++
	}
	return out
}
