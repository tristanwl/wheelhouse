package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddressDeniedByDefault(t *testing.T) {
	denied := []string{
		"169.254.169.254:80", "169.254.0.1:443", "[fe80::1]:80", "[fe80::1%eth0]:80",
		"[::ffff:169.254.169.254]:80", "[fd00:ec2::254]:80", "not-an-ip:80",
	}
	allowed := []string{
		"127.0.0.1:8989", "[::1]:80", "10.0.0.5:80", "192.168.1.10:80", "172.16.0.2:80",
		"192.0.2.1:80", "[fd12:3456::1]:80", "169.253.255.255:80", "170.0.0.1:80",
	}
	for _, a := range denied {
		if !addressDenied(a) {
			t.Errorf("%s: allowed, want denied", a)
		}
	}
	for _, a := range allowed {
		if addressDenied(a) {
			t.Errorf("%s: denied, want allowed", a)
		}
	}
}

func TestParseDeniedNetworks(t *testing.T) {
	if p, err := ParseDeniedNetworks(""); err != nil || len(p) != len(DefaultDeniedNetworks()) {
		t.Errorf("default: %v %v", p, err)
	}
	if p, err := ParseDeniedNetworks("none"); err != nil || p != nil {
		t.Errorf("none: %v %v", p, err)
	}
	p, err := ParseDeniedNetworks("203.0.113.0/24, 198.51.100.7")
	if err != nil || len(p) != 2 {
		t.Fatalf("custom: %v %v", p, err)
	}
	for _, bad := range []string{"nonsense", "1.2.3.4/99"} {
		if _, err := ParseDeniedNetworks(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A client never connects to a link-local address, and says why without the URL.
func TestHTTPClientRefusesMetadataAddresses(t *testing.T) {
	for _, base := range []string{"http://169.254.169.254", "http://[fe80::1]:8080", "http://[::ffff:169.254.169.254]"} {
		c := newClient(t, HTTPOptions{BaseURL: base, Label: "svc", Secrets: []string{"canary-key-0001"}})
		_, err := c.Do(context.Background(), "/latest/meta-data", Request{})
		if err == nil || !strings.Contains(err.Error(), "address not allowed") {
			t.Errorf("%s: err = %v", base, err)
		}
		if err != nil && strings.Contains(err.Error(), "169.254") {
			t.Errorf("%s: the address leaked into %q", base, err)
		}
	}
}

func TestHTTPClientAllowsLoopbackAndTheDenyListIsConfigurable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "svc"})
	if _, err := c.Do(context.Background(), "/", Request{}); err != nil {
		t.Fatalf("loopback: %v", err)
	}
	// Deny loopback explicitly: the same request is refused, and restoring works.
	p, _ := ParseDeniedNetworks("127.0.0.0/8")
	SetDeniedNetworks(p)
	defer SetDeniedNetworks(DefaultDeniedNetworks())
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})) // a new port: no pooled connection
	defer srv2.Close()
	c2 := newClient(t, HTTPOptions{BaseURL: srv2.URL, Label: "svc"})
	if _, err := c2.Do(context.Background(), "/", Request{}); err == nil || !strings.Contains(err.Error(), "address not allowed") {
		t.Errorf("configured deny: %v", err)
	}
}
