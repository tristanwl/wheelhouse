package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/converter"
	"github.com/tristanwl/wheelhouse/server/internal/instances"
	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
	"github.com/tristanwl/wheelhouse/server/internal/providers/all"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent/fakeqbittorrent"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd/fakesabnzbd"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/jellyfin/jellyfintest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex/plextest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
	"github.com/tristanwl/wheelhouse/server/internal/ui"
)

// The HTTP surface fuzz test drives the whole router (all middleware except
// panic recovery, so a panic fails the test instead of becoming a 500) with
// arbitrary requests against a prepared app: every provider configured against
// its fake service (one more, stored in the database, with a planted canary key),
// the converter pointed at a scratch media folder with fake ffmpeg tools, and
// three kinds of caller (nobody, an administrator, a user) in several states of
// credential. The expectations are derived from the router table
// (Router.Routes and the mux's own match), not from lists of paths.

// httpFixture is one prepared app. It is rebuilt after a request that changed
// anything (a 2xx answer to a state-changing method) or killed a session.
type httpFixture struct {
	h      http.Handler
	rt     *Router
	levels map[string]Level
	svc    *auth.Service
	logs   *syncBuffer

	adminCookie, adminCSRF string
	userCookie, userCSRF   string
	adminName              string

	dir    string
	root   string
	db     *store.DB
	cancel context.CancelFunc
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) take() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	b.buf.Reset()
	return s
}

const (
	fuzzAdminName = "fuzz-admin"
	fuzzUserName  = "fuzz-user"
	fuzzAdminPass = "correct-battery-staple-1"
	fuzzUserPass  = "another-long-passphrase-2"
)

// fuzzUpstreams are the fake services, started once for the whole fuzz run.
type fuzzUpstreams struct {
	env map[string]string
}

func startUpstreams(tb testing.TB) *fuzzUpstreams {
	radarr := mmtest.StartRadarr(tb)
	sonarr := mmtest.StartSonarr(tb)
	sab := fakesabnzbd.Start(tb, fakesabnzbd.Active)
	qbit := fakeqbittorrent.Start(tb, fakeqbittorrent.V5, fakeqbittorrent.Active)
	plex := plextest.Start(tb, plextest.Scenario{Claimed: true, Active: true})
	jf := jellyfintest.Start(tb, jellyfintest.Scenario{SetupCompleted: true})
	tm := tmdbtest.Start(tb, tmdbtest.Options{})
	return &fuzzUpstreams{env: map[string]string{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"SONARR_URL": sonarr.URL, "SONARR_API_KEY": mmtest.GoodKey,
		"SABNZBD_URL": sab.URL, "SABNZBD_API_KEY": fakesabnzbd.GoodKey,
		"QBITTORRENT_URL": qbit.URL, "QBITTORRENT_USERNAME": fakeqbittorrent.GoodUser, "QBITTORRENT_PASSWORD": fakeqbittorrent.GoodPassword,
		"PLEX_URL": plex.URL, "PLEX_TOKEN": plextest.GoodToken,
		"JELLYFIN_URL": jf.URL, "JELLYFIN_API_KEY": jellyfintest.GoodKey,
		"TMDB_API_KEY": tmdbtest.GoodKey, "TMDB_BASE_URL": tm.APIRoot(),
		// A second Radarr (Radarr 4K) whose key is a planted canary the fake rejects.
		"RADARR_4K_URL": radarr.URL, "RADARR_4K_API_KEY": hostiletest.Key,
	}}
}

func (u *fuzzUpstreams) secrets() []string {
	var out []string
	for k, v := range u.env {
		if strings.HasSuffix(k, "_KEY") || strings.HasSuffix(k, "_TOKEN") || strings.HasSuffix(k, "_PASSWORD") {
			out = append(out, v)
		}
	}
	return out
}

// newHTTPFixture builds a fresh app in its own directory.
func newHTTPFixture(up *fuzzUpstreams) (*httpFixture, error) {
	dir, err := os.MkdirTemp("", "wheelhouse-fuzz-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	fx := &httpFixture{dir: dir, logs: &syncBuffer{}, adminName: fuzzAdminName, cancel: cancel}
	db, err := store.Open(ctx, filepath.Join(dir, "wheelhouse.db"))
	if err != nil {
		return nil, err
	}
	fx.db = db
	loaded, err := secrets.Load(secrets.LoadOptions{Getenv: func(string) string { return "" }, ConfigDir: dir, Generate: true})
	if err != nil {
		return nil, err
	}
	cipher, err := db.UnlockSecrets(ctx, loaded, true)
	if err != nil {
		return nil, err
	}
	svc := auth.New(db, auth.Options{Params: auth.Params{Memory: 8, Time: 1, Threads: 1}, ConfigDir: dir})
	fx.svc = svc
	for _, u := range []struct {
		name, pass string
		role       auth.Role
	}{{fuzzAdminName, fuzzAdminPass, auth.RoleAdmin}, {fuzzUserName, fuzzUserPass, auth.RoleUser}} {
		if _, err := svc.CreateUser(ctx, auth.Actor{Name: "fuzz"}, auth.NewUser{Username: u.name, Password: u.pass, Role: u.role}); err != nil {
			return nil, err
		}
	}
	login := func(name, pass string) (token, csrf string, err error) {
		res, err := svc.Login(ctx, name, pass, auth.ClientMeta{Addr: "192.0.2.1", UserAgent: "fuzz"})
		if err != nil {
			return "", "", err
		}
		return res.Token, res.CSRF, nil
	}
	if fx.adminCookie, fx.adminCSRF, err = login(fuzzAdminName, fuzzAdminPass); err != nil {
		return nil, err
	}
	if fx.userCookie, fx.userCSRF, err = login(fuzzUserName, fuzzUserPass); err != nil {
		return nil, err
	}

	registry, err := all.Registry()
	if err != nil {
		return nil, err
	}
	stored := instances.New(db, cipher, registry, instances.Options{Environment: core.NewEnvConfigSource(up.env)})
	network, err := netaccess.New(ctx, db, nil)
	if err != nil {
		return nil, err
	}

	fx.root, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	media := filepath.Join(fx.root, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(media, "Example Movie 1 (2025).mkv"), []byte("original"), 0o644); err != nil {
		return nil, err
	}
	tools := filepath.Join(fx.root, "tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		return nil, err
	}
	ffmpeg := filepath.Join(tools, "ffmpeg")
	ffprobe := filepath.Join(tools, "ffprobe")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\necho out_time_ms=1000000\ncase \"$out\" in /*) printf converted > \"$out\";; esac\n"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(ffprobe, []byte("#!/bin/sh\ncase \"$*\" in *stream=*) echo '{\"streams\":[{\"color_transfer\":\"bt709\"}]}';; *) echo 2.0;; esac\n"), 0o755); err != nil {
		return nil, err
	}
	convSettings, err := converter.NewSettings(ctx, db, []string{media}, []string{dir})
	if err != nil {
		return nil, err
	}

	opts := Options{
		Logger: slog.New(slog.NewTextHandler(fx.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		UI:     ui.Placeholder(), Auth: svc, Config: stored, Network: network,
		Ffmpeg:  func() bool { return true },
		Context: ctx,
		Converter: ConverterOptions{
			Settings: convSettings, Store: db, ConfigDir: filepath.Join(dir, "config"), Ffmpeg: ffmpeg, Ffprobe: ffprobe,
		},
	}
	fx.h, fx.rt = buildHandler(opts, false)
	fx.levels = map[string]Level{}
	for _, r := range fx.rt.Routes() {
		fx.levels[r.Pattern] = r.Level
	}
	return fx, nil
}

func (fx *httpFixture) close() {
	fx.cancel()
	time.Sleep(5 * time.Millisecond) // let a running conversion see the cancellation before its folder goes
	_ = fx.db.Close()
	_ = os.RemoveAll(fx.dir)
}

// alive reports whether both sessions still work.
func (fx *httpFixture) alive() bool {
	ctx := context.Background()
	for _, c := range []string{fx.adminCookie, fx.userCookie} {
		if _, err := fx.svc.Authenticate(ctx, c); err != nil {
			return false
		}
	}
	return true
}

// validHostByte follows net/http's own check of the Host header
// (httpguts.ValidHostHeader), so only requests the server would hand to a
// handler are generated.
func validHostHeader(h string) bool {
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case strings.IndexByte("!$%&()*+,-.:;=[]_~'", c) >= 0:
		case c >= 0x80:
		default:
			return false
		}
	}
	return true
}

// who says which caller sends the request.
type who uint8

const (
	nobody who = iota
	adminOK
	userOK
	adminNoCSRF
	userNoCSRF
	adminWrongCSRF
	numWho
)

func (w who) String() string {
	return [...]string{"nobody", "admin", "user", "admin without CSRF", "user without CSRF", "admin with a wrong CSRF"}[w]
}

// buildRequest parses a raw request the way net/http's server does (the request
// line, the headers and the body), then adds the caller's credentials. ok is
// false for input the server would refuse before any handler runs.
func (fx *httpFixture) buildRequest(method, target, headers string, body []byte, w who, remote uint8) (*http.Request, bool) {
	if strings.ContainsAny(method, " \r\n\x00") || method == "" || strings.ContainsAny(target, " \r\n\x00") || target == "" {
		return nil, false
	}
	var raw bytes.Buffer
	raw.WriteString(method + " " + target + " HTTP/1.1\r\n")
	host := ""
	var extraCookie string
	for _, line := range strings.Split(headers, "\n") {
		line = strings.TrimRight(line, "\r")
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		switch lower {
		case "content-length", "transfer-encoding", "x-csrf-token", "connection", "upgrade", "expect":
			continue
		case "cookie":
			extraCookie = strings.TrimSpace(value)
			continue
		case "host":
			if host != "" {
				continue
			}
			host = strings.TrimSpace(value)
			continue
		}
		raw.WriteString(line + "\r\n")
	}
	if host == "" {
		host = "wheelhouse.example.test"
	}
	if !validHostHeader(host) {
		return nil, false
	}
	raw.WriteString("Host: " + host + "\r\n")
	cookies := []string{}
	if extraCookie != "" {
		cookies = append(cookies, extraCookie)
	}
	switch w {
	case adminOK, adminNoCSRF, adminWrongCSRF:
		cookies = append(cookies, "wheelhouse="+fx.adminCookie)
	case userOK, userNoCSRF:
		cookies = append(cookies, "wheelhouse="+fx.userCookie)
	}
	if len(cookies) > 0 {
		raw.WriteString("Cookie: " + strings.Join(cookies, "; ") + "\r\n")
	}
	switch w {
	case adminOK:
		raw.WriteString("X-CSRF-Token: " + fx.adminCSRF + "\r\n")
	case userOK:
		raw.WriteString("X-CSRF-Token: " + fx.userCSRF + "\r\n")
	case adminWrongCSRF:
		raw.WriteString("X-CSRF-Token: " + strings.ToUpper(fx.adminCSRF) + "x\r\n")
	}
	if len(body) > 1<<20 {
		body = body[:1<<20]
	}
	fmt.Fprintf(&raw, "Content-Length: %d\r\n\r\n", len(body))
	raw.Write(body)
	req, err := http.ReadRequest(bufio.NewReader(&raw))
	if err != nil {
		return nil, false
	}
	req.RemoteAddr = []string{"192.0.2.77:40000", "[2001:db8::77]:40000", "127.0.0.1:1"}[int(remote)%3]
	return req.WithContext(context.Background()), true
}

// plantedSecrets are every secret the response must not contain, in every
// encoding.
func plantedSecrets(up *fuzzUpstreams) []string {
	out := append([]string{fuzzAdminPass, fuzzUserPass, hostiletest.Key, hostiletest.Pass}, up.secrets()...)
	return out
}

// secretForms are the ways a secret can come back in text: as is, URL-encoded,
// base64 and JSON-escaped.
func secretForms(s string) []string {
	b := []byte(s)
	forms := []string{
		s, url.QueryEscape(s), url.PathEscape(s), strings.ReplaceAll(url.QueryEscape(s), "+", "%20"),
		base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
	}
	for _, html := range []bool{true, false} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(html)
		_ = enc.Encode(s)
		if j := strings.TrimSpace(buf.String()); len(j) > 2 {
			forms = append(forms, j[1:len(j)-1])
		}
	}
	return forms
}

func leakedIn(text string, secrets []string) string {
	for _, s := range secrets {
		if len(s) < 4 {
			continue
		}
		for _, form := range secretForms(s) {
			if form != "" && strings.Contains(text, form) {
				return form
			}
		}
	}
	return ""
}

// FuzzHTTPSurface sends arbitrary requests through the full router.
//
// Invariants, for every request:
//   - nothing panics (recovery is not installed, so a panic fails the test) and
//     no log line reports one;
//   - no 5xx except an upstream's failure (502 and 504) and only as JSON; a 500
//     or a 503 means the request shaped something the server did not handle;
//   - no response header, body or log line contains a planted secret in any
//     encoding (provider keys, the password of each account, a canary key);
//   - a route the router table marks non-public answers 401 or 403 (never
//     success, never a redirect) to a caller with no session, and 401 or 403 to
//     a user session on an admin route;
//   - a state-changing request to a non-public route without a valid CSRF token
//     is refused with 401 or 403;
//   - a state-changing request that is cross-site (Sec-Fetch-Site other than
//     same-origin and none) or carries an Origin that is not the request's own
//     host is 403 on every route, public ones included;
//   - an unmatched path or method answers 404, 405 or a redirect that stays on
//     this host, never a success;
//   - API answers are JSON, every answer carries the security headers, no
//     redirect leaves the host, and the answer arrives in a few seconds.
func FuzzHTTPSurface(f *testing.F) {
	up := startUpstreams(f)
	planted := plantedSecrets(up)
	fx, err := newHTTPFixture(up)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { fx.close() })
	f.Fuzz(func(t *testing.T, method, target, headers string, body []byte, whoSel uint8, remote uint8) {
		w := who(whoSel % uint8(numWho))
		if !fx.alive() {
			fx.close()
			if fx, err = newHTTPFixture(up); err != nil {
				t.Fatal(err)
			}
		}
		req, ok := fx.buildRequest(method, target, headers, body, w, remote)
		if !ok {
			t.Skip()
		}
		_, pattern := fx.rt.mux.Handler(req)
		level, routed := fx.levels[pattern]

		start := time.Now()
		rec := httptest.NewRecorder()
		fx.h.ServeHTTP(rec, req)
		res := rec.Result()
		elapsed := time.Since(start)
		status := res.StatusCode
		logs := fx.logs.take()

		var dump strings.Builder
		for k, vs := range res.Header {
			dump.WriteString(k + ": " + strings.Join(vs, ", ") + "\n")
		}
		dump.WriteString(rec.Body.String())
		desc := fmt.Sprintf("%s %q as %s -> %d (route %q, level %q)", req.Method, req.URL.RequestURI(), w, status, pattern, level)

		// No panic, anywhere.
		if strings.Contains(logs, "panic") {
			t.Fatalf("%s: a panic was logged: %s", desc, logs)
		}
		// Secrets.
		if form := leakedIn(dump.String(), planted); form != "" {
			t.Fatalf("%s: the response contains the planted secret %q", desc, form)
		}
		if form := leakedIn(logs, planted); form != "" {
			t.Fatalf("%s: the log contains the planted secret %q", desc, form)
		}
		if strings.Contains(dump.String(), "$argon2id$") || strings.Contains(logs, "$argon2id$") {
			t.Fatalf("%s: a password hash left the server", desc)
		}
		// Server errors.
		if status >= 500 && status != http.StatusBadGateway && status != http.StatusGatewayTimeout {
			t.Fatalf("%s: server error: %s", desc, rec.Body.String())
		}
		if status >= 500 && !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("%s: a %d that is not JSON", desc, status)
		}
		if elapsed > 8*time.Second {
			t.Fatalf("%s: took %s", desc, elapsed)
		}
		if rec.Body.Len() > 16<<20 {
			t.Fatalf("%s: a %d byte response", desc, rec.Body.Len())
		}

		unsafe := !isSafeMethod(req.Method)
		// Access levels, derived from the router table.
		if routed && level != Public {
			denied := status == http.StatusUnauthorized || status == http.StatusForbidden
			switch {
			case w == nobody && !denied:
				t.Fatalf("%s: an unauthenticated request was not refused", desc)
			case level == Admin && (w == userOK || w == userNoCSRF) && !denied:
				t.Fatalf("%s: a user session reached an admin route", desc)
			case unsafe && (w == adminNoCSRF || w == userNoCSRF || w == adminWrongCSRF) && !denied:
				t.Fatalf("%s: a state-changing request without a valid CSRF token succeeded", desc)
			}
		}
		// Cross-site requests, on every route.
		if unsafe && routed {
			site := req.Header.Get("Sec-Fetch-Site")
			crossSite := site != "" && site != "same-origin" && site != "none"
			origin := req.Header.Get("Origin")
			badOrigin := false
			if origin != "" {
				u, err := url.Parse(origin)
				badOrigin = err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || !strings.EqualFold(u.Host, req.Host)
			}
			if (crossSite || badOrigin) && status != http.StatusForbidden {
				t.Fatalf("%s: a cross-site request (Sec-Fetch-Site %q, Origin %q, Host %q) was not refused", desc, site, origin, req.Host)
			}
		}
		// Nothing unrouted succeeds.
		if !routed && status < 300 {
			t.Fatalf("%s: an unrouted request succeeded", desc)
		}
		// Redirects stay on the host.
		if loc := res.Header.Get("Location"); loc != "" {
			u, err := url.Parse(loc)
			if err != nil || u.Host != "" || u.Scheme != "" || strings.HasPrefix(loc, "//") {
				t.Fatalf("%s: redirect to %q", desc, loc)
			}
		}
		// API answers are JSON (a redirect of the mux itself excepted); every answer has the security headers.
		if IsAPI(req.URL.Path) && rec.Body.Len() > 0 && (status < 300 || status >= 400) {
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") || !json.Valid(rec.Body.Bytes()) {
				t.Fatalf("%s: API answer is %q: %q", desc, ct, rec.Body.String())
			}
		}
		for _, h := range []string{"X-Content-Type-Options", "Content-Security-Policy", "X-Request-Id"} {
			if res.Header.Get(h) == "" {
				t.Fatalf("%s: missing %s", desc, h)
			}
		}

		// A request that changed something starts the next one from a clean app.
		if unsafe && status < 400 {
			fx.close()
			if fx, err = newHTTPFixture(up); err != nil {
				t.Fatal(err)
			}
		}
	})
}
