// Package config reads the server's settings from the environment.
package config

import (
	"fmt"
	"net"
	"strconv"
)

const (
	defaultPort = 3000
	defaultHost = "0.0.0.0"
)

// Config holds the settings the skeleton needs. The names match the Next app so
// the same container environment works for both.
type Config struct {
	// Host is the address to listen on (HOSTNAME).
	Host string
	// Port is the TCP port to listen on (PORT).
	Port int
}

// Addr returns the listen address as host:port.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Load builds a Config from getenv (os.Getenv in production). An empty
// variable counts as unset.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{Host: defaultHost, Port: defaultPort}
	if v := getenv("HOSTNAME"); v != "" {
		cfg.Host = v
	}
	if v := getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return Config{}, fmt.Errorf("PORT must be a number from 1 to 65535")
		}
		cfg.Port = p
	}
	return cfg, nil
}
