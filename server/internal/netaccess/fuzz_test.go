package netaccess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"unicode"
)

// FuzzParseRange: a bypass or proxy range is canonical or refused.
//
// Invariants: an accepted range is valid, masked, free of a zone and never an
// IPv4-mapped IPv6 prefix; it contains the address that was typed; a bare
// address is a single-address range; the canonical text parses back to the same
// range; and the error text is short (it quotes at most a clipped prefix).
func FuzzParseRange(f *testing.F) {
	f.Fuzz(func(t *testing.T, text string) {
		p, err := ParseRange(text)
		if err != nil {
			if len(err.Error()) > 4*(64+3)+120 { // the clipped text, quoted (a byte can take four characters)
				t.Fatalf("error text of %d bytes", len(err.Error()))
			}
			return
		}
		if !p.IsValid() || p != p.Masked() || p.Addr().Zone() != "" || p.Addr().Is4In6() {
			t.Fatalf("%q produced a non-canonical %v", text, p)
		}
		back, err := ParseRange(p.String())
		if err != nil || back != p {
			t.Fatalf("%q -> %v does not round-trip: %v %v", text, p, back, err)
		}
		trimmed := strings.TrimSpace(text)
		if !strings.Contains(trimmed, "/") {
			a, aerr := netip.ParseAddr(trimmed)
			if aerr != nil {
				t.Fatalf("accepted %q as an address range but it is not an address", text)
			}
			if p.Bits() != p.Addr().BitLen() || p.Addr() != a.Unmap() {
				t.Fatalf("single address %q became %v", text, p)
			}
			return
		}
		typed, perr := netip.ParsePrefix(trimmed)
		if perr != nil {
			t.Fatalf("accepted %q but netip refuses it: %v", text, perr)
		}
		if !p.Contains(typed.Addr().Unmap()) {
			t.Fatalf("%v does not contain the typed address of %q", p, text)
		}
	})
}

// FuzzUpdateLists: the lists an administrator saves. Accepted lists are
// canonical, within bounds, never match every address, and stable under a second
// save of what was returned.
func FuzzUpdateLists(f *testing.F) {
	f.Fuzz(func(t *testing.T, a, b, c, d string, enabled bool) {
		s := NewMemory(nil)
		in := Input{Enabled: enabled, CIDRs: []string{a, b}, TrustedProxies: []string{c, d}}
		st, warnings, err := s.Update(context.Background(), in)
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error is %T, want *ValidationError", err)
			}
			if got := s.Settings(); got.Enabled || len(got.Bypass) != 0 {
				t.Fatalf("a refused update changed the settings: %+v", got)
			}
			return
		}
		for _, p := range append(append([]netip.Prefix(nil), st.Bypass...), st.Proxies...) {
			if p.Bits() == 0 || p != p.Masked() || p.Addr().Is4In6() {
				t.Fatalf("stored a non-canonical or match-everything range %v", p)
			}
		}
		if st.Enabled && len(st.Bypass) == 0 {
			t.Fatal("enabled with no ranges")
		}
		_ = warnings
		again, _, err := s.Update(context.Background(), Input{Enabled: st.Enabled, CIDRs: Strings(st.Bypass), TrustedProxies: Strings(st.Proxies)})
		if err != nil || strings.Join(Strings(again.Bypass), ",") != strings.Join(Strings(st.Bypass), ",") ||
			strings.Join(Strings(again.Proxies), ",") != strings.Join(Strings(st.Proxies), ",") {
			t.Fatalf("saving the stored lists again changed them: %v %v", again, err)
		}
	})
}

// addrFromByte maps a byte to one of 256 addresses: half in 10.0.0.0/24 (which
// the tests below trust as a proxy network), the rest in documentation space,
// and some IPv6.
func addrFromByte(b byte) netip.Addr {
	switch b % 4 {
	case 0:
		return netip.AddrFrom4([4]byte{10, 0, 0, b >> 2})
	case 1:
		return netip.AddrFrom4([4]byte{203, 0, 113, b >> 2})
	case 2:
		return netip.AddrFrom16([16]byte{0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, b >> 2})
	default:
		return netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, b >> 2})
	}
}

// FuzzResolveChain builds forwarded headers from a generated chain of
// addresses and checks the right-most-untrusted walk against an oracle.
//
// Invariants: from a peer that is not a trusted proxy the client is the peer,
// whatever the headers say; from a trusted peer it is the right-most address of
// the chain that is not itself a trusted proxy (the left-most when every hop is
// trusted), in whichever of the supported header forms the chain arrives, and an
// X-Forwarded-For built from the same chain agrees with Forwarded; a forged
// X-Real-IP that disagrees with the chain leaves the client unknown, and an
// unknown client never gets the bypass.
func FuzzResolveChain(f *testing.F) {
	f.Fuzz(func(t *testing.T, peerByte byte, hops []byte, style uint8, split bool, fromEnv bool) {
		if len(hops) > 70 {
			hops = hops[:70]
		}
		trustedNets := []string{"10.0.0.0/24", "fd00::/120"}
		var env []netip.Prefix
		var stored []string
		if fromEnv {
			env = mustPrefixes(trustedNets...)
		} else {
			stored = trustedNets
		}
		s := NewMemory(env)
		if _, _, err := s.Update(context.Background(), Input{Enabled: true, CIDRs: []string{"203.0.113.0/24", "2001:db8::/32"}, TrustedProxies: stored}); err != nil {
			t.Fatal(err)
		}
		isTrusted := func(a netip.Addr) bool {
			for _, n := range mustPrefixes(trustedNets...) {
				if n.Contains(a) {
					return true
				}
			}
			return false
		}

		peer := addrFromByte(peerByte)
		chain := make([]netip.Addr, len(hops))
		parts := make([]string, len(hops))
		fparts := make([]string, len(hops))
		for i, b := range hops {
			chain[i] = addrFromByte(b)
			parts[i] = chain[i].String()
			fparts[i] = "for=" + chain[i].String()
			if chain[i].Is6() {
				fparts[i] = `for="[` + chain[i].String() + `]:443"`
			}
		}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = netip.AddrPortFrom(peer, 51000).String()
		switch style % 3 {
		case 0:
			if split {
				for _, p := range parts {
					r.Header.Add("X-Forwarded-For", p)
				}
			} else if len(parts) > 0 {
				r.Header.Set("X-Forwarded-For", strings.Join(parts, ", "))
			}
		case 1:
			if len(fparts) > 0 {
				r.Header.Set("Forwarded", strings.Join(fparts, ", "))
			}
		default:
			if len(parts) > 0 {
				r.Header.Set("X-Forwarded-For", strings.Join(parts, ","))
				r.Header.Set("Forwarded", strings.Join(fparts, ","))
			}
		}

		c := s.Resolve(r)
		if !isTrusted(peer) {
			if c.Addr != peer || c.Forwarded || c.Unknown {
				t.Fatalf("untrusted peer %v: client %+v", peer, c)
			}
			if len(hops) > 0 && !c.Ignored {
				t.Fatalf("forwarded headers from an untrusted peer were not reported as ignored")
			}
			return
		}
		if len(chain) == 0 {
			if c.Addr != peer || c.Forwarded {
				t.Fatalf("no headers: client %+v", c)
			}
			return
		}
		want := chain[0]
		for i := len(chain) - 1; i >= 0; i-- {
			if !isTrusted(chain[i]) {
				want = chain[i]
				break
			}
		}
		if len(chain) > maxChain {
			if !c.Unknown || c.Addr.IsValid() {
				t.Fatalf("a chain of %d hops was believed: %+v", len(chain), c)
			}
			return
		}
		if c.Addr != want || !c.Forwarded || c.Unknown {
			t.Fatalf("peer %v chain %v: client %+v, want %v", peer, chain, c, want)
		}

		// A forged X-Real-IP that differs from the walk makes the client unknown.
		forged := addrFromByte(peerByte ^ 0x55)
		r.Header.Set("X-Real-IP", forged.String())
		c2 := s.Resolve(r)
		if forged != want {
			if !c2.Unknown || c2.Addr.IsValid() || s.Bypasses(c2) {
				t.Fatalf("contradicting X-Real-IP %v against %v: %+v", forged, want, c2)
			}
		} else if c2.Addr != want {
			t.Fatalf("agreeing X-Real-IP changed the client: %+v", c2)
		}
	})
}

// FuzzResolveRaw feeds arbitrary strings as the peer address and every
// forwarded header.
//
// Invariants: with no trusted proxy configured the client is always the TCP
// peer, whatever the headers say; the client is never an address taken from a
// header unless the peer is a trusted proxy, and then it is an address that
// appears in the headers; an unparsable peer yields no client; the client is
// canonical (unmapped, no zone); and an unknown client matches no range.
func FuzzResolveRaw(f *testing.F) {
	f.Fuzz(func(t *testing.T, remote, xff1, xff2, fwd, realIP string, trustAll bool) {
		var env []netip.Prefix
		if trustAll {
			env = mustPrefixes("10.0.0.0/8", "fd00::/8", "::1/128", "127.0.0.0/8")
		}
		s := NewMemory(env)
		// Everything is in the bypass range, so that "no bypass" means "no client".
		if _, _, err := s.Update(context.Background(), Input{Enabled: true, CIDRs: []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}}); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		var headers []string
		for _, kv := range [][2]string{{"X-Forwarded-For", xff1}, {"X-Forwarded-For", xff2}, {"Forwarded", fwd}, {"X-Real-IP", realIP}} {
			if kv[1] == "" || strings.ContainsAny(kv[1], "\r\n\x00") {
				continue
			}
			r.Header.Add(kv[0], kv[1])
			headers = append(headers, kv[1])
		}
		c := s.Resolve(r)

		if !c.Peer.IsValid() {
			if c.Addr.IsValid() || c.Forwarded || s.Bypasses(c) {
				t.Fatalf("unparsable peer %q gave client %+v", remote, c)
			}
			return
		}
		if c.Peer.Is4In6() || c.Peer.Zone() != "" || c.Addr.Zone() != "" || c.Addr.Is4In6() {
			t.Fatalf("non-canonical peer or client: %+v", c)
		}
		if !s.IsTrustedProxy(c.Peer) {
			if c.Addr != c.Peer || c.Forwarded || c.Unknown {
				t.Fatalf("peer %v is not a proxy but client is %+v (headers %q)", c.Peer, c, headers)
			}
			return
		}
		if c.Unknown {
			if c.Addr.IsValid() || s.Bypasses(c) {
				t.Fatalf("unknown client with an address or a bypass: %+v", c)
			}
			return
		}
		if c.Forwarded && !appearsIn(c.Addr, headers) {
			t.Fatalf("client %v does not appear in the headers %q", c.Addr, headers)
		}
		if !c.Forwarded && c.Addr != c.Peer {
			t.Fatalf("not forwarded but client %v differs from the peer %v", c.Addr, c.Peer)
		}
	})
}

// appearsIn reports whether a appears, in any textual form, among the tokens of
// the header values.
func appearsIn(a netip.Addr, values []string) bool {
	for _, v := range values {
		for _, tok := range strings.FieldsFunc(v, func(r rune) bool { return strings.ContainsRune(",;=\"[]", r) || unicode.IsSpace(r) }) {
			cands := []string{tok}
			if i := strings.LastIndex(tok, ":"); i > 0 {
				cands = append(cands, tok[:i])
			}
			for _, c := range cands {
				if p, err := netip.ParseAddr(c); err == nil && p.Unmap().WithZone("") == a {
					return true
				}
			}
		}
	}
	return false
}

// FuzzTrustedProxyWithoutProxies: with no trusted proxy, the client is the peer
// for every request; the one-line form of the property above, run on raw
// RemoteAddr values only.
func FuzzNoTrustedProxies(f *testing.F) {
	f.Fuzz(func(t *testing.T, remote, xff, fwd string) {
		s := NewMemory(nil)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if !strings.ContainsAny(xff+fwd, "\r\n\x00") {
			r.Header.Set("X-Forwarded-For", xff)
			r.Header.Set("Forwarded", fwd)
			r.Header.Set("X-Real-IP", xff)
		}
		c := s.Resolve(r)
		if c.Forwarded || c.Unknown {
			t.Fatalf("client taken from a header with no trusted proxy: %+v", c)
		}
		if c.Addr.IsValid() && c.Addr != c.Peer {
			t.Fatalf("client %v is not the peer %v", c.Addr, c.Peer)
		}
	})
}
