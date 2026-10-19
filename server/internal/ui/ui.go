// Package ui serves the single-page UI embedded in the binary.
//
// The UI is built from web/ (Vite) and copied into dist/ by `npm run embed`
// there (or `make ui` here); dist/ is not committed, only its .gitkeep, so a
// clean checkout still compiles. When dist/ holds no index.html the binary
// serves the committed placeholder page instead, so `go build`, `go vet` and
// `go test` work with or without a built UI.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var builtFS embed.FS

//go:embed placeholder
var placeholderFS embed.FS

// Handler serves the built UI when dist/ has one, and the placeholder otherwise.
func Handler() http.Handler {
	if built, ok := Built(); ok {
		return NewHandler(built)
	}
	return Placeholder()
}

// Built is the embedded build output, and whether there is one (an index.html).
func Built() (fs.FS, bool) {
	sub, err := fs.Sub(builtFS, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time, so this cannot happen
	}
	return sub, hasIndex(sub)
}

// Placeholder serves the committed stand-in page.
func Placeholder() http.Handler {
	sub, err := fs.Sub(placeholderFS, "placeholder")
	if err != nil {
		panic(err) // placeholder is embedded at build time, so this cannot happen
	}
	return NewHandler(sub)
}

func hasIndex(fsys fs.FS) bool {
	info, err := fs.Stat(fsys, "index.html")
	return err == nil && !info.IsDir()
}

// NewHandler serves files from fsys, falling back to index.html for paths that
// are not files so client-side routes survive a reload. A missing path that
// looks like an asset (it has a file extension) is a real 404, so a stale
// script URL does not turn into HTML. Only GET and HEAD are answered.
func NewHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if hasDotSegment(name) {
			// Dot files in the embedded tree (.gitkeep, .gitignore) are build
			// artefacts, never content.
			http.NotFound(w, r)
			return
		}
		if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
			switch {
			case name == "index.html":
				w.Header().Set("Cache-Control", "no-cache")
			case strings.HasPrefix(name, "assets/"):
				// Vite names these by content hash, so a changed file has a new URL.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, fsys)
	})
}

func hasDotSegment(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(index)
}
