package ui

import (
	"testing"
	"testing/fstest"
)

func TestDotFilesOfTheEmbeddedTreeAreNeverServed(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte("<html>app</html>")},
		".gitkeep":         {Data: []byte("")},
		".gitignore":       {Data: []byte("*\n")},
		"assets/.hidden":   {Data: []byte("x")},
		"assets/app-1.js":  {Data: []byte("1")},
		".well/known/file": {Data: []byte("x")},
	}
	h := NewHandler(fsys)
	for _, p := range []string{"/.gitkeep", "/.gitignore", "/assets/.hidden", "/.well/known/file", "/%2egitkeep", "/a/../.gitignore"} {
		if rec := get(h, "GET", p); rec.Code != 404 || rec.Body.Len() > 20 && rec.Body.String() == "*\n" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
	if rec := get(h, "GET", "/assets/app-1.js"); rec.Code != 200 {
		t.Errorf("a normal asset: %d", rec.Code)
	}
}
