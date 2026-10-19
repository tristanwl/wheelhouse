package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"testing/fstest"
)

// FuzzStaticHandler: the handler that serves the embedded UI and falls back to
// index.html for client-side routes.
//
// Invariants: it never panics; only GET and HEAD are answered (anything else is
// 405 with an Allow header); a dot file or a path with a dot segment ("/.env",
// "/assets/.hidden", "/a/.git/config") is never served, and the answer never
// contains a dot file's content; a 200 body is the content of one of the
// files of the tree or the index (nothing is read from outside it, whatever
// "..", encoded dots, backslashes or NUL the path holds); and a request for
// something that looks like an asset (it has an extension) that does not exist
// is a 404, never the index page.
func FuzzStaticHandler(f *testing.F) {
	const secret = "DOT-FILE-SECRET-CONTENT"
	files := map[string]string{
		"index.html":            "<!doctype html><title>index</title>",
		"assets/app-1a2b3c.js":  "console.log('app')",
		"assets/app-1a2b3c.css": "body{}",
		"favicon.svg":           "<svg/>",
		"dir/page.html":         "<p>page</p>",
		"dir/index.html":        "<p>dir index</p>",
		".gitkeep":              secret,
		".env":                  secret,
		"assets/.hidden":        secret,
		"secret/.git/config":    secret,
		"secret/visible.txt":    "visible",
	}
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	h := NewHandler(fsys)
	f.Fuzz(func(t *testing.T, method, target string) {
		if method == "" || strings.ContainsAny(method, " \r\n\x00") {
			t.Skip()
		}
		u, err := url.ParseRequestURI(target)
		if err != nil {
			t.Skip()
		}
		req := (&http.Request{Method: method, URL: u, Header: http.Header{}, Host: "example.test"}).WithContext(t.Context())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		res := rec.Result()

		if method != http.MethodGet && method != http.MethodHead {
			if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") == "" {
				t.Fatalf("%s %q: %d", method, target, res.StatusCode)
			}
			return
		}
		body := rec.Body.String()
		if strings.Contains(body, secret) {
			t.Fatalf("%s %q served a dot file", method, target)
		}
		if res.StatusCode != http.StatusOK {
			return
		}
		if method == http.MethodHead {
			return
		}
		known := false
		for _, b := range files {
			known = known || b == body
		}
		if !known {
			t.Fatalf("%s %q answered 200 with something that is not in the tree: %.80q", method, target, body)
		}
		name := strings.TrimPrefix(path.Clean("/"+u.Path), "/")
		if body == files["index.html"] && path.Ext(name) != "" && name != "index.html" {
			t.Fatalf("%s %q (an asset that does not exist) answered with the index page", method, target)
		}
	})
}
