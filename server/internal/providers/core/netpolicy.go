package core

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
)

// Instance URLs are typed by an administrator and the server requests them, so
// they are trusted input: an admin can point Wheelhouse at any host it can
// reach, and private LAN ranges are the whole point of a media stack. What the
// server refuses by default is the address family that is almost never a media
// service and often a credential store: link-local addresses, where cloud
// platforms serve instance metadata (169.254.169.254 and its IPv6 twins). The
// check runs on the address actually being dialled, after DNS, so a name that
// resolves there (or is rebound there) is refused too, and it covers redirects.
// Loopback and private ranges stay allowed.

// ErrAddressDenied is the error of a connection to a denied network.
var ErrAddressDenied = errors.New("address not allowed")

// DefaultDeniedNetworks are refused unless configured otherwise: IPv4 and IPv6
// link-local, the EC2 IPv6 metadata address, the NAT64 well-known prefix (a
// DNS64 gateway would translate 64:ff9b::a9fe:a9fe to 169.254.169.254), and the
// metadata addresses of Alibaba Cloud (100.100.100.200, inside the carrier-grade
// NAT range) and Oracle Cloud (192.0.0.192).
func DefaultDeniedNetworks() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("fd00:ec2::254/128"),
		nat64Prefix,
		netip.MustParsePrefix("100.100.100.200/32"),
		netip.MustParsePrefix("192.0.0.192/32"),
	}
}

// nat64Prefix is the well-known NAT64 prefix (RFC 6052). An address in it
// embeds an IPv4 address in its last four bytes.
var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

// embeddedNAT64 returns the IPv4 address inside a NAT64 well-known address.
func embeddedNAT64(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() || !nat64Prefix.Contains(ip) {
		return netip.Addr{}, false
	}
	b := ip.As16()
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
}

var deniedNetworks atomic.Pointer[[]netip.Prefix]

func init() { SetDeniedNetworks(DefaultDeniedNetworks()) }

// SetDeniedNetworks replaces the networks provider clients refuse to connect to
// (nil allows everything). It applies to every client, existing ones included.
func SetDeniedNetworks(p []netip.Prefix) {
	cp := append([]netip.Prefix(nil), p...)
	deniedNetworks.Store(&cp)
}

// ParseDeniedNetworks parses a comma-separated list of CIDR ranges or addresses
// (WHEELHOUSE_DENIED_NETWORKS). Empty means the defaults; "none" allows every
// address.
func ParseDeniedNetworks(s string) ([]netip.Prefix, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "":
		return DefaultDeniedNetworks(), nil
	case "none":
		return nil, nil
	}
	var out []netip.Prefix
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
		} else if a, aerr := netip.ParseAddr(part); aerr == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		} else {
			return nil, fmt.Errorf("%q is not an address or CIDR range", part)
		}
	}
	return out, nil
}

// addressDenied reports whether address (host:port with an IP literal, as the
// dialer's Control hook sees it) is in a denied network.
func addressDenied(address string) bool {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return true // not an IP literal after resolution: do not guess
	}
	ip := ap.Addr().Unmap().WithZone("")
	denied := *deniedNetworks.Load()
	// A NAT64 address stands for the IPv4 address inside it: apply the IPv4
	// rules to that too, so a custom list that names only 169.254.0.0/16 still
	// refuses 64:ff9b::a9fe:a9fe.
	embedded, isNAT64 := embeddedNAT64(ip)
	for _, p := range denied {
		if p.Contains(ip) || (isNAT64 && p.Contains(embedded)) {
			return true
		}
	}
	return false
}

// controlDial is the net.Dialer Control hook: it runs after name resolution and
// before connect.
func controlDial(_, address string, _ syscall.RawConn) error {
	if addressDenied(address) {
		return ErrAddressDenied
	}
	return nil
}
