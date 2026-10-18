package ui

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerAlwaysAnswers(t *testing.T) {
	// Holds on a clean checkout (dist/ has only .gitkeep) and after `npm run embed`.
	rec := get(Handler(), "GET", "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if _, ok := Built(); !ok && !strings.Contains(rec.Body.String(), "work in progress") {
		t.Fatal("no build is embedded, but the placeholder is not served")
	}
}

func TestDistWithoutIndexIsNotABuild(t *testing.T) {
	if hasIndex(fstest.MapFS{".gitkeep": {}}) {
		t.Fatal("an empty dist counts as a build")
	}
	if hasIndex(fstest.MapFS{"index.html": {Mode: fs.ModeDir}}) {
		t.Fatal("a directory named index.html counts as a build")
	}
	if !hasIndex(fstest.MapFS{"index.html": {Data: []byte("x")}}) {
		t.Fatal("a real index.html is not a build")
	}
}

func TestHashedAssetsAreCachedForeverAndTheIndexNever(t *testing.T) {
	h := NewHandler(fstest.MapFS{
		"index.html":          {Data: []byte("<html>index</html>")},
		"assets/index-abc.js": {Data: []byte("1")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	})
	if got := get(h, "GET", "/assets/index-abc.js").Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("asset Cache-Control = %q", got)
	}
	if got := get(h, "GET", "/").Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q", got)
	}
	if got := get(h, "GET", "/library").Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("fallback Cache-Control = %q", got)
	}
	if got := get(h, "GET", "/favicon.svg").Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("an unhashed file must not be immutable: %q", got)
	}
}

// inlineProblems lists what in an HTML page the CSP (no inline script or style)
// would block.
func inlineProblems(html string) []string {
	var problems []string
	for _, bad := range []string{"<style", "style=", "onclick=", "onload=", "onerror=", "javascript:"} {
		if strings.Contains(html, bad) {
			problems = append(problems, bad)
		}
	}
	for rest := html; ; {
		i := strings.Index(rest, "<script")
		if i < 0 {
			break
		}
		end := strings.Index(rest[i:], ">")
		if end < 0 || !strings.Contains(rest[i:i+end], "src=") {
			problems = append(problems, "inline <script>")
			break
		}
		rest = rest[i+end:]
	}
	return problems
}

func TestInlineProblemsFindsWhatTheCSPBlocks(t *testing.T) {
	for _, html := range []string{
		`<script>alert(1)</script>`,
		`<div style="color:red">`,
		`<style>a{}</style>`,
		`<a href="javascript:void(0)">`,
		`<img onerror="x()">`,
	} {
		if len(inlineProblems(html)) == 0 {
			t.Errorf("%q was not flagged", html)
		}
	}
	ok := `<script type="module" crossorigin src="/assets/index-1.js"></script><link rel="stylesheet" href="/assets/s.css">`
	if p := inlineProblems(ok); len(p) != 0 {
		t.Errorf("a clean page was flagged: %v", p)
	}
}

// After `npm run embed` this checks the real build; on a clean checkout it skips.
func TestBuiltIndexHasNoInlineScriptOrStyle(t *testing.T) {
	built, ok := Built()
	if !ok {
		t.Skip("no UI build is embedded (run `npm run embed` in web/)")
	}
	index, err := fs.ReadFile(built, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if p := inlineProblems(string(index)); len(p) != 0 {
		t.Errorf("index.html has what the CSP would block: %v", p)
	}
}
