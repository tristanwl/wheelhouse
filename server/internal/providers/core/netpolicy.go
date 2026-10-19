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
// link-local, and the EC2 IPv6 metadata address.
func DefaultDeniedNetworks() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("fd00:ec2::254/128"),
	}
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
	for _, p := range *deniedNetworks.Load() {
		if p.Contains(ip) {
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
