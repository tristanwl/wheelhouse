package netaccess

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// FuzzNormalizeHost: a Host header value is reduced to a bare host or refused.
//
// Invariants: it never panics; a result is non-empty, lower case, without
// brackets, without a trailing dot and without a port; normalising the result
// again changes nothing (so the allow-list and the logs agree on one spelling).
// It is a normaliser, not the check: FuzzHostAllowed holds the verdict.
func FuzzNormalizeHost(f *testing.F) {
	f.Fuzz(func(t *testing.T, in string) {
		h, ok := NormalizeHost(in)
		if !ok {
			if h != "" {
				t.Fatalf("refused %q but returned %q", in, h)
			}
			return
		}
		if h == "" || h != strings.ToLower(h) || strings.HasSuffix(h, ".") || strings.ContainsAny(h, "[]") {
			t.Fatalf("%q normalised to %q", in, h)
		}
		if again, ok := NormalizeHost(h); !ok || again != h {
			t.Fatalf("%q -> %q -> %q (%v) is not stable", in, h, again, ok)
		}
	})
}

// FuzzHostAllowed: which Host header values may be granted the bypass.
//
// Invariants (with an environment entry and a stored entry in play): a host is
// allowed only if, once normalised, it is an IP literal, "localhost", a valid
// single label, or matches a list entry exactly or lies below a wildcard entry
// as valid labels (a Host such as "a..lan" or "x y.example.lan" matches
// nothing); a name outside those forms, with any case, port, trailing dot,
// userinfo or unicode spelling, never gets the bypass, and BypassesHost agrees.
func FuzzHostAllowed(f *testing.F) {
	f.Fuzz(func(t *testing.T, host, envEntry, storedEntry string) {
		s := NewMemory(nil)
		var env, stored []string
		if e, err := ParseHostEntry(envEntry); err == nil {
			env = []string{e}
			s.SetEnvHosts(env)
		}
		if e, err := ParseHostEntry(storedEntry); err == nil {
			stored = []string{e}
		}
		if _, _, err := s.Update(context.Background(), Input{
			Enabled: true, CIDRs: []string{"0.0.0.0/1", "128.0.0.0/1"}, AllowedHosts: &stored,
		}); err != nil {
			t.Fatal(err)
		}

		allowed := s.HostAllowed(host)
		client := Client{Peer: netip.MustParseAddr("192.0.2.9"), Addr: netip.MustParseAddr("192.0.2.9")}
		if s.BypassesHost(client, host) != allowed {
			t.Fatalf("BypassesHost and HostAllowed disagree for %q", host)
		}
		if !allowed {
			return
		}
		h, ok := NormalizeHost(host)
		if !ok {
			t.Fatalf("%q is allowed but does not normalise", host)
		}
		if _, err := netip.ParseAddr(h); err == nil || h == "localhost" {
			return
		}
		if !strings.Contains(h, ".") && validLabel(h) {
			return
		}
		// A name with dots: it must be a valid domain and be on a list.
		if !validDomain(h) {
			t.Fatalf("%q is allowed as %q, which is not a valid host name", host, h)
		}
		listed := false
		for _, e := range append(append([]string(nil), env...), stored...) {
			if base, wild := strings.CutPrefix(e, "*."); wild {
				if strings.HasSuffix(h, "."+base) && h != base {
					listed = true
				}
			} else if e == h {
				listed = true
			}
		}
		if !listed {
			t.Fatalf("%q is allowed as %q but matches no entry (env %q, stored %q)", host, h, env, stored)
		}
	})
}

// FuzzParseHostEntry: an allow-list entry is canonical or refused.
//
// Invariants: an accepted entry is lower case without a trailing dot, is a valid
// domain of at least two labels (optionally behind a single leading "*."),
// is never an IP literal, "localhost", a single label or a wildcard over a whole
// top-level domain, and parses to itself.
func FuzzParseHostEntry(f *testing.F) {
	f.Fuzz(func(t *testing.T, text string) {
		e, err := ParseHostEntry(text)
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error is %T", err)
			}
			if len(err.Error()) > 4*(64+3)+160 {
				t.Fatalf("error text of %d bytes", len(err.Error()))
			}
			return
		}
		base := strings.TrimPrefix(e, "*.")
		if e != strings.ToLower(e) || strings.HasSuffix(e, ".") || strings.Count(e, "*") > 1 || !validDomain(base) {
			t.Fatalf("%q produced the entry %q", text, e)
		}
		if _, err := netip.ParseAddr(base); err == nil || base == "localhost" || !strings.Contains(base, ".") {
			t.Fatalf("%q produced %q, a form that is always allowed or refused", text, e)
		}
		if again, err := ParseHostEntry(e); err != nil || again != e {
			t.Fatalf("%q -> %q -> %q (%v)", text, e, again, err)
		}
		list, err := ParseHostList([]string{text, e, strings.ToUpper(e)})
		if err != nil || len(list) != 1 || list[0] != e {
			t.Fatalf("duplicates of %q were not collapsed: %v %v", e, list, err)
		}
	})
}
