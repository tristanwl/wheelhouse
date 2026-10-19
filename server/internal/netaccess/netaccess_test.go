package netaccess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

func prefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range ss {
		p, err := ParseRange(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func newSvc(t *testing.T, enabled bool, bypass, stored, env []string) *Service {
	t.Helper()
	s := NewMemory(prefixes(t, env...))
	if _, _, err := s.Update(context.Background(), Input{Enabled: enabled, CIDRs: bypass, TrustedProxies: stored}); err != nil {
		t.Fatal(err)
	}
	return s
}

func req(remote string, h ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for i := 0; i+1 < len(h); i += 2 {
		r.Header.Add(h[i], h[i+1])
	}
	return r
}

func TestParseRangeNormalises(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.77/24":     "192.168.1.0/24",
		"192.168.1.5":         "192.168.1.5/32",
		" 10.0.0.0/8 ":        "10.0.0.0/8",
		"::ffff:10.1.2.3":     "10.1.2.3/32",
		"::ffff:10.1.0.0/112": "10.1.0.0/16",
		"FD00::1":             "fd00::1/128",
		"fd00::/8":            "fd00::/8",
	} {
		p, err := ParseRange(in)
		if err != nil || p.String() != want {
			t.Errorf("%q: %v %v, want %s", in, p, err, want)
		}
	}
	for _, in := range []string{"", " ", "x", "10.0.0.0/33", "10.0.0/8", "fe80::1%eth0", "fe80::/10%eth0", "::ffff:0:0/80", "1.2.3.4/", "/8", "10.0.0.0/8/8", "1.2.3.4,5.6.7.8"} {
		if _, err := ParseRange(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestUpdateValidation(t *testing.T) {
	s := NewMemory(nil)
	ctx := context.Background()
	bad := map[string]Input{
		"enabled and empty": {Enabled: true},
		"v4 default":        {Enabled: true, CIDRs: []string{"0.0.0.0/0"}},
		"v6 default":        {Enabled: true, CIDRs: []string{"::/0"}},
		"mapped default":    {CIDRs: []string{"::ffff:0:0/96"}},
		"proxy default":     {TrustedProxies: []string{"::/0"}},
		"too many":          {CIDRs: many(MaxEntries + 1)},
		"too many proxies":  {TrustedProxies: many(MaxEntries + 1)},
	}
	for name, in := range bad {
		if _, _, err := s.Update(ctx, in); err == nil {
			t.Errorf("%s accepted", name)
		} else if _, ok := err.(*ValidationError); !ok { //nolint:errorlint // concrete type returned directly
			t.Errorf("%s: %T", name, err)
		}
	}
	if st := s.Settings(); st.Enabled || len(st.Bypass) != 0 {
		t.Errorf("a refusal changed the settings: %+v", st)
	}
	if _, _, err := s.Update(ctx, Input{CIDRs: many(MaxEntries)}); err != nil {
		t.Errorf("the maximum is allowed: %v", err)
	}
	// The widest allowed ranges are /1 (a warning, not a refusal).
	st, w, err := s.Update(ctx, Input{Enabled: true, CIDRs: []string{"0.0.0.0/1"}})
	if err != nil || len(w) != 1 || !st.Enabled {
		t.Errorf("/1: %v %v", w, err)
	}
}

func many(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = netip.AddrFrom4([4]byte{10, 1, byte(i / 256), byte(i % 256)}).String()
	}
	return out
}

func TestWarningsForRangesWiderThanPrivateSpace(t *testing.T) {
	quiet := []string{"10.0.0.0/8", "10.2.3.0/24", "172.16.0.0/12", "172.31.5.5", "192.168.0.0/16", "100.64.0.0/10", "127.0.0.1", "127.0.0.0/8",
		"169.254.0.0/16", "fc00::/7", "fd12:3456::/32", "fe80::/10", "::1"}
	loud := []string{"8.8.8.0/24", "10.0.0.0/7", "172.0.0.0/8", "192.168.0.0/15", "100.0.0.0/8", "128.0.0.0/1", "0.0.0.0/1", "fc00::/6", "2001:db8::/32", "::/1", "fe00::/9", "192.0.2.0/24"}
	for _, c := range quiet {
		if w := Warnings(Settings{Bypass: prefixes(t, c)}); len(w) != 0 {
			t.Errorf("%s warned: %v", c, w)
		}
	}
	for _, c := range loud {
		if w := Warnings(Settings{Bypass: prefixes(t, c)}); len(w) != 1 || !strings.Contains(w[0], prefixes(t, c)[0].String()) {
			t.Errorf("%s: %v", c, w)
		}
	}
	if w := Warnings(Settings{Proxies: prefixes(t, "8.0.0.0/8")}); len(w) != 1 {
		t.Errorf("proxy warning: %v", w)
	}
}

func TestPersistenceAndDatabaseChecks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s, err := New(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Settings(); st.Enabled || len(st.Bypass) != 0 || len(st.Proxies) != 0 {
		t.Fatalf("not off by default: %+v", st)
	}
	if _, _, err := s.Update(ctx, Input{Enabled: true, CIDRs: []string{"192.168.0.0/16", "fd00::/8"}, TrustedProxies: []string{"10.0.0.2"}}); err != nil {
		t.Fatal(err)
	}
	s2, err := New(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Settings()
	if !got.Enabled || len(got.Bypass) != 2 || got.Bypass[1].String() != "fd00::/8" || got.Proxies[0].String() != "10.0.0.2/32" {
		t.Errorf("reloaded: %+v", got)
	}
	// The table itself refuses what the code would never write.
	for name, q := range map[string]string{
		"enabled with no ranges": `UPDATE network_settings SET bypass_enabled = 1, bypass_cidrs = '[]'`,
		"not json":               `UPDATE network_settings SET bypass_cidrs = 'x'`,
		"not an array":           `UPDATE network_settings SET trusted_proxies = '{}'`,
		"second row":             `INSERT INTO network_settings (id, updated_at) VALUES (2, 0)`,
		"bad flag":               `UPDATE network_settings SET bypass_enabled = 2`,
	} {
		if _, err := db.Exec(ctx, q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A damaged row stops the server rather than being read as "off".
	if _, err := db.Exec(ctx, `UPDATE network_settings SET bypass_cidrs = '["nonsense"]'`); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, db, nil); err == nil {
		t.Error("a damaged row was accepted")
	}
}

func TestResolveFromAnUntrustedPeerIgnoresEveryForwardedHeader(t *testing.T) {
	s := newSvc(t, true, []string{"192.168.0.0/16"}, nil, nil)
	r := req("203.0.113.5:4000", "X-Forwarded-For", "192.168.1.1", "X-Real-IP", "192.168.1.1", "Forwarded", "for=192.168.1.1")
	c := s.Resolve(r)
	if c.Addr.String() != "203.0.113.5" || c.Forwarded || !c.Ignored || s.Bypasses(c) {
		t.Errorf("%+v", c)
	}
	if c := s.Resolve(req("203.0.113.5:4000")); c.Ignored {
		t.Error("nothing was ignored")
	}
}

func TestResolveFromATrustedProxy(t *testing.T) {
	s := newSvc(t, true, []string{"192.168.0.0/16", "fd00::/8"}, []string{"10.0.0.0/24", "fd10::/16"}, []string{"172.20.0.1"})
	h := func(k, v string) []string { return []string{k, v} }
	type tc struct {
		name    string
		remote  string
		headers []string
		want    string // client address; "" = unknown
		bypass  bool
	}
	cases := []tc{
		{"single", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9"), "192.168.1.9", true},
		{"outside", "10.0.0.2:1", h("X-Forwarded-For", "203.0.113.9"), "203.0.113.9", false},
		{"no header: the proxy itself", "10.0.0.2:1", nil, "10.0.0.2", false},
		{"env source", "172.20.0.1:1", h("X-Forwarded-For", "192.168.1.9"), "192.168.1.9", true},
		{"right-most untrusted wins over a spoofed left", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9, 203.0.113.9"), "203.0.113.9", false},
		{"spoofed left outside, real inside", "10.0.0.2:1", h("X-Forwarded-For", "203.0.113.9, 192.168.1.9"), "192.168.1.9", true},
		{"chain of trusted proxies", "10.0.0.2:1", h("X-Forwarded-For", "203.0.113.9, 192.168.1.9, 10.0.0.7, 172.20.0.1"), "192.168.1.9", true},
		{"all hops trusted: left-most", "10.0.0.2:1", h("X-Forwarded-For", "10.0.0.9, 10.0.0.7"), "10.0.0.9", false},
		{"two headers are one chain", "10.0.0.2:1", []string{"X-Forwarded-For", "203.0.113.9", "X-Forwarded-For", "192.168.1.9"}, "192.168.1.9", true},
		{"two headers, outside last", "10.0.0.2:1", []string{"X-Forwarded-For", "192.168.1.9", "X-Forwarded-For", "203.0.113.9"}, "203.0.113.9", false},
		{"with ports", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9:51000"), "192.168.1.9", true},
		{"v6", "10.0.0.2:1", h("X-Forwarded-For", "fd00::5"), "fd00::5", true},
		{"v6 bracket port", "10.0.0.2:1", h("X-Forwarded-For", "[fd00::5]:443"), "fd00::5", true},
		{"v6 bracket", "10.0.0.2:1", h("X-Forwarded-For", "[fd00::5]"), "fd00::5", true},
		{"v6 trusted proxy peer", "[fd10::1]:1", h("X-Forwarded-For", "fd00::5"), "fd00::5", true},
		{"v4-mapped client", "10.0.0.2:1", h("X-Forwarded-For", "::ffff:192.168.1.9"), "192.168.1.9", true},
		{"v4-mapped peer", "[::ffff:10.0.0.2]:1", h("X-Forwarded-For", "192.168.1.9"), "192.168.1.9", true},
		{"v4-mapped trusted hop", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9, ::ffff:10.0.0.7"), "192.168.1.9", true},
		{"zone id", "10.0.0.2:1", h("X-Forwarded-For", "fe80::1%eth0"), "", false},
		{"zone in chain", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9, fd00::1%eth0"), "", false},
		{"unknown", "10.0.0.2:1", h("X-Forwarded-For", "unknown"), "", false},
		{"hostname", "10.0.0.2:1", h("X-Forwarded-For", "example.com"), "", false},
		{"empty header", "10.0.0.2:1", h("X-Forwarded-For", ""), "", false},
		{"empty element", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9,"), "", false},
		{"garbage element", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9, 1.2.3"), "", false},
		{"octal-looking", "10.0.0.2:1", h("X-Forwarded-For", "0192.168.1.9"), "", false},
		{"multiple addresses in one element", "10.0.0.2:1", h("X-Forwarded-For", "192.168.1.9 203.0.113.9"), "", false},
		{"real ip", "10.0.0.2:1", h("X-Real-IP", "192.168.1.9"), "192.168.1.9", true},
		{"real ip garbage", "10.0.0.2:1", h("X-Real-IP", "nope"), "", false},
		{"two real ips", "10.0.0.2:1", []string{"X-Real-IP", "192.168.1.9", "X-Real-IP", "192.168.1.9"}, "", false},
		{"forwarded", "10.0.0.2:1", h("Forwarded", "for=192.168.1.9"), "192.168.1.9", true},
		{"forwarded quoted v6", "10.0.0.2:1", h("Forwarded", `for="[fd00::5]:4711";proto=https;by=10.0.0.2`), "fd00::5", true},
		{"forwarded case", "10.0.0.2:1", h("Forwarded", "For=192.168.1.9;Proto=https"), "192.168.1.9", true},
		{"forwarded chain", "10.0.0.2:1", h("Forwarded", "for=203.0.113.9, for=192.168.1.9, for=10.0.0.7"), "192.168.1.9", true},
		{"forwarded obfuscated", "10.0.0.2:1", h("Forwarded", "for=_hidden"), "", false},
		{"forwarded unknown", "10.0.0.2:1", h("Forwarded", "for=unknown"), "", false},
		{"forwarded without for", "10.0.0.2:1", h("Forwarded", "proto=https"), "", false},
		{"forwarded two for", "10.0.0.2:1", h("Forwarded", "for=192.168.1.9;for=203.0.113.9"), "", false},
		{"headers agree", "10.0.0.2:1", []string{"X-Forwarded-For", "1.1.1.1, 192.168.1.9", "X-Real-IP", "192.168.1.9", "Forwarded", "for=192.168.1.9"}, "192.168.1.9", true},
		{"xff vs real ip disagree", "10.0.0.2:1", []string{"X-Forwarded-For", "192.168.1.9", "X-Real-IP", "203.0.113.9"}, "", false},
		{"xff vs forwarded disagree", "10.0.0.2:1", []string{"X-Forwarded-For", "192.168.1.9", "Forwarded", "for=203.0.113.9"}, "", false},
		{"client forged real ip behind an appending proxy", "10.0.0.2:1", []string{"X-Forwarded-For", "192.168.1.9, 203.0.113.9", "X-Real-IP", "192.168.1.9"}, "", false},
		{"too long", "10.0.0.2:1", h("X-Forwarded-For", strings.Repeat("1.1.1.1,", 70)+"1.1.1.1"), "", false},
		{"huge", "10.0.0.2:1", h("X-Forwarded-For", strings.Repeat("a", 9000)), "", false},
	}
	for _, c := range cases {
		got := s.Resolve(req(c.remote, c.headers...))
		addr := ""
		if got.Addr.IsValid() {
			addr = got.Addr.String()
		}
		if addr != c.want || s.Bypasses(got) != c.bypass || (c.want == "" && !got.Unknown) {
			t.Errorf("%s: client %q (bypass %v, unknown %v), want %q (bypass %v)", c.name, addr, s.Bypasses(got), got.Unknown, c.want, c.bypass)
		}
		if got.Peer.IsValid() && !got.Peer.Is4() && got.Peer.Is4In6() {
			t.Errorf("%s: peer not unmapped", c.name)
		}
	}
}

func TestPeerNormalisation(t *testing.T) {
	s := newSvc(t, true, []string{"192.168.0.0/16", "fe80::/10"}, nil, nil)
	for remote, bypass := range map[string]bool{
		"192.168.1.1:80":          true,
		"[::ffff:192.168.1.1]:80": true, // IPv4-mapped peer
		"[fe80::1%eth0]:80":       true, // zone stripped
		"[fe80::1]:80":            true,
		"[2001:db8::1]:80":        false,
		"203.0.113.1:80":          false,
		"192.168.1.1":             true, // no port
		"garbage":                 false,
		"":                        false,
	} {
		if got := s.Bypasses(s.Resolve(req(remote))); got != bypass {
			t.Errorf("%q: %v", remote, got)
		}
	}
}

func TestBypassNeedsEnabledAndAKnownAddress(t *testing.T) {
	s := newSvc(t, false, []string{"192.168.0.0/16"}, nil, nil)
	if s.Bypasses(s.Resolve(req("192.168.1.1:1"))) {
		t.Error("disabled bypass matched")
	}
	if s.Bypasses(Client{}) {
		t.Error("an unknown client matched")
	}
}

func TestTrustedProxySourcesAreAUnion(t *testing.T) {
	s := newSvc(t, false, nil, []string{"10.0.0.0/24"}, []string{"172.20.0.1"})
	for a, want := range map[string]bool{"10.0.0.9": true, "172.20.0.1": true, "::ffff:172.20.0.1": true, "172.20.0.2": false, "10.0.1.1": false} {
		if got := s.IsTrustedProxy(netip.MustParseAddr(a)); got != want {
			t.Errorf("%s: %v", a, got)
		}
	}
}
