package all

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The web UI builds its service forms from each provider's JSON Schema. Its
// tests render those forms from a committed copy of what GET /api/admin/providers
// returns (web/src/fixtures/providers.json). This test fails when the registry
// and the copy differ, so a provider that changes a setting cannot leave the UI
// tests checking an outdated form.
//
// Regenerate with: UPDATE_PROVIDER_SNAPSHOT=1 go test ./internal/providers/all
const snapshotPath = "../../../../web/src/fixtures/providers.json"

// uiProviders is the registry as the admin UI uses it: the public description of
// every provider without its capabilities (those belong to the role, change
// with the adapters, and the forms do not read them).
func uiProviders(t *testing.T) []byte {
	t.Helper()
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	for _, def := range reg.All() {
		raw, err := json.Marshal(def)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "capabilities")
		list = append(list, m)
	}
	out, err := json.MarshalIndent(map[string]any{"providers": list}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func TestWebProviderSnapshotMatchesRegistry(t *testing.T) {
	want := uiProviders(t)
	if os.Getenv("UPDATE_PROVIDER_SNAPSHOT") == "1" {
		if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapshotPath, want, 0o644); err != nil { //nolint:gosec // a source file, world-readable like the rest
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("%v\nrun: UPDATE_PROVIDER_SNAPSHOT=1 go test ./internal/providers/all", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("web/src/fixtures/providers.json is out of date with the provider registry; regenerate it:\n  UPDATE_PROVIDER_SNAPSHOT=1 go test ./internal/providers/all")
	}
}
