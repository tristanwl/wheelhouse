package tmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

func video(mod func(*Video)) Video {
	v := Video{Key: "k", Site: "YouTube", Type: "Trailer", Official: true, Name: "x"}
	if mod != nil {
		mod(&v)
	}
	return v
}

func TestPickTrailerKeyReturnsNothingWithoutVideos(t *testing.T) {
	if k, ok := PickTrailerKey(nil); ok || k != "" {
		t.Errorf("nil: %q %v", k, ok)
	}
	if k, ok := PickTrailerKey([]Video{}); ok || k != "" {
		t.Errorf("empty: %q %v", k, ok)
	}
}

func TestPickTrailerKeyPrefersAnOfficialVideoNamedOfficialTrailer(t *testing.T) {
	k, ok := PickTrailerKey([]Video{
		video(func(v *Video) { v.Key, v.Type = "teaser", "Teaser" }),
		video(func(v *Video) { v.Key, v.Name = "plain", "Trailer 2" }),
		video(func(v *Video) { v.Key, v.Name = "best", "Official Trailer" }),
	})
	if !ok || k != "best" {
		t.Errorf("got %q %v", k, ok)
	}
}

func TestPickTrailerKeyMatchesOfficialTrailerCaseInsensitively(t *testing.T) {
	k, _ := PickTrailerKey([]Video{
		video(func(v *Video) { v.Key, v.Name = "plain", "Trailer 2" }),
		video(func(v *Video) { v.Key, v.Name = "best", "THE OFFICIAL TRAILER (4K)" }),
	})
	if k != "best" {
		t.Errorf("got %q", k)
	}
}

func TestPickTrailerKeyFallsBackOfficialTrailerThenAnyTrailerThenTeaser(t *testing.T) {
	pick := func(vs ...Video) string { k, _ := PickTrailerKey(vs); return k }
	notOfficial := func(key string) Video { return video(func(v *Video) { v.Key, v.Official = key, false }) }
	if got := pick(notOfficial("a"), video(func(v *Video) { v.Key = "b" })); got != "b" {
		t.Errorf("official: %q", got)
	}
	if got := pick(notOfficial("a")); got != "a" {
		t.Errorf("any trailer: %q", got)
	}
	if got := pick(video(func(v *Video) { v.Key, v.Type = "t", "Teaser" })); got != "t" {
		t.Errorf("teaser: %q", got)
	}
	if got := pick(video(func(v *Video) { v.Type = "Featurette" })); got != "" {
		t.Errorf("featurette: %q", got)
	}
}

func TestPickTrailerKeyIgnoresNonYouTubeVideos(t *testing.T) {
	if k, ok := PickTrailerKey([]Video{video(func(v *Video) { v.Site = "Vimeo" })}); ok || k != "" {
		t.Errorf("got %q %v", k, ok)
	}
}

func TestImageURLs(t *testing.T) {
	p := "/poster1.jpg"
	if u, ok := PosterURL(&p); !ok || u != "https://image.tmdb.org/t/p/w342/poster1.jpg" {
		t.Errorf("poster = %q %v", u, ok)
	}
	if u, ok := BackdropURL(&p); !ok || u != "https://image.tmdb.org/t/p/w780/poster1.jpg" {
		t.Errorf("backdrop = %q %v", u, ok)
	}
	empty := ""
	for _, in := range []*string{nil, &empty} {
		if _, ok := PosterURL(in); ok {
			t.Error("poster for nothing")
		}
		if _, ok := BackdropURL(in); ok {
			t.Error("backdrop for nothing")
		}
	}
}

func TestYear(t *testing.T) {
	for in, want := range map[string]int{"2024-01-01": 2024, "1999-12-31": 1999, "2024": 2024} {
		if y := (Movie{ReleaseDate: in}).Year(); y == nil || *y != want {
			t.Errorf("%q: %v", in, y)
		}
	}
	for _, in := range []string{"", "abcd-01-01", "20"} {
		if y := (Movie{ReleaseDate: in}).Year(); y != nil {
			t.Errorf("%q: %d", in, *y)
		}
	}
}

func TestAttributionIsExposed(t *testing.T) {
	if !strings.Contains(AttributionText, "uses the TMDB API") || !strings.Contains(AttributionText, "not endorsed or certified") ||
		AttributionURL != "https://www.themoviedb.org" {
		t.Errorf("attribution = %q %q", AttributionText, AttributionURL)
	}
}

// pagingServer answers every page with 20 movies whose ids encode the page.
func pagingServer(t *testing.T, totalPages int) (*Client, func() []int) {
	t.Helper()
	var mu sync.Mutex
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		results := make([]map[string]any, 20)
		for i := range results {
			results[i] = map[string]any{
				"id": page*100 + i, "title": fmt.Sprintf("Movie %d-%d", page, i), "overview": "",
				"release_date": "2024-01-01", "poster_path": nil, "vote_average": 7,
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "total_pages": totalPages})
	}))
	t.Cleanup(srv.Close)
	c := tuned(t, srv.URL, nil)
	return c, func() []int {
		mu.Lock()
		defer mu.Unlock()
		out := slices.Clone(pages)
		slices.Sort(out)
		return out
	}
}

func TestListFetchesPages1And2ForTheFirstBatchOf40(t *testing.T) {
	c, pages := pagingServer(t, 600)
	got, err := c.Popular(ctx, 40, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pages(), []int{1, 2}) || len(got.Results) != 40 || !got.HasMore {
		t.Errorf("pages %v, %d results, hasMore %v", pages(), len(got.Results), got.HasMore)
	}
	// Order follows the pages.
	if got.Results[0].ID != 100 || got.Results[20].ID != 200 {
		t.Errorf("ids = %d, %d", got.Results[0].ID, got.Results[20].ID)
	}
}

func TestListMapsBatchNToTheNextTMDBPages(t *testing.T) {
	c, pages := pagingServer(t, 600)
	if _, err := c.Popular(ctx, 40, 2); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pages(), []int{3, 4}) {
		t.Errorf("pages = %v", pages())
	}
}

func TestListReportsNoMoreOnceTMDBsPageCapIsReached(t *testing.T) {
	c, pages := pagingServer(t, 600)
	last, err := c.Popular(ctx, 40, 250)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pages(), []int{499, 500}) || last.HasMore {
		t.Errorf("pages %v, hasMore %v", pages(), last.HasMore)
	}
}

func TestListReturnsNothingWithoutFetchingPastTheCap(t *testing.T) {
	c, pages := pagingServer(t, 600)
	past, err := c.Popular(ctx, 40, 251)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages()) != 0 || len(past.Results) != 0 || past.HasMore {
		t.Errorf("pages %v, past %+v", pages(), past)
	}
}

func TestListStopsWhenTMDBHasNoMorePages(t *testing.T) {
	c, _ := pagingServer(t, 2)
	got, _ := c.Popular(ctx, 40, 1)
	if got.HasMore {
		t.Error("hasMore with exactly the last page fetched")
	}
	c, _ = pagingServer(t, 3)
	if got, _ = c.Popular(ctx, 40, 1); !got.HasMore {
		t.Error("page 3 still to come")
	}
}

func TestListTrimsToTheRequestedCount(t *testing.T) {
	c, pages := pagingServer(t, 600)
	got, _ := c.Popular(ctx, 30, 1)
	if len(got.Results) != 30 || !slices.Equal(pages(), []int{1, 2}) {
		t.Errorf("%d results, pages %v", len(got.Results), pages())
	}
	c, pages = pagingServer(t, 600)
	got, _ = c.Popular(ctx, 5, 3)
	if len(got.Results) != 5 || !slices.Equal(pages(), []int{3}) {
		t.Errorf("%d results, pages %v", len(got.Results), pages())
	}
}

func TestListTreatsTooSmallCountAndPageAsOne(t *testing.T) {
	c, pages := pagingServer(t, 600)
	got, err := c.Popular(ctx, 0, 0)
	if err != nil || len(got.Results) != 1 || !slices.Equal(pages(), []int{1}) {
		t.Errorf("%v: %d results, pages %v", err, len(got.Results), pages())
	}
}

func TestListFailsIfAnyPageFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"total_pages":9}`))
	}))
	defer srv.Close()
	_, err := tuned(t, srv.URL, nil).Popular(ctx, 40, 1)
	if err == nil || !strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), tmdbtest.GoodKey) {
		t.Errorf("err = %v", err)
	}
}

func TestEndpointsAndParameters(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	c := create(t, tmdbtest.GoodKey, srv.APIRoot())
	c.now = func() time.Time { return time.Date(2025, 3, 9, 23, 30, 0, 0, time.FixedZone("x", -8*3600)) }

	if _, err := c.NowPlaying(ctx, 20, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Popular(ctx, 20, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecentPhysicalReleases(ctx, 20, 1); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %v", reqs)
	}
	want := []struct {
		path   string
		params map[string]string
	}{
		{"/3/movie/now_playing", map[string]string{"region": "US", "page": "1"}},
		{"/3/movie/popular", map[string]string{"region": "US", "page": "1"}},
		{"/3/discover/movie", map[string]string{
			"region": "US", "page": "1", "with_release_type": "5", "sort_by": "primary_release_date.desc",
			// The clock is 2025-03-09 23:30 at UTC-8, which is already the 10th in UTC.
			"release_date.lte": "2025-03-10", "vote_count.gte": "10",
		}},
	}
	for i, w := range want {
		if reqs[i].Path != w.path {
			t.Errorf("path %d = %q", i, reqs[i].Path)
		}
		q := reqs[i].Query()
		for k, v := range w.params {
			if q.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", w.path, k, q.Get(k), v)
			}
		}
		if q.Get("api_key") != tmdbtest.GoodKey {
			t.Errorf("%s: no key", w.path)
		}
		if len(q) != len(w.params)+1 {
			t.Errorf("%s: unexpected params %v", w.path, q)
		}
	}
}

func TestMovieDetail(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	c := create(t, tmdbtest.GoodKey, srv.APIRoot())
	d, err := c.MovieDetail(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != 101 || d.Title != "Example Movie 101" || d.Overview != "A made-up plot." ||
		len(d.Genres) != 1 || d.Genres[0].Name != "Drama" ||
		d.Runtime == nil || *d.Runtime != 100 || d.VoteAverage != 7.5 {
		t.Errorf("detail = %+v", d)
	}
	if u, ok := PosterURL(d.PosterPath); !ok || u != "https://image.tmdb.org/t/p/w342/poster101.jpg" {
		t.Errorf("poster = %q", u)
	}
	if u, ok := BackdropURL(d.BackdropPath); !ok || u != "https://image.tmdb.org/t/p/w780/backdrop.jpg" {
		t.Errorf("backdrop = %q", u)
	}
	if y := d.Year(); y == nil || *y != 2024 {
		t.Errorf("year = %v", y)
	}
	if _, ok := d.TrailerKey(); ok {
		t.Error("trailer in a detail without videos")
	}
	req := srv.Requests()[0]
	if req.Path != "/3/movie/101" || req.Query().Get("append_to_response") != "videos" {
		t.Errorf("request = %v", req)
	}
}

func TestMovieDetailWithVideosAndNulls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":7,"title":"Example Movie 7","overview":"","release_date":"",
			"poster_path":null,"vote_average":0,"genres":[],"runtime":null,"backdrop_path":null,
			"videos":{"results":[{"key":"abc","site":"YouTube","type":"Teaser","official":false,"name":"t"}]}}`))
	}))
	defer srv.Close()
	d, err := tuned(t, srv.URL, nil).MovieDetail(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if d.Runtime != nil || d.PosterPath != nil || d.BackdropPath != nil || d.Year() != nil {
		t.Errorf("nulls: %+v", d)
	}
	if k, ok := d.TrailerKey(); !ok || k != "abc" {
		t.Errorf("trailer = %q %v", k, ok)
	}
}

func TestListThroughTheFakeServer(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{MovieIDs: []int{5, 6}})
	got, err := create(t, tmdbtest.GoodKey, srv.APIRoot()).Popular(ctx, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 2 || got.Results[0].ID != 5 || got.Results[1].Title != "Example Movie 6" || got.HasMore {
		t.Errorf("got %+v", got)
	}
}
