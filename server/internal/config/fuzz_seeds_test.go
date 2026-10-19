package config

import (
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/config
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	for _, v := range []string{
		"", "10.0.0.1", "10.0.0.0/8", "10.0.0.0/8, 192.168.0.0/16 ,", "fd00::/8,::1", "10.0.0.1/8", "::ffff:10.0.0.1",
		"nonsense", "10.0.0.0/33", "10.0.0.1,,nonsense", "fe80::1%eth0", "0.0.0.0/0", ",,,", " 172.16.0.5 ",
	} {
		w.Add("FuzzTrustedProxies", "list", v)
	}
	// Regression found by fuzzing: a mapped range stayed mapped and so trusted
	// nobody (peers are compared unmapped).
	for _, v := range []string{"::ffff:10.0.0.0/104", "::ffff:10.0.0.0/96", "::ffff:0:0/96", "::ffff:10.0.0.7/128"} {
		w.Add("FuzzTrustedProxies", "bug_mapped", v)
	}
	for _, c := range [][2]string{
		{"", ""}, {"127.0.0.1:3050", ""}, {":3050", ""}, {"[::]:3050", ""}, {"[::1]:80", "9"}, {"", "8080"}, {"", "0"}, {"", "65536"},
		{"", "abc"}, {"127.0.0.1", ""}, {"host:99999", ""}, {"a:b:c", ""}, {"0.0.0.0:3000", "1"}, {"", "-1"}, {"", " 80"},
	} {
		w.Add("FuzzLoad", "listen", c[0], c[1])
	}
}
