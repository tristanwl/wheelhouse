package ui

import (
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the paths of the UI tests
// and the traversal and dot-file tricks of the threat model:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/ui
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	for _, p := range []string{
		"/", "/index.html", "/library", "/explore/some/deep/route", "/assets/app-1a2b3c.js", "/assets/app-1a2b3c.css", "/assets/app.js",
		"/favicon.svg", "/dir/", "/dir/page.html", "/dir", "/missing.png", "/.gitkeep", "/.env", "/assets/.hidden", "/secret/.git/config",
		"/secret/visible.txt", "/secret/./.env", "/assets/../.env", "/../.env", "/%2e%2e/.env", "/%2e/.env", "/.%65nv", "/assets/%2e%2e/%2e%65nv",
		"//.env", "/\\.env", "/assets\\..\\.env", "/%00", "/index.html/", "/index.html/x", "/./index.html", "/dir/../index.html", "/a/../../b",
		"/%ff", "/assets/", "/assets", "/*", "/dir/index.html", "/dir//page.html", "/.", "/..", "/...", "/%2f%2f", "/@x", "/x.y.z",
	} {
		w.Add("FuzzStaticHandler", "path", "GET", p)
		w.Add("FuzzStaticHandler", "path-head", "HEAD", p)
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "OPTIONS", "TRACE", "PATCH", "get", "GET ", "CONNECT"} {
		w.Add("FuzzStaticHandler", "method", m, "/")
		w.Add("FuzzStaticHandler", "method", m, "/.env")
	}
}
