package hostiletest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Tamper decides which upstream answer a TamperProxy corrupts. The proxies of
// one run share a Tamper, so the n-th request of the whole run (across every
// upstream) is the one that is changed and all the others come back exactly as
// the real fake service sent them. That keeps a fuzz test on the deep paths of an
// adapter: it gets a valid conversation with one hostile answer in it.
type Tamper struct {
	mu     sync.Mutex
	seen   int
	at     int
	status int
	body   []byte
	hit    bool
}

// Statuses are the HTTP statuses a fuzzed status byte selects.
var Statuses = []int{200, 200, 200, 201, 204, 301, 302, 400, 401, 403, 404, 429, 500, 502, 503}

// Reset arms the Tamper: request number at (counting from 0) is answered with
// body and with the status Statuses[statusSel % len] (200 keeps the real
// status).
func (t *Tamper) Reset(at int, statusSel uint8, body []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen, t.at, t.body, t.hit = 0, at, append([]byte(nil), body...), false
	t.status = Statuses[int(statusSel)%len(Statuses)]
}

// Hit reports whether the armed request was reached.
func (t *Tamper) Hit() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hit
}

// Requests is how many requests the proxies have seen since Reset.
func (t *Tamper) Requests() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seen
}

func (t *Tamper) take() (status int, body []byte, mutate bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.seen
	t.seen++
	if n != t.at {
		return 0, nil, false
	}
	t.hit = true
	return t.status, t.body, true
}

// Upstream is the address a proxy forwards to; it can be changed while the proxy
// runs, so a fuzz test can start a fresh fake service (one whose state the last
// iteration did not change) for every input and keep the proxy and the
// configuration that points at it.
type Upstream struct {
	mu  sync.Mutex
	url string
}

// Set points the proxy at a new upstream base URL.
func (u *Upstream) Set(url string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.url = url
}

func (u *Upstream) get() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.url
}

// Proxy starts a reverse proxy in front of upstream (the base URL of a fake
// service) and returns its base URL. Every request is forwarded and answered as
// the upstream answered, except the one the Tamper is armed for, whose body (and
// status, unless 200) is replaced.
func Proxy(tb testing.TB, upstream string, t *Tamper) string {
	tb.Helper()
	u := &Upstream{url: upstream}
	return ProxyTo(tb, u, t)
}

// ProxyTo is Proxy for an upstream whose address can change (see Upstream).
func ProxyTo(tb testing.TB, up *Upstream, t *Tamper) string {
	tb.Helper()
	client := &http.Client{
		Transport:     &http.Transport{},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := http.NewRequestWithContext(r.Context(), r.Method, up.get()+r.URL.RequestURI(), r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		out.Header = r.Header.Clone()
		res, err := client.Do(out)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		status, body, mutate := t.take()
		for k, vs := range res.Header {
			switch k {
			case "Content-Length", "Transfer-Encoding", "Connection":
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		code := res.StatusCode
		if mutate {
			raw = body
			if status != 200 {
				code = status
			}
		}
		if code == http.StatusNoContent || code == http.StatusNotModified {
			raw = nil
		}
		w.WriteHeader(code)
		_, _ = w.Write(raw)
	}))
	tb.Cleanup(srv.Close)
	return srv.URL
}
