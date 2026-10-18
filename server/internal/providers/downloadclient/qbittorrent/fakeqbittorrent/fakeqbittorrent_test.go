package fakeqbittorrent

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The embedded fixtures must stay byte-identical to the acceptance suite's
// originals in acceptance/fakes, which are the source of truth.
func TestFixturesMatchAcceptanceOriginals(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "..", "..", "acceptance", "fakes", "qbittorrent")
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		orig, err := fs.ReadFile(os.DirFS(dir), e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(orig, Fixture(e.Name())) {
			t.Errorf("%s differs from the acceptance original", e.Name())
		}
	}
}
