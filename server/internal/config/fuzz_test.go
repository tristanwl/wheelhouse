package config

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
)

// FuzzTrustedProxies: the environment list of trusted proxies.
//
// Invariants: each entry is a valid, masked prefix; an IPv4-mapped IPv6 entry is
// written as IPv4 (the form netaccess compares peers in, because it unmaps every
// peer: an entry kept in the mapped form would silently trust nobody); the entry
// is the one the stored list (netaccess.ParseRange) makes of the same text, so
// the two sources of a union mean the same thing; and a bad entry is an error
// that names the entry.
func FuzzTrustedProxies(f *testing.F) {
	f.Fuzz(func(t *testing.T, v string) {
		got, err := TrustedProxies(func(k string) string {
			if k == "WHEELHOUSE_TRUSTED_PROXIES" {
				return v
			}
			return ""
		})
		if err != nil {
			if !strings.Contains(err.Error(), "WHEELHOUSE_TRUSTED_PROXIES") {
				t.Fatalf("error does not name the variable: %v", err)
			}
			return
		}
		for _, p := range got {
			if !p.IsValid() || p != p.Masked() || p.Addr().Zone() != "" || p.Addr().Is4In6() {
				t.Fatalf("%q produced the non-canonical entry %v", v, p)
			}
		}
		var want []netip.Prefix
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			p, perr := netaccess.ParseRange(part)
			if perr != nil {
				continue // the stored list is stricter in places (a zone, a wide mapped range)
			}
			want = append(want, p)
		}
		for _, w := range want {
			found := false
			for _, p := range got {
				found = found || p == w
			}
			if !found {
				t.Fatalf("%q: the stored list reads %v but the environment list has %v", v, w, got)
			}
		}
	})
}

// FuzzLoad: the listen address.
//
// Invariants: a result has a port from 1 to 65535 and a host that round-trips
// through Addr; a refusal is an error without the raw value; HOSTNAME is never
// read.
func FuzzLoad(f *testing.F) {
	f.Fuzz(func(t *testing.T, listen, port string) {
		reads := map[string]bool{}
		cfg, err := Load(func(k string) string {
			reads[k] = true
			switch k {
			case "WHEELHOUSE_LISTEN":
				return listen
			case "PORT":
				return port
			}
			return ""
		})
		if reads["HOSTNAME"] {
			t.Fatal("HOSTNAME was read")
		}
		if err != nil {
			if (len(listen) >= 8 && strings.Contains(err.Error(), listen)) || (len(port) >= 8 && strings.Contains(err.Error(), port)) {
				t.Fatalf("error quotes the value: %v", err)
			}
			return
		}
		if cfg.Port < 1 || cfg.Port > 65535 {
			t.Fatalf("port %d", cfg.Port)
		}
		if !strings.HasSuffix(cfg.Addr(), ":"+strconv.Itoa(cfg.Port)) {
			t.Fatalf("Addr %q for %+v", cfg.Addr(), cfg)
		}
		if !strings.HasSuffix(cfg.DialAddr(), ":"+strconv.Itoa(cfg.Port)) {
			t.Fatalf("DialAddr %q for %+v", cfg.DialAddr(), cfg)
		}
	})
}
