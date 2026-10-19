package netaccess

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

func TestNormalizeHost(t *testing.T) {
	for in, want := range map[string]string{
		"nas": "nas", "NAS:3050": "nas", "nas.example.lan.": "nas.example.lan", "[::1]:80": "::1", "[2001:DB8::1]": "2001:db8::1",
		"2001:db8::1": "2001:db8::1", "192.0.2.1:8080": "192.0.2.1", "[fe80::1%25eth0]:1": "fe80::1%25eth0",
	} {
		if got, ok := NormalizeHost(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", " ", ":80", "nas:x", "[::1", "[::1]x", "[::1]:x", "a:b:c", "."} {
		if got, ok := NormalizeHost(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestHostEntries(t *testing.T) {
	for in, want := range map[string]string{"NAS.Example.LAN": "nas.example.lan", "*.Example.lan.": "*.example.lan", "a-b.c1.example": "a-b.c1.example"} {
		if got, err := ParseHostEntry(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "*", "*.", "*.com", "com", "nas", "localhost", "192.0.2.1", "*.192.0.2.1", "::1", "a.*.b.lan", "*a.b.lan", "a_b.lan", "-a.lan", "a-.lan", "a..lan", "a.lan/x", "a.lan:80", "a b.lan", "nás.lan"} {
		if got, err := ParseHostEntry(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	if _, err := ParseHostEnv("a.example.lan, ,*.b.example.lan"); err != nil {
		t.Error(err)
	}
	if _, err := ParseHostEnv("a.example.lan,*"); err == nil {
		t.Error("a bad env entry was ignored")
	}
}

func TestAllowedHostsPersistAndMatchWildcardsBelowOnly(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := New(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	hosts := []string{"nas.example.lan", "*.apps.example.lan"}
	if _, _, err := s.Update(ctx, Input{AllowedHosts: &hosts}); err != nil {
		t.Fatal(err)
	}
	again, err := New(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	for h, want := range map[string]bool{"nas.example.lan": true, "x.apps.example.lan": true, "apps.example.lan": false, "evilapps.example.lan": false, "nas.example.lan.evil.example": false, "evil.example": false, "nas": true} {
		if got := again.HostAllowed(h); got != want {
			t.Errorf("%q: %v, want %v", h, got, want)
		}
	}
}
