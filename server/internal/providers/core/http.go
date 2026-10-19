package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultTimeout bounds one upstream request, response body included.
	DefaultTimeout = 8 * time.Second
	// DefaultMaxResponseBytes bounds how much of an upstream response is read, so
	// a hostile or broken service cannot exhaust memory. Endpoints that return a
	// whole library set their own larger cap (LargeListBytes); small ones a lower
	// one (SmallResponseBytes). The cap bounds bytes, not what they decode to:
	// see DecodeJSON.
	DefaultMaxResponseBytes int64 = 8 << 20

	maxRedirects = 10
)

// UserAgentVersion is the version in the User-Agent header ("Wheelhouse/<version>").
// The release build can set it with -ldflags "-X .../core.UserAgentVersion=0.2.0".
var UserAgentVersion = "dev"

// ProviderError is an error that is safe to show to users and write to logs:
// secrets are already stripped. Status is the upstream HTTP status, or 0 when
// the failure was not an HTTP status (network error, timeout, bad config).
type ProviderError struct {
	Message string
	Status  int
}

func (e *ProviderError) Error() string { return e.Message }

func providerErrorf(status int, format string, args ...any) *ProviderError {
	return &ProviderError{Message: fmt.Sprintf(format, args...), Status: status}
}

// HTTPOptions configures an HTTPClient.
type HTTPOptions struct {
	// BaseURL is the instance URL; it may include a path prefix
	// ("http://host:8989/sonarr"). Credentials in it ("user:pass@") are added to
	// the secrets to redact.
	BaseURL string
	// Label prefixes error messages: "sonarr", "radarr-4k".
	Label   string
	Headers http.Header
	// Secrets are values to strip from any error message.
	Secrets []string
	// Timeout defaults to DefaultTimeout.
	Timeout time.Duration
	// MaxResponseBytes defaults to DefaultMaxResponseBytes. When set it is a
	// ceiling for the whole client: a call may lower it with Request.MaxBytes but
	// not raise it. When unset, Request.MaxBytes may raise it per call (library
	// lists).
	MaxResponseBytes int64
	// Logger, if set, gets one Debug line per failed request: label, path without
	// query, and the redacted error. Never a URL query, header or body.
	Logger *slog.Logger
}

// Request describes one call. The zero value is a GET.
type Request struct {
	Method string
	Query  url.Values
	Header http.Header
	Body   []byte
	// Timeout overrides the client's default for this call.
	Timeout time.Duration
	// MaxBytes overrides the client's response size limit for this call.
	MaxBytes int64
	// Limits bound the shape of the JSON that JSON, DecodeJSON and the adapters
	// decode (zero fields: the defaults of JSONLimits).
	Limits JSONLimits
	// CacheTTL opts in to caching this GET response for this long. Concurrent
	// identical calls share one upstream request; errors are never cached; writes
	// should call Invalidate. Ignored for anything but GET.
	CacheTTL time.Duration
}

// Response is a fully read upstream response (the body is bounded). Each caller
// gets its own copy, so mutating one never affects another or the cache.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r *Response) clone() *Response {
	return &Response{Status: r.Status, Header: r.Header.Clone(), Body: bytes.Clone(r.Body)}
}

// flight is one upstream call that callers can share, and later a cached result.
type flight struct {
	done     chan struct{} // closed when res and err are set
	res      *Response
	err      error
	finished bool      // guarded by HTTPClient.mu
	expires  time.Time // guarded by HTTPClient.mu; set on success
}

// HTTPClient talks to one upstream instance. It is safe for concurrent use.
//
// Security properties: TLS verification is on and there is deliberately no
// switch to turn it off (the TypeScript version has none either; its
// allowInsecureTls option was deferred and never built). Redirects are followed
// only within the same host and never from https to http. Response bodies are
// size-limited and never appear in errors or logs.
type HTTPClient struct {
	base     string
	label    string
	headers  http.Header
	redactor *Redactor
	timeout  time.Duration
	maxBytes int64
	capSet   bool // MaxResponseBytes was set: a ceiling no per-call MaxBytes may raise
	logger   *slog.Logger
	hc       *http.Client

	mu    sync.Mutex
	cache map[string]*flight // keyed by the full request URL
}

var (
	transportOnce   sync.Once
	sharedTransport *http.Transport
)

// transport is shared by every client. No proxy from the environment (Node's
// fetch does not use one either), TLS verification on.
func transport() *http.Transport {
	transportOnce.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		// Refuse denied networks (link-local, cloud metadata) at connect time,
		// after name resolution (see netpolicy.go).
		t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: controlDial}).DialContext
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		sharedTransport = t
	})
	return sharedTransport
}

var errRedirectHost = errors.New("redirect to another host")

// checkRedirect lets a request follow redirects within the same host name
// (http to https included, and a port change) but not to another host, and never
// from https down to http. Stopping here also keeps API-key headers from being
// replayed to a host we were not configured for.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	first := via[0].URL
	if !strings.EqualFold(req.URL.Hostname(), first.Hostname()) ||
		(first.Scheme == "https" && req.URL.Scheme != "https") {
		return errRedirectHost
	}
	return nil
}

// NewHTTPClient validates the base URL. The error never contains the URL.
func NewHTTPClient(opts HTTPOptions) (*HTTPClient, error) {
	secrets := append([]string(nil), opts.Secrets...)
	u, err := url.Parse(opts.BaseURL)
	if err == nil && (u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "") {
		err = errors.New("not an http or https URL")
	}
	if err != nil {
		return nil, providerErrorf(0, "%s: invalid base URL", Redact(opts.Label, secrets...))
	}
	if u.User != nil {
		pw, _ := u.User.Password()
		secrets = append(secrets, u.User.Username(), pw,
			base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pw)))
	}
	c := &HTTPClient{
		base:     opts.BaseURL,
		label:    opts.Label,
		headers:  opts.Headers.Clone(),
		redactor: NewRedactor(secrets),
		timeout:  opts.Timeout,
		maxBytes: opts.MaxResponseBytes,
		logger:   opts.Logger,
		hc:       &http.Client{Transport: transport(), CheckRedirect: checkRedirect},
		cache:    map[string]*flight{},
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	c.capSet = c.maxBytes > 0
	if c.maxBytes <= 0 {
		c.maxBytes = DefaultMaxResponseBytes
	}
	return c, nil
}

// JoinURL joins a base URL (with an optional path prefix) and a path without
// dropping the prefix.
func JoinURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

// fullURL builds the request URL. Query values are set (replacing any with the
// same name already in path); with no extra query the path's own is untouched.
func (c *HTTPClient) fullURL(path string, query url.Values) (string, error) {
	u, err := url.Parse(JoinURL(c.base, path))
	if err != nil {
		return "", err
	}
	if len(query) > 0 {
		q := u.Query()
		for name, values := range query {
			q[name] = values
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// Do performs the call. It returns a ProviderError for network errors, timeouts,
// oversized responses and non-2xx statuses (with Status set for the latter).
func (c *HTTPClient) Do(ctx context.Context, path string, req Request) (*Response, error) {
	res, err := c.do(ctx, path, req)
	if err != nil && c.logger != nil {
		c.logger.Debug("upstream request failed",
			"label", c.label, "path", c.redactor.Redact(pathOnly(path)), "error", err.Error())
	}
	return res, err
}

// JSON performs the call and decodes the response body into out.
func (c *HTTPClient) JSON(ctx context.Context, path string, req Request, out any) error {
	res, err := c.Do(ctx, path, req)
	if err != nil {
		return err
	}
	if err := DecodeJSON(ctx, res.Body, out, req.Limits); err != nil {
		// Not err.Error(): decoder errors can quote parts of the body.
		return c.errorf(0, "%s %s %s", c.label, pathOnly(path), InvalidJSONText(err))
	}
	return nil
}

// Invalidate drops every cached response of this client (call after add,
// delete, rescan). Requests already in flight finish but are not cached.
func (c *HTTPClient) Invalidate() {
	c.mu.Lock()
	clear(c.cache)
	c.mu.Unlock()
}

func (c *HTTPClient) do(ctx context.Context, path string, req Request) (*Response, error) {
	if req.CacheTTL <= 0 || (req.Method != "" && !strings.EqualFold(req.Method, http.MethodGet)) {
		return c.request(ctx, path, req)
	}
	key, err := c.fullURL(path, req.Query)
	if err != nil {
		return nil, c.errorf(0, "%s: invalid request path", c.label)
	}

	now := time.Now()
	c.mu.Lock()
	f := c.cache[key]
	if f != nil && f.finished && !now.Before(f.expires) {
		f = nil // expired
	}
	if f == nil {
		c.pruneLocked(now)
		f = &flight{done: make(chan struct{})}
		c.cache[key] = f
		// The shared call must not die with the first caller's context.
		go c.run(context.WithoutCancel(ctx), key, f, path, req)
	}
	c.mu.Unlock()

	select {
	case <-f.done:
	case <-ctx.Done():
		return nil, c.errorf(0, "%s: request canceled", c.label)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.res.clone(), nil
}

func (c *HTTPClient) run(ctx context.Context, key string, f *flight, path string, req Request) {
	res, err := c.request(ctx, path, req)
	c.mu.Lock()
	f.res, f.err, f.finished = res, err, true
	// Only keep it if a write has not cleared (or replaced) the entry meanwhile.
	if c.cache[key] == f {
		if err != nil {
			delete(c.cache, key)
		} else {
			f.expires = time.Now().Add(req.CacheTTL)
		}
	}
	c.mu.Unlock()
	close(f.done)
}

// pruneLocked drops expired entries so the map does not grow without bound.
func (c *HTTPClient) pruneLocked(now time.Time) {
	for k, f := range c.cache {
		if f.finished && !now.Before(f.expires) {
			delete(c.cache, k)
		}
	}
}

func (c *HTTPClient) request(parent context.Context, path string, r Request) (*Response, error) {
	target, err := c.fullURL(path, r.Query)
	if err != nil {
		return nil, c.errorf(0, "%s: invalid request path", c.label)
	}
	timeout := c.timeout
	if r.Timeout > 0 {
		timeout = r.Timeout
	}
	limit := c.maxBytes
	if r.MaxBytes > 0 && (!c.capSet || r.MaxBytes < c.maxBytes) {
		limit = r.MaxBytes
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, c.errorf(0, "%s: invalid request", c.label)
	}
	req.Header.Set("User-Agent", "Wheelhouse/"+UserAgentVersion)
	for _, h := range []http.Header{c.headers, r.Header} {
		for name, values := range h {
			req.Header.Del(name)
			for _, v := range values {
				req.Header.Add(name, v)
			}
		}
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, c.networkError(err, parent, timeout)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Not the reason phrase and never the body: a hostile server controls both,
		// and the URL can carry a key in its query string.
		return nil, c.errorf(resp.StatusCode, "%s %s failed: %d %s",
			c.label, pathOnly(path), resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if resp.ContentLength > limit {
		return nil, c.tooLarge(path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, c.networkError(err, parent, timeout)
	}
	if int64(len(data)) > limit {
		return nil, c.tooLarge(path, limit)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
}

func (c *HTTPClient) tooLarge(path string, limit int64) error {
	return c.errorf(0, "%s %s: response larger than %d bytes", c.label, pathOnly(path), limit)
}

// errorf builds a redacted ProviderError.
func (c *HTTPClient) errorf(status int, format string, args ...any) error {
	return &ProviderError{Message: c.redactor.Redact(fmt.Sprintf(format, args...)), Status: status}
}

func (c *HTTPClient) networkError(err error, parent context.Context, timeout time.Duration) error {
	detail := describeNetworkError(err)
	switch {
	case parent.Err() != nil:
		detail = "request canceled"
	case detail == "timed out":
		detail = fmt.Sprintf("timed out after %dms", timeout.Milliseconds())
	}
	return c.errorf(0, "%s: %s", c.label, detail)
}

// describeNetworkError turns a transport error into a short plain reason. It
// never uses err.Error(): that text contains the full URL, query string
// included.
func describeNetworkError(err error) string {
	var (
		dns       *net.DNSError
		unknownCA x509.UnknownAuthorityError
		invalid   x509.CertificateInvalidError
		hostname  x509.HostnameError
		netErr    net.Error
	)
	switch {
	case errors.Is(err, errRedirectHost):
		return "redirected to another host (not followed)"
	case errors.Is(err, ErrAddressDenied):
		return "address not allowed"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "request canceled"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "connection reset"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return "host unreachable"
	case errors.Is(err, syscall.ENETUNREACH):
		return "network unreachable"
	case errors.Is(err, syscall.ETIMEDOUT):
		return "connection timed out"
	case errors.As(err, &dns):
		return "host not found"
	case errors.As(err, &unknownCA):
		return "TLS certificate could not be verified"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "TLS certificate has expired"
		}
		return "TLS certificate could not be verified"
	case errors.As(err, &hostname):
		return "TLS certificate does not match the host"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	}
	return "request failed"
}

// pathOnly drops the query string: it can carry a key.
func pathOnly(path string) string {
	p, _, _ := strings.Cut(path, "?")
	return p
}
