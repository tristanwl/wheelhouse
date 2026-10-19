package app

import (
	"sort"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
)

// seedBodies are request bodies that reach the handler of a route (and a few
// hostile ones), keyed by route pattern.
var seedBodies = map[string][]string{
	"POST /api/auth/setup": {`{"token":"x","username":"new-admin","password":"` + fuzzAdminPass + `"}`},
	"POST /api/auth/login": {
		`{"username":"` + fuzzAdminName + `","password":"` + fuzzAdminPass + `"}`,
		`{"username":"` + fuzzAdminName + `","password":"wrong"}`,
		`{"username":["x"],"password":{"a":1}}`,
	},
	"POST /api/auth/password":       {`{"currentPassword":"` + fuzzAdminPass + `","newPassword":"yet-another-long-passphrase-3"}`},
	"POST /api/users":               {`{"username":"second-user","password":"a-fresh-long-passphrase-4","role":"user"}`, `{"username":"local-network","password":"x","role":"admin"}`},
	"PATCH /api/users/{id}":         {`{"role":"user","disabled":true}`, `{"username":"renamed-user"}`},
	"POST /api/users/{id}/password": {`{"password":"a-fresh-long-passphrase-4"}`},
	"POST /api/add":                 {`{"instanceId":"radarr","id":603,"qualityProfileId":1,"rootFolderPath":"/movies","monitored":true,"searchNow":false}`},
	"POST /api/admin/instances": {
		`{"providerId":"radarr","name":"Radarr Stored","enabled":true,"config":{"url":"http://192.0.2.10:7878","apiKey":"` + hostiletest.Key + `"}}`,
		`{"providerId":"sonarr","id":"sonarr-4k","name":"Sonarr 4K","config":{"url":"http://user:` + hostiletest.Pass + `@192.0.2.11:8989","apiKey":"` + hostiletest.Key + `"}}`,
	},
	"POST /api/admin/instances/test": {
		`{"providerId":"radarr","config":{"url":"http://192.0.2.10:7878","apiKey":"` + hostiletest.Key + `"}}`,
		`{"id":"radarr","providerId":"radarr","config":{"url":"http://169.254.169.254/latest"}}`,
	},
	"PUT /api/admin/instances/{id}":                   {`{"name":"Renamed","config":{"apiKey":"` + hostiletest.Key + `"}}`},
	"POST /api/admin/instances/{id}/import":           {``},
	"PUT /api/admin/features":                         {`{"features":{"library":false,"explore":true}}`},
	"PUT /api/admin/network":                          {`{"enabled":true,"cidrs":["192.0.2.0/24"],"trustedProxies":["10.0.0.0/8"],"allowedHosts":["nas.example.lan"]}`, `{"enabled":true,"cidrs":["0.0.0.0/0"]}`},
	"PUT /api/admin/converter":                        {`{"mediaRoots":["/"],"includeHdr":true}`, `{"mediaRoots":["/tmp"]}`},
	"PUT /api/codec-analysis/settings":                {`{"hevcTargetBitrate":4000}`, `{"x":1}`},
	"POST /api/codec-analysis/convert":                {`{"instanceId":"radarr","itemId":1,"filePath":"/etc/passwd","title":"Example Movie 1","originalSize":8,"backend":"software","targetFamily":"h264"}`, `{"filePath":"../../etc/passwd","backend":"--help","targetFamily":"-vf"}`},
	"POST /api/codec-analysis/convert/{jobId}/cancel": {``},
}

// TestWriteFuzzSeeds regenerates testdata/fuzz. The HTTP seeds are derived from
// the router table (every route, as nobody, an administrator and a user) and
// the existing tests' hostile requests.
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/app
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)

	for _, r := range Routes(Options{}).Routes() {
		method, path := samplePath(r.Pattern)
		bodies := seedBodies[r.Pattern]
		if len(bodies) == 0 {
			bodies = []string{""}
		}
		for i, body := range bodies {
			for _, wh := range []uint8{uint8(nobody), uint8(adminOK), uint8(userOK)} {
				label := strings.NewReplacer(" ", "_", "/", "_", "{", "", "}", "").Replace(r.Pattern)
				w.Add("FuzzHTTPSurface", label, method, path, "Content-Type: application/json", []byte(body), wh, uint8(i))
			}
		}
	}
	// Hostile shapes of the requests themselves.
	type req struct {
		method, target, headers, body string
		who                           who
	}
	for _, c := range []req{
		{"GET", "/api/status?x=" + strings.Repeat("a", 3000), "", "", adminOK},
		{"GET", "/api/../etc/passwd", "", "", nobody},
		{"GET", "/api//status", "", "", adminOK},
		{"GET", "/api/%2e%2e/status", "", "", adminOK},
		{"GET", "/api/library/radarr/1%2f2/details", "", "", adminOK},
		{"GET", "/api/library/radarr/abc/details", "", "", adminOK},
		{"GET", "/api/library/radarr/-1/details", "", "", adminOK},
		{"GET", "/api/library/radarr/99999999999999999999/details", "", "", adminOK},
		{"DELETE", "/api/library/radarr/1?confirm=true", "", "", adminOK},
		{"DELETE", "/api/library/radarr/1/seasons/-1?confirm=true", "", "", adminOK},
		{"GET", "/api/explore?category=" + strings.Repeat("%ff", 40), "", "", adminOK},
		{"GET", "/api/search?q=" + strings.Repeat("a", 5000), "", "", adminOK},
		{"GET", "/api/codec-analysis?kind=movies&instanceId=radarr&page=-1&pageSize=99999999", "", "", adminOK},
		{"POST", "/api/auth/login", "Content-Type: text/plain", `{"username":"x","password":"y"}`, nobody},
		{"POST", "/api/auth/login", "Origin: https://evil.example", `{"username":"` + fuzzAdminName + `","password":"` + fuzzAdminPass + `"}`, nobody},
		{"POST", "/api/users", "Origin: null", `{"username":"x"}`, adminOK},
		{"POST", "/api/users", "Sec-Fetch-Site: cross-site", `{"username":"x"}`, adminOK},
		{"POST", "/api/users", "Sec-Fetch-Site: same-site", `{"username":"x"}`, adminOK},
		{"POST", "/api/users", "Origin: http://wheelhouse.example.test:81", `{"username":"x"}`, adminOK},
		{"POST", "/api/users", "Origin: http://user@wheelhouse.example.test", `{"username":"x"}`, adminOK},
		{"POST", "/api/users", "Origin: HTTP://WHEELHOUSE.EXAMPLE.TEST", `{"username":"x","password":"a-fresh-long-passphrase-4","role":"user"}`, adminOK},
		{"POST", "/api/users", "", `{"username":"x"}`, adminNoCSRF},
		{"POST", "/api/users", "", `{"username":"x"}`, adminWrongCSRF},
		{"DELETE", "/api/users/" + strings.Repeat("a", 32), "", "", userOK},
		{"GET", "/api/audit?limit=-1", "", "", adminOK},
		{"GET", "/api/audit?limit=99999999999&before=abc", "", "", adminOK},
		{"GET", "/api/status", "X-Forwarded-For: 127.0.0.1\nX-Real-IP: 127.0.0.1\nForwarded: for=127.0.0.1", "", nobody},
		{"GET", "/api/status", "Host: localhost", "", nobody},
		{"GET", "/api/status", "Cookie: wheelhouse=" + strings.Repeat("A", 43), "", nobody},
		{"GET", "/api/status", "Cookie: __Host-wheelhouse=" + strings.Repeat("A", 43) + "\nX-Forwarded-Proto: https", "", nobody},
		{"POST", "/api/admin/instances", "Content-Type: application/json", strings.Repeat("[", 5000), adminOK},
		{"POST", "/api/admin/instances", "Content-Type: application/json", `{"config":` + strings.Repeat(`{"a":`, 200) + `1` + strings.Repeat("}", 200) + `}`, adminOK},
		{"PUT", "/api/admin/converter", "Content-Type: application/json", `{"mediaRoots":["` + strings.Repeat("a/", 3000) + `"]}`, adminOK},
		{"PUT", "/api/admin/converter", "Content-Type: application/json", `{"mediaRoots":["/proc","/sys","/dev","/etc","/config","/"]}`, adminOK},
		{"GET", "/", "", "", nobody},
		{"GET", "/.gitkeep", "", "", nobody},
		{"GET", "/assets/../index.html", "", "", nobody},
		{"POST", "/", "", "x", adminOK},
		{"OPTIONS", "*", "", "", nobody},
		{"TRACE", "/api/status", "", "", adminOK},
		{"PATCH", "/healthz", "", "", nobody},
		{"GET", "/healthz", "", "", nobody},
		{"HEAD", "/api/status", "", "", adminOK},
		{"GET", "/api", "", "", nobody},
		{"GET", "/api/", "", "", nobody},
		{"GET", "/api/nonexistent", "", "", adminOK},
		{"GET", "/%00", "", "", nobody},
		{"GET", "http://evil.example/api/status", "", "", adminOK},
	} {
		w.Add("FuzzHTTPSurface", "hostile", c.method, c.target, c.headers, []byte(c.body), uint8(c.who), uint8(0))
	}

	// Hostile upstream answers: the real fixtures of every fake service, each as the
	// answer to a different request of the conversation, then
	// hostile bodies at the first requests of it.
	fixtures := fuzzseed.Fixtures(t,
		"../providers/mediamanager/mmtest/testdata/*/*.json",
		"../providers/downloadclient/qbittorrent/fakeqbittorrent/testdata/*.json",
		"../providers/downloadclient/sabnzbd/fakesabnzbd/testdata/*.json",
		"../providers/mediaserver/plex/plextest/testdata/*",
		"../providers/mediaserver/jellyfin/jellyfintest/testdata/*.json")
	names := make([]string, 0, len(fixtures))
	for n := range fixtures {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, name := range names {
		if i%2 == 0 { // every second one: each seed runs the whole API, and go test runs them all
			w.Add("FuzzHostileUpstream", name, uint8(i*3%56), uint8(0), fixtures[name])
		}
	}
	for i, h := range []string{
		``, `null`, `[]`, `{}`, `"` + hostiletest.Key + `"`, `{"error":"` + hostiletest.Key + ` ` + hostiletest.Pass + `"}`,
		`{"progress":"NaN","eta":"Infinity","size":1e999,"queue":{"slots":"x","noofslots":-5}}`, `[{"id":-1,"title":null,"sizeOnDisk":-9,"movieFile":{"size":-1,"mediaInfo":"x"}}]`,
		`<html>401 Unauthorized</html>`, strings.Repeat("[", 60), "\x00\xff", `{"streams":[]}`, `{"MediaContainer":{"Metadata":[{}]}}`,
		`[{"hash":"` + strings.Repeat("a", 40) + `","progress":7,"size":-1,"dlspeed":"x"}]`,
	} {
		w.Add("FuzzHostileUpstream", "hostile", uint8(i*4)%56, uint8(i), []byte(h))
	}

	// Guard inputs: the Origin, Sec-Fetch-Site and Host combinations of the guard
	// tests.
	for _, c := range [][3]string{
		{"", "", "example.com"}, {"same-origin", "", "example.com"}, {"none", "", "example.com"}, {"cross-site", "", "example.com"},
		{"same-site", "", "example.com"}, {"", "http://example.com", "example.com"}, {"", "https://EXAMPLE.com", "example.com"},
		{"", "http://example.com:81", "example.com"}, {"", "http://example.com:81", "example.com:81"}, {"", "null", "example.com"},
		{"", "https://evil.example", "example.com"}, {"", "http://user@example.com", "example.com"}, {"", "http://example.com/", "example.com"},
		{"", "ftp://example.com", "example.com"}, {"", "http://", ""}, {"", "http://[::1]:3000", "[::1]:3000"}, {"", "http://xn--nxasmq6b", "xn--nxasmq6b"},
		{"", "http://münchen.example", "xn--mnchen-3ya.example"}, {"", "http://example.com.", "example.com"}, {"", "//example.com", "example.com"},
		{"", "http://evilexample.com", "example.com"}, {"", "http://sub.example.com", "example.com"}, {"", "http://example.com", "sub.example.com"},
		{"", "http://example.com:80", "example.com"}, {"", "https://example.com", "example.com:443"},
		{"same-origin", "http://example.com", "example.com"}, {"Same-Origin", "", "example.com"}, {" none ", "", "example.com"},
	} {
		w.Add("FuzzSameOrigin", "combo", c[0], c[1], c[2])
	}
	for _, c := range []struct {
		h      string
		secure bool
	}{
		{"wheelhouse=" + strings.Repeat("A", 43), false}, {"wheelhouse=" + strings.Repeat("A", 43), true},
		{"__Host-wheelhouse=" + strings.Repeat("A", 43), true}, {"__Host-wheelhouse=" + strings.Repeat("A", 43), false},
		{"a=b; wheelhouse=x; wheelhouse=y", false}, {"wheelhouse=\"quoted\"", false}, {"wheelhouse=a;b", false}, {"wheelhouse", false},
		{"=wheelhouse", false}, {"wheelhouse=; __Host-wheelhouse=z", true}, {" wheelhouse = x ", false}, {"Wheelhouse=x", false},
		{"wheelhouse=\x7f", false}, {strings.Repeat("a=b;", 500) + "wheelhouse=z", false},
	} {
		w.Add("FuzzSessionCookie", "cookie", c.h, c.secure)
	}
	for _, c := range [][2]string{{"", ""}, {"abc", "abc"}, {"abc", "abd"}, {"ABC", "abc"}, {"abc ", "abc"}, {"abc", "abcd"}, {"", "abc"}, {"abc", ""}} {
		w.Add("FuzzCSRFCompare", "token", c[0], c[1])
	}
	for _, c := range [][2]string{
		{"192.0.2.77:40000", "10.0.0.1"}, {"[2001:db8::77]:40000", "203.0.113.5"}, {"[::ffff:192.0.2.1]:80", "1.1.1.1"}, {"192.0.2.1", ""},
		{"@", "127.0.0.1"}, {"unix", "::1"}, {"[fe80::1%eth0]:80", "x"}, {"", "x"}, {"192.0.2.1:80", "\t192.0.2.2"},
	} {
		w.Add("FuzzClientAddress", "addr", c[0], c[1])
	}
}
