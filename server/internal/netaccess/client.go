package netaccess

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Client is where a request came from.
type Client struct {
	// Peer is the TCP peer, normalised (IPv4-mapped unmapped, no zone). Invalid
	// when RemoteAddr could not be parsed.
	Peer netip.Addr
	// Addr is the client address: the peer, or, when the peer is a trusted proxy,
	// the address its forwarded headers name. Invalid when it cannot be told.
	Addr netip.Addr
	// Forwarded reports that Addr came from forwarded headers.
	Forwarded bool
	// Ignored reports that forwarded headers were present and not believed because
	// the peer is not a trusted proxy.
	Ignored bool
	// Unknown reports that the peer is trusted but its headers were malformed or
	// contradicted each other, so no address is claimed.
	Unknown bool
}

// String is the address to record: Addr, else the peer, else "".
func (c Client) String() string {
	switch {
	case c.Addr.IsValid():
		return c.Addr.String()
	case c.Peer.IsValid():
		return c.Peer.String()
	}
	return ""
}

// IsTrustedProxy reports whether a is in the environment list or the stored one.
// The two sources are a union: an address trusted by either is trusted.
func (s *Service) IsTrustedProxy(a netip.Addr) bool {
	a = normalize(a)
	for _, p := range s.env {
		if p.Contains(a) {
			return true
		}
	}
	for _, p := range s.cur.Load().Proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Bypasses reports whether the bypass is on and c is inside its ranges. An
// unknown client address is never inside a range.
func (s *Service) Bypasses(c Client) bool {
	st := s.cur.Load()
	if !st.Enabled || !c.Addr.IsValid() {
		return false
	}
	for _, p := range st.Bypass {
		if p.Contains(c.Addr) {
			return true
		}
	}
	return false
}

func normalize(a netip.Addr) netip.Addr { return a.WithZone("").Unmap() }

// Forwarded-header limits: a longer value is treated as malformed.
const (
	maxHeaderBytes = 8 << 10
	maxChain       = 64
)

// Resolve decides the client address of r.
//
// The TCP peer is the client unless it is a trusted proxy. For a trusted peer
// the forwarded headers (X-Forwarded-For, Forwarded, X-Real-IP) are read; of
// each chain only the right-most entry that is not itself a trusted proxy is
// believed (each proxy appends the address it saw, so everything to the left of
// the first untrusted hop is whatever the client claimed). When several headers
// are present they must name the same client; a malformed value or a
// disagreement makes the client unknown rather than guessed.
func (s *Service) Resolve(r *http.Request) Client {
	peer, ok := parseHostPort(r.RemoteAddr)
	if !ok {
		return Client{}
	}
	c := Client{Peer: peer, Addr: peer}
	xff, fwd, realIP := r.Header.Values("X-Forwarded-For"), r.Header.Values("Forwarded"), r.Header.Values("X-Real-IP")
	present := len(xff) > 0 || len(fwd) > 0 || len(realIP) > 0
	if !s.IsTrustedProxy(peer) {
		c.Ignored = present
		return c
	}
	if !present {
		return c // the proxy host itself, talking to us directly
	}
	fail := func() Client {
		c.Addr, c.Unknown = netip.Addr{}, true
		return c
	}
	var found []netip.Addr
	if len(xff) > 0 {
		chain, ok := parseChain(strings.Join(xff, ","))
		if !ok {
			return fail()
		}
		found = append(found, s.rightmostUntrusted(chain))
	}
	if len(fwd) > 0 {
		chain, ok := parseForwarded(strings.Join(fwd, ","))
		if !ok {
			return fail()
		}
		found = append(found, s.rightmostUntrusted(chain))
	}
	if len(realIP) > 0 {
		if len(realIP) != 1 {
			return fail()
		}
		a, ok := parseNode(realIP[0])
		if !ok {
			return fail()
		}
		found = append(found, a)
	}
	for _, a := range found[1:] {
		if a != found[0] {
			return fail()
		}
	}
	c.Addr, c.Forwarded = found[0], true
	return c
}

// rightmostUntrusted walks a chain from the right and returns the first address
// that is not a trusted proxy; if every hop is trusted, the left-most one (the
// original client is itself inside the trusted network).
func (s *Service) rightmostUntrusted(chain []netip.Addr) netip.Addr {
	for i := len(chain) - 1; i >= 0; i-- {
		if !s.IsTrustedProxy(chain[i]) {
			return chain[i]
		}
	}
	return chain[0]
}

func parseHostPort(s string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		host = s
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return normalize(a), true // a local peer may carry a zone; it names the interface, not the client
}

// parseChain splits a comma-separated list and parses every element; an empty
// list or element is malformed.
func parseChain(v string) ([]netip.Addr, bool) {
	if len(v) > maxHeaderBytes {
		return nil, false
	}
	parts := strings.Split(v, ",")
	if len(parts) > maxChain {
		return nil, false
	}
	out := make([]netip.Addr, 0, len(parts))
	for _, p := range parts {
		a, ok := parseNode(p)
		if !ok {
			return nil, false
		}
		out = append(out, a)
	}
	return out, true
}

// parseNode parses one forwarded node: an IPv4 or IPv6 address, optionally with
// a port, IPv6 optionally in brackets. Zone ids, host names, "unknown" and
// obfuscated identifiers (RFC 7239 "_hidden") are malformed.
func parseNode(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 {
		return netip.Addr{}, false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return finishNode(a)
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return finishNode(ap.Addr())
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") { // "[::1]" without a port
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return finishNode(a)
		}
	}
	return netip.Addr{}, false
}

func finishNode(a netip.Addr) (netip.Addr, bool) {
	if a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// parseForwarded reads the for= parameter of every element of an RFC 7239
// Forwarded header. An element without for= is malformed.
func parseForwarded(v string) ([]netip.Addr, bool) {
	if len(v) > maxHeaderBytes {
		return nil, false
	}
	elems := strings.Split(v, ",")
	if len(elems) > maxChain {
		return nil, false
	}
	out := make([]netip.Addr, 0, len(elems))
	for _, e := range elems {
		var node string
		found := false
		for _, pair := range strings.Split(e, ";") {
			k, val, ok := strings.Cut(pair, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "for") {
				continue
			}
			if found {
				return nil, false // two for= in one element
			}
			found = true
			node = strings.TrimSpace(val)
			if len(node) >= 2 && node[0] == '"' && node[len(node)-1] == '"' {
				node = node[1 : len(node)-1]
			}
		}
		if !found {
			return nil, false
		}
		a, ok := parseNode(node)
		if !ok {
			return nil, false
		}
		out = append(out, a)
	}
	return out, true
}
