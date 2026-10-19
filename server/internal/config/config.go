// Package config reads the server's settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultPort = 3000
	defaultHost = "0.0.0.0"
)

// Config holds where the server listens.
//
// HOSTNAME is deliberately never read: container runtimes set it to the
// container id (or, with host networking, the machine's name), so honouring it
// can make the server unreachable. The bind address comes from WHEELHOUSE_LISTEN
// or PORT only.
type Config struct {
	// Host is the address to listen on.
	Host string
	// Port is the TCP port to listen on.
	Port int
}

// Addr returns the listen address as host:port.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// DialAddr returns an address a local client (the container health check) can
// connect to: the listen address, with a wildcard host replaced by loopback.
func (c Config) DialAddr() string {
	host := c.Host
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(c.Port))
}

// Load builds a Config from getenv (os.Getenv in production). An empty
// variable counts as unset.
//
//   - WHEELHOUSE_LISTEN is a host:port ("127.0.0.1:3050", ":3050",
//     "[::]:3050"). When set it wins, and PORT is ignored.
//   - Otherwise the server listens on 0.0.0.0 and PORT (default 3000).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{Host: defaultHost, Port: defaultPort}
	if v := getenv("WHEELHOUSE_LISTEN"); v != "" {
		host, port, err := net.SplitHostPort(v)
		if err != nil {
			return Config{}, errors.New("WHEELHOUSE_LISTEN must be host:port, for example 0.0.0.0:3050")
		}
		p, err := parsePort(port)
		if err != nil {
			return Config{}, fmt.Errorf("WHEELHOUSE_LISTEN: %w", err)
		}
		if host == "" {
			host = defaultHost
		}
		return Config{Host: host, Port: p}, nil
	}
	if v := getenv("PORT"); v != "" {
		p, err := parsePort(v)
		if err != nil {
			return Config{}, fmt.Errorf("PORT: %w", err)
		}
		cfg.Port = p
	}
	return cfg, nil
}

// DBFileName is the SQLite database's name inside the config directory.
const DBFileName = "wheelhouse.db"

// DefaultConfigDir is the config directory in the image: a volume that holds
// the database, the generated secret.key, the setup token and settings.json.
const DefaultConfigDir = "/config"

// ConfigDir returns the config directory: WHEELHOUSE_CONFIG_DIR when set,
// otherwise /config. The image never sets it; it exists so the server can run
// outside a container (and so the acceptance suite can give every server its
// own directory).
func ConfigDir(getenv func(string) string) string {
	if v := getenv("WHEELHOUSE_CONFIG_DIR"); v != "" {
		return filepath.Clean(v)
	}
	return DefaultConfigDir
}

// TrustedProxies parses WHEELHOUSE_TRUSTED_PROXIES: a comma-separated list of
// addresses and CIDR ranges of reverse proxies whose X-Forwarded-Proto header is
// believed (it decides whether the session cookie is Secure). The default is
// empty: no proxy is trusted, the header is ignored. A malformed entry is an
// error, so a typo cannot silently leave the list empty.
func TrustedProxies(getenv func(string) string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(getenv("WHEELHOUSE_TRUSTED_PROXIES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			// Peers are compared as IPv4 (netaccess unmaps every address), so a
			// range written as IPv4-mapped IPv6 must be kept as IPv4, or it would
			// silently trust nobody; one wider than the mapped block cannot be
			// written as IPv4 at all.
			if p.Addr().Is4In6() {
				if p.Bits() < 96 {
					return nil, fmt.Errorf("WHEELHOUSE_TRUSTED_PROXIES: %q: an IPv4-mapped IPv6 range must be /96 or longer; write the IPv4 range instead", part)
				}
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("WHEELHOUSE_TRUSTED_PROXIES: %q is not an address or CIDR range", part)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// DBPath returns where the SQLite database lives: WHEELHOUSE_DB when set,
// otherwise <configDir>/wheelhouse.db. configDir is the single config directory
// the rest of the server uses (ConfigDir), which also holds the generated
// secret.key and the setup token.
func DBPath(getenv func(string) string, configDir string) string {
	if v := getenv("WHEELHOUSE_DB"); v != "" {
		return v
	}
	return filepath.Join(configDir, DBFileName)
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, errors.New("the port must be a number from 1 to 65535")
	}
	return p, nil
}
