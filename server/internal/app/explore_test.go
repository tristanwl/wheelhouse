package app

import (
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

const (
	neitherID   = 5
	exploreKeyC = "canary-explore-key-0f3a9c"
)

func TestExploreMarksMoviesInEitherRadarr(t *testing.T) {
	a := mmtest.FixtureMovies()
	b := mmtest.FourKMovies()
	inA, inB := mmtest.Int(a[0], "tmdbId"), mmtest.Int(b[0], "tmdbId")
	tm := tmdbtest.Start(t, tmdbtest.Options{MovieIDs: []int{inA, inB, neitherID}})
	radarr := mmtest.StartRadarr(t)
	radarr4k := mmtest.StartRadarr(t, mmtest.WithMovies(b))
	h, logs := stack(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_4K_URL": radarr4k.URL, "RADARR_4K_API_KEY": mmtest.GoodKey,
		"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": tmdbtest.GoodKey,
	}, time.Now())

	for _, cat := range []string{"popular", "theaters", "bluray"} {
		code, body, raw := getJSON(t, h, "/api/explore?category="+cat)
		if code != 200 || body["hasMore"] != false {
			t.Fatalf("%s: %d %s", cat, code, raw)
		}
		var ids []int
		var added []bool
		for _, r := range body["results"].([]any) {
			m := r.(map[string]any)
			ids = append(ids, int(m["id"].(float64)))
			added = append(added, m["alreadyAdded"].(bool))
			if m["year"] != float64(2024) || m["title"] == "" || m["posterUrl"] == nil {
				t.Errorf("%s: %v", cat, m)
			}
		}
		if !slices.Equal(ids, []int{inA, inB, neitherID}) || !slices.Equal(added, []bool{true, true, false}) {
			t.Errorf("%s: ids %v added %v", cat, ids, added)
		}
		// The mark says which instance has the movie.
		var where []string
		for _, r := range body["results"].([]any) {
			var names []string
			for _, ref := range r.(map[string]any)["libraryInstances"].([]any) {
				names = append(names, ref.(map[string]any)["instanceId"].(string))
			}
			where = append(where, strings.Join(names, "+"))
		}
		if !slices.Equal(where, []string{"radarr", "radarr-4k", ""}) {
			t.Errorf("%s: libraryInstances %v", cat, where)
		}
	}

	_, body, _ := getJSON(t, h, "/api/explore?category=popular&limit=2")
	if len(body["results"].([]any)) != 2 {
		t.Errorf("limit=2: %v", body["results"])
	}

	code, d, raw := getJSON(t, h, "/api/explore/"+strconv.Itoa(inB))
	if code != 200 || d["alreadyAdded"] != true || d["year"] != float64(2024) || d["runtime"] != float64(100) || d["trailerKey"] != nil {
		t.Fatalf("detail: %d %s", code, raw)
	}
	if g := d["genres"].([]any); len(g) != 1 || g[0] != "Drama" {
		t.Errorf("genres: %v", d["genres"])
	}
	if !strings.HasPrefix(d["backdropUrl"].(string), "https://image.tmdb.org/t/p/w780/") {
		t.Errorf("backdrop: %v", d["backdropUrl"])
	}
	_, d, _ = getJSON(t, h, "/api/explore/"+strconv.Itoa(neitherID))
	if d["alreadyAdded"] != false {
		t.Errorf("neither: %v", d)
	}

	// The key is sent to TMDB but never shown or logged.
	if strings.Contains(logs.String(), tmdbtest.GoodKey) || strings.Contains(logs.String(), "category=") {
		t.Errorf("log: %s", logs)
	}
}

func TestExplorePagingAndLimitReachTMDB(t *testing.T) {
	tm := tmdbtest.Start(t, tmdbtest.Options{})
	h, _ := stack(t, env{"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": tmdbtest.GoodKey}, time.Now())
	pages := func(query string) []string {
		before := len(tm.Requests())
		if code, _, raw := getJSON(t, h, "/api/explore?"+query); code != 200 {
			t.Fatalf("%s: %d %s", query, code, raw)
		}
		var got []string
		for _, u := range tm.Requests()[before:] {
			got = append(got, u.Query().Get("page"))
		}
		slices.Sort(got)
		return got
	}
	for query, want := range map[string]string{
		"category=popular":                     "1",
		"category=popular&limit=40&page=2":     "3,4",
		"category=popular&limit=100":           "1,2,3", // clamped to 60
		"category=popular&limit=abc&page=0":    "1",     // junk falls back to the defaults
		"category=popular&limit=0":             "1",     // clamped up to 1
		"category=popular&page=2.5":            "1",
		"category=popular&limit=20&page=3":     "3",
		"category=theaters&limit=-5&page=1e0":  "1",
		"category=popular&limit=40&page=9999":  "", // past TMDB's 500 pages: nothing to ask
		"category=popular&limit=60&page=167":   "499,500",
		"category=popular&limit=%2040%20&page": "1,2", // Number(" 40 ") and Number("") = 0
	} {
		if got := strings.Join(pages(query), ","); got != want {
			t.Errorf("%s: pages %q, want %q", query, got, want)
		}
	}
}

func TestExploreRejectsBadInput(t *testing.T) {
	tm := tmdbtest.Start(t, tmdbtest.Options{})
	h, _ := stack(t, env{"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": tmdbtest.GoodKey}, time.Now())
	for path, msg := range map[string]string{
		"/api/explore":                   "category must be",
		"/api/explore?category=nope":     "category must be",
		"/api/explore?category=Popular":  "category must be",
		"/api/explore/abc":               "invalid id",
		"/api/explore/1.5":               "invalid id",
		"/api/explore/Infinity":          "invalid id",
		"/api/explore/12abc":             "invalid id",
		"/api/explore/99999999999999999": "invalid id",
	} {
		code, body, raw := getJSON(t, h, path)
		if code != 400 || !strings.Contains(body["error"].(string), msg) {
			t.Errorf("%s: %d %s", path, code, raw)
		}
	}
	if n := len(tm.Requests()); n != 0 {
		t.Errorf("bad input still reached TMDB (%d requests)", n)
	}
}

func TestExploreTMDBFailuresAre502WithAMessage(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		h, _ := stack(t, env{}, time.Now())
		for _, p := range []string{"/api/explore?category=popular", "/api/explore/101"} {
			code, body, raw := getJSON(t, h, p)
			if code != 502 || body["error"] != "TMDB is not configured. Set TMDB_API_KEY." {
				t.Errorf("%s: %d %s", p, code, raw)
			}
		}
	})
	t.Run("rejected key", func(t *testing.T) {
		tm := tmdbtest.Start(t, tmdbtest.Options{})
		h, _ := stack(t, env{"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": exploreKeyC}, time.Now())
		for _, p := range []string{"/api/explore?category=popular", "/api/explore/101"} {
			code, body, raw := getJSON(t, h, p)
			if code != 502 || body["error"] == "" || strings.Contains(raw, exploreKeyC) {
				t.Errorf("%s: %d %s", p, code, raw)
			}
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		tm := tmdbtest.Start(t, tmdbtest.Options{})
		root := tm.APIRoot()
		tm.Close()
		h, _ := stack(t, env{"TMDB_BASE_URL": root, "TMDB_API_KEY": exploreKeyC}, time.Now())
		code, body, raw := getJSON(t, h, "/api/explore?category=popular")
		if code != 502 || body["error"] == "" || strings.Contains(raw, exploreKeyC) || strings.Contains(raw, root) {
			t.Errorf("%d %s", code, raw)
		}
	})
	t.Run("invalid config names the problem without the key", func(t *testing.T) {
		h, _ := stack(t, env{"TMDB_BASE_URL": "http://user:" + exploreKeyC + "@example.com/3", "TMDB_API_KEY": exploreKeyC}, time.Now())
		code, body, raw := getJSON(t, h, "/api/explore?category=popular")
		if code != 502 || !strings.HasPrefix(body["error"].(string), "TMDB: ") || strings.Contains(raw, exploreKeyC) {
			t.Errorf("%d %s", code, raw)
		}
	})
}

func TestExploreSurvivesABrokenRadarr(t *testing.T) {
	a := mmtest.FixtureMovies()
	b := mmtest.FourKMovies()
	inA, inB := mmtest.Int(a[0], "tmdbId"), mmtest.Int(b[0], "tmdbId")
	tm := tmdbtest.Start(t, tmdbtest.Options{MovieIDs: []int{inA, inB}})
	radarr := mmtest.StartRadarr(t)
	radarr4k := mmtest.StartRadarr(t, mmtest.WithMovies(b))
	radarr.Close()
	h, _ := stack(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_4K_URL": radarr4k.URL, "RADARR_4K_API_KEY": mmtest.GoodKey,
		"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": tmdbtest.GoodKey,
	}, time.Now())
	code, body, raw := getJSON(t, h, "/api/explore?category=popular")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	r := body["results"].([]any)
	if r[0].(map[string]any)["alreadyAdded"] != false || r[1].(map[string]any)["alreadyAdded"] != true {
		t.Errorf("only the instance that answers counts: %s", raw)
	}
	// A wrong key on one Radarr is the same.
	h, _ = stack(t, env{
		"RADARR_URL": radarr4k.URL, "RADARR_API_KEY": "wrong-key-value-123",
		"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": tmdbtest.GoodKey,
	}, time.Now())
	if code, _, raw = getJSON(t, h, "/api/explore/"+strconv.Itoa(inB)); code != 200 || !strings.Contains(raw, `"alreadyAdded":false`) {
		t.Errorf("%d %s", code, raw)
	}
}

func TestExploreEmptyListIsAnArray(t *testing.T) {
	tm := tmdbtest.Start(t, tmdbtest.Options{MovieIDs: []int{}})
	h, _ := stack(t, env{"TMDB_BASE_URL": tm.APIRoot(), "TMDB_API_KEY": tmdbtest.GoodKey}, time.Now())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/explore?category=popular", nil))
	if strings.TrimSpace(rec.Body.String()) != `{"results":[],"hasMore":false}` {
		t.Errorf("%s", rec.Body)
	}
}
