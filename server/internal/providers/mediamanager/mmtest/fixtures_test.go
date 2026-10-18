package mmtest

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The embedded fixtures are copies of the TypeScript ones. They must stay
// byte-identical to lib/providers/media-manager/__fixtures__ while that
// directory exists (the Next app keeps using it until the cut-over).
func TestFixturesMatchTheTypeScriptOnes(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "lib", "providers", "media-manager", "__fixtures__")
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Skip("the TypeScript fixtures are not next to the Go module")
	}
	defer func() { _ = dir.Close() }()
	n := 0
	err = fs.WalkDir(fixtureFS, "testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("testdata", p)
		want, err := dir.ReadFile(rel)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		got, _ := fixtureFS.ReadFile(p)
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the TypeScript fixture", rel)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 19 {
		t.Errorf("checked %d fixtures, want 19", n)
	}
}

func TestFixtureHelpers(t *testing.T) {
	movies := FixtureMovies()
	if len(movies) != 5 {
		t.Fatalf("movies = %d", len(movies))
	}
	// Fresh copies every time.
	movies[0]["title"] = "changed"
	if Str(FixtureMovies()[0], "title") == "changed" {
		t.Error("fixtures are shared")
	}
	k := FourKMovies()
	if len(k) != 2 || Int(k[0], "id") != 101 || Str(k[0], "rootFolderPath") != "/movies-4k" ||
		Str(k[0], "path") != "/movies-4k/Example Movie 1 (2025)" {
		t.Errorf("4K = %v", k)
	}
	file := k[0]["movieFile"].(Doc)
	if Str(file, "path") == "" || file["path"] == FixtureMovies()[0]["movieFile"].(Doc)["path"] {
		t.Errorf("movie file path = %v", file["path"])
	}
}
