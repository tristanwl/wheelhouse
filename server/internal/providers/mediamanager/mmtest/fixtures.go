package mmtest

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// The fixtures are sanitized real responses from Sonarr v4 and Radarr v5, copied
// byte for byte from lib/providers/media-manager/__fixtures__ (a test checks
// they still match). The fakes serve copies of them.
//
//go:embed testdata
var fixtureFS embed.FS

// Doc is a decoded JSON object. Numbers are json.Number, so large sizes
// round-trip exactly.
type Doc = map[string]any

// FixtureBytes returns a fixture file, for example "radarr/movies.json".
func FixtureBytes(name string) []byte {
	b, err := fixtureFS.ReadFile(path.Join("testdata", name))
	if err != nil {
		panic(fmt.Sprintf("mmtest: missing fixture %s", name))
	}
	return b
}

func decode(name string, out any) {
	dec := json.NewDecoder(bytes.NewReader(FixtureBytes(name)))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		panic(fmt.Sprintf("mmtest: bad fixture %s: %v", name, err))
	}
}

// FixtureDocs decodes a fixture that is a JSON array (a fresh copy every call).
func FixtureDocs(name string) []Doc {
	var docs []Doc
	decode(name, &docs)
	return docs
}

// FixtureDoc decodes a fixture that is a JSON object (a fresh copy every call).
func FixtureDoc(name string) Doc {
	var doc Doc
	decode(name, &doc)
	return doc
}

// Int reads a number from a Doc field (0 when absent or not a number).
func Int(d Doc, key string) int {
	switch n := d[key].(type) {
	case json.Number:
		v, _ := n.Int64()
		return int(v)
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// Str reads a string from a Doc field.
func Str(d Doc, key string) string {
	s, _ := d[key].(string)
	return s
}

// Copy deep-copies docs.
func Copy(docs []Doc) []Doc {
	b, err := json.Marshal(docs)
	if err != nil {
		panic(err)
	}
	var out []Doc
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		panic(err)
	}
	return out
}

// FixtureMovies is the fixture library: five movies, one without a file.
func FixtureMovies() []Doc { return FixtureDocs("radarr/movies.json") }

// RelocateMovies moves movies under another root folder with their own ids,
// for example for a second (4K) instance.
func RelocateMovies(movies []Doc, root string, idOffset, tmdbOffset int) []Doc {
	out := Copy(movies)
	for _, m := range out {
		folder := lastSegment(Str(m, "path"))
		p := root + "/" + folder
		if file, ok := m["movieFile"].(Doc); ok {
			file["path"] = p + "/" + lastSegment(Str(file, "path"))
		}
		m["id"] = Int(m, "id") + idOffset
		m["tmdbId"] = Int(m, "tmdbId") + tmdbOffset
		m["rootFolderPath"] = root
		m["path"] = p
	}
	return out
}

// FourKMovies is a second Radarr's library: the two movies with files, under
// /movies-4k with their own ids.
func FourKMovies() []Doc {
	var withFile []Doc
	for _, m := range FixtureMovies() {
		if has, _ := m["hasFile"].(bool); has && len(withFile) < 2 {
			withFile = append(withFile, m)
		}
	}
	return RelocateMovies(withFile, "/movies-4k", 100, 100)
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
