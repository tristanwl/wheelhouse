package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/ui"
)

// harness is the full app (all middleware) over a temporary account database.
type harness struct {
	t    *testing.T
	acc  *accounts
	h    http.Handler
	logs *bytes.Buffer
}

type reqOpts struct {
	cookie  *http.Cookie
	csrf    string
	headers map[string]string
	body    string
	remote  string
	host    string // overrides the Host header (httptest's default is example.com)
	tls     bool
	noType  bool
}

func newHarness(t *testing.T, mut ...func(*Options)) *harness {
	t.Helper()
	acc := newAccounts(t)
	logs := &bytes.Buffer{}
	o := Options{Logger: slog.New(slog.NewTextHandler(logs, nil)), UI: ui.Placeholder(), Auth: acc.Svc, Ffmpeg: func() bool { return false }}
	for _, m := range mut {
		m(&o)
	}
	return &harness{t: t, acc: acc, h: Handler(o), logs: logs}
}

// do sends a request as the given client. A body is sent as JSON.
func (x *harness) do(method, path string, o reqOpts) *httptest.ResponseRecorder {
	x.t.Helper()
	var body *strings.Reader
	if o.body != "" {
		body = strings.NewReader(o.body)
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if o.body != "" && !o.noType {
		req.Header.Set("Content-Type", "application/json")
	}
	if o.cookie != nil {
		req.AddCookie(o.cookie)
	}
	if o.csrf != "" {
		req.Header.Set("X-CSRF-Token", o.csrf)
	}
	for k, v := range o.headers {
		req.Header.Set(k, v)
	}
	if o.host != "" {
		req.Host = o.host
	}
	if o.remote != "" {
		req.RemoteAddr = o.remote
	}
	if o.tls {
		req.TLS = &tls.ConnectionState{}
	}
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	return rec
}

// admin sends a request as the signed-in administrator, CSRF token included.
func (x *harness) admin(method, path, body string) *httptest.ResponseRecorder {
	x.t.Helper()
	return x.do(method, path, reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, body: body})
}

func (x *harness) as(s session, method, path, body string) *httptest.ResponseRecorder {
	x.t.Helper()
	return x.do(method, path, reqOpts{cookie: s.cookie, csrf: s.csrf, body: body})
}

type session struct {
	cookie *http.Cookie
	csrf   string
}

// user creates an account and signs it in directly.
func (x *harness) user(name string, role auth.Role) session {
	x.t.Helper()
	if _, err := x.acc.Svc.CreateUser(context.Background(), auth.Actor{Name: "test"}, auth.NewUser{Username: name, Password: testPassword, Role: role}); err != nil {
		x.t.Fatal(err)
	}
	c, csrf := x.acc.login(x.t, name, testPassword)
	return session{c, csrf}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON (%d): %q", rec.Code, rec.Body.String())
	}
	return m
}

func jsonBody(kv ...any) string {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// samplePath turns a route pattern into a concrete request path.
func samplePath(pattern string) (method, path string) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return http.MethodGet, pattern
	}
	for _, p := range []string{"{instanceId}", "{jobId}", "{season}", "{id}"} {
		repl := "x"
		switch p {
		case "{id}":
			repl = strings.Repeat("a", 32)
		case "{season}":
			repl = "1"
		}
		path = strings.ReplaceAll(path, p, repl)
	}
	return method, path
}

// nonPublic lists the routes behind the guard, as (method, path, level).
func nonPublic(t *testing.T) []Route {
	t.Helper()
	var out []Route
	for _, r := range Routes(Options{}).Routes() {
		if r.Level != Public {
			out = append(out, r)
		}
	}
	if len(out) < 30 {
		t.Fatalf("only %d guarded routes", len(out))
	}
	return out
}

// ---------------------------------------------------------------- levels

func TestUnauthenticatedRequestsGet401OnEveryGuardedRoute(t *testing.T) {
	x := newHarness(t)
	for _, r := range nonPublic(t) {
		method, path := samplePath(r.Pattern)
		for name, o := range map[string]reqOpts{
			"no cookie":          {},
			"garbage cookie":     {cookie: &http.Cookie{Name: cookieName, Value: strings.Repeat("A", 43)}},
			"short cookie":       {cookie: &http.Cookie{Name: cookieName, Value: "x"}},
			"csrf without login": {csrf: x.acc.CSRF},
		} {
			rec := x.do(method, path, o)
			if rec.Code != http.StatusUnauthorized || decode(t, rec)["error"] != "authentication required" {
				t.Errorf("%s %s (%s): %d %s", method, path, name, rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Location") != "" {
				t.Errorf("%s %s redirects", method, path)
			}
		}
	}
}

func TestAUserGets403OnEveryAdminRouteAndAdminsGetThrough(t *testing.T) {
	x := newHarness(t)
	x.user("plain-user", auth.RoleUser)
	admins := 0
	for _, r := range nonPublic(t) {
		method, path := samplePath(r.Pattern)
		// Fresh sessions per route: logout and password change end the one they use.
		uc, ucsrf := x.acc.login(t, "plain-user", testPassword)
		ac, acsrf := x.acc.login(t, testAdminName, testPassword)
		rec := x.as(session{uc, ucsrf}, method, path, "")
		if r.Level == Admin {
			admins++
			if rec.Code != http.StatusForbidden || decode(t, rec)["error"] != "forbidden" {
				t.Errorf("user on %s: %d %s", r.Pattern, rec.Code, rec.Body.String())
			}
			// The administrator is not stopped by the guard (the handler may still refuse the sample input).
			if rec := x.as(session{ac, acsrf}, method, path, ""); rec.Code == http.StatusForbidden && decode(t, rec)["error"] == "forbidden" {
				t.Errorf("admin refused on %s", r.Pattern)
			}
		} else if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("user refused on %s (level %s): %d %s", r.Pattern, r.Level, rec.Code, rec.Body.String())
		}
	}
	if admins < 9 {
		t.Fatalf("only %d admin routes", admins)
	}
}

func TestSetupRequiredServesOnlyPublicRoutes(t *testing.T) {
	x := newHarness(t)
	// A service over an empty database: no account exists.
	empty := newHarness(t)
	_, err := empty.acc.DB.Exec(context.Background(), "DROP TRIGGER users_keep_last_admin_delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.acc.DB.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	_ = x
	for _, r := range nonPublic(t) {
		method, path := samplePath(r.Pattern)
		rec := empty.do(method, path, reqOpts{cookie: empty.acc.Cookie, csrf: empty.acc.CSRF})
		if rec.Code != http.StatusServiceUnavailable || decode(t, rec)["error"] != "setup required" {
			t.Errorf("%s: %d %s", r.Pattern, rec.Code, rec.Body.String())
		}
	}
	if rec := empty.do("GET", "/healthz", reqOpts{}); rec.Code != 200 {
		t.Errorf("healthz: %d", rec.Code)
	}
	if rec := empty.do("GET", "/", reqOpts{}); rec.Code != 200 {
		t.Errorf("the UI: %d", rec.Code)
	}
	if rec := empty.do("GET", "/api/auth/setup", reqOpts{}); rec.Code != 200 || decode(t, rec)["setupRequired"] != true {
		t.Errorf("setup status: %d %s", rec.Code, rec.Body)
	}
	if rec := empty.do("POST", "/api/auth/login", reqOpts{body: jsonBody("username", "x", "password", "y")}); rec.Code != 503 {
		t.Errorf("login during setup: %d", rec.Code)
	}
}

// ---------------------------------------------------------------- CSRF

func TestEveryMutatingRouteRefusesMissingCSRFAndForeignOrigins(t *testing.T) {
	x := newHarness(t)
	checked := 0
	for _, r := range nonPublic(t) {
		method, path := samplePath(r.Pattern)
		if isSafeMethod(method) {
			continue
		}
		checked++
		// A fresh session per route: logout and password change end the one they use.
		cookie, csrfToken := x.acc.login(t, testAdminName, testPassword)
		x := &harness{t: x.t, acc: &accounts{Svc: x.acc.Svc, DB: x.acc.DB, Clock: x.acc.Clock, Dir: x.acc.Dir, Admin: x.acc.Admin, Cookie: cookie, CSRF: csrfToken}, h: x.h, logs: x.logs}
		type attempt struct {
			name string
			o    reqOpts
			want string
		}
		hdr := func(kv ...string) map[string]string {
			m := map[string]string{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i]] = kv[i+1]
			}
			return m
		}
		for _, a := range []attempt{
			{"no token", reqOpts{cookie: x.acc.Cookie}, "missing or invalid CSRF token"},
			{"wrong token", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF + "x"}, "missing or invalid CSRF token"},
			{"another session's token", reqOpts{cookie: x.acc.Cookie, csrf: auth.CSRFToken(strings.Repeat("B", 43))}, "missing or invalid CSRF token"},
			{"the cookie as token", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.Cookie.Value}, "missing or invalid CSRF token"},
			{"foreign Origin", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, headers: hdr("Origin", "https://evil.example")}, "cross-origin request refused"},
			{"null Origin", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, headers: hdr("Origin", "null")}, "cross-origin request refused"},
			{"same host other port", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, headers: hdr("Origin", "http://example.com:81")}, "cross-origin request refused"},
			{"cross-site fetch metadata", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, headers: hdr("Sec-Fetch-Site", "cross-site")}, "cross-origin request refused"},
			{"same-site fetch metadata", reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, headers: hdr("Sec-Fetch-Site", "same-site")}, "cross-origin request refused"},
		} {
			rec := x.do(method, path, a.o)
			if rec.Code != http.StatusForbidden || decode(t, rec)["error"] != a.want {
				t.Errorf("%s %s (%s): %d %s", method, path, a.name, rec.Code, rec.Body.String())
			}
		}
		// And with the token and the right origin the guard lets it through.
		for _, h := range []map[string]string{nil, hdr("Origin", "http://example.com", "Sec-Fetch-Site", "same-origin")} {
			rec := x.do(method, path, reqOpts{cookie: x.acc.Cookie, csrf: x.acc.CSRF, headers: h})
			if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "CSRF") || strings.Contains(rec.Body.String(), "cross-origin") {
				t.Errorf("%s %s refused by the guard with a good token: %s", method, path, rec.Body)
			}
		}
	}
	if checked < 14 {
		t.Fatalf("only %d mutating routes", checked)
	}
	// Login and setup have no session yet, but still refuse a foreign origin.
	for _, p := range []string{"/api/auth/login", "/api/auth/setup"} {
		rec := x.do("POST", p, reqOpts{body: jsonBody("username", "a"), headers: map[string]string{"Origin": "https://evil.example"}})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with a foreign Origin: %d", p, rec.Code)
		}
	}
}

func TestSafeMethodsIgnoreOriginAndNeedNoToken(t *testing.T) {
	x := newHarness(t)
	rec := x.do("GET", "/api/auth/session", reqOpts{cookie: x.acc.Cookie, headers: map[string]string{"Origin": "https://evil.example"}})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// The response is addressed to the page that holds the cookie; a cross-origin
	// page cannot read it (no CORS headers are ever sent).
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header sent")
	}
}

// GET must never change state: walk every GET route as an administrator and
// compare what the database holds before and after.
func TestGETRoutesNeverChangeState(t *testing.T) {
	x := newHarness(t)
	x.user("plain-user", auth.RoleUser)
	snapshot := func() string {
		var out []string
		for _, q := range []string{
			"SELECT count(*) || ':' || coalesce(group_concat(id || username || role || disabled || must_change_password || password_hash || password_changed_at), '') FROM (SELECT * FROM users ORDER BY id)",
			"SELECT count(*) || ':' || coalesce(group_concat(id || user_id || token_hash), '') FROM (SELECT * FROM sessions ORDER BY id)",
			"SELECT count(*) FROM audit_log",
		} {
			var s string
			if err := x.acc.DB.QueryRow(context.Background(), q).Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return strings.Join(out, "|")
	}
	walked := 0
	for _, r := range Routes(Options{}).Routes() {
		method, path := samplePath(r.Pattern)
		if method != http.MethodGet && !strings.HasPrefix(r.Pattern, "/") {
			continue
		}
		if method != http.MethodGet {
			method = http.MethodGet // "/api" and "/" patterns answer every method; GET is the one that must be harmless
		}
		before := snapshot()
		x.admin(method, path, "")
		x.do(method, path, reqOpts{})
		if after := snapshot(); after != before {
			t.Errorf("GET %s changed the database", path)
		}
		walked++
	}
	if walked < 25 {
		t.Fatalf("walked %d routes", walked)
	}
	// And no route registered for GET is also a state changer: GET handlers are
	// listed by pattern, so check none of them is a setup/login/logout path.
	for _, r := range Routes(Options{}).Routes() {
		if strings.HasPrefix(r.Pattern, "GET ") {
			for _, bad := range []string{"login", "logout", "password"} {
				if strings.HasSuffix(r.Pattern, "/"+bad) {
					t.Errorf("%s: a state-changing action behind GET", r.Pattern)
				}
			}
		}
	}
}

// ---------------------------------------------------------------- cookies

func parseSetCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cs := (&http.Response{Header: rec.Header()}).Cookies()
	if len(cs) != 1 {
		t.Fatalf("%d Set-Cookie headers: %v", len(cs), rec.Header()["Set-Cookie"])
	}
	return cs[0]
}

func loginAs(x *harness, o reqOpts) *httptest.ResponseRecorder {
	o.body = jsonBody("username", testAdminName, "password", testPassword)
	return x.do("POST", "/api/auth/login", o)
}

func TestSessionCookieFlags(t *testing.T) {
	prefix := netip.MustParsePrefix("203.0.113.0/24")
	x := newHarness(t, func(o *Options) { o.TrustedProxies = []netip.Prefix{prefix} })

	rec := loginAs(x, reqOpts{remote: "192.0.2.5:4000"})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := parseSetCookie(t, rec)
	if c.Name != "wheelhouse" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure || c.Domain != "" || len(c.Value) != 43 {
		t.Fatalf("plain HTTP cookie: %+v", c)
	}
	if c.MaxAge < int(29*24*time.Hour/time.Second) || c.MaxAge > int(30*24*time.Hour/time.Second) {
		t.Fatalf("lifetime %ds", c.MaxAge)
	}
	if strings.Contains(rec.Body.String(), c.Value) {
		t.Fatal("the session token is in the response body")
	}
	body := decode(t, rec)
	if body["csrfToken"] != auth.CSRFToken(c.Value) || body["csrfToken"] == c.Value {
		t.Fatalf("csrf token: %v", body)
	}
	for _, k := range []string{"user", "expiresAt", "absoluteExpiresAt", "sessionId"} {
		if body[k] == nil {
			t.Errorf("login response lacks %s", k)
		}
	}

	// Each way of arriving over HTTPS gives a Secure, __Host- cookie...
	for name, o := range map[string]reqOpts{
		"direct TLS":         {tls: true, remote: "192.0.2.5:4000"},
		"trusted proxy":      {remote: "203.0.113.9:4000", headers: map[string]string{"X-Forwarded-Proto": "https"}},
		"trusted proxy case": {remote: "203.0.113.9:4000", headers: map[string]string{"X-Forwarded-Proto": " HTTPS "}},
	} {
		c := parseSetCookie(t, loginAs(x, o))
		if c.Name != "__Host-wheelhouse" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
			t.Errorf("%s: %+v", name, c)
		}
	}
	// ...and a header from anyone else is ignored.
	for name, o := range map[string]reqOpts{
		"untrusted peer":     {remote: "192.0.2.5:4000", headers: map[string]string{"X-Forwarded-Proto": "https"}},
		"proxy says http":    {remote: "203.0.113.9:4000", headers: map[string]string{"X-Forwarded-Proto": "http"}},
		"proxy says nothing": {remote: "203.0.113.9:4000"},
		"just outside":       {remote: "203.0.114.1:4000", headers: map[string]string{"X-Forwarded-Proto": "https"}},
	} {
		if c := parseSetCookie(t, loginAs(x, o)); c.Name != "wheelhouse" || c.Secure {
			t.Errorf("%s: %+v", name, c)
		}
	}

	// Without a trusted-proxy list, the header never counts.
	y := newHarness(t)
	if c := parseSetCookie(t, loginAs(y, reqOpts{remote: "203.0.113.9:4000", headers: map[string]string{"X-Forwarded-Proto": "https"}})); c.Secure || c.Name != "wheelhouse" {
		t.Errorf("default list: %+v", c)
	}
}

func TestOnlyTheCookieThatFitsTheTransportIsRead(t *testing.T) {
	x := newHarness(t)
	plain := &http.Cookie{Name: cookieName, Value: x.acc.Cookie.Value}
	host := &http.Cookie{Name: secureCookieName, Value: x.acc.Cookie.Value}
	if rec := x.do("GET", "/api/auth/session", reqOpts{cookie: plain}); rec.Code != 200 {
		t.Fatalf("plain over HTTP: %d", rec.Code)
	}
	if rec := x.do("GET", "/api/auth/session", reqOpts{cookie: host}); rec.Code != 401 {
		t.Fatalf("__Host- cookie over plain HTTP: %d", rec.Code)
	}
	// Over HTTPS a plain-named cookie (settable by a sibling subdomain or over HTTP) is ignored.
	if rec := x.do("GET", "/api/auth/session", reqOpts{cookie: plain, tls: true}); rec.Code != 401 {
		t.Fatalf("plain cookie over HTTPS: %d", rec.Code)
	}
	if rec := x.do("GET", "/api/auth/session", reqOpts{cookie: host, tls: true}); rec.Code != 200 {
		t.Fatalf("__Host- cookie over HTTPS: %d", rec.Code)
	}
}

func TestDeadCookiesAreCleared(t *testing.T) {
	x := newHarness(t)
	rec := x.do("GET", "/api/status", reqOpts{cookie: &http.Cookie{Name: cookieName, Value: strings.Repeat("A", 43)}})
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	c := parseSetCookie(t, rec)
	if c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("%+v", c)
	}
	if rec := x.do("GET", "/api/status", reqOpts{}); len(rec.Header()["Set-Cookie"]) != 0 {
		t.Fatal("a cookie was set for a request that had none")
	}
}

// ---------------------------------------------------------------- login, logout, timeouts

func TestLoginSessionLogoutRoundTrip(t *testing.T) {
	x := newHarness(t)
	rec := loginAs(x, reqOpts{})
	c := parseSetCookie(t, rec)
	csrf := decode(t, rec)["csrfToken"].(string)
	s := session{c, csrf}

	rec = x.as(s, "GET", "/api/auth/session", "")
	got := decode(t, rec)
	user := got["user"].(map[string]any)
	if rec.Code != 200 || got["csrfToken"] != csrf || user["username"] != testAdminName || user["role"] != "admin" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "argon2") || strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatal("a password hash in the response")
	}
	if x.as(s, "GET", "/api/status", "").Code != 200 {
		t.Fatal("the session does not work")
	}

	// Logout needs the token like any other mutation, then kills the session and clears the cookie.
	if rec := x.do("POST", "/api/auth/logout", reqOpts{cookie: c}); rec.Code != 403 {
		t.Fatalf("logout without CSRF: %d", rec.Code)
	}
	rec = x.as(s, "POST", "/api/auth/logout", "")
	if rec.Code != 200 || parseSetCookie(t, rec).MaxAge >= 0 {
		t.Fatalf("logout: %d %v", rec.Code, rec.Header())
	}
	if x.as(s, "GET", "/api/status", "").Code != 401 {
		t.Fatal("the session survived logout")
	}
}

func TestSessionsEndWhenIdleOrOld(t *testing.T) {
	x := newHarness(t)
	s := session{x.acc.Cookie, x.acc.CSRF}
	status := func() int { return x.as(s, "GET", "/api/status", "").Code }

	x.acc.Clock.Advance(auth.IdleTimeout - time.Minute)
	if status() != 200 {
		t.Fatal("expired before the idle timeout")
	}
	x.acc.Clock.Advance(auth.IdleTimeout - time.Minute)
	if status() != 200 {
		t.Fatal("activity did not extend the session")
	}
	x.acc.Clock.Advance(auth.IdleTimeout + time.Minute)
	if status() != 401 {
		t.Fatal("an idle session was accepted")
	}

	c, csrf := x.acc.login(t, testAdminName, testPassword)
	s = session{c, csrf}
	elapsed := time.Duration(0)
	for ; elapsed+11*time.Hour < auth.AbsoluteTimeout; elapsed += 11 * time.Hour {
		x.acc.Clock.Advance(11 * time.Hour)
		if status() != 200 {
			t.Fatalf("a busy session ended early, after %v", elapsed)
		}
	}
	x.acc.Clock.Advance(auth.AbsoluteTimeout - elapsed + time.Minute) // under the idle limit, over the absolute one
	if status() != 401 {
		t.Fatal("a session older than 30 days was accepted")
	}
	// The expiry reported to the client matches.
	c, csrf = x.acc.login(t, testAdminName, testPassword)
	got := decode(t, x.as(session{c, csrf}, "GET", "/api/auth/session", ""))
	exp, _ := time.Parse(time.RFC3339, got["expiresAt"].(string))
	abs, _ := time.Parse(time.RFC3339, got["absoluteExpiresAt"].(string))
	now := x.acc.Clock.Now()
	if exp.Sub(now) != auth.IdleTimeout || abs.Sub(now) != auth.AbsoluteTimeout {
		t.Fatalf("expiresAt %v, absoluteExpiresAt %v from %v", exp, abs, now)
	}
}

func TestEveryFailedLoginAnswersTheSame(t *testing.T) {
	x := newHarness(t)
	x.user("bob-user", auth.RoleUser)
	if rec := x.admin("PATCH", "/api/users/"+x.userID("bob-user"), jsonBody("disabled", true)); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	var first *httptest.ResponseRecorder
	attempts := []struct{ name, user, pw string }{
		{"unknown user", "nobody-here", testPassword},
		{"wrong password", testAdminName, "wrong-password-1"},
		{"disabled account", "bob-user", testPassword},
		{"empty", "", ""},
	}
	for _, a := range attempts {
		rec := x.do("POST", "/api/auth/login", reqOpts{body: jsonBody("username", a.user, "password", a.pw)})
		if rec.Code != 401 {
			t.Fatalf("%s: %d", a.name, rec.Code)
		}
		if first == nil {
			first = rec
			continue
		}
		if rec.Body.String() != first.Body.String() || fmt.Sprint(rec.Header()["Set-Cookie"]) != fmt.Sprint(first.Header()["Set-Cookie"]) ||
			rec.Header().Get("Content-Type") != first.Header().Get("Content-Type") {
			t.Errorf("%s differs: %q vs %q", a.name, rec.Body, first.Body)
		}
	}
	if first.Body.String() != `{"error":"invalid username or password"}`+"\n" {
		t.Fatalf("%q", first.Body)
	}
	// Locked looks the same as well, even for the right password.
	for range 6 {
		x.do("POST", "/api/auth/login", reqOpts{body: jsonBody("username", testAdminName, "password", "wrong-password-1")})
	}
	rec := x.do("POST", "/api/auth/login", reqOpts{body: jsonBody("username", testAdminName, "password", testPassword)})
	if rec.Code != 401 || rec.Body.String() != first.Body.String() {
		t.Fatalf("locked: %d %q", rec.Code, rec.Body)
	}
	// The existing admin session keeps working: a lockout stops logins, not people already in.
	if x.admin("GET", "/api/status", "").Code != 200 {
		t.Fatal("a lockout ended a live session")
	}
}

func (x *harness) userID(name string) string {
	x.t.Helper()
	users, err := x.acc.Svc.ListUsers(context.Background())
	if err != nil {
		x.t.Fatal(err)
	}
	for _, u := range users {
		if u.Username == name {
			return u.ID
		}
	}
	x.t.Fatalf("no user %s", name)
	return ""
}

func TestPasswordChangeRotatesAndRevokes(t *testing.T) {
	x := newHarness(t)
	other, otherCSRF := x.acc.login(t, testAdminName, testPassword)
	body := jsonBody("currentPassword", testPassword, "newPassword", testOtherPassword)

	if rec := x.admin("POST", "/api/auth/password", jsonBody("currentPassword", "wrong-current-pw-1", "newPassword", testOtherPassword)); rec.Code != 400 {
		t.Fatalf("wrong current password: %d %s", rec.Code, rec.Body)
	}
	if rec := x.admin("POST", "/api/auth/password", jsonBody("currentPassword", testPassword, "newPassword", "short")); rec.Code != 400 {
		t.Fatalf("weak password: %d", rec.Code)
	}
	rec := x.admin("POST", "/api/auth/password", body)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	fresh := parseSetCookie(t, rec)
	if fresh.Value == x.acc.Cookie.Value {
		t.Fatal("the session token was not rotated")
	}
	if got := decode(t, rec)["csrfToken"]; got == x.acc.CSRF || got != auth.CSRFToken(fresh.Value) {
		t.Fatalf("csrf %v", got)
	}
	for name, s := range map[string]session{"old": {x.acc.Cookie, x.acc.CSRF}, "other": {other, otherCSRF}} {
		if x.as(s, "GET", "/api/status", "").Code != 401 {
			t.Errorf("the %s session survived a password change", name)
		}
	}
	if x.as(session{fresh, decode(t, rec)["csrfToken"].(string)}, "GET", "/api/status", "").Code != 200 {
		t.Fatal("the new session does not work")
	}
	if loginAs(x, reqOpts{}).Code != 401 {
		t.Fatal("the old password still logs in")
	}
}

func TestMustChangePasswordBlocksEverythingButTheChange(t *testing.T) {
	x := newHarness(t)
	x.user("temp-user", auth.RoleUser)
	id := x.userID("temp-user")
	if rec := x.admin("POST", "/api/users/"+id+"/password", jsonBody("password", testOtherPassword)); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec := x.do("POST", "/api/auth/login", reqOpts{body: jsonBody("username", "temp-user", "password", testOtherPassword)})
	if rec.Code != 200 || decode(t, rec)["user"].(map[string]any)["mustChangePassword"] != true {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s := session{parseSetCookie(t, rec), decode(t, rec)["csrfToken"].(string)}
	for _, p := range []string{"/api/features", "/api/status", "/api/auth/sessions"} {
		rec := x.as(s, "GET", p, "")
		if rec.Code != 403 || decode(t, rec)["code"] != "password-change-required" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	if x.as(s, "GET", "/api/auth/session", "").Code != 200 {
		t.Fatal("cannot read the session while a change is required")
	}
	rec = x.as(s, "POST", "/api/auth/password", jsonBody("currentPassword", testOtherPassword, "newPassword", "a-brand-new-passphrase-3"))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s = session{parseSetCookie(t, rec), decode(t, rec)["csrfToken"].(string)}
	if x.as(s, "GET", "/api/features", "").Code != 200 {
		t.Fatal("still blocked after the change")
	}
}

func TestOwnSessionsListAndRevoke(t *testing.T) {
	x := newHarness(t)
	bob := x.user("bob-user", auth.RoleUser)
	second, secondCSRF := x.acc.login(t, testAdminName, testPassword)

	list := decode(t, x.admin("GET", "/api/auth/sessions", ""))["sessions"].([]any)
	if len(list) != 2 {
		t.Fatalf("%v", list)
	}
	currents := 0
	var otherID string
	for _, e := range list {
		m := e.(map[string]any)
		if m["current"] == true {
			currents++
		} else {
			otherID = m["id"].(string)
		}
		for _, k := range []string{"id", "createdAt", "lastSeenAt", "expiresAt", "ip", "userAgent"} {
			if m[k] == nil {
				t.Errorf("session lacks %s", k)
			}
		}
		if strings.Contains(fmt.Sprint(m), x.acc.Cookie.Value) {
			t.Error("a token in the session list")
		}
	}
	if currents != 1 {
		t.Fatalf("%d current sessions", currents)
	}
	// bob cannot end (or even see) the administrator's session.
	if rec := x.as(bob, "DELETE", "/api/auth/sessions/"+otherID, ""); rec.Code != 404 {
		t.Fatalf("revoking someone else's session: %d", rec.Code)
	}
	if rec := x.admin("DELETE", "/api/auth/sessions/"+otherID, ""); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if x.as(session{second, secondCSRF}, "GET", "/api/status", "").Code != 401 {
		t.Fatal("the revoked session works")
	}
	if rec := x.admin("DELETE", "/api/auth/sessions/not-an-id", ""); rec.Code != 404 {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// Revoking the current session logs out and clears the cookie.
	cur := decode(t, x.admin("GET", "/api/auth/session", ""))["sessionId"].(string)
	rec := x.admin("DELETE", "/api/auth/sessions/"+cur, "")
	if rec.Code != 200 || parseSetCookie(t, rec).MaxAge >= 0 {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

// ---------------------------------------------------------------- setup over HTTP

func TestSetupOverHTTP(t *testing.T) {
	x := newHarness(t)
	// A fresh service over an empty database.
	empty := newHarness(t)
	if _, err := empty.acc.DB.Exec(context.Background(), "DROP TRIGGER users_keep_last_admin_delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := empty.acc.DB.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	svc := auth.New(empty.acc.DB, auth.Options{Clock: empty.acc.Clock.Now, Params: cheapParams, ConfigDir: empty.acc.Dir})
	if err := svc.PrepareSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := Handler(Options{Logger: slog.New(slog.DiscardHandler), UI: ui.Placeholder(), Auth: svc})
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	raw := readFile(t, empty.acc.Dir+"/setup-token")
	token := strings.TrimSpace(raw)

	for name, body := range map[string]string{
		"no token":    jsonBody("username", "boss-user", "password", testPassword),
		"wrong token": jsonBody("token", "wrong", "username", "boss-user", "password", testPassword),
		"extra field": jsonBody("token", token, "username", "boss-user", "password", testPassword, "role", "admin"),
	} {
		if rec := post(body); rec.Code == 201 {
			t.Errorf("%s: setup succeeded", name)
		}
	}
	if rec := post(jsonBody("token", "wrong", "username", "boss-user", "password", testPassword)); rec.Code != 403 || decode(t, rec)["error"] != "invalid setup token" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(jsonBody("token", token, "username", "boss-user", "password", "short")); rec.Code != 400 {
		t.Fatalf("weak password: %d %s", rec.Code, rec.Body)
	}
	rec := post(jsonBody("token", token, "username", "boss-user", "password", testPassword))
	if rec.Code != 201 || decode(t, rec)["user"].(map[string]any)["role"] != "admin" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), token) || len(rec.Header()["Set-Cookie"]) != 0 {
		t.Fatal("setup leaked the token or signed in")
	}
	// Setup is over: a second attempt with the (now deleted) token is a conflict.
	if rec := post(jsonBody("token", token, "username", "other-user", "password", testPassword)); rec.Code != 409 {
		t.Fatalf("second setup: %d %s", rec.Code, rec.Body)
	}
	_ = x
}

// ---------------------------------------------------------------- users API

func TestUserManagementAPI(t *testing.T) {
	x := newHarness(t)

	rec := x.admin("POST", "/api/users", jsonBody("username", "new-user", "password", testPassword, "role", "user"))
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	created := decode(t, rec)["user"].(map[string]any)
	id := created["id"].(string)
	if created["role"] != "user" || created["mustChangePassword"] != true {
		t.Fatalf("%v", created)
	}
	for body, want := range map[string]int{
		jsonBody("username", "NEW-USER", "password", testPassword, "role", "user"):                      409,
		jsonBody("username", "x", "password", testPassword, "role", "user"):                             400,
		jsonBody("username", "another-user", "password", "short", "role", "user"):                       400,
		jsonBody("username", "another-user", "password", testPassword, "role", "owner"):                 400,
		jsonBody("username", "another-user", "password", testPassword):                                  400,
		jsonBody("username", "another-user", "password", testPassword, "role", "user", "isAdmin", true): 400,
		`{"username": "another-user", "password": "` + testPassword + `", "role": "user"} {}`:           400,
		`not json`: 400,
	} {
		if rec := x.admin("POST", "/api/users", body); rec.Code != want {
			t.Errorf("%s: %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}
	list := decode(t, x.admin("GET", "/api/users", ""))["users"].([]any)
	if len(list) != 2 {
		t.Fatalf("%d users", len(list))
	}
	if raw := x.admin("GET", "/api/users", "").Body.String(); strings.Contains(raw, "argon2") || strings.Contains(raw, "hash") {
		t.Fatal("password hashes in the user list")
	}

	// A user cannot make themselves an administrator, or touch anyone else.
	u := x.user("plain-user", auth.RoleUser)
	uid := x.userID("plain-user")
	for _, c := range []struct{ method, path, body string }{
		{"PATCH", "/api/users/" + uid, jsonBody("role", "admin")},
		{"POST", "/api/users", jsonBody("username", "evil-admin", "password", testPassword, "role", "admin")},
		{"POST", "/api/users/" + uid + "/password", jsonBody("password", testOtherPassword)},
		{"DELETE", "/api/users/" + x.acc.Admin.ID, ""},
		{"GET", "/api/audit", ""},
	} {
		if rec := x.as(u, c.method, c.path, c.body); rec.Code != 403 {
			t.Errorf("user: %s %s: %d", c.method, c.path, rec.Code)
		}
	}
	if got := decode(t, x.admin("GET", "/api/users", "")); len(got["users"].([]any)) != 3 {
		t.Fatal("a refused request created a user")
	}

	// Patch: unknown field, empty, bad values, bad ids.
	for body, want := range map[string]int{
		`{}`: 400, jsonBody("role", "owner"): 400, jsonBody("username", "a b"): 400, jsonBody("admin", true): 400,
		jsonBody("username", testAdminName): 409,
	} {
		if rec := x.admin("PATCH", "/api/users/"+id, body); rec.Code != want {
			t.Errorf("PATCH %s: %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}
	if rec := x.admin("PATCH", "/api/users/"+strings.Repeat("0", 32), jsonBody("disabled", true)); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
	if rec := x.admin("PATCH", "/api/users/..%2F..", jsonBody("disabled", true)); rec.Code != 404 {
		t.Errorf("odd id: %d", rec.Code)
	}
	if rec := x.admin("PATCH", "/api/users/"+id, jsonBody("role", "admin", "username", "promoted-user")); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Sessions of another user: list and revoke.
	s := x.as(u, "GET", "/api/status", "")
	if s.Code != 200 {
		t.Fatal(s.Code)
	}
	sess := decode(t, x.admin("GET", "/api/users/"+uid+"/sessions", ""))["sessions"].([]any)
	if len(sess) != 1 {
		t.Fatalf("%v", sess)
	}
	rec = x.admin("DELETE", "/api/users/"+uid+"/sessions", "")
	if rec.Code != 200 || decode(t, rec)["revoked"] != float64(1) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if x.as(u, "GET", "/api/status", "").Code != 401 {
		t.Fatal("a revoked user session works")
	}
	if rec := x.admin("GET", "/api/users/"+strings.Repeat("0", 32)+"/sessions", ""); rec.Code != 404 {
		t.Fatalf("sessions of nobody: %d", rec.Code)
	}

	// Delete.
	if rec := x.admin("DELETE", "/api/users/"+id, ""); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if rec := x.admin("DELETE", "/api/users/"+id, ""); rec.Code != 404 {
		t.Fatalf("again: %d", rec.Code)
	}
}

func TestTheLastAdminCannotBeRemovedThroughTheAPI(t *testing.T) {
	x := newHarness(t)
	id := x.acc.Admin.ID
	for name, c := range map[string]struct{ method, body string }{
		"delete": {"DELETE", ""}, "demote": {"PATCH", jsonBody("role", "user")}, "disable": {"PATCH", jsonBody("disabled", true)},
	} {
		if rec := x.admin(c.method, "/api/users/"+id, c.body); rec.Code != 409 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if x.admin("GET", "/api/status", "").Code != 200 {
		t.Fatal("the admin is gone")
	}
	// With a second administrator either may go, but never both.
	if rec := x.admin("POST", "/api/users", jsonBody("username", "second-admin", "password", testPassword, "role", "admin")); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	second := x.userID("second-admin")
	if rec := x.admin("PATCH", "/api/users/"+second, jsonBody("disabled", true)); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := x.admin("DELETE", "/api/users/"+id, ""); rec.Code != 409 {
		t.Fatalf("a disabled second admin does not count: %d", rec.Code)
	}
}

func TestRequestBodiesAreStrict(t *testing.T) {
	x := newHarness(t)
	big := `{"username":"` + strings.Repeat("a", maxAuthBody) + `","password":"x"}`
	for name, c := range map[string]struct {
		o    reqOpts
		want int
	}{
		"too large":         {reqOpts{body: big}, 413},
		"wrong type":        {reqOpts{body: `username=a&password=b`, headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, noType: true}, 415},
		"text/plain":        {reqOpts{body: jsonBody("username", "a", "password", "b"), headers: map[string]string{"Content-Type": "text/plain"}, noType: true}, 415},
		"no type":           {reqOpts{body: jsonBody("username", "a", "password", "b"), noType: true}, 415},
		"unknown field":     {reqOpts{body: jsonBody("username", "a", "password", "b", "admin", true)}, 400},
		"wrong field type":  {reqOpts{body: `{"username": 1, "password": "b"}`}, 400},
		"array":             {reqOpts{body: `[]`}, 400},
		"trailing garbage":  {reqOpts{body: `{"username":"a","password":"b"}x`}, 400},
		"second document":   {reqOpts{body: `{"username":"a","password":"b"}{"username":"a"}`}, 400},
		"empty":             {reqOpts{body: ``, headers: map[string]string{"Content-Type": "application/json"}, noType: true}, 400},
		"charset parameter": {reqOpts{body: jsonBody("username", "a", "password", "b"), headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}, noType: true}, 401},
	} {
		if rec := x.do("POST", "/api/auth/login", c.o); rec.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body, c.want)
		}
	}
}

// ---------------------------------------------------------------- audit and logs

func TestAuditAPIAndNoSecretsAnywhere(t *testing.T) {
	x := newHarness(t)
	rec := loginAs(x, reqOpts{remote: "192.0.2.5:4000"})
	c := parseSetCookie(t, rec)
	csrf := decode(t, rec)["csrfToken"].(string)
	x.do("POST", "/api/auth/login", reqOpts{body: jsonBody("username", testAdminName, "password", "wrong-password-xyz")})
	s := session{c, csrf}
	x.as(s, "POST", "/api/users", jsonBody("username", "audited-user", "password", testOtherPassword, "role", "user"))
	x.as(s, "POST", "/api/auth/logout", "")

	rec = x.admin("GET", "/api/audit?limit=100", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	raw := rec.Body.String()
	for _, want := range []string{`"action":"login.ok"`, `"action":"login.fail"`, `"action":"user.create"`, `"action":"logout"`, `"address":"192.0.2.5"`, `"actor":"` + testAdminName + `"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("audit lacks %s: %s", want, raw)
		}
	}
	for _, secret := range []string{testPassword, testOtherPassword, "wrong-password-xyz", c.Value, csrf, x.acc.CSRF, "argon2"} {
		if strings.Contains(raw, secret) || strings.Contains(x.logs.String(), secret) {
			t.Errorf("a secret (%q) is in the audit log response or the server log", secret[:6])
		}
	}
	if rec := x.admin("GET", "/api/audit?limit=0", ""); rec.Code != 400 {
		t.Errorf("limit=0: %d", rec.Code)
	}
	if rec := x.admin("GET", "/api/audit?limit=2", ""); decode(t, rec)["nextBefore"] == nil || len(decode(t, rec)["entries"].([]any)) != 2 {
		t.Errorf("paging: %s", rec.Body)
	}
	// Nothing about requests with credentials reached the access log beyond method, path and status.
	if strings.Contains(x.logs.String(), "Cookie") || strings.Contains(x.logs.String(), "X-Csrf") {
		t.Error("headers in the access log")
	}
}

func TestHealthzReportsADegradedStore(t *testing.T) {
	x := newHarness(t)
	if rec := x.do("GET", "/healthz", reqOpts{}); rec.Code != 200 || decode(t, rec)["status"] != "ok" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	_ = x.acc.DB.Close()
	rec := x.do("GET", "/healthz", reqOpts{})
	if rec.Code != 503 || decode(t, rec)["status"] != "degraded" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "sql") || strings.Contains(rec.Body.String(), x.acc.Dir) {
		t.Fatalf("the failure was described: %s", rec.Body)
	}
	// Guarded routes fail closed, with a generic message.
	rec = x.admin("GET", "/api/status", "")
	if rec.Code < 500 || strings.Contains(rec.Body.String(), x.acc.Dir) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAnAppWithoutAccountsRefusesEveryGuardedRoute(t *testing.T) {
	h := Handler(Options{Logger: slog.New(slog.DiscardHandler), UI: ui.Placeholder()})
	for _, r := range nonPublic(t) {
		method, path := samplePath(r.Pattern)
		req := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d", r.Pattern, rec.Code)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
