package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/ui"
)

// The local-network bypass, end to end through the guard. The tests make a
// request appear from a chosen peer by setting RemoteAddr (what the server's
// listener does with the real TCP peer); the acceptance suite does the same with
// the real loopback peer.

const (
	inRange  = "198.51.100.10:4000" // inside the bypass range used below
	outRange = "203.0.113.9:4000"   // outside it
	proxy    = "192.0.2.50:4000"    // a trusted proxy
)

type bypassHarness struct {
	*harness
	net *netaccess.Service
}

func newBypass(t *testing.T, enabled bool, stored netaccess.Input, mut ...func(*Options)) *bypassHarness {
	t.Helper()
	return newBypassEnv(t, enabled, stored, nil, mut...)
}

func newBypassEnv(t *testing.T, enabled bool, stored netaccess.Input, envProxies []netip.Prefix, mut ...func(*Options)) *bypassHarness {
	t.Helper()
	acc := newAccounts(t)
	nw, err := netaccess.New(context.Background(), acc.DB, envProxies)
	if err != nil {
		t.Fatal(err)
	}
	stored.Enabled = enabled
	if stored.CIDRs == nil {
		stored.CIDRs = []string{"198.51.100.0/24"}
	}
	if _, _, err := nw.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	opts := append([]func(*Options){func(o *Options) { o.Network = nw }}, mut...)
	return &bypassHarness{harness: newHarnessOn(t, acc, opts...), net: nw}
}

// get is a plain request from a peer (no session).
func (b *bypassHarness) from(peer, method, path string, o reqOpts) *httptest.ResponseRecorder {
	b.t.Helper()
	o.remote = peer
	return b.do(method, path, o)
}

// token fetches the bypass CSRF token the way the UI does.
func (b *bypassHarness) token(peer string, headers map[string]string) string {
	b.t.Helper()
	rec := b.from(peer, "GET", "/api/auth/session", reqOpts{headers: headers})
	if rec.Code != 200 {
		b.t.Fatalf("session from %s: %d %s", peer, rec.Code, rec.Body)
	}
	return decode(b.t, rec)["csrfToken"].(string)
}

func TestBypassIsOffByDefault(t *testing.T) {
	x := newHarness(t) // no Network: the default
	for _, peer := range []string{inRange, "127.0.0.1:1", "[::1]:1", "10.0.0.1:1"} {
		if rec := x.do("GET", "/api/status", reqOpts{remote: peer}); rec.Code != 401 {
			t.Errorf("%s: %d", peer, rec.Code)
		}
	}
	b := newBypass(t, false, netaccess.Input{}) // ranges stored but disabled
	if rec := b.from(inRange, "GET", "/api/status", reqOpts{}); rec.Code != 401 {
		t.Errorf("disabled bypass: %d", rec.Code)
	}
	// A fresh database has the bypass off with no ranges.
	acc := newAccounts(t)
	nw, err := netaccess.New(context.Background(), acc.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st := nw.Settings(); st.Enabled || len(st.Bypass) != 0 || len(st.Proxies) != 0 {
		t.Errorf("fresh settings: %+v", st)
	}
}

func TestBypassGrantsUserNeverAdmin(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{})
	// Outside the range: still 401.
	if rec := b.from(outRange, "GET", "/api/status", reqOpts{}); rec.Code != 401 {
		t.Errorf("outside: %d", rec.Code)
	}
	csrf := b.token(inRange, nil)
	for _, r := range nonPublic(t) {
		method, path := samplePath(r.Pattern)
		rec := b.from(inRange, method, path, reqOpts{csrf: csrf})
		switch {
		case r.Level == Admin:
			if rec.Code != 403 || decode(t, rec)["error"] != "forbidden" {
				t.Errorf("admin route %s: %d %s", r.Pattern, rec.Code, rec.Body)
			}
		case needsRealLogin[r.Pattern]:
			if rec.Code != 401 {
				t.Errorf("login-only route %s: %d %s", r.Pattern, rec.Code, rec.Body)
			}
		default:
			if rec.Code == 401 || rec.Code == 403 || rec.Code == 503 {
				t.Errorf("authenticated route %s refused: %d %s", r.Pattern, rec.Code, rec.Body)
			}
		}
	}
	// The principal is marked, is a user, and gets no cookie.
	rec := b.from(inRange, "GET", "/api/auth/session", reqOpts{})
	m := decode(t, rec)
	u := m["user"].(map[string]any)
	if m["bypass"] != true || u["role"] != "user" || u["username"] != auth.BypassUsername || u["id"] != "" {
		t.Errorf("session body: %s", rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 || rec.Header().Get("Set-Cookie") != "" {
		t.Error("the bypass set a cookie")
	}
	// No bypass request ever creates a session row.
	var n int
	if err := b.acc.DB.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 { // only the harness administrator's
		t.Errorf("%d sessions", n)
	}
}

func TestBypassCSRFRules(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{CIDRs: []string{"198.51.100.0/24"}})
	csrf := b.token(inRange, nil)
	other := b.token("198.51.100.11:5", nil)
	if csrf == other || csrf == "" {
		t.Error("the token must differ per client address")
	}
	if again := b.token(inRange, nil); again != csrf {
		t.Error("the token is stateless: the same client gets the same token")
	}
	post := func(o reqOpts) int {
		o.body = jsonBody("x", 1)
		return b.from(inRange, "POST", "/api/add", o).Code
	}
	for name, o := range map[string]reqOpts{
		"no token":       {},
		"wrong token":    {csrf: strings.Repeat("A", 43)},
		"another client": {csrf: other},
	} {
		if code := post(o); code != 403 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// The same-origin rules still apply, with a valid token.
	for name, h := range map[string]map[string]string{
		"foreign origin": {"Origin": "https://evil.example.com"},
		"cross-site":     {"Sec-Fetch-Site": "cross-site"},
		"null origin":    {"Origin": "null"},
	} {
		if code := post(reqOpts{csrf: csrf, headers: h}); code != 403 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := post(reqOpts{csrf: csrf, headers: map[string]string{"Origin": "http://example.com"}}); code == 403 {
		t.Error("a same-origin request with the token was refused") // httptest hosts are example.com
	}
	if code := post(reqOpts{csrf: csrf}); code == 403 || code == 401 {
		t.Errorf("valid token: %d", code)
	}
	// A bypass token is no use on its own against a real session's CSRF check, and a
	// session's token is no use for the bypass.
	if rec := b.from(inRange, "POST", "/api/add", reqOpts{csrf: b.acc.CSRF, body: jsonBody("x", 1)}); rec.Code != 403 {
		t.Errorf("a session token for a bypass request: %d", rec.Code)
	}
	// A restart (new guard key) invalidates old tokens.
	b2 := newBypass(t, true, netaccess.Input{})
	if got := b2.token(inRange, nil); got == csrf {
		t.Error("tokens survived a new key")
	}
}

func TestSpoofedForwardedHeadersAreIgnoredFromUntrustedPeers(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{TrustedProxies: []string{"192.0.2.50"}})
	for name, h := range map[string]map[string]string{
		"xff":       {"X-Forwarded-For": "198.51.100.7"},
		"real ip":   {"X-Real-IP": "198.51.100.7"},
		"forwarded": {"Forwarded": "for=198.51.100.7"},
		"all":       {"X-Forwarded-For": "198.51.100.7", "X-Real-IP": "198.51.100.7", "Forwarded": "for=198.51.100.7"},
	} {
		if rec := b.from(outRange, "GET", "/api/status", reqOpts{headers: h}); rec.Code != 401 {
			t.Errorf("%s from an untrusted peer: %d", name, rec.Code)
		}
	}
	// The other direction: an in-range peer is not demoted by headers.
	if rec := b.from(inRange, "GET", "/api/status", reqOpts{headers: map[string]string{"X-Forwarded-For": "203.0.113.1"}}); rec.Code == 401 {
		t.Error("headers from an untrusted peer changed the peer's own address")
	}
}

func TestTrustedProxyForwardedAddressDecides(t *testing.T) {
	for source, in := range map[string]netaccess.Input{
		"stored": {TrustedProxies: []string{"192.0.2.0/24"}},
		"env":    {},
	} {
		t.Run(source, func(t *testing.T) {
			var b *bypassHarness
			if source == "env" {
				b = newBypassEnv(t, true, in, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")})
			} else {
				b = newBypass(t, true, in)
			}
			for xff, want := range map[string]int{
				"198.51.100.7":                         200, // client inside the range
				"203.0.113.9":                          401, // outside
				"198.51.100.7, 192.0.2.7":              200, // a second trusted hop is skipped
				"203.0.113.9, 198.51.100.7":            200, // spoofed left entry does not matter
				"198.51.100.7, 203.0.113.9":            401, // the right-most untrusted entry is outside
				"198.51.100.7, 203.0.113.9, 192.0.2.8": 401,
				"garbage":                              401,
				"198.51.100.7,,":                       401,
				"::ffff:198.51.100.7":                  200, // IPv4-mapped is normalised
				"198.51.100.7:5555":                    200,
				"fe80::1%eth0":                         401, // zone ids are malformed
			} {
				rec := b.from(proxy, "GET", "/api/status", reqOpts{headers: map[string]string{"X-Forwarded-For": xff}})
				if granted := rec.Code != 401 && rec.Code != 403; granted != (want == 200) {
					t.Errorf("XFF %q: %d, want %d", xff, rec.Code, want)
				}
			}
			// No header: the proxy host itself (outside the range here).
			if rec := b.from(proxy, "GET", "/api/status", reqOpts{}); rec.Code != 401 {
				t.Errorf("proxy with no header: %d", rec.Code)
			}
			// Conflicting headers from a trusted proxy: no client, no bypass.
			rec := b.from(proxy, "GET", "/api/status", reqOpts{headers: map[string]string{"X-Forwarded-For": "198.51.100.7", "X-Real-IP": "203.0.113.9"}})
			if rec.Code != 401 {
				t.Errorf("conflict: %d", rec.Code)
			}
		})
	}
}

// newHarnessOn rebuilds the app over an existing accounts fixture.
func newHarnessOn(t *testing.T, acc *accounts, mut ...func(*Options)) *harness {
	t.Helper()
	o := Options{Logger: slog.New(slog.DiscardHandler), UI: ui.Placeholder(), Auth: acc.Svc, Ffmpeg: func() bool { return false }}
	for _, m := range mut {
		m(&o)
	}
	return &harness{t: t, acc: acc, h: Handler(o)}
}

func TestARealSessionBeatsTheBypass(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{})
	// The administrator from inside the range is an administrator.
	rec := b.do("GET", "/api/admin/network", reqOpts{remote: inRange, cookie: b.acc.Cookie, csrf: b.acc.CSRF})
	if rec.Code != 200 {
		t.Fatalf("admin from the local network: %d %s", rec.Code, rec.Body)
	}
	sess := decode(t, b.do("GET", "/api/auth/session", reqOpts{remote: inRange, cookie: b.acc.Cookie}))
	if sess["bypass"] != false || sess["user"].(map[string]any)["username"] != testAdminName {
		t.Errorf("session body: %v", sess)
	}
	// A user session is a user, and its own CSRF token is the one that counts.
	u := b.user("local-user", auth.RoleUser)
	if rec := b.do("GET", "/api/admin/network", reqOpts{remote: inRange, cookie: u.cookie}); rec.Code != 403 {
		t.Errorf("user from the local network on an admin route: %d", rec.Code)
	}
	if rec := b.do("POST", "/api/auth/logout", reqOpts{remote: inRange, cookie: u.cookie, csrf: u.csrf}); rec.Code != 200 {
		t.Errorf("a real session can log out: %d", rec.Code)
	}
	// A dead cookie from inside the range falls back to the bypass.
	if rec := b.do("GET", "/api/status", reqOpts{remote: inRange, cookie: u.cookie}); rec.Code != 200 {
		t.Errorf("dead cookie in range: %d", rec.Code)
	}
	// Login is unaffected: it issues a real session even from the range.
	rec = b.do("POST", "/api/auth/login", reqOpts{remote: inRange, body: jsonBody("username", testAdminName, "password", testPassword)})
	if rec.Code != 200 || decode(t, rec)["bypass"] != nil || len(rec.Result().Cookies()) == 0 {
		t.Errorf("login from the range: %d %s", rec.Code, rec.Body)
	}
}

func TestSetupIsNeverBypassed(t *testing.T) {
	empty := newHarness(t)
	ctx := context.Background()
	for _, q := range []string{"DROP TRIGGER users_keep_last_admin_delete", "DELETE FROM users"} {
		if _, err := empty.acc.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	svc := auth.New(empty.acc.DB, auth.Options{Clock: empty.acc.Clock.Now, Params: cheapParams, ConfigDir: empty.acc.Dir})
	if err := svc.PrepareSetup(ctx); err != nil {
		t.Fatal(err)
	}
	nw, _ := netaccess.New(ctx, empty.acc.DB, nil)
	if _, _, err := nw.Update(ctx, netaccess.Input{Enabled: true, CIDRs: []string{"198.51.100.0/24"}}); err != nil {
		t.Fatal(err)
	}
	h := Handler(Options{Logger: slog.New(slog.DiscardHandler), UI: ui.Placeholder(), Auth: svc, Network: nw})
	for _, p := range []string{"/api/status", "/api/auth/session", "/api/admin/network"} {
		req := httptest.NewRequest("GET", p, nil)
		req.RemoteAddr = inRange
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), "setup required") {
			t.Errorf("%s during setup from the range: %d %s", p, rec.Code, rec.Body)
		}
	}
	req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(jsonBody("token", "wrong", "username", "newadmin", "password", testPassword)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = inRange
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("setup without the token from the range: %d %s", rec.Code, rec.Body)
	}
}

func TestBypassAuditsDestructiveActionsWithTheAddress(t *testing.T) {
	radarr := mmtest.StartRadarr(t)
	src := core.NewEnvConfigSource(env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey})
	b := newBypass(t, true, netaccess.Input{TrustedProxies: []string{"192.0.2.0/24"}}, func(o *Options) { o.Source = src })

	csrf := b.token(inRange, nil)
	if rec := b.from(inRange, "DELETE", "/api/library/radarr/1?confirm=true", reqOpts{csrf: csrf}); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	// Through a trusted proxy: the forwarded client is the address, the proxy the peer.
	hdr := map[string]string{"X-Forwarded-For": "198.51.100.77"}
	pcsrf := b.token(proxy, hdr)
	if rec := b.from(proxy, "DELETE", "/api/library/radarr/2?confirm=true", reqOpts{csrf: pcsrf, headers: hdr}); rec.Code != 200 {
		t.Fatalf("proxied delete: %d %s", rec.Code, rec.Body)
	}
	raw := b.admin("GET", "/api/audit?limit=20", "").Body.String()
	for _, want := range []string{
		`"action":"library.delete"`, `"actor":"local-network"`, `"actorId":null`, `"address":"198.51.100.10"`,
		`"via":"bypass"`, `"address":"198.51.100.77"`, `"peer":"192.0.2.50"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("audit lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(raw, csrf) || strings.Contains(raw, pcsrf) {
		t.Error("a CSRF token reached the audit log")
	}
}

func TestBypassIsNotThrottledOrLockedOut(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{})
	for i := 0; i < 40; i++ {
		b.from(outRange, "POST", "/api/auth/login", reqOpts{body: jsonBody("username", testAdminName, "password", "wrong-password-xyz")})
	}
	// The failures above are from elsewhere; the local client is unaffected.
	if rec := b.from(inRange, "GET", "/api/status", reqOpts{}); rec.Code != 200 {
		t.Errorf("bypass after failures elsewhere: %d", rec.Code)
	}
	for i := 0; i < 40; i++ {
		if rec := b.from(inRange, "GET", "/api/status", reqOpts{}); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
}

func TestAdminNetworkAPI(t *testing.T) {
	b := newBypass(t, false, netaccess.Input{CIDRs: []string{}})
	rec := b.admin("GET", "/api/admin/network", "")
	m := decode(t, rec)
	if rec.Code != 200 || m["enabled"] != false || len(m["cidrs"].([]any)) != 0 || len(m["warnings"].([]any)) != 0 {
		t.Fatalf("defaults: %d %s", rec.Code, rec.Body)
	}
	put := func(body string) *httptest.ResponseRecorder { return b.admin("PUT", "/api/admin/network", body) }

	for name, body := range map[string]string{
		"empty with enabled":   jsonBody("enabled", true, "cidrs", []string{}),
		"no enabled":           jsonBody("cidrs", []string{"10.0.0.0/8"}),
		"v4 default":           jsonBody("enabled", true, "cidrs", []string{"0.0.0.0/0"}),
		"v6 default":           jsonBody("enabled", true, "cidrs", []string{"::/0"}),
		"mapped default":       jsonBody("enabled", true, "cidrs", []string{"::ffff:0.0.0.0/96"}),
		"proxy default":        jsonBody("enabled", false, "cidrs", []string{}, "trustedProxies", []string{"0.0.0.0/0"}),
		"garbage":              jsonBody("enabled", true, "cidrs", []string{"not-a-cidr"}),
		"bad prefix length":    jsonBody("enabled", true, "cidrs", []string{"10.0.0.0/33"}),
		"zone":                 jsonBody("enabled", true, "cidrs", []string{"fe80::1%eth0"}),
		"too many":             jsonBody("enabled", true, "cidrs", manyCIDRs(65)),
		"unknown field":        `{"enabled":false,"cidrs":[],"extra":1}`,
		"short mapped prefix":  jsonBody("enabled", true, "cidrs", []string{"::ffff:0:0/80"}),
		"empty entry":          jsonBody("enabled", true, "cidrs", []string{" "}),
		"not an object (list)": `[]`,
	} {
		if rec := put(body); rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if st := b.net.Settings(); st.Enabled || len(st.Bypass) != 0 {
		t.Errorf("a refused update changed the settings: %+v", st)
	}

	// A good update: normalised, de-duplicated, persisted, applied at once.
	rec = put(jsonBody("enabled", true, "cidrs", []string{"192.168.1.77/24", "192.168.1.0/24", "::ffff:10.1.0.0/112", "fd00::1"}, "trustedProxies", []string{"10.9.9.9"}))
	m = decode(t, rec)
	if rec.Code != 200 || m["enabled"] != true || len(m["warnings"].([]any)) != 0 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	want := []any{"192.168.1.0/24", "10.1.0.0/16", "fd00::1/128"}
	got := m["cidrs"].([]any)
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("cidrs: %v", got)
	}
	if rec := b.from("192.168.1.5:1", "GET", "/api/status", reqOpts{}); rec.Code != 200 {
		t.Errorf("new range not applied: %d", rec.Code)
	}
	reloaded, err := netaccess.New(context.Background(), b.acc.DB, nil)
	if err != nil || len(reloaded.Settings().Bypass) != 3 || !reloaded.Settings().Enabled || len(reloaded.Settings().Proxies) != 1 {
		t.Errorf("not persisted: %v %+v", err, reloaded.Settings())
	}

	// Wide ranges are allowed with warnings, shown on GET too.
	rec = put(jsonBody("enabled", true, "cidrs", []string{"8.8.8.0/24", "10.0.0.0/8", "100.64.0.0/10", "fe80::/10", "2001:db8::/32"}, "trustedProxies", []string{"1.0.0.0/8"}))
	w := decode(t, rec)["warnings"].([]any)
	if rec.Code != 200 || len(w) != 3 { // 8.8.8.0/24 and 2001:db8::/32 (bypass), 1.0.0.0/8 (proxy); the private ranges are quiet
		t.Errorf("warnings: %d %v", rec.Code, w)
	}
	if w2 := decode(t, b.admin("GET", "/api/admin/network", ""))["warnings"].([]any); len(w2) != len(w) {
		t.Errorf("GET warnings %v", w2)
	}

	// Audited, without anything secret, by the administrator.
	raw := b.admin("GET", "/api/audit?limit=50", "").Body.String()
	for _, s := range []string{`"action":"network.update"`, `"actor":"` + testAdminName + `"`, `"enabled":true`} {
		if !strings.Contains(raw, s) {
			t.Errorf("audit lacks %s: %s", s, raw)
		}
	}
	// CSRF and the role are enforced on the writer.
	if rec := b.do("PUT", "/api/admin/network", reqOpts{cookie: b.acc.Cookie, body: jsonBody("enabled", false, "cidrs", []string{})}); rec.Code != 403 {
		t.Errorf("PUT without CSRF: %d", rec.Code)
	}
	csrf := b.token("10.0.0.5:1", nil)
	if rec := b.from("10.0.0.5:1", "PUT", "/api/admin/network", reqOpts{csrf: csrf, body: jsonBody("enabled", false, "cidrs", []string{})}); rec.Code != 403 {
		t.Errorf("a bypass client changed the network settings: %d", rec.Code)
	}
	if !b.net.Settings().Enabled {
		t.Error("settings changed by a non-admin")
	}
}

func manyCIDRs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "10." + string(rune('0'+i/100)) + "." + string(rune('0'+(i/10)%10)) + "." + string(rune('0'+i%10)) + "/32"
	}
	return out
}

var _ = http.StatusOK
