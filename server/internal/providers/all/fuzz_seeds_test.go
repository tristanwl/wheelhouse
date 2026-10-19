package all

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the fixtures of the fake
// services (real responses, sanitized) and hostile bodies, spread over the
// requests of each adapter's conversation:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/providers/all
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	// Provider indexes, as in FuzzUpstreamResponses: radarr, sonarr, sabnzbd,
	// qbittorrent, plex, jellyfin, tmdb.
	groups := []struct {
		prov  uint8
		globs []string
	}{
		{0, []string{"../mediamanager/mmtest/testdata/radarr/*.json"}},
		{1, []string{"../mediamanager/mmtest/testdata/sonarr/*.json"}},
		{2, []string{"../downloadclient/sabnzbd/fakesabnzbd/testdata/*.json"}},
		{3, []string{"../downloadclient/qbittorrent/fakeqbittorrent/testdata/*.json"}},
		{4, []string{"../mediaserver/plex/plextest/testdata/*"}},
		{5, []string{"../mediaserver/jellyfin/jellyfintest/testdata/*.json"}},
	}
	for _, g := range groups {
		fixtures := fuzzseed.Fixtures(t, g.globs...)
		names := make([]string, 0, len(fixtures))
		for n := range fixtures {
			names = append(names, n)
		}
		sort.Strings(names)
		for i, name := range names {
			at := uint8(i*5) % 48
			w.Add("FuzzUpstreamResponses", name, g.prov, at, uint8(0), fixtures[name])
			w.Add("FuzzUpstreamResponses", name+"-truncated", g.prov, (at+1)%48, uint8(0), fixtures[name][:len(fixtures[name])/2])
		}
	}
	for _, v := range []any{tmdbtest.Movie(603), tmdbtest.MovieDetail(603), map[string]any{"results": []any{tmdbtest.Movie(603), tmdbtest.Movie(604)}, "page": 1, "total_pages": 2}} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		w.Add("FuzzUpstreamResponses", "tmdb", uint8(6), uint8(0), uint8(0), raw)
		w.Add("FuzzUpstreamResponses", "tmdb", uint8(6), uint8(1), uint8(0), raw)
	}

	// Regression found by fuzzing (docs/threat-model.md, "Fuzzing findings"):
	// qBittorrent's version is plain text, and any answer to that request was
	// taken as the version: a proxy's login page, megabytes of text.
	for at := range uint8(4) {
		w.Add("FuzzUpstreamResponses", "bug_qbit_version", uint8(3), at, uint8(0), []byte("<html><body>Please sign in to continue</body></html>"))
		w.Add("FuzzUpstreamResponses", "bug_qbit_version", uint8(3), at, uint8(0), []byte(strings.Repeat("v5.0.1 ", 100)))
	}

	// Hostile bodies, against each adapter, at the first few requests.
	hostile := []struct{ name, body string }{
		{"empty", ``}, {"null", `null`}, {"string", `"` + hostiletest.Key + `"`}, {"array", `[1,2,3]`}, {"object", `{}`},
		{"odd-types", `{"queue":{"slots":"x","noofslots":[],"speed":{},"kbpersec":null,"paused":"yes"},"version":5}`},
		{"huge-number", `{"size":1e999,"progress":1e308,"eta":-1e308,"state":1e400}`},
		{"negative", `[{"size":-5,"progress":-3,"eta":-1,"downloaded":-9,"amount_left":-7,"num_seeds":-2,"ratio":-1}]`},
		{"nan-ish", `{"progress":"NaN","eta":"Infinity","kbpersec":"-Infinity","mb":"1e999","mbleft":"0x10"}`},
		{"echo", `{"error":"` + hostiletest.Key + ` ` + hostiletest.Pass + `","message":"` + hostiletest.Key + `"}`},
		{"truncated", `{"queue":{"slots":[{"nzo_id":"x","filename":"` + hostiletest.Key},
		{"nested", strings.Repeat("[", 40) + strings.Repeat("]", 40)}, {"deep", strings.Repeat(`{"a":`, 100) + `1` + strings.Repeat("}", 100)},
		{"html", `<html><body>401 Unauthorized ` + hostiletest.Key + `</body></html>`}, {"binary", "\x00\x01\x02\xff\xfe"},
		{"long-string", `{"title":"` + strings.Repeat("A", 70000) + `"}`},
		{"many-elements", `[` + strings.Repeat(`{},`, 5000) + `{}]`},
		{"bad-utf8", "{\"title\":\"\xff\xfe\"}"}, {"escapes", `{"title":"\ud800","name":"\u0000\n\t"}`},
		{"qbit-ok", `Ok.`}, {"qbit-fails", `Fails.`}, {"torrents", `[{"hash":"` + strings.Repeat("a", 40) + `","name":"Example","size":1,"progress":2,"state":"nonsense","eta":8640000,"dlspeed":-1,"num_seeds":"x","ratio":"y"}]`},
		{"plex", `{"MediaContainer":{"claimed":"yes","machineIdentifier":5,"Metadata":"x","size":"many"}}`},
		{"jellyfin", `[{"UserName":5,"NowPlayingItem":"x","LastActivityDate":[]},{"NowPlayingItem":{}}]`},
		{"arr", `[{"id":"x","title":5,"sizeOnDisk":"y","hasFile":"z","movieFile":[],"statistics":"w","seasons":{}}]`},
	}
	statuses := []uint8{0, 7, 8, 9, 14}
	for i, h := range hostile {
		for prov := range uint8(7) {
			w.Add("FuzzUpstreamResponses", "hostile-"+h.name, prov, uint8(i)%6, statuses[(i+int(prov))%len(statuses)], []byte(h.body))
		}
	}
}
