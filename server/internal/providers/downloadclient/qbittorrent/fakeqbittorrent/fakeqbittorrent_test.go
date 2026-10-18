package fakeqbittorrent

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The embedded fixtures must stay byte-identical to the TypeScript originals,
// which remain the source of truth until the Next app is removed.
func TestFixturesMatchTypeScriptOriginals(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "..", "..", "lib", "providers", "download-client", "__fixtures__", "qbittorrent")
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		orig, err := fs.ReadFile(os.DirFS(dir), e.Name())
		if os.IsNotExist(err) {
			t.Skipf("TypeScript originals not present (%s)", dir)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(orig, Fixture(e.Name())) {
			t.Errorf("%s differs from the TypeScript original", e.Name())
		}
	}
}
