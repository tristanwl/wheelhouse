package middleware

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func ok(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello") }

func serve(h http.Handler, method, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func logger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestChainOrderFirstIsOutermost(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	serve(Chain(http.HandlerFunc(ok), mk("a"), mk("b")), "GET", "/", nil)
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("order = %v", order)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := serve(Chain(http.HandlerFunc(ok), SecurityHeaders()), "GET", "/", nil)
	h := rec.Header()

	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	for _, bad := range []string{"unsafe-inline", "unsafe-eval", "*", "http:"} {
		if strings.Contains(csp, bad) {
			t.Errorf("CSP %q must not contain %q", csp, bad)
		}
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
}

func TestSecurityHeadersOnErrorResponses(t *testing.T) {
	boom := func(http.ResponseWriter, *http.Request) { panic("boom") }
	h := Chain(http.HandlerFunc(boom), SecurityHeaders(), Recover(logger(&bytes.Buffer{})))
	rec := serve(h, "GET", "/", nil)
	if rec.Code != 500 || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("status %d, CSP %q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
}

func TestRequestIDGeneratedAndInContext(t *testing.T) {
	var inCtx string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inCtx = RequestID(r.Context())
	}), WithRequestID())
	rec := serve(h, "GET", "/", nil)
	id := rec.Header().Get(RequestIDHeader)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		t.Fatalf("generated id %q", id)
	}
	if inCtx != id {
		t.Fatalf("context id %q != header id %q", inCtx, id)
	}
	other := serve(h, "GET", "/", nil).Header().Get(RequestIDHeader)
	if other == id {
		t.Fatal("two requests got the same id")
	}
}

func TestRequestIDKeepsSafeClientValueOnly(t *testing.T) {
	h := Chain(http.HandlerFunc(ok), WithRequestID())
	set := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(RequestIDHeader, v) }
	}
	if got := serve(h, "GET", "/", set("abc-123_X.y")).Header().Get(RequestIDHeader); got != "abc-123_X.y" {
		t.Errorf("safe id replaced: %q", got)
	}
	for _, bad := range []string{"has space", "semi;colon", strings.Repeat("a", 65), "<script>", "new\\nline"} {
		got := serve(h, "GET", "/", set(bad)).Header().Get(RequestIDHeader)
		if got == bad || len(got) != 32 {
			t.Errorf("unsafe id %q kept as %q", bad, got)
		}
	}
}

func TestRecoverHidesDetails(t *testing.T) {
	const secret = "panic-secret-value-123"
	var buf bytes.Buffer
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(secret)
	}), WithRequestID(), Recover(logger(&buf)))

	rec := serve(h, "GET", "/x", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	if rec.Body.String() != "{\"error\":\"Internal server error\"}\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "goroutine") {
		t.Error("response leaks panic details")
	}
	if strings.Contains(buf.String(), secret) {
		t.Errorf("log contains the panic value: %s", buf.String())
	}
	id := rec.Header().Get(RequestIDHeader)
	if !strings.Contains(buf.String(), id) || !strings.Contains(buf.String(), "panic_type=string") {
		t.Errorf("log lacks request id or type: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "middleware_test.go") {
		t.Errorf("log lacks a stack: %s", buf.String())
	}
}

func TestRecoverHidesErrorValues(t *testing.T) {
	var buf bytes.Buffer
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(io.ErrUnexpectedEOF)
	}), Recover(logger(&buf)))
	rec := serve(h, "GET", "/", nil)
	if strings.Contains(rec.Body.String(), "unexpected EOF") || strings.Contains(buf.String(), "unexpected EOF") {
		t.Error("error text leaked")
	}
}

func TestRecoverRepanicsAbortHandler(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), Recover(logger(&bytes.Buffer{})))
	defer func() {
		err, _ := recover().(error)
		if !errors.Is(err, http.ErrAbortHandler) {
			t.Fatal("ErrAbortHandler must propagate")
		}
	}()
	serve(h, "GET", "/", nil)
}

func TestAccessLogOmitsQueryHeadersAndBodies(t *testing.T) {
	var buf bytes.Buffer
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "response-body-canary")
	}), WithRequestID(), AccessLog(logger(&buf)))

	req := httptest.NewRequest("POST", "/api/thing?apikey=QUERY-CANARY&token=abc", strings.NewReader("request-body-canary"))
	req.Header.Set("Authorization", "Bearer HEADER-CANARY")
	req.Header.Set("Cookie", "session=COOKIE-CANARY")
	req.Header.Set("X-Api-Key", "APIKEY-CANARY")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	for _, canary := range []string{"QUERY-CANARY", "apikey", "token=abc", "HEADER-CANARY", "COOKIE-CANARY", "APIKEY-CANARY", "request-body-canary", "response-body-canary", "?"} {
		if strings.Contains(out, canary) {
			t.Errorf("log contains %q: %s", canary, out)
		}
	}
	for _, want := range []string{"method=POST", "path=/api/thing", "status=418", "bytes=20", "request_id="} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
}

func TestAccessLogQuotesControlCharactersInPath(t *testing.T) {
	var buf bytes.Buffer
	h := Chain(http.HandlerFunc(ok), AccessLog(logger(&buf)))
	req := httptest.NewRequest("GET", "/", nil)
	req.URL.Path = "/a\nlevel=ERROR msg=forged"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("log line was split: %q", buf.String())
	}
}

func TestAccessLogDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), AccessLog(logger(&buf)))
	serve(h, "GET", "/", nil)
	if !strings.Contains(buf.String(), "status=200") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestRecorderKeepsFlusher(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush through the logger failed: %v", err)
		}
	}), AccessLog(logger(&bytes.Buffer{})))
	serve(h, "GET", "/", nil)
}
