package netaccess

import (
	"net/netip"
	"strings"
)

// Host allow-list for the local-network bypass.
//
// A browser on a hostile web page can be tricked (DNS rebinding) into sending
// requests whose source address is inside the bypass range and whose Host and
// Origin both name the attacker's domain, which then looks same-origin. A
// session cookie is bound to the real host name, so this only matters for the
// bypass. The defence is to grant the bypass only to requests whose Host is one
// an attacker's domain cannot be: an IP literal, "localhost", a single-label
// name (a LAN name such as "nas" cannot be registered in public DNS), or a name
// an administrator listed (stored list, or WHEELHOUSE_ALLOWED_HOSTS).

// MaxHosts bounds the stored list.
const MaxHosts = 64

// NormalizeHost reduces a Host header value to the bare lower-case host: the
// port, the brackets of an IPv6 literal and a trailing dot are removed. ok is
// false for an empty or unparsable value.
func NormalizeHost(hostport string) (host string, ok bool) {
	h := strings.TrimSpace(hostport)
	if h == "" || len(h) > 300 {
		return "", false
	}
	switch {
	case strings.HasPrefix(h, "["):
		end := strings.Index(h, "]")
		if end < 0 {
			return "", false
		}
		rest := h[end+1:]
		if rest != "" && (rest[0] != ':' || !allDigits(rest[1:])) {
			return "", false
		}
		h = h[1:end]
	case strings.Count(h, ":") > 1: // a bare IPv6 literal without brackets
		if _, err := netip.ParseAddr(h); err != nil {
			return "", false
		}
	case strings.Contains(h, ":"):
		i := strings.LastIndex(h, ":")
		if !allDigits(h[i+1:]) {
			return "", false
		}
		h = h[:i]
	}
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "" {
		return "", false
	}
	return h, true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// implicitHost reports whether a normalised host is always eligible.
func implicitHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return h == "localhost" || (!strings.Contains(h, ".") && validLabel(h))
}

func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// validDomain checks a lower-case dotted name of at least two valid labels.
func validDomain(d string) bool {
	if len(d) > 253 {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !validLabel(l) {
			return false
		}
	}
	return true
}

// ParseHostEntry validates one allow-list entry and returns its canonical form
// (lower case, no port, no trailing dot). An entry is a host name with at least
// two labels ("nas.example.lan") or "*." followed by such a name
// ("*.example.lan", which matches any name below it but not the name itself).
// "*", a bare TLD, an IP literal, localhost and single-label names (those are
// always allowed anyway) are refused.
func ParseHostEntry(text string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimSuffix(t, ".")
	if t == "" {
		return "", invalid("allowedHosts: empty entry")
	}
	if (strings.Contains(t, "*") && !strings.HasPrefix(t, "*.")) || strings.Count(t, "*") > 1 {
		return "", invalid("allowedHosts: %q: a wildcard is only allowed as a leading \"*.\" label", clipText(text))
	}
	base, wild := strings.CutPrefix(t, "*.")
	if _, err := netip.ParseAddr(base); err == nil {
		return "", invalid("allowedHosts: %q is an IP address; those are always allowed", clipText(text))
	}
	if !strings.Contains(base, ".") {
		if wild {
			return "", invalid("allowedHosts: %q would match a whole top-level domain and is refused", clipText(text))
		}
		return "", invalid("allowedHosts: %q has a single label; single-label names and localhost are always allowed", clipText(text))
	}
	if !validDomain(base) {
		return "", invalid("allowedHosts: %q is not a valid host name (letters, digits and hyphens in dot-separated labels)", clipText(text))
	}
	if wild {
		return "*." + base, nil
	}
	return base, nil
}

// ParseHostList validates a whole list: at most MaxHosts entries, duplicates
// dropped.
func ParseHostList(in []string) ([]string, error) {
	if len(in) > MaxHosts {
		return nil, invalid("allowedHosts: at most %d entries", MaxHosts)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		h, err := ParseHostEntry(t)
		if err != nil {
			return nil, err
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out, nil
}

// ParseHostEnv parses WHEELHOUSE_ALLOWED_HOSTS (comma separated). A bad entry is
// an error, so a typo cannot silently leave the list short.
func ParseHostEnv(v string) ([]string, error) {
	var parts []string
	for _, p := range strings.Split(v, ",") {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return ParseHostList(parts)
}

func matchHost(list []string, h string) bool {
	for _, e := range list {
		if base, wild := strings.CutPrefix(e, "*."); wild {
			if strings.HasSuffix(h, "."+base) {
				return true
			}
		} else if e == h {
			return true
		}
	}
	return false
}

// SetEnvHosts sets the read-only WHEELHOUSE_ALLOWED_HOSTS entries. Call it
// before serving.
func (s *Service) SetEnvHosts(hosts []string) { s.envHosts = append([]string(nil), hosts...) }

// EnvHosts are the allowed hosts from the environment.
func (s *Service) EnvHosts() []string { return append([]string(nil), s.envHosts...) }

// HostAllowed reports whether a request with this Host header value may be
// granted the bypass.
func (s *Service) HostAllowed(hostport string) bool {
	h, ok := NormalizeHost(hostport)
	if !ok {
		return false
	}
	return implicitHost(h) || matchHost(s.envHosts, h) || matchHost(s.cur.Load().AllowedHosts, h)
}

// BypassesHost is Bypasses for a request: the client must be in range and the
// Host header acceptable.
func (s *Service) BypassesHost(c Client, hostport string) bool {
	return s.Bypasses(c) && s.HostAllowed(hostport)
}
