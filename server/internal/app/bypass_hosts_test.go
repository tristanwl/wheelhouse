package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
)

// DNS rebinding: the bypass is granted only to requests whose Host cannot be an
// attacker's domain.

func TestBypassNeedsAnAcceptableHost(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{AllowedHosts: &[]string{"nas.example.lan", "*.apps.example.lan"}})
	b.net.SetEnvHosts([]string{"env.example.lan"})
	for host, want := range map[string]int{
		"nas:3050": 200, "NAS": 200, "nas.": 200, "localhost:3050": 200, "192.0.2.5:3050": 200, "[::1]:3050": 200,
		"[2001:db8::7]": 200, "nas.example.lan:3050": 200, "NAS.Example.LAN": 200, "env.example.lan": 200,
		"x.apps.example.lan": 200, "a.b.apps.example.lan": 200,
		// A rebound public name: both Host and Origin are the attacker's.
		"rebind.attacker.example": 401, "apps.example.lan": 401, "nas.example.lan.attacker.example": 401,
		"example.lan": 401, "nas:abc": 401, "[::1": 401,
	} {
		rec := b.from(inRange, "GET", "/api/status", reqOpts{host: host})
		if rec.Code != want {
			t.Errorf("Host %q: %d, want %d", host, rec.Code, want)
		}
		if want == 401 && rec.Body.String() != b.from(outRange, "GET", "/api/status", reqOpts{host: "nas"}).Body.String() {
			t.Errorf("Host %q: the refusal differs from a plain anonymous 401: %s", host, rec.Body)
		}
	}
}

func TestSpoofedHostAndOriginPairIsRefusedForTheBypass(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{})
	evil := map[string]string{"Origin": "http://rebind.attacker.example:3050"}
	// The rebinding pair: same-origin by the browser's own rules, from an address in range.
	if rec := b.from(inRange, "GET", "/api/auth/session", reqOpts{host: "rebind.attacker.example:3050", headers: evil}); rec.Code != 401 {
		t.Errorf("bypass token endpoint with a foreign Host: %d %s", rec.Code, rec.Body)
	}
	if rec := b.from(inRange, "POST", "/api/add", reqOpts{host: "rebind.attacker.example:3050", headers: evil, csrf: b.token(inRange, nil), body: "{}"}); rec.Code != 401 {
		t.Errorf("state change with a foreign Host: %d", rec.Code)
	}
	// A real session still works on any host: the cookie is bound to the host.
	if rec := b.harness.do("GET", "/api/auth/session", reqOpts{host: "anything.example.com", cookie: b.acc.Cookie}); rec.Code != 200 {
		t.Errorf("a session cookie was refused by the host check: %d", rec.Code)
	}
}

func TestEnablingTheBypassAddsTheRequestsHost(t *testing.T) {
	b := newBypass(t, false, netaccess.Input{})
	put := func(host, body string) map[string]any {
		t.Helper()
		rec := b.harness.do("PUT", "/api/admin/network", reqOpts{cookie: b.acc.Cookie, csrf: b.acc.CSRF, host: host, body: body})
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return decode(t, rec)
	}
	m := put("Nas.Example.Lan:3050", jsonBody("enabled", true, "cidrs", []string{"198.51.100.0/24"}))
	if m["addedHost"] != "nas.example.lan" || len(m["allowedHosts"].([]any)) != 1 {
		t.Fatalf("addedHost: %v", m)
	}
	if rec := b.from(inRange, "GET", "/api/status", reqOpts{host: "nas.example.lan:3050"}); rec.Code != 200 {
		t.Errorf("the host the admin used does not work: %d", rec.Code)
	}
	// Saving again while already enabled adds nothing; a single-label or IP host needs nothing.
	if m := put("other.example.lan", jsonBody("enabled", true, "cidrs", []string{"198.51.100.0/24"})); m["addedHost"] != "" {
		t.Errorf("added on a save that did not enable: %v", m["addedHost"])
	}
	put("nas", jsonBody("enabled", false, "cidrs", []string{"198.51.100.0/24"}))
	if m := put("192.0.2.1:3050", jsonBody("enabled", true, "cidrs", []string{"198.51.100.0/24"})); m["addedHost"] != "" || len(m["allowedHosts"].([]any)) != 1 {
		t.Errorf("an IP host was added: %v", m)
	}
	// The list can be replaced and is validated.
	for name, hosts := range map[string][]string{
		"star": {"*"}, "bare tld": {"*.com"}, "mid wild": {"a.*.example.lan"}, "ip": {"192.0.2.1"},
		"underscore": {"a_b.example.lan"}, "path": {"nas.example.lan/x"}, "port": {"nas.example.lan:3050"},
		"single": {"nas"}, "too many": manyHosts(65), "long label": {strings.Repeat("a", 64) + ".example.lan"},
		"unicode": {"nás.example.lan"}, "space": {"a b.example.lan"}, "leading dot": {".example.lan"},
		"empty": {""}, "double wild": {"*.*.example.lan"},
	} {
		if rec := b.admin("PUT", "/api/admin/network", jsonBody("enabled", false, "cidrs", []string{}, "allowedHosts", hosts)); rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	m = put("nas", jsonBody("enabled", false, "cidrs", []string{}, "allowedHosts", []string{"A.Example.Lan.", "a.example.lan"}))
	if l := m["allowedHosts"].([]any); len(l) != 1 || l[0] != "a.example.lan" {
		t.Errorf("not normalised: %v", l)
	}
	// An update that leaves allowedHosts out keeps the list.
	m = put("nas", jsonBody("enabled", false, "cidrs", []string{}))
	if len(m["allowedHosts"].([]any)) != 1 {
		t.Errorf("list lost: %v", m)
	}
}

func TestNetworkAPIShowsHostStateAndEnvEntriesReadOnly(t *testing.T) {
	b := newBypass(t, true, netaccess.Input{AllowedHosts: &[]string{"nas.example.lan"}})
	b.net.SetEnvHosts([]string{"env.example.lan"})
	rec := b.harness.do("GET", "/api/admin/network", reqOpts{cookie: b.acc.Cookie, host: "rebind.attacker.example"})
	m := decode(t, rec)
	c := m["client"].(map[string]any)
	if c["host"] != "rebind.attacker.example" || c["hostAllowed"] != false {
		t.Errorf("client: %v", c)
	}
	if h := m["envAllowedHosts"].([]any); len(h) != 1 || h[0] != "env.example.lan" {
		t.Errorf("env hosts: %v", h)
	}
	if h := m["allowedHosts"].([]any); len(h) != 1 || h[0] != "nas.example.lan" {
		t.Errorf("stored hosts (must not include env ones): %v", h)
	}
}

func manyHosts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("h%d.example.lan", i)
	}
	return out
}
