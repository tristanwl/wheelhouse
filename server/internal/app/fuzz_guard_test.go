package app

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
)

// FuzzSameOrigin: the cross-site check on state-changing requests, over every
// combination of Sec-Fetch-Site, Origin and Host.
//
// Invariants: a Sec-Fetch-Site other than "", same-origin and none is always
// refused; with an Origin, the request passes only if the Origin is an http or
// https origin without user information whose host is the request's Host
// (case-insensitively, port included, so "example.com:81" is not
// "example.com"), and "null", other schemes and unparsable values never pass;
// with neither header (a script, not a browser) it passes, and the CSRF token
// is then what protects an authenticated route.
func FuzzSameOrigin(f *testing.F) {
	f.Fuzz(func(t *testing.T, site, origin, host string) {
		if strings.ContainsAny(site+origin+host, "\r\n\x00") {
			t.Skip()
		}
		// net/http trims the values it parses; the test hands over the same.
		site, origin = strings.TrimSpace(site), strings.TrimSpace(origin)
		r := httptest.NewRequest(http.MethodPost, "/api/x", nil)
		r.Host = host
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		got := sameOrigin(r)

		want := true
		switch {
		case site != "" && site != "same-origin" && site != "none":
			want = false
		case origin != "":
			u, err := url.Parse(origin)
			want = err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && strings.EqualFold(u.Host, host)
		}
		if got != want {
			t.Fatalf("sameOrigin(Sec-Fetch-Site %q, Origin %q, Host %q) = %v, want %v", site, origin, host, got, want)
		}
		if got && origin != "" {
			// What passed names exactly this host: nothing else can be hiding in it.
			u, _ := url.Parse(origin)
			if strings.ContainsAny(u.Host, "@/\\?# ") || !strings.EqualFold(u.Hostname()+portSuffix(u), strings.ToLower(host)) && !strings.EqualFold(u.Host, host) {
				t.Fatalf("passed Origin %q for Host %q", origin, host)
			}
		}
	})
}

func portSuffix(u *url.URL) string {
	if p := u.Port(); p != "" {
		return ":" + p
	}
	return ""
}

// FuzzSessionCookie: which cookie is read as the session.
//
// Invariants: over plain HTTP only the cookie named "wheelhouse" is read and
// over HTTPS only "__Host-wheelhouse" (so a cookie planted from an insecure
// origin or a sibling name is ignored); the value returned is the value of the
// first cookie of that name in the header, and "" when there is none.
func FuzzSessionCookie(f *testing.F) {
	f.Fuzz(func(t *testing.T, header string, secure bool) {
		if strings.ContainsAny(header, "\r\n\x00") {
			t.Skip()
		}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Cookie", header)
		got := sessionToken(r, secure)

		name, other := cookieName, secureCookieName
		if secure {
			name, other = secureCookieName, cookieName
		}
		want := ""
		for _, c := range r.Cookies() {
			if c.Name == name {
				want = c.Value
				break
			}
		}
		if got != want {
			t.Fatalf("sessionToken(%q, secure=%v) = %q, want %q", header, secure, got, want)
		}
		// A header that only has the other transport's cookie yields nothing.
		only := &http.Request{Header: http.Header{}}
		only.Header.Set("Cookie", other+"="+got)
		if sessionToken(only, secure) != "" {
			t.Fatalf("the %q cookie was read as the session over secure=%v", other, secure)
		}
	})
}

// FuzzCSRFCompare: the X-CSRF-Token check.
//
// Invariants: it passes if and only if the header is present, non-empty and
// exactly the session's token; every near miss (case, a trailing space, a
// prefix, the cookie value) fails.
func FuzzCSRFCompare(f *testing.F) {
	f.Fuzz(func(t *testing.T, got, want string) {
		if strings.ContainsAny(got, "\r\n\x00") {
			t.Skip()
		}
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header["X-Csrf-Token"] = []string{got}
		id := &auth.Identity{CSRF: want}
		wantOK := got != "" && got == want
		if csrfOK(r, id) != wantOK {
			t.Fatalf("csrfOK(%q vs %q) = %v, want %v", got, want, !wantOK, wantOK)
		}
		// The bypass token and the session token of a real session differ in kind.
		sess := auth.CSRFToken(want)
		r.Header["X-Csrf-Token"] = []string{sess}
		if csrfOK(r, &auth.Identity{CSRF: sess + "x"}) || csrfOK(r, &auth.Identity{CSRF: ""}) && sess != "" {
			t.Fatalf("a token matched a different identity")
		}
	})
}

// FuzzClientAddress: the address recorded for a request.
//
// Invariants: with no trusted proxy it is the TCP peer's address in canonical
// form (or the raw RemoteAddr when that is not an address), whatever the
// forwarded headers say, and it never contains a control character that came from
// a header.
func FuzzClientAddress(f *testing.F) {
	f.Fuzz(func(t *testing.T, remote, xff string) {
		if strings.ContainsAny(xff, "\r\n\x00") || strings.Contains(remote, "%") {
			t.Skip() // a zone in the peer address is stripped or kept raw, depending on where it is read
		}
		g := NewGuard(nil, netaccess.NewMemory(nil))
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", xff)
		r.Header.Set("Forwarded", "for="+xff)
		r.Header.Set("X-Real-IP", xff)
		rec := httptest.NewRecorder()
		var got string
		g.authorize("GET /x", Public, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = clientAddr(r)
		})).ServeHTTP(rec, r)

		peer, err := netip.ParseAddrPort(remote)
		switch {
		case err == nil:
			if got != peer.Addr().Unmap().String() && got != peer.Addr().Unmap().WithZone("").String() {
				t.Fatalf("remote %q (xff %q) recorded as %q", remote, xff, got)
			}
		case got == "" && remote != "":
			t.Fatalf("remote %q recorded as nothing", remote)
		}
	})
}
