package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestEmbeddedPlaceholder(t *testing.T) {
	h := Handler()
	for _, p := range []string{"/", "/index.html", "/library", "/explore/some/deep/route"} {
		rec := get(h, "GET", p)
		if p == "/index.html" {
			// FileServer redirects /index.html to ./ ; that is fine.
			if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusOK {
				t.Errorf("%s: status %d", p, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", p, rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: content type %q", p, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), "work in progress") {
			t.Errorf("%s: body lacks the placeholder text", p)
		}
	}
}

func TestPlaceholderHasNoInlineScriptOrStyle(t *testing.T) {
	body := get(Handler(), "GET", "/").Body.String()
	for _, bad := range []string{"<script", "<style", "style=", "onclick=", "javascript:"} {
		if strings.Contains(body, bad) {
			t.Errorf("placeholder contains %q, which the CSP would block", bad)
		}
	}
	if rec := get(Handler(), "GET", "/placeholder.css"); rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("placeholder.css: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestMissingAssetIs404NotIndex(t *testing.T) {
	rec := get(Handler(), "GET", "/assets/app.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("a missing asset must not return the index")
	}
}

func TestPathTraversalStaysInsideTheUI(t *testing.T) {
	rec := get(Handler(), "GET", "/../../go.mod")
	if strings.Contains(rec.Body.String(), "module ") {
		t.Fatal("served a file outside dist")
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := Handler()
	if rec := get(h, "POST", "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
	rec := get(h, "HEAD", "/library")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d, %d body bytes", rec.Code, rec.Body.Len())
	}
}

func TestNewHandlerServesRealFilesAndFallsBack(t *testing.T) {
	h := NewHandler(fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})
	if rec := get(h, "GET", "/assets/app.js"); rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Errorf("asset: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(h, "GET", "/library/42"); rec.Code != 200 || rec.Body.String() != "<html>index</html>" {
		t.Errorf("fallback: %d %q", rec.Code, rec.Body.String())
	}
}
