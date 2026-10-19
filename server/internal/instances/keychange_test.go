package instances

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// otherProcessRekeys plays `wheelhouse rekey` run by another process against the
// same database file (issue #83): the key version moves on under the server.
func otherProcessRekeys(t *testing.T, f *fixture) {
	t.Helper()
	other, err := store.Open(ctx(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	c, err := other.UnlockSecrets(ctx(), secrets.Loaded{Key: f.key}, false)
	if err != nil {
		t.Fatal(err)
	}
	next, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Rekey(ctx(), c, next); err != nil {
		t.Fatal(err)
	}
}

// A server that notices the key moved on must say so, in the log and in the
// instance's status, and must refuse to save anything it would seal under the
// old key (which the next start could not open): never a silent drop.
func TestAKeyChangedByAnotherProcessIsNoticedAndNothingIsSealedUnderTheOldKey(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	f.src.log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := f.src.Create(ctx(), radarr("http://192.0.2.10:7878", keyA)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.CheckKey(ctx(), nil, f.cipher); err != nil {
		t.Fatalf("the key is current before the rekey: %v", err)
	}

	otherProcessRekeys(t, f)

	// Reading: the credentials cannot be opened with the old key; the instance
	// carries a clear problem, the log names the cause, and no key is leaked.
	got, err := f.src.Instances(ctx())
	if err != nil || len(got) != 1 {
		t.Fatalf("Instances: %+v %v", got, err)
	}
	if _, has := got[0].Config["apiKey"]; has {
		t.Error("the key must not be readable with the old key")
	}
	if !strings.Contains(got[0].Problem, "secret key was changed") || !strings.Contains(got[0].Problem, "Restart") {
		t.Errorf("problem = %q", got[0].Problem)
	}
	if !strings.Contains(logs.String(), "changed while Wheelhouse was running") || strings.Contains(logs.String(), keyA) {
		t.Errorf("log = %q", logs.String())
	}

	// Writing: every path that would seal refuses with ErrKeyChanged.
	if _, err := f.src.Create(ctx(), NewInstance{ProviderID: "sonarr", Name: "Sonarr", Config: map[string]any{"url": "http://192.0.2.11:8989", "apiKey": keyB}}); !errors.Is(err, ErrKeyChanged) {
		t.Errorf("Create: %v", err)
	}
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "http://192.0.2.10:7878", "apiKey": keyB}}); !errors.Is(err, ErrKeyChanged) {
		t.Errorf("Update: %v", err)
	}
	name := "Renamed"
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Name: &name}); !errors.Is(err, ErrKeyChanged) {
		t.Errorf("Update without secrets still refuses, so no half-saved state: %v", err)
	}
	f.env["sonarr"] = core.Instance{ID: "sonarr", ProviderID: "sonarr", Name: "Sonarr", Enabled: true, Config: map[string]any{"url": "http://192.0.2.12:8989", "apiKey": keyB}}
	if _, err := f.src.Import(ctx(), "sonarr"); !errors.Is(err, ErrKeyChanged) {
		t.Errorf("Import: %v", err)
	}
	// Nothing new was stored.
	var n int
	if err := f.db.QueryRow(ctx(), "SELECT count(*) FROM instances").Scan(&n); err != nil || n != 1 {
		t.Errorf("instances = %d, %v", n, err)
	}
}

func TestTheCurrentKeyPassesTheCheck(t *testing.T) {
	f := newFixture(t)
	if err := f.db.Tx(ctx(), func(tx *sql.Tx) error { return f.db.CheckKey(ctx(), tx, f.cipher) }); err != nil {
		t.Fatal(err)
	}
	var wrong = f.cipher
	otherProcessRekeys(t, f)
	if err := f.db.CheckKey(ctx(), nil, wrong); !errors.Is(err, store.ErrKeyChanged) {
		t.Fatalf("after the rekey: %v", err)
	}
}
