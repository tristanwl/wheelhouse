package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJoinURLKeepsAPathPrefix(t *testing.T) {
	cases := [][3]string{
		{"http://h:8989/sonarr", "/api/v3/x", "http://h:8989/sonarr/api/v3/x"},
		{"http://h:8989/sonarr/", "api/v3/x", "http://h:8989/sonarr/api/v3/x"},
		{"http://h:8989", "/api", "http://h:8989/api"},
		{"http://h:8989///", "///api", "http://h:8989/api"},
	}
	for _, c := range cases {
		if got := JoinURL(c[0], c[1]); got != c[2] {
			t.Errorf("JoinURL(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func newClient(t *testing.T, opts HTTPOptions) *HTTPClient {
	t.Helper()
	c, err := NewHTTPClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func asProviderError(t *testing.T, err error) *ProviderError {
	t.Helper()
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("not a ProviderError: %v", err)
	}
	return pe
}

type recorded struct {
	url    string
	header http.Header
	method string
	body   string
}

func TestHTTPClientSendsPrefixQueryUserAgentAndHeaders(t *testing.T) {
	var mu sync.Mutex
	var last recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = recorded{url: r.URL.String(), header: r.Header.Clone()}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	c := newClient(t, HTTPOptions{BaseURL: srv.URL + "/sonarr", Label: "sonarr", Headers: http.Header{"X-Api-Key": {"k-1234"}}})
	var out struct{ Hello string }
	err := c.JSON(context.Background(), "/ok", Request{Query: url.Values{"a": {"1"}}}, &out)
	if err != nil || out.Hello != "world" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if last.url != "/sonarr/ok?a=1" {
		t.Errorf("url = %q", last.url)
	}
	if last.header.Get("X-Api-Key") != "k-1234" {
		t.Errorf("headers = %v", last.header)
	}
	if !strings.HasPrefix(last.header.Get("User-Agent"), "Wheelhouse/") || len(last.header.Get("User-Agent")) < 12 {
		t.Errorf("user agent = %q", last.header.Get("User-Agent"))
	}
}

func TestHTTPClientMergesQueryAndPerRequestHeadersOverride(t *testing.T) {
	var mu sync.Mutex
	var last recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		mu.Lock()
		last = recorded{url: r.URL.String(), header: r.Header.Clone(), method: r.Method, body: body.String()}
		mu.Unlock()
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x", Headers: http.Header{"X-Token": {"base"}}})
	_, err := c.Do(context.Background(), "/api?mode=queue&n=1", Request{
		Method: "post", Query: url.Values{"n": {"2"}}, Header: http.Header{"x-token": {"override"}}, Body: []byte("payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if last.url != "/api?mode=queue&n=2" || last.method != "POST" || last.body != "payload" || last.header.Get("X-Token") != "override" {
		t.Fatalf("got %+v", last)
	}
}

func TestHTTPClientErrorCarriesStatusButNotURLOrKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "sab", Secrets: []string{"topsecretkey"}})
	_, err := c.Do(context.Background(), "/denied?apikey=topsecretkey", Request{Query: url.Values{"apikey": {"topsecretkey"}}})
	pe := asProviderError(t, err)
	if pe.Status != 401 || pe.Message != "sab /denied failed: 401 Unauthorized" {
		t.Fatalf("got %+v", pe)
	}
	if strings.Contains(err.Error(), "topsecretkey") {
		t.Fatal("key leaked")
	}
}

func TestHTTPClientRedactsSecretsInTheLabelAndPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "svc", Secrets: []string{"pathsecret1"}})
	_, err := c.Do(context.Background(), "/api/pathsecret1/list", Request{})
	if err == nil || strings.Contains(err.Error(), "pathsecret1") {
		t.Fatalf("err = %v", err)
	}
}

func slowServer(t *testing.T, d time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(d):
			_, _ = w.Write([]byte("late"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPClientTimesOutInsteadOfHanging(t *testing.T) {
	srv := slowServer(t, 1500*time.Millisecond)
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "slowsvc", Timeout: 150 * time.Millisecond})
	_, err := c.Do(context.Background(), "/slow", Request{})
	if err == nil || err.Error() != "slowsvc: timed out after 150ms" {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPClientPerCallTimeoutOverride(t *testing.T) {
	srv := slowServer(t, 1500*time.Millisecond)
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x", Timeout: 5 * time.Second})
	start := time.Now()
	_, err := c.Do(context.Background(), "/slow", Request{Timeout: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timed out after 100ms") || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestHTTPClientDefaultTimeoutIsEightSeconds(t *testing.T) {
	c := newClient(t, HTTPOptions{BaseURL: "http://192.0.2.1", Label: "x"})
	if c.timeout != 8*time.Second || DefaultTimeout != 8*time.Second {
		t.Fatalf("timeout = %v", c.timeout)
	}
}

// The timeout covers reading the body too, not only the headers.
func TestHTTPClientTimeoutCoversTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "stall", Timeout: 150 * time.Millisecond})
	_, err := c.Do(context.Background(), "/x", Request{})
	if err == nil || err.Error() != "stall: timed out after 150ms" {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPClientReportsACanceledCallerPlainly(t *testing.T) {
	srv := slowServer(t, time.Second)
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x"})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := c.Do(ctx, "/slow", Request{})
	if err == nil || err.Error() != "x: request canceled" {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPClientReportsARefusedConnectionPlainly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // now nothing is listening there

	c := newClient(t, HTTPOptions{BaseURL: "http://" + addr, Label: "down"})
	_, err = c.Do(context.Background(), "/x", Request{})
	if err == nil || err.Error() != "down: connection refused" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), addr) {
		t.Fatal("address leaked")
	}
}

func TestHTTPClientReportsUnknownHostPlainly(t *testing.T) {
	c := newClient(t, HTTPOptions{BaseURL: "http://nonexistent.invalid", Label: "ghost", Timeout: 5 * time.Second})
	_, err := c.Do(context.Background(), "/x", Request{})
	if err == nil || err.Error() != "ghost: host not found" {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPClientReportsInvalidJSONAsAProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>" + canary))
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "bad"})
	var out map[string]any
	err := c.JSON(context.Background(), "/notjson?x=1", Request{}, &out)
	_ = asProviderError(t, err)
	if err.Error() != "bad /notjson returned invalid JSON" {
		t.Fatalf("err = %v", err)
	}
}

func TestNewHTTPClientRejectsBadBaseURLsWithoutEchoingThem(t *testing.T) {
	for _, base := range []string{"", "not a url", "ftp://h", "http://", "://x", "http://" + canary + ":%zz@h"} {
		_, err := NewHTTPClient(HTTPOptions{BaseURL: base, Label: "svc"})
		if err == nil || err.Error() != "svc: invalid base URL" {
			t.Errorf("%q: err = %v", base, err)
		}
	}
}

// Credentials embedded in the base URL are secrets too.
func TestHTTPClientTreatsBaseURLCredentialsAsSecrets(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer srv.Close()
	base := strings.Replace(srv.URL, "http://", "http://admin:"+canary+"@", 1)
	c := newClient(t, HTTPOptions{BaseURL: base, Label: "svc " + canary})
	_, err := c.Do(context.Background(), "/x", Request{})
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("err = %v", err)
	}
	if auth, _ := gotAuth.Load().(string); !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("expected basic auth from the URL, got %q", auth)
	}
}

// ---- response size limit ----

func TestHTTPClientRejectsOversizedResponses(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 10_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" { // no Content-Length: the limit must hold anyway
			f := w.(http.Flusher)
			for range 50 {
				_, _ = w.Write(big[:1000])
				f.Flush()
			}
			return
		}
		_, _ = w.Write(big) // Content-Length is known
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "big", MaxResponseBytes: 4096})
	for _, path := range []string{"/known", "/chunked"} {
		res, err := c.Do(context.Background(), path, Request{})
		if res != nil || err == nil || err.Error() != fmt.Sprintf("big %s: response larger than 4096 bytes", path) {
			t.Errorf("%s: res=%v err=%v", path, res, err)
		}
		_ = asProviderError(t, err)
	}
}

// The limit must bound what is read, not only what is returned: an endless
// stream is cut off after about the limit instead of being buffered.
func TestHTTPClientStopsReadingAnEndlessStream(t *testing.T) {
	const limit = 1 << 20
	chunk := bytes.Repeat([]byte("e"), 32<<10)
	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for written.Load() < 512<<20 { // far more than the client may keep
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "endless", MaxResponseBytes: limit, Timeout: 20 * time.Second})
	if _, err := c.Do(context.Background(), "/", Request{}); err == nil || !strings.Contains(err.Error(), "response larger than") {
		t.Fatalf("err = %v", err)
	}
	// Socket buffers hold a few MiB beyond what the client read; 128 MiB would
	// mean the client drained the stream.
	time.Sleep(50 * time.Millisecond)
	if got := written.Load(); got > 64<<20 {
		t.Fatalf("server wrote %d bytes: the client did not stop reading", got)
	}
}

func TestHTTPClientAllowsExactlyTheLimitAndPerCallOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("y"), 100))
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x", MaxResponseBytes: 100})
	if res, err := c.Do(context.Background(), "/", Request{}); err != nil || len(res.Body) != 100 {
		t.Fatalf("at the limit: %v", err)
	}
	if _, err := c.Do(context.Background(), "/", Request{MaxBytes: 99}); err == nil {
		t.Fatal("per-call limit not applied")
	}
	if c.maxBytes != 100 || DefaultMaxResponseBytes != 8<<20 {
		t.Fatalf("defaults wrong: %d", c.maxBytes)
	}
}

func TestHTTPClientDoesNotCacheOversizedResponses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("z"), 500))
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x", MaxResponseBytes: 100})
	for range 2 {
		if _, err := c.Do(context.Background(), "/", Request{CacheTTL: time.Minute}); err == nil {
			t.Fatal("expected error")
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d", hits.Load())
	}
}

// ---- redirects ----

func TestHTTPClientRefusesRedirectsToAnotherHost(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { otherHits.Add(1) }))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer origin.Close()
	c := newClient(t, HTTPOptions{BaseURL: origin.URL, Label: "svc"})
	if _, err := c.Do(context.Background(), "/", Request{}); err == nil || err.Error() != "svc: redirected to another host (not followed)" {
		t.Fatalf("err = %v", err)
	}
	if otherHits.Load() != 0 {
		t.Fatal("the other host was contacted")
	}
}

func TestHTTPClientFollowsRedirectsWithinTheSameHost(t *testing.T) {
	var gotKey atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/sonarr/new", http.StatusMovedPermanently)
			return
		}
		gotKey.Store(r.Header.Get("X-Api-Key"))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "svc", Headers: http.Header{"X-Api-Key": {"same-host-key"}}})
	res, err := c.Do(context.Background(), "/old", Request{})
	if err != nil || string(res.Body) != "ok" || gotKey.Load() != "same-host-key" {
		t.Fatalf("res=%v err=%v key=%v", res, err, gotKey.Load())
	}
}

func TestHTTPClientStopsRedirectLoops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "loop"})
	if _, err := c.Do(context.Background(), "/", Request{}); err == nil || err.Error() != "loop: request failed" {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRedirectRefusesHTTPSDowngrade(t *testing.T) {
	first, _ := http.NewRequest("GET", "https://svc.example.com/a", nil)
	down, _ := http.NewRequest("GET", "http://svc.example.com/a", nil)
	if err := checkRedirect(down, []*http.Request{first}); !errors.Is(err, errRedirectHost) {
		t.Fatalf("downgrade allowed: %v", err)
	}
	up, _ := http.NewRequest("GET", "https://SVC.example.com:8443/a", nil)
	plain, _ := http.NewRequest("GET", "http://svc.example.com/a", nil)
	if err := checkRedirect(up, []*http.Request{plain}); err != nil {
		t.Fatalf("upgrade refused: %v", err)
	}
}

// ---- TLS ----

func TestHTTPClientVerifiesTLSCertificates(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the handshake failure is expected
	srv.StartTLS()
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "tls"})
	_, err := c.Do(context.Background(), "/", Request{})
	if err == nil || err.Error() != "tls: TLS certificate could not be verified" {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("request reached the server despite the bad certificate")
	}
	if transport().TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify is on")
	}
}

// ---- logging ----

func TestHTTPClientLogsFailuresWithoutSecretsOrQueries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "echo "+canary, http.StatusForbidden)
	}))
	defer srv.Close()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "sab", Secrets: []string{canary}, Logger: logger})
	_, _ = c.Do(context.Background(), "/api?apikey="+canary, Request{Query: url.Values{"apikey": {canary}}})
	line := buf.String()
	if !strings.Contains(line, "upstream request failed") || !strings.Contains(line, "403") {
		t.Fatalf("log = %q", line)
	}
	if strings.Contains(line, canary) || strings.Contains(line, "apikey") {
		t.Fatalf("log leaks: %q", line)
	}
}

// ---- response cache ----

type countingServer struct {
	*httptest.Server
	mu    sync.Mutex
	hits  map[string]int
	delay time.Duration
}

func (s *countingServer) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	s := &countingServer{hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		n := s.hits[r.URL.Path]
		delay := s.delay
		s.mu.Unlock()
		time.Sleep(delay)
		if r.URL.Path == "/flaky" && n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(counted{Path: r.URL.Path, N: n})
	}))
	t.Cleanup(s.Close)
	return s
}

type counted struct {
	Path string `json:"path"`
	N    int    `json:"n"`
}

func getJSON(t *testing.T, c *HTTPClient, path string, req Request) counted {
	t.Helper()
	var out counted
	if err := c.JSON(context.Background(), path, req, &out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return out
}

const ttl = 5 * time.Second

func TestCacheIsOffUnlessAskedFor(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	getJSON(t, c, "/list", Request{})
	getJSON(t, c, "/list", Request{})
	if s.hitCount("/list") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/list"))
	}
}

func TestCacheAnswersInsideTheTTLWithoutGoingUpstream(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	first := getJSON(t, c, "/list", Request{CacheTTL: ttl})
	second := getJSON(t, c, "/list", Request{CacheTTL: ttl})
	if first != second || s.hitCount("/list") != 1 {
		t.Fatalf("first=%v second=%v hits=%d", first, second, s.hitCount("/list"))
	}
}

func TestCacheGoesUpstreamAgainAfterTheTTL(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	getJSON(t, c, "/list", Request{CacheTTL: 40 * time.Millisecond})
	time.Sleep(80 * time.Millisecond)
	getJSON(t, c, "/list", Request{CacheTTL: 40 * time.Millisecond})
	if s.hitCount("/list") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/list"))
	}
}

func TestCacheKeysOnPathAndQuery(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	getJSON(t, c, "/list", Request{CacheTTL: ttl, Query: url.Values{"page": {"1"}}})
	getJSON(t, c, "/list", Request{CacheTTL: ttl, Query: url.Values{"page": {"2"}}})
	getJSON(t, c, "/other", Request{CacheTTL: ttl})
	getJSON(t, c, "/list", Request{CacheTTL: ttl, Query: url.Values{"page": {"1"}}})
	if s.hitCount("/list") != 2 || s.hitCount("/other") != 1 {
		t.Fatalf("hits = %v", s.hits)
	}
}

func TestCacheSharesOneUpstreamCallBetweenConcurrentRequests(t *testing.T) {
	s := newCountingServer(t)
	s.delay = 60 * time.Millisecond
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	results := make([]counted, 3)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = getJSON(t, c, "/list", Request{CacheTTL: ttl})
		}()
	}
	wg.Wait()
	if s.hitCount("/list") != 1 || results[1] != results[0] || results[2] != results[0] {
		t.Fatalf("hits=%d results=%v", s.hitCount("/list"), results)
	}
}

func TestCacheHandsEachCallerItsOwnCopy(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	first, err := c.Do(context.Background(), "/list", Request{CacheTTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	first.Body[0] = 'X'
	first.Header.Set("Content-Type", "mutated")
	second, _ := c.Do(context.Background(), "/list", Request{CacheTTL: ttl})
	if second.Body[0] != '{' || second.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("cache was affected by a caller: %q %v", second.Body, second.Header)
	}
}

func TestCacheNeverCachesErrorsAndAFailureDoesNotPoisonTheNextCall(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	var out counted
	err := c.JSON(context.Background(), "/flaky", Request{CacheTTL: ttl}, &out)
	if err == nil || !strings.Contains(err.Error(), "failed: 500") {
		t.Fatalf("err = %v", err)
	}
	if got := getJSON(t, c, "/flaky", Request{CacheTTL: ttl}); got.N != 2 {
		t.Fatalf("got %+v", got)
	}
	if got := getJSON(t, c, "/flaky", Request{CacheTTL: ttl}); got.N != 2 {
		t.Fatalf("got %+v", got)
	}
	if s.hitCount("/flaky") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/flaky"))
	}
}

func TestCacheSharesAFailureBetweenConcurrentCallersButRetriesAfterwards(t *testing.T) {
	s := newCountingServer(t)
	s.delay = 40 * time.Millisecond
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.JSON(context.Background(), "/flaky", Request{CacheTTL: ttl}, &counted{})
		}()
	}
	wg.Wait()
	if errs[0] == nil || errs[1] == nil || s.hitCount("/flaky") != 1 {
		t.Fatalf("errs=%v hits=%d", errs, s.hitCount("/flaky"))
	}
	if got := getJSON(t, c, "/flaky", Request{CacheTTL: ttl}); got.N != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestInvalidateMakesTheNextReadGoUpstream(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	getJSON(t, c, "/list", Request{CacheTTL: ttl})
	c.Invalidate()
	getJSON(t, c, "/list", Request{CacheTTL: ttl})
	if s.hitCount("/list") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/list"))
	}
}

func TestAReadInFlightDuringAWriteDoesNotRepopulateTheCache(t *testing.T) {
	s := newCountingServer(t)
	s.delay = 60 * time.Millisecond
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		getJSON(t, c, "/list", Request{CacheTTL: ttl})
	}()
	time.Sleep(10 * time.Millisecond)
	c.Invalidate() // a write completed while the read was still pending
	<-done
	s.mu.Lock()
	s.delay = 0
	s.mu.Unlock()
	getJSON(t, c, "/list", Request{CacheTTL: ttl})
	if s.hitCount("/list") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/list"))
	}
}

func TestCacheOnlyAppliesToGET(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	for range 2 {
		if _, err := c.Do(context.Background(), "/list", Request{Method: "POST", CacheTTL: ttl}); err != nil {
			t.Fatal(err)
		}
	}
	if s.hitCount("/list") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/list"))
	}
}

func TestCachesOfDifferentClientsAreApart(t *testing.T) {
	s := newCountingServer(t)
	for range 2 {
		getJSON(t, newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"}), "/list", Request{CacheTTL: ttl})
	}
	if s.hitCount("/list") != 2 {
		t.Fatalf("hits = %d", s.hitCount("/list"))
	}
}

// A caller that gives up must not take the shared upstream call down with it.
func TestCacheSurvivesTheFirstCallerCancelling(t *testing.T) {
	s := newCountingServer(t)
	s.delay = 100 * time.Millisecond
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := c.Do(ctx, "/list", Request{CacheTTL: ttl})
		first <- err
	}()
	time.Sleep(20 * time.Millisecond)
	second := make(chan counted, 1)
	go func() { second <- getJSON(t, c, "/list", Request{CacheTTL: ttl}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-first; err == nil || err.Error() != "cached: request canceled" {
		t.Fatalf("first err = %v", err)
	}
	if got := <-second; got.N != 1 || s.hitCount("/list") != 1 {
		t.Fatalf("second = %+v hits=%d", got, s.hitCount("/list"))
	}
}

func TestPruneDropsExpiredEntries(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	for i := range 5 {
		getJSON(t, c, fmt.Sprintf("/p%d", i), Request{CacheTTL: time.Millisecond})
	}
	time.Sleep(10 * time.Millisecond)
	getJSON(t, c, "/last", Request{CacheTTL: ttl})
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) != 1 {
		t.Fatalf("cache has %d entries", len(c.cache))
	}
}

// Run with -race.
func TestHTTPClientIsSafeForConcurrentUse(t *testing.T) {
	s := newCountingServer(t)
	c := newClient(t, HTTPOptions{BaseURL: s.URL, Label: "cached"})
	var wg sync.WaitGroup
	for g := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				path := fmt.Sprintf("/k%d", i%4)
				switch {
				case g == 0 && i%5 == 0:
					c.Invalidate()
				case i%2 == 0:
					getJSON(t, c, path, Request{CacheTTL: 20 * time.Millisecond})
				default:
					getJSON(t, c, path, Request{})
				}
			}
		}()
	}
	wg.Wait()
}
