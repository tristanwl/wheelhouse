package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
)

func send(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	h.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

func libraryStack(t *testing.T) (http.Handler, *mmtest.Fake, *mmtest.Fake, *mmtest.Fake, *bytes.Buffer) {
	t.Helper()
	radarr := mmtest.StartRadarr(t)
	fourK := mmtest.StartRadarr(t, mmtest.WithMovies(mmtest.FourKMovies()))
	sonarr := mmtest.StartSonarr(t)
	h, logs := stack(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_4K_URL": fourK.URL, "RADARR_4K_API_KEY": mmtest.GoodKey,
		"SONARR_URL": sonarr.URL, "SONARR_API_KEY": mmtest.GoodKey,
	}, sabNow)
	return h, radarr, fourK, sonarr, logs
}

func TestLibraryMergesInstancesBiggestFirst(t *testing.T) {
	h, _, _, _, _ := libraryStack(t)
	code, body, raw := send(t, h, "GET", "/api/library", "")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	items := body["items"].([]any)
	seen := map[string]bool{}
	prev := -1.0
	for i, it := range items {
		m := it.(map[string]any)
		seen[m["instanceId"].(string)] = true
		size := m["sizeOnDisk"].(float64)
		if i > 0 && size > prev {
			t.Errorf("not biggest first at %d: %v after %v", i, size, prev)
		}
		prev = size
	}
	if !seen["radarr"] || !seen["radarr-4k"] || !seen["sonarr"] {
		t.Errorf("an instance is missing from the merged library: %v", seen)
	}
	if n := len(body["instances"].([]any)); n != 3 {
		t.Errorf("want 3 instance summaries, got %d", n)
	}
}

func TestLibraryKeepsHealthyInstancesWhenOneIsBroken(t *testing.T) {
	const canary = "CANARY-KEY-5a4b3c2d1e"
	radarr := mmtest.StartRadarr(t)
	sonarr := mmtest.StartSonarr(t)
	h, logs := stack(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": canary, // rejected
		"SONARR_URL": sonarr.URL, "SONARR_API_KEY": mmtest.GoodKey,
	}, sabNow)
	for _, path := range []string{"/api/library", "/api/storage"} {
		code, body, raw := send(t, h, "GET", path, "")
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, raw)
		}
		var down, up int
		for _, s := range body["instances"].([]any) {
			m := s.(map[string]any)
			if m["up"] == true {
				up++
			} else if m["error"] != "" && m["error"] != nil {
				down++
			}
		}
		if up != 1 || down != 1 {
			t.Errorf("%s: instances %s", path, raw)
		}
		for _, it := range itemsOrEntries(body) {
			if it.(map[string]any)["instanceId"] != "sonarr" {
				t.Errorf("%s: row from the broken instance: %v", path, it)
			}
		}
		if strings.Contains(raw, canary) || strings.Contains(logs.String(), canary) {
			t.Errorf("%s leaked the key", path)
		}
	}
	// A route addressed to the broken instance is a 502 with a reason.
	code, body, raw := send(t, h, "GET", "/api/library/radarr/1/details", "")
	if code != 502 || !strings.Contains(body["error"].(string), "API key incorrect") || strings.Contains(raw, canary) {
		t.Errorf("details of a broken instance: %d %s", code, raw)
	}
}

func itemsOrEntries(body map[string]any) []any {
	if v, ok := body["items"].([]any); ok {
		return v
	}
	return body["entries"].([]any)
}

func TestStoragePlacesItemsByPathWithRootFallback(t *testing.T) {
	movies := []mmtest.Doc{
		{"id": 1, "title": "Example Movie 1", "path": "/movies/a", "rootFolderPath": "/movies", "sizeOnDisk": 100},
		// No path of its own: placed by its root folder path.
		{"id": 2, "title": "Example Movie 2", "rootFolderPath": "/movies", "sizeOnDisk": 50},
		// Inside the nested (deepest) root only.
		{"id": 3, "title": "Example Movie 3", "path": "/movies/4k/c", "sizeOnDisk": 7},
		// Outside every root: counted nowhere. A sibling with a shared prefix is not "within".
		{"id": 4, "title": "Example Movie 4", "path": "/movies-other/d", "sizeOnDisk": 900},
	}
	roots := []mmtest.Doc{
		{"id": 2, "path": "/movies/4k", "accessible": true, "freeSpace": 2000},
		{"id": 1, "path": "/movies/", "accessible": true, "freeSpace": 1000},
	}
	radarr := mmtest.StartRadarr(t, mmtest.WithMovies(movies), mmtest.WithRootFolders(roots))
	h, _ := stack(t, env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey}, sabNow)
	code, body, raw := send(t, h, "GET", "/api/storage", "")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	got := map[string]float64{}
	for _, e := range body["entries"].([]any) {
		m := e.(map[string]any)
		if m["label"] != "Movies" || m["instanceId"] != "radarr" {
			t.Errorf("entry: %v", m)
		}
		got[m["path"].(string)] = m["libraryUsedBytes"].(float64)
	}
	if got["/movies/"] != 150 || got["/movies/4k"] != 7 {
		t.Errorf("used bytes per root: %v", got)
	}
}

func TestStorageLabelsUseInstanceNamesWhenAKindHasSeveral(t *testing.T) {
	h, _, _, _, _ := libraryStack(t)
	_, body, raw := send(t, h, "GET", "/api/storage", "")
	labels := map[string]string{}
	for _, e := range body["entries"].([]any) {
		m := e.(map[string]any)
		labels[m["instanceId"].(string)] = m["label"].(string)
	}
	if labels["radarr-4k"] != "Radarr 4K" || labels["sonarr"] != "TV" || labels["radarr"] == "Movies" {
		t.Errorf("labels: %v in %s", labels, raw)
	}
}

func TestDeleteNeedsServerSideConfirmation(t *testing.T) {
	h, radarr, fourK, sonarr, logs := libraryStack(t)

	for _, path := range []string{
		"/api/library/radarr/1",
		"/api/library/radarr/1?confirm=false",
		"/api/library/radarr/1?confirm=1",
		"/api/library/sonarr/1/seasons/1",
	} {
		code, body, raw := send(t, h, "DELETE", path, "")
		if code != 400 || !strings.Contains(body["error"].(string), "confirm") {
			t.Errorf("%s without confirmation: %d %s", path, code, raw)
		}
	}
	if radarr.Count("DELETE", "/api/v3/movie/1") != 0 || len(sonarr.Requests()) != 0 && sonarr.Count("DELETE", "/api/v3/episodefile/1") != 0 {
		t.Fatal("an unconfirmed request reached the upstream")
	}
	// Malformed requests keep their specific answers, confirmed or not.
	for path, want := range map[string]int{
		"/api/library/radarr/x":                      400,
		"/api/library/nope/1":                        404,
		"/api/library/radarr/1/seasons/1":            400,
		"/api/library/nope/1/seasons/1?confirm=true": 404,
	} {
		if code, _, raw := send(t, h, "DELETE", path, ""); code != want {
			t.Errorf("%s: %d %s, want %d", path, code, raw, want)
		}
	}

	code, body, raw := send(t, h, "DELETE", "/api/library/radarr/1?confirm=true", "")
	if code != 200 || body["ok"] != true {
		t.Fatalf("confirmed delete: %d %s", code, raw)
	}
	if radarr.Count("DELETE", "/api/v3/movie/1") != 1 {
		t.Error("the movie was not removed")
	}
	if req, _ := radarr.Find("DELETE", "/api/v3/movie/1"); req.Query.Get("deleteFiles") != "true" {
		t.Errorf("deleteFiles not sent: %v", req.Query)
	}
	for _, f := range []*mmtest.Fake{fourK} {
		for _, r := range f.Requests() {
			if r.Method == "DELETE" {
				t.Errorf("another instance was touched: %v", r)
			}
		}
	}
	if !strings.Contains(logs.String(), "library.delete") {
		t.Errorf("no audit entry: %s", logs)
	}
	if strings.Contains(logs.String(), "?") {
		t.Errorf("a query string reached the log: %s", logs)
	}

	code, body, raw = send(t, h, "DELETE", "/api/library/sonarr/1/seasons/1?confirm=true", "")
	if code != 200 || body["ok"] != true || body["filesDeleted"] == nil {
		t.Fatalf("confirmed season delete: %d %s", code, raw)
	}
}

func TestSearchAddMetaAndAdd(t *testing.T) {
	h, radarr, fourK, sonarr, _ := libraryStack(t)

	code, body, raw := send(t, h, "GET", "/api/search?kind=movies&term=example", "")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	hits := body["results"].([]any)
	byInstance := map[string]int{}
	for _, x := range hits {
		m := x.(map[string]any)
		byInstance[m["instanceId"].(string)]++
		if m["id"].(float64) == 0 {
			t.Errorf("hit without external id: %v", m)
		}
	}
	if byInstance["radarr"] == 0 || byInstance["radarr-4k"] == 0 || byInstance["sonarr"] != 0 {
		t.Errorf("fan-out: %v", byInstance)
	}
	if code, body, _ := send(t, h, "GET", "/api/search?kind=movies&term=%20%20", ""); code != 200 || len(body["results"].([]any)) != 0 {
		t.Errorf("blank term: %d %v", code, body)
	}
	if code, _, _ := send(t, h, "GET", "/api/search?kind=nope&term=x", ""); code != 400 {
		t.Errorf("bad kind: %d", code)
	}

	code, meta, raw := send(t, h, "GET", "/api/add-meta?instanceId=radarr-4k", "")
	if code != 200 || meta["instanceId"] != "radarr-4k" || meta["rootFolderPath"] == nil || len(meta["qualityProfiles"].([]any)) == 0 {
		t.Fatalf("add-meta: %d %s", code, raw)
	}
	if code, _, _ := send(t, h, "GET", "/api/add-meta", ""); code != 400 {
		t.Errorf("add-meta without params: %d", code)
	}

	add := func(payload string) (int, map[string]any, string) { return send(t, h, "POST", "/api/add", payload) }
	for _, bad := range []string{"not json", `{}`, `{"instanceId":"radarr","id":1}`, `{"instanceId":"radarr","id":"x","qualityProfileId":1,"rootFolderPath":"/m"}`} {
		if code, _, raw := add(bad); code != 400 {
			t.Errorf("bad body %q: %d %s", bad, code, raw)
		}
	}
	if code, _, _ := add(`{"instanceId":"nope","id":5,"qualityProfileId":1,"rootFolderPath":"/m"}`); code != 404 {
		t.Errorf("unknown instance: %d", code)
	}
	code, created, raw := add(`{"instanceId":"radarr-4k","id":424242,"qualityProfileId":4,"rootFolderPath":"/movies-4k","monitored":true,"searchNow":true}`)
	if code != 200 || created["ok"] != true || created["id"] == nil {
		t.Fatalf("add movie: %d %s", code, raw)
	}
	post, ok := fourK.Find("POST", "/api/v3/movie")
	if !ok {
		t.Fatal("4K Radarr got no add")
	}
	b := post.Body.(map[string]any)
	if b["qualityProfileId"].(float64) != 4 || b["minimumAvailability"] != "released" || b["rootFolderPath"] != "/movies-4k" ||
		b["addOptions"].(map[string]any)["searchForMovie"] != true {
		t.Errorf("add payload: %v", b)
	}
	if radarr.Count("POST", "/api/v3/movie") != 0 {
		t.Error("the wrong Radarr received the add")
	}

	code, _, raw = add(`{"instanceId":"sonarr","id":99001,"qualityProfileId":2,"rootFolderPath":"/tv"}`)
	if code != 200 {
		t.Fatalf("add series: %d %s", code, raw)
	}
	sp, _ := sonarr.Find("POST", "/api/v3/series")
	sb := sp.Body.(map[string]any)
	if _, has := sb["id"]; has || sb["seasonFolder"] != true || sb["languageProfileId"].(float64) != 1 {
		t.Errorf("series payload: %v", sb)
	}
}

func TestSearchWithEveryInstanceBrokenIs502(t *testing.T) {
	const canary = "CANARY-KEY-77aa88bb99"
	radarr := mmtest.StartRadarr(t)
	h, _ := stack(t, env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": canary}, sabNow)
	code, body, raw := send(t, h, "GET", "/api/search?kind=movies&term=x", "")
	if code != 502 || body["problems"] == nil || strings.Contains(raw, canary) {
		t.Errorf("%d %s", code, raw)
	}
	if code, _, _ := send(t, h, "GET", "/api/search?kind=series&term=x", ""); code != 404 {
		t.Errorf("no Sonarr: %d", code)
	}
}
