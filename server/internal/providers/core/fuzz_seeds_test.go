package core

import (
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz for this package from the real
// fixtures and the hostile cases of the existing tests:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/providers/core
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)

	// Real service responses (sanitized fixtures of the fake servers), decoded
	// with the default limits and with limits tight enough to refuse them.
	fixtures := fuzzseed.Fixtures(t,
		"../mediamanager/mmtest/testdata/*/*.json",
		"../downloadclient/qbittorrent/fakeqbittorrent/testdata/*.json",
		"../downloadclient/sabnzbd/fakesabnzbd/testdata/*.json",
		"../mediaserver/plex/plextest/testdata/*.json",
		"../mediaserver/jellyfin/jellyfintest/testdata/*.json")
	for name, raw := range fixtures {
		w.Add("FuzzDecodeJSON", name, raw, uint8(255), uint8(255), uint8(255), uint8(255))
		w.Add("FuzzDecodeJSON", name+"-tight", raw, uint8(3), uint8(2), uint8(9), uint8(20))
		if len(raw) > 40 {
			w.Add("FuzzDecodeJSON", name+"-truncated", raw[:len(raw)/2], uint8(255), uint8(255), uint8(255), uint8(255))
		}
	}
	// Hostile shapes: a body that is cheap on the wire and costly decoded.
	hostile := map[string]string{
		"singletons":       `[[1],[1],[1],[1],[1]]`,
		"nested-singleton": `[[[[[[[[[[1]]]]]]]]]]`,
		"flat-floods":      `[{},{},{},{},{},{},{},{},{},{},{},{},{},{}]`,
		"object-members":   `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10,"k":11,"l":12,"m":13}`,
		"escaped-quote":    `["a\",\",\",\",\",\",b"]`,
		"unterminated":     `["abc\`,
		"long-string":      `["` + strings.Repeat("a", 60) + `"]`,
		"invalid-utf8":     "[\"\xff\xfe\xfd\"]",
		"commas-in-string": `["` + strings.Repeat(",", 40) + `"]`,
		"deep":             strings.Repeat("[", 40) + strings.Repeat("]", 40),
		"secret-in-key":    `{"SECRET-BODY":[` + strings.Repeat("[", 20),
		"empty":            ``,
		"two-values":       `[1][2]`,
	}
	for name, body := range hostile {
		w.Add("FuzzDecodeJSON", name, []byte(body), uint8(3), uint8(3), uint8(9), uint8(7))
		w.Add("FuzzDecodeJSON", name+"-wide", []byte(body), uint8(11), uint8(7), uint8(23), uint8(47))
	}

	// Redaction: the planted canaries of the hostile-upstream tests, each in
	// every encoding, inside the kind of text that echoes them.
	secrets := []string{`CANARY-key/+&"9f8e7d6c`, "CANARY-pass-3b2a1f0e", "p@ss w0rd!", "ключ-секрет-ü", "a+b/c=d==", "k=v&x"}
	surroundings := [][2]string{
		{"", ""},
		{"GET /api/v3/series?apikey=", " HTTP/1.1"},
		{"Sonarr: upstream said ", " (401)"},
		{"http://user:pass@host.example.com/x?token=", "&y=1"},
		{`{"error":"`, `"}`},
	}
	// Every encoding once, rotating through the secrets and surroundings.
	for form := range 44 {
		i := form % len(secrets)
		sur := surroundings[form%len(surroundings)]
		w.Add("FuzzRedact", "secret"+string(rune('a'+i)), secrets[i], sur[0], sur[1], uint8(form))
	}
	// The encodings that only differ for some characters need such a secret:
	// the canary (quote, slash, plus, ampersand) for JSON, HTML and the escaped
	// slash; "a?>b?>c?>" for the base64 alphabets ("+" and "/" in the standard one).
	for form := 9; form <= 13; form++ {
		w.Add("FuzzRedact", "alphabet", "ÿÿÿ-key", "token ", " ", uint8(form)) // base64 "w7/Dvy1rZXk=": a slash
	}
	for form := 9; form <= 21; form++ {
		w.Add("FuzzRedact", "alphabet", `CANARY-key/+&"9f8e7d6c`, `"key":"`, `"`, uint8(form))
		w.Add("FuzzRedact", "alphabet", "a?>b?>c?>", "Authorization: ", " ", uint8(form))
	}
	for _, text := range []string{
		"https://user:pass@example.com/api?apikey=abc123&token=xyz",
		"request failed: Get \"http://192.0.2.1:8989/api?apikey=SECRETVALUE\": connection refused",
		"//a@b//c@d", "token=a", "api_key=", "x-plex-token=abcdef", "***", "",
	} {
		w.Add("FuzzRedactArbitrary", "text", text, "SECRETVALUE", "abc")
	}

	// Regressions found by fuzzing (docs/threat-model.md, "Fuzzing findings").
	// bug_values: the first element of every container went uncounted, so
	// MaxValues let through up to a nesting-depth's multiple of itself.
	w.Add("FuzzDecodeJSON", "bug_values_first_element", []byte(`[[1],[1],[1]]`), uint8(11), uint8(7), uint8(23), uint8(3))
	w.Add("FuzzDecodeJSON", "bug_values_nested_chains", []byte(`[[[1]],[[1]],[[1]]]`), uint8(11), uint8(7), uint8(23), uint8(5))
	// bug_redact: a space written as %20 beside a mark ! and a reserved & was
	// in none of the three URL encodings the redactor knew.
	w.Add("FuzzRedact", "bug_redact_mixed_percent", "ab c&!d", "k=", "&x=1", uint8(4))
	w.Add("FuzzRedact", "bug_redact_mixed_percent_lower", "p@ss w0rd!", "", "", uint8(8))

	// URLs: the accepted and refused ones of the config tests, then hostile ones.
	for _, u := range []string{
		"http://localhost:8989", "https://example.com/sonarr", "http://192.0.2.10:7878/", "http://[::1]:8080/base/",
		"http://user:pass@example.com", "http://example.com?x=1", "http://example.com#frag", "ftp://example.com",
		"example.com", "http://", "http:///x", "http://example.com:99999", "HTTP://EXAMPLE.COM", "http://a@b@c",
		"http://example.com\\@evil.example.net", "http://exa mple.com", "http://example.com/%zz", "javascript:alert(1)",
	} {
		paths := []string{"/api/v3/system/status", "api/v3/x", "../../x", "//evil.example.net/x", "@evil.example.net", "?x=1", "#f", ""}
		for k := range 2 {
			w.Add("FuzzInstanceURL", "url", u, paths[(len(u)+k*3)%len(paths)])
		}
		w.Add("FuzzJoinURL", "url", u, "/api/v3/x")
	}

	// Addresses: the allowed and denied lists of TestAddressDeniedByDefault, and
	// each default network's own metadata address.
	for _, a := range []string{
		"169.254.169.254:80", "169.254.0.1:443", "[fe80::1]:80", "[fe80::1%eth0]:80", "[::ffff:169.254.169.254]:80",
		"[fd00:ec2::254]:80", "not-an-ip:80", "127.0.0.1:8989", "[::1]:80", "10.0.0.5:80", "192.168.1.10:80",
		"[64:ff9b::a9fe:a9fe]:80", "[64:ff9b::7f00:1]:80", "100.100.100.200:80", "192.0.0.192:80", "", ":", "[::ffff:10.0.0.1]:0",
		"169.254.169.254", "localhost:80", "[2002:a9fe:a9fe::1]:80",
	} {
		w.Add("FuzzAddressDenied", "addr", a, uint8(169), uint8(254), uint8(169), uint8(254), uint16(80), false)
	}
	for _, q := range [][4]uint8{{10, 0, 0, 1}, {127, 0, 0, 1}, {192, 0, 2, 1}, {100, 100, 100, 200}, {192, 0, 0, 192}, {169, 254, 0, 0}, {169, 253, 255, 255}} {
		w.Add("FuzzAddressDenied", "v4", "", q[0], q[1], q[2], q[3], uint16(8989), false)
		w.Add("FuzzAddressDenied", "v4-custom", "", q[0], q[1], q[2], q[3], uint16(8989), true)
	}
	for _, s := range []string{"", "none", "NONE", "169.254.0.0/16", "10.0.0.0/8, 192.0.2.1", "fe80::/10,,64:ff9b::/96", "300.1.1.1", "10.0.0.1/33", "::ffff:10.0.0.1", "none,10.0.0.0/8"} {
		w.Add("FuzzParseDeniedNetworks", "list", s)
	}
}
