package instances

import (
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the destination cases of the
// instance tests (same, changed, equivalent spellings) and the URL tricks that
// make two parsers disagree about where a request goes:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/instances
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	// The same destination as the stored radarr (selector 0) in other spellings.
	for _, u := range []string{
		"http://192.0.2.10:7878/radarr", "http://192.0.2.10:7878/radarr/", "HTTP://192.0.2.10:7878/radarr", "http://192.0.2.10:7878/radarr//",
		"http://192.0.2.10:7878/RADARR", "http://192.0.2.10:7878", "http://192.0.2.10:7879/radarr", "https://192.0.2.10:7878/radarr",
		"http://192.0.2.11:7878/radarr", "http://attacker.example.com", "http://192.0.2.10:7878/radarr/../other", "http://192.0.2.10:7878/radarr/%2e%2e/x",
		"http://192.0.2.10:7878/radarr%2f..%2fother", "http://192.0.2.10:7878\\@attacker.example.com/radarr", "http://attacker.example.com\\@192.0.2.10:7878/radarr",
		"http://192.0.2.10:7878@attacker.example.com/radarr", "http://192.0.2.10.attacker.example.com:7878/radarr", "http://192.0.2.10:7878:80/radarr",
		"http://192.0.2.010:7878/radarr", "http://0xc0.0.2.10:7878/radarr", "http://3221225994:7878/radarr", "http://192.0.2.10:07878/radarr",
		"http://192.0.2.10:7878./radarr", "http://[::ffff:192.0.2.10]:7878/radarr", "http://192.0.2.10:+7878/radarr", "http://192.0.2.10:7878/radarr\t",
		" http://192.0.2.10:7878/radarr ", "http://192.0.2.10:7878/radarr\n", "http://192.0.2.10:7878/radarr#frag", "http://192.0.2.10:7878/radarr?x=1",
		"//192.0.2.10:7878/radarr", "192.0.2.10:7878/radarr", "",
	} {
		w.Add("FuzzStoredSecretDestination", "radarr", uint8(0), u, false)
		w.Add("FuzzStoredSecretDestination", "radarr-typed", uint8(0), u, true)
	}
	// The other providers: their stored destinations in the spellings that matter.
	for _, c := range []struct {
		sel uint8
		u   string
	}{
		{1, "https://sab.example.com:8443"}, {1, "https://SAB.example.com:8443/"}, {1, "https://sab.example.com"}, {1, "https://sab.example.com:443"},
		{1, "http://sab.example.com:8443"}, {2, "http://qbit.example.com"}, {2, "http://qbit.example.com:80"}, {2, "http://qbit.example.com:8080"},
		{2, "http://qbit.example.com./"}, {3, "http://[2001:db8::10]:32400"}, {3, "http://[2001:DB8:0:0:0:0:0:10]:32400"}, {3, "http://[2001:db8::10]:32400/"},
		{3, "http://[2001:db8::11]:32400"}, {3, "http://[2001:db8::10%25eth0]:32400"}, {4, "http://jelly.example.com:8096/jf/"}, {4, "http://jelly.example.com:8096/jf"},
		{4, "http://jelly.example.com:8096/JF"}, {4, "http://jelly.example.com:8096"}, {4, "http://jelly.example.com:8096/jf/."}, {4, "http://jelly.example.com:8096/jf/../jf"},
	} {
		w.Add("FuzzStoredSecretDestination", "other", c.sel, c.u, false)
	}
}
