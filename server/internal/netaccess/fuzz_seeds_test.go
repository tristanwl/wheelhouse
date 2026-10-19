package netaccess

import (
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the cases of the existing
// tests (the trusted-proxy table, host normalisation and entries, range
// parsing) and the hostile forms the threat model names:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/netaccess
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)

	for _, in := range []string{
		"192.168.1.77/24", "192.168.1.5", " 10.0.0.0/8 ", "::ffff:10.1.2.3", "::ffff:10.1.0.0/112", "FD00::1", "fd00::/8",
		"", " ", "x", "10.0.0.0/33", "10.0.0/8", "fe80::1%eth0", "fe80::/10%eth0", "::ffff:0:0/80", "1.2.3.4/", "/8",
		"10.0.0.0/8/8", "1.2.3.4,5.6.7.8", "0.0.0.0/0", "::/0", "::ffff:0.0.0.0/96", "::ffff:1.2.3.4/128", "1.2.3.4\x00", "١٢٣.٤.٥.٦",
	} {
		w.Add("FuzzParseRange", "range", in)
	}
	w.Add("FuzzUpdateLists", "private", "192.168.0.0/16", "fd00::/8", "10.0.0.0/24", "fd10::/16", true)
	w.Add("FuzzUpdateLists", "wide", "0.0.0.0/0", "::/0", "::ffff:0:0/96", "8.8.8.0/24", true)
	w.Add("FuzzUpdateLists", "none", "", "", "", "", false)
	w.Add("FuzzUpdateLists", "mapped", "::ffff:192.168.1.0/120", "192.168.1.0/24", "::ffff:10.0.0.1", "10.0.0.1", true)

	// The trusted-proxy table of the resolve test, as raw header strings.
	type hdr struct{ remote, xff1, xff2, fwd, real string }
	for _, c := range []hdr{
		{"10.0.0.2:1", "192.168.1.9", "", "", ""},
		{"10.0.0.2:1", "203.0.113.9", "", "", ""},
		{"10.0.0.2:1", "192.168.1.9, 203.0.113.9", "", "", ""},
		{"10.0.0.2:1", "203.0.113.9, 192.168.1.9", "", "", ""},
		{"10.0.0.2:1", "203.0.113.9, 192.168.1.9, 10.0.0.7, 172.20.0.1", "", "", ""},
		{"10.0.0.2:1", "10.0.0.9, 10.0.0.7", "", "", ""},
		{"10.0.0.2:1", "203.0.113.9", "192.168.1.9", "", ""},
		{"10.0.0.2:1", "192.168.1.9:51000", "", "", ""},
		{"10.0.0.2:1", "[fd00::5]:443", "", "", ""},
		{"[::ffff:10.0.0.2]:1", "192.168.1.9", "", "", ""},
		{"10.0.0.2:1", "::ffff:192.168.1.9", "", "", ""},
		{"10.0.0.2:1", "fe80::1%eth0", "", "", ""},
		{"10.0.0.2:1", "unknown", "", "", ""},
		{"10.0.0.2:1", "192.168.1.9,", "", "", ""},
		{"10.0.0.2:1", "0192.168.1.9", "", "", ""},
		{"10.0.0.2:1", "192.168.1.9 203.0.113.9", "", "", ""},
		{"10.0.0.2:1", "", "", "", "192.168.1.9"},
		{"10.0.0.2:1", "", "", "", "nope"},
		{"10.0.0.2:1", "", "", `for="[fd00::5]:4711";proto=https;by=10.0.0.2`, ""},
		{"10.0.0.2:1", "", "", "For=192.168.1.9;Proto=https", ""},
		{"10.0.0.2:1", "", "", "for=203.0.113.9, for=192.168.1.9, for=10.0.0.7", ""},
		{"10.0.0.2:1", "", "", "for=_hidden", ""},
		{"10.0.0.2:1", "", "", "for=192.168.1.9;for=203.0.113.9", ""},
		{"10.0.0.2:1", "192.168.1.9", "", "for=203.0.113.9", ""},
		{"10.0.0.2:1", "192.168.1.9, 203.0.113.9", "", "", "192.168.1.9"},
		{"10.0.0.2:1", strings.Repeat("1.1.1.1,", 70) + "1.1.1.1", "", "", ""},
		{"203.0.113.5:4000", "192.168.1.1", "", "for=192.168.1.1", "192.168.1.1"},
		{"[fe80::1%eth0]:80", "192.168.1.1", "", "", ""},
		{"not-an-address", "10.0.0.1", "", "", ""},
		{"", "", "", "", ""},
		{"10.0.0.2", "192.168.1.1", "", "", ""},
	} {
		w.Add("FuzzResolveRaw", "headers", c.remote, c.xff1, c.xff2, c.fwd, c.real, true)
		w.Add("FuzzResolveRaw", "headers-notrust", c.remote, c.xff1, c.xff2, c.fwd, c.real, false)
		w.Add("FuzzNoTrustedProxies", "headers", c.remote, c.xff1, c.fwd)
	}
	for _, c := range []struct {
		peer  byte
		hops  []byte
		style uint8
		split bool
		env   bool
	}{
		{0, []byte{1, 2, 3}, 0, false, true},      // trusted peer, mixed chain
		{1, []byte{0, 4, 8}, 0, true, false},      // untrusted peer, split headers
		{4, []byte{5, 9, 12, 16}, 1, false, true}, // Forwarded
		{8, []byte{3, 7, 11}, 2, false, false},    // both header forms
		{0, []byte{0, 4, 8, 12}, 0, false, true},  // every hop trusted
		{2, []byte{2, 3, 1}, 0, false, true},      // IPv6 chain
	} {
		w.Add("FuzzResolveChain", "chain", c.peer, c.hops, c.style, c.split, c.env)
	}

	// Hosts: the normalisation cases of the host tests, then the spoofing forms
	// DNS rebinding and header tricks use.
	hosts := []string{
		"nas", "NAS:3050", "nas.example.lan.", "[::1]:80", "[2001:DB8::1]", "2001:db8::1", "192.0.2.1:8080", "[fe80::1%25eth0]:1",
		"", " ", ":80", "nas:x", "[::1", "[::1]x", "[::1]:x", "a:b:c", ".", "localhost", "LOCALHOST.", "localhost:3000",
		"evil.example.com", "evil.example.com.", "evil.example.com:80", "user@nas", "nas@evil.example.com", "evil.example.com@nas",
		"nas/evil", "nas\\evil", "nas?x", "nas#x", "nas%2eevil.example.com", "nás", "xn--ns-qla", "ｌｏｃａｌｈｏｓｔ", "127.0.0.1.nip.io",
		"0x7f.1", "2130706433", "1.2.3.4.evil.example.com", "nas.example.lan", "a.nas.example.lan", "nas.example.lan\x00", "-a", "a-", strings.Repeat("a", 64), strings.Repeat("a.", 130),
	}
	for _, h := range hosts {
		w.Add("FuzzNormalizeHost", "host", h)
		w.Add("FuzzHostAllowed", "host", h, "*.nas.example.lan", "nas.example.lan")
		w.Add("FuzzHostAllowed", "host-nolist", h, "", "")
	}
	// Names that only differ from an allowed one by a suffix or a prefix.
	for _, c := range [][3]string{
		{"evil.localhost", "", ""}, {"localhost.evil.example.com", "", ""}, {"x.localhost.", "", ""}, {"notlocalhost", "", ""},
		{"nas.example.lan", "*.nas.example.lan", ""}, {"xnas.example.lan", "*.nas.example.lan", ""}, {"a.nas.example.lan", "*.nas.example.lan", ""},
		{"evilnas.example.lan", "", "*.nas.example.lan"}, {"nas.example.lan.evil.com", "nas.example.lan", ""}, {"NAS.EXAMPLE.LAN.", "nas.example.lan", ""},
	} {
		w.Add("FuzzHostAllowed", "suffix", c[0], c[1], c[2])
	}
	// Regressions found by fuzzing (docs/threat-model.md, "Fuzzing findings"):
	// a wildcard entry matched any text that ended in ".<base>", labels or not.
	for _, h := range []string{".nas.example.lan", "a..nas.example.lan", "x y.nas.example.lan", "evil.example.com/.nas.example.lan", "a@b.nas.example.lan"} {
		w.Add("FuzzHostAllowed", "bug_wildcard_labels", h, "*.nas.example.lan", "")
	}
	for _, e := range []string{
		"NAS.Example.LAN", "*.Example.lan.", "a-b.c1.example", "", "*", "*.", "*.com", "com", "nas", "localhost", "192.0.2.1",
		"*.192.0.2.1", "::1", "a.*.b.lan", "*a.b.lan", "a_b.lan", "-a.lan", "a-.lan", "a..lan", "a.lan/x", "a.lan:80", "a b.lan", "nás.lan",
	} {
		w.Add("FuzzParseHostEntry", "entry", e)
	}
}
