// Package config reads the server's settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
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

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, errors.New("the port must be a number from 1 to 65535")
	}
	return p, nil
}
