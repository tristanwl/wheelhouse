package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/ui"
	"github.com/tristanwl/wheelhouse/server/internal/version"
)

func testHandler(ffmpeg bool, buf *bytes.Buffer) http.Handler {
	log := slog.New(slog.DiscardHandler)
	if buf != nil {
		log = slog.New(slog.NewTextHandler(buf, nil))
	}
	// The placeholder, not the embedded build: these tests must pass with or without `npm run embed`.
	return Handler(Options{Logger: log, Ffmpeg: func() bool { return ffmpeg }, UI: ui.Placeholder()})
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := do(testHandler(false, nil), "GET", "/healthz")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"status":"ok","version":"`+version.Version+`"}` {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestFeaturesWithNoProviders(t *testing.T) {
	rec := do(testHandler(false, nil), "GET", "/api/features")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body struct {
		Features []struct {
			ID        string   `json:"id"`
			Available bool     `json:"available"`
			Missing   []string `json:"missing"`
		} `json:"features"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var ids, available []string
	for _, f := range body.Features {
		ids = append(ids, f.ID)
		if f.Available {
			available = append(available, f.ID)
		} else if len(f.Missing) == 0 {
			t.Errorf("%s is unavailable but says nothing is missing", f.ID)
		}
	}
	if strings.Join(ids, ",") != "dashboard,library,explore,codecs" || strings.Join(available, ",") != "dashboard" {
		t.Fatalf("ids %v, available %v", ids, available)
	}
	if strings.Contains(rec.Body.String(), `"missing":null`) {
		t.Fatal("missing must be [] not null")
	}
}

func TestUnknownAPIPathsAreJSON404(t *testing.T) {
	h := testHandler(false, nil)
	for _, p := range []string{"/api", "/api/", "/api/does-not-exist", "/api/features/extra", "/api/a/b/c"} {
		rec := do(h, "GET", p)
		if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Header().Get("Content-Type"))
		}
		if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: body %q", p, rec.Body.String())
		}
	}
}

func TestWrongMethodOnAPIIsA4xx(t *testing.T) {
	rec := do(testHandler(false, nil), "DELETE", "/api/features")
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestNonAPIPathsGetTheUI(t *testing.T) {
	h := testHandler(false, nil)
	for _, p := range []string{"/", "/library", "/explore", "/codecs", "/library/radarr/123"} {
		rec := do(h, "GET", p)
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestEveryResponseHasSecurityHeadersAndRequestID(t *testing.T) {
	h := testHandler(false, nil)
	for _, p := range []string{"/", "/healthz", "/api/features", "/api/nope", "/missing.js"} {
		rec := do(h, "GET", p)
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
			rec.Header().Get("Referrer-Policy") == "" || rec.Header().Get("X-Request-Id") == "" {
			t.Errorf("%s: missing headers: %v", p, rec.Header())
		}
	}
}

func TestPanicingRouteIsAGeneric500(t *testing.T) {
	var buf bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic("db password is hunter2") })
	o := Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), UI: mux, Ffmpeg: func() bool { return false }}
	rec := do(Handler(o), "GET", "/boom?token=QUERY-CANARY")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(buf.String(), "hunter2") || strings.Contains(buf.String(), "QUERY-CANARY") {
		t.Fatalf("log leaks: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "status=500") {
		t.Fatalf("the 500 was not access-logged: %s", buf.String())
	}
}

// The real server on a real socket, then a clean shutdown.
func TestServeOnARandomPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.DiscardHandler)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, testHandler(false, nil), log) }()

	base := "http://" + ln.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	resp, body := get("/api/features")
	if resp.StatusCode != 200 || !strings.Contains(body, `"id":"dashboard"`) {
		t.Fatalf("features: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("no CSP over the wire")
	}
	if resp, _ = get("/api/nope"); resp.StatusCode != 404 {
		t.Errorf("unknown api: %d", resp.StatusCode)
	}
	if resp, body = get("/library"); resp.StatusCode != 200 || !strings.Contains(body, "work in progress") {
		t.Errorf("spa fallback: %d %s", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
	if _, err := client.Get(base + "/healthz"); err == nil {
		t.Fatal("server still answering after shutdown")
	}
}
