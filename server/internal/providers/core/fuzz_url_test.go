package core

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type urlConfig struct {
	URL string `json:"url" config:"required,url"`
}

// FuzzInstanceURL feeds arbitrary text to the instance URL validation that every
// provider config goes through, and joins API paths onto whatever it accepts.
//
// Invariants: an accepted URL is an absolute http or https URL with a host, no
// user name or password, no query and no fragment; JoinURL on it keeps the
// scheme, host and port of the base for every path (a path cannot move the
// request to another host), and keeps the base's path prefix unless the path
// itself climbs out with "..", which no caller passes; a refusal is a
// *ValidationError whose text does not contain the value.
func FuzzInstanceURL(f *testing.F) {
	spec, err := newConfigSpec(urlConfig{})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw, path string) {
		out, derr := spec.decode(map[string]any{"url": raw})
		if derr != nil {
			ve, ok := derr.(*ValidationError)
			if !ok {
				t.Fatalf("error is %T, want *ValidationError", derr)
			}
			if len(raw) >= 8 && strings.Contains(ve.Error(), raw) {
				t.Fatalf("validation error quotes the value: %q", ve.Error())
			}
			return
		}
		got := out.(urlConfig).URL
		if got != raw {
			t.Fatalf("accepted URL was altered: %q -> %q", raw, got)
		}
		u, perr := url.Parse(got)
		if perr != nil {
			t.Fatalf("accepted a URL url.Parse refuses: %v", perr)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			t.Fatalf("accepted scheme %q host %q", u.Scheme, u.Hostname())
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.ContainsAny(got, "?#") {
			t.Fatalf("accepted a URL with userinfo, query or fragment: %q", got)
		}
		if strings.ContainsAny(got, "\x00\r\n\t ") || strings.ContainsRune(got, '\\') {
			t.Fatalf("accepted a URL with control characters, spaces or a backslash: %q", got)
		}

		joined := JoinURL(got, path)
		ju, jerr := url.Parse(joined)
		if jerr != nil {
			return // an unusable path is refused later, by fullURL, as an error
		}
		if ju.Scheme != u.Scheme || ju.Host != u.Host || ju.User != nil {
			t.Fatalf("JoinURL(%q, %q) = %q moved the request to %s://%s (userinfo %v)", got, path, joined, ju.Scheme, ju.Host, ju.User)
		}
		if !strings.Contains(path, "..") && !strings.ContainsAny(path, "?#%\\") {
			prefix := strings.TrimRight(u.Path, "/")
			if jp := ju.Path; jp != prefix && !strings.HasPrefix(jp, prefix+"/") {
				t.Fatalf("JoinURL(%q, %q) = %q left the base path %q", got, path, joined, prefix)
			}
		}
	})
}

// FuzzJoinURL: JoinURL on arbitrary text never panics and always has exactly one
// slash at the join (so neither side's slashes can merge into a "//host" form).
func FuzzJoinURL(f *testing.F) {
	f.Fuzz(func(t *testing.T, base, path string) {
		got := JoinURL(base, path)
		if !strings.HasPrefix(got, strings.TrimRight(base, "/")+"/") {
			t.Fatalf("JoinURL(%q, %q) = %q does not start with the base", base, path, got)
		}
		rest := got[len(strings.TrimRight(base, "/"))+1:]
		if rest != strings.TrimLeft(path, "/") {
			t.Fatalf("JoinURL(%q, %q) = %q: path part %q", base, path, got, rest)
		}
	})
}

// withDenied runs f with the denied networks set to list, then restores them.
func withDenied(list []netip.Prefix, f func()) {
	old := *deniedNetworks.Load()
	SetDeniedNetworks(list)
	defer SetDeniedNetworks(old)
	f()
}

var (
	v4OnlyList = []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("100.100.100.200/32"), netip.MustParsePrefix("10.0.0.0/8")}
)

// FuzzAddressDenied checks the dial-time address policy.
//
// Invariants: it never panics on any string; a string that is not an IP literal
// with a port is denied; an IPv4-mapped IPv6 address gets exactly the verdict of
// the plain IPv4 address, with or without a zone; an address in the NAT64
// well-known prefix is denied whenever the IPv4 address inside it is (and with a
// list that names IPv4 networks only, gets exactly that verdict); and the
// default list always denies every address of the networks it names, in every one
// of these embeddings.
func FuzzAddressDenied(f *testing.F) {
	f.Fuzz(func(t *testing.T, address string, a, b, c, d byte, port uint16, custom bool) {
		_ = addressDenied(address)
		if _, err := netip.ParseAddrPort(address); err != nil && !addressDenied(address) {
			t.Fatalf("%q is not an IP literal and was allowed", address)
		}

		v4 := netip.AddrFrom4([4]byte{a, b, c, d})
		hostport := func(ip netip.Addr, zone string) string {
			return netip.AddrPortFrom(ip.WithZone(zone), port).String()
		}
		mapped := netip.AddrFrom16(v4.As16())
		var n64 [16]byte
		copy(n64[:], []byte{0, 0x64, 0xff, 0x9b})
		n64[12], n64[13], n64[14], n64[15] = a, b, c, d
		nat64 := netip.AddrFrom16(n64)

		list := DefaultDeniedNetworks()
		if custom {
			list = v4OnlyList // IPv4 networks only: no NAT64 prefix of its own
		}
		withDenied(list, func() {
			plain := addressDenied(hostport(v4, ""))
			for _, zone := range []string{"", "eth0"} {
				if got := addressDenied(hostport(mapped, zone)); got != plain {
					t.Fatalf("mapped %s denied=%v, plain %s denied=%v", hostport(mapped, zone), got, v4, plain)
				}
			}
			n := addressDenied(hostport(nat64, ""))
			if plain && !n {
				t.Fatalf("%s is denied but its NAT64 form %s is allowed", v4, nat64)
			}
			if custom && n != plain {
				t.Fatalf("custom list: NAT64 form %s denied=%v, plain %s denied=%v", nat64, n, v4, plain)
			}
			if !custom && !n {
				t.Fatalf("default list allowed %s inside the NAT64 prefix", nat64)
			}
			for _, p := range list {
				if p.Addr().Is4() && p.Contains(v4) && !(plain && n) {
					t.Fatalf("%s is inside denied %s but plain=%v nat64=%v", v4, p, plain, n)
				}
			}
		})
	})
}

// FuzzParseDeniedNetworks: the operator-supplied list parses to masked,
// valid prefixes, or fails with an error that names the entry.
func FuzzParseDeniedNetworks(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		list, err := ParseDeniedNetworks(s)
		if err != nil {
			if !strings.Contains(err.Error(), "is not an address or CIDR range") {
				t.Fatalf("unexpected error %q", err)
			}
			return
		}
		for _, p := range list {
			if !p.IsValid() || p != p.Masked() {
				t.Fatalf("%q produced %v (valid=%v masked=%v)", s, p, p.IsValid(), p.Masked())
			}
		}
		if strings.TrimSpace(strings.ToLower(s)) == "none" && list != nil {
			t.Fatalf("none produced %v", list)
		}
	})
}
