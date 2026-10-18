package radarr_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/radarr"
)

const label = "radarr"

var ctx = context.Background()

func clientFor(f *mmtest.Fake, key string) *radarr.Client {
	return radarr.New(radarr.Config{URL: f.URL, APIKey: key}, label, mm.ArrOptions{})
}

func TestContract(t *testing.T) {
	mmtest.RunContract(t, "radarr", mm.KindMovies, func(t *testing.T) mmtest.Subject {
		return mmtest.Subject{Client: clientFor(mmtest.StartRadarr(t), mmtest.GoodKey), NewExternalID: 900090}
	})
}

func TestRobustness(t *testing.T) {
	mmtest.RunRobustness(t, label, mmtest.StartRadarr,
		func(cfg mm.ArrConfig, label string, opts mm.ArrOptions) mm.Manager {
			return radarr.New(cfg, label, opts)
		})
}

func TestDefinition(t *testing.T) {
	def := radarr.Definition()
	reg, err := core.NewRegistry(def)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get("radarr")
	if d.Role != core.RoleMediaManager || d.DisplayName != "Radarr" {
		t.Errorf("definition = %+v", d)
	}
	if !reflect.DeepEqual(d.Capabilities, mm.Capabilities{Kind: mm.KindMovies}) {
		t.Errorf("capabilities = %#v", d.Capabilities)
	}
	if !reflect.DeepEqual(d.SecretKeys(), []string{"apiKey"}) {
		t.Errorf("secret keys = %v", d.SecretKeys())
	}
	b, _ := json.Marshal(d)
	var pub struct {
		Capabilities map[string]string `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &pub); err != nil || pub.Capabilities["kind"] != "movies" {
		t.Errorf("public description = %s", b)
	}
}

func TestConfigValidation(t *testing.T) {
	reg, _ := core.NewRegistry(radarr.Definition())
	d, _ := reg.Get("radarr")
	cases := []struct {
		name string
		cfg  map[string]any
		ok   bool
	}{
		{"valid", map[string]any{"url": "http://radarr:7878", "apiKey": "k"}, true},
		{"valid with a path prefix", map[string]any{"url": "https://example.com/radarr", "apiKey": "k"}, true},
		{"not a url", map[string]any{"url": "not a url", "apiKey": "k"}, false},
		{"no scheme", map[string]any{"url": "radarr", "apiKey": "k"}, false},
		{"empty key", map[string]any{"url": "http://radarr:7878", "apiKey": ""}, false},
		{"missing key", map[string]any{"url": "http://radarr:7878"}, false},
		{"missing url", map[string]any{"apiKey": "k"}, false},
	}
	for _, c := range cases {
		_, err := d.Decode(c.cfg)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestCreateYieldsAMovieManager(t *testing.T) {
	reg, _ := core.NewRegistry(radarr.Definition())
	d, _ := reg.Get("radarr")
	fake := mmtest.StartRadarr(t)
	cfg, err := d.Decode(map[string]any{"url": fake.URL, "apiKey": mmtest.GoodKey})
	if err != nil {
		t.Fatal(err)
	}
	client := d.Create(cfg, core.ProviderContext{InstanceID: "radarr-4k", InstanceName: "Radarr 4K"})
	m, ok := mm.AsMovies(client)
	if !ok {
		t.Fatalf("client is %T", client)
	}
	if _, ok := mm.AsSeries(client); ok {
		t.Error("a Radarr client must not be a SeriesManager")
	}
	// The instance id is the error label.
	bad, _ := d.Decode(map[string]any{"url": fake.URL, "apiKey": "wrong-key-value-123"})
	r := d.Create(bad, core.ProviderContext{InstanceID: "radarr-4k"}).(mm.Manager).TestConnection(ctx)
	if r.OK || r.Error != "radarr-4k: API key incorrect" {
		t.Errorf("result = %+v", r)
	}
	if res := m.TestConnection(ctx); !res.OK || res.Version != "5.26.2.10099" {
		t.Errorf("result = %+v", res)
	}
}

func TestSendsTheKeyAsXAPIKeyAndTalksToAPIV3(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	if _, err := clientFor(fake, mmtest.GoodKey).Movies(ctx); err != nil {
		t.Fatal(err)
	}
	r := fake.Requests()[0]
	if r.Method != "GET" || r.Path != "/api/v3/movie" {
		t.Errorf("request = %s %s", r.Method, r.Path)
	}
	if r.Header.Get("X-Api-Key") != mmtest.GoodKey {
		t.Errorf("X-Api-Key = %q", r.Header.Get("X-Api-Key"))
	}
	if r.Query.Has("apikey") || r.Query.Has("apiKey") {
		t.Error("the key must not travel in the query")
	}
}

func TestKeepsMovieFieldsTheRoutesRelyOn(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	movies, err := clientFor(fake, mmtest.GoodKey).Movies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var withFile, withoutFile bool
	for _, m := range movies {
		if m.MovieFile != nil {
			withFile = true
			if m.MovieFile.MediaInfo == nil || m.MovieFile.MediaInfo.VideoCodec == "" || m.MovieFile.Size <= 0 || m.MovieFile.Path == "" {
				t.Errorf("movie %d file = %+v", m.ID, m.MovieFile)
			}
			if bd := m.MovieFile.MediaInfo.VideoBitDepth; bd == nil || (*bd != 8 && *bd != 10) {
				t.Errorf("movie %d bit depth = %v", m.ID, bd)
			}
			if m.MovieFile.MediaInfo.Resolution == "" {
				t.Errorf("movie %d has no resolution", m.ID)
			}
		}
		if !m.HasFile && m.SizeOnDisk == 0 && m.MovieFile == nil {
			withoutFile = true
		}
	}
	if !withFile || !withoutFile {
		t.Errorf("withFile = %v, withoutFile = %v", withFile, withoutFile)
	}
	if movies[0].TmdbID != 900001 || movies[0].SizeOnDisk != 2377765057 {
		t.Errorf("first movie = %+v", movies[0])
	}
}

func TestMediaInfoCarriesTheDynamicRangeFields(t *testing.T) {
	// The fixtures are all SDR (empty strings), so make one movie HDR.
	movies := mmtest.FixtureMovies()
	info := movies[0]["movieFile"].(mmtest.Doc)["mediaInfo"].(mmtest.Doc)
	info["videoDynamicRange"] = "HDR"
	info["videoDynamicRangeType"] = "HDR10"
	fake := mmtest.StartRadarr(t, mmtest.WithMovies(movies))

	got, err := clientFor(fake, mmtest.GoodKey).Movies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mi := got[0].MovieFile.MediaInfo
	if mi.VideoDynamicRange != "HDR" || mi.VideoDynamicRangeType != "HDR10" {
		t.Errorf("media info = %+v", mi)
	}
	if sdr := got[1].MovieFile.MediaInfo; sdr.VideoDynamicRange != "" || sdr.VideoDynamicRangeType != "" {
		t.Errorf("SDR media info = %+v", sdr)
	}
	// The fields survive marshaling too.
	b, _ := json.Marshal(mi)
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if back["videoDynamicRange"] != "HDR" || back["videoDynamicRangeType"] != "HDR10" || back["videoCodec"] != "x265" {
		t.Errorf("marshaled = %s", b)
	}
}

func TestMovieDetail(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	d, err := clientFor(fake, mmtest.GoodKey).MovieDetail(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != 1 || d.Title == "" || d.Overview == "" || d.Year == 0 || d.Studio == "" || len(d.Genres) == 0 || d.Runtime == 0 {
		t.Errorf("detail = %+v", d)
	}
	var poster string
	for _, img := range d.Images {
		if img.CoverType == "poster" {
			poster = img.RemoteURL
		}
	}
	if poster == "" {
		t.Errorf("no poster in %+v", d.Images)
	}
	if len(d.Ratings) == 0 {
		t.Fatal("no ratings")
	}
	for source, r := range d.Ratings {
		if source == "" || r.Value <= 0 || r.Votes < 0 {
			t.Errorf("rating %s = %+v", source, r)
		}
	}
	_, err = clientFor(fake, mmtest.GoodKey).MovieDetail(ctx, 4040)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Status != 404 {
		t.Errorf("error = %v", err)
	}
}

func TestLookupByTmdbIDLeavesIDOffResultsNotInTheLibrary(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	client := clientFor(fake, mmtest.GoodKey)

	found, err := client.LookupByExternalID(ctx, 900090)
	if err != nil || found == nil {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got := fake.Last().Query.Get("tmdbId"); got != "900090" {
		t.Errorf("tmdbId = %q", got)
	}
	if found.ID != nil || found.TmdbID != 900090 {
		t.Errorf("found = %+v", found)
	}
	var marshaled map[string]json.RawMessage
	b, _ := json.Marshal(found)
	_ = json.Unmarshal(b, &marshaled)
	if _, has := marshaled["id"]; has {
		t.Errorf("marshaled lookup carries an id: %s", b)
	}

	hits, err := client.Lookup(ctx, "some term")
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.Last().Query.Get("term"); got != "some term" {
		t.Errorf("term = %q", got)
	}
	if hits[0].ID == nil || hits[1].ID != nil {
		t.Errorf("ids = %v, %v", hits[0].ID, hits[1].ID)
	}
	if hits[1].Studio == "" || hits[1].Overview == "" || len(hits[1].Images) == 0 {
		t.Errorf("new hit = %+v", hits[1])
	}

	// A library movie found by tmdb id does carry its id.
	inLib, err := client.LookupByExternalID(ctx, 900001)
	if err != nil || inLib == nil || inLib.ID == nil || *inLib.ID != 1 {
		t.Errorf("in library = %+v, err = %v", inLib, err)
	}
}

func TestLookupByExternalIDWithAnEmptyAnswerIsNil(t *testing.T) {
	// Radarr can answer 200 with nothing; that is "no match", not an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := radarr.New(radarr.Config{URL: srv.URL, APIKey: "k"}, label, mm.ArrOptions{})
	got, err := c.LookupByExternalID(ctx, 1)
	if err != nil || got != nil {
		t.Errorf("got = %v, err = %v", got, err)
	}
}

func TestAddPostsThePayload(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	client := clientFor(fake, mmtest.GoodKey)

	base, err := client.LookupByExternalID(ctx, 900090)
	if err != nil || base == nil {
		t.Fatal(err)
	}
	payload, err := base.MovieAddPayload(mm.AddOptions{QualityProfileID: 4, RootFolderPath: "/movies", Monitored: true, SearchNow: true})
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.Add(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == nil || *created.ID != 1000 || created.Title != base.Title {
		t.Errorf("created = %+v", created)
	}
	post, found := fake.Find("POST", "/api/v3/movie")
	if !found {
		t.Fatal("no POST")
	}
	body := post.Body.(map[string]any)
	want := map[string]any{
		"qualityProfileId":    4.0,
		"rootFolderPath":      "/movies",
		"monitored":           true,
		"minimumAvailability": "released",
		"addOptions":          map[string]any{"searchForMovie": true},
	}
	for k, v := range want {
		if !reflect.DeepEqual(body[k], v) {
			t.Errorf("%s = %#v, want %#v", k, body[k], v)
		}
	}
	// Everything the lookup sent travels along, including fields we do not model.
	for _, k := range []string{"title", "tmdbId", "imdbId", "titleSlug", "images", "ratings", "keywords", "popularity", "folderName"} {
		if _, ok := body[k]; !ok {
			t.Errorf("payload lost %q", k)
		}
	}
	if _, ok := body["id"]; ok {
		t.Error("payload carries an id")
	}
	// The movie is in the fake's library now.
	if len(fake.Movies()) != 6 {
		t.Errorf("library has %d movies", len(fake.Movies()))
	}
}

func TestDeleteWithFilesAndTheEmptyOKResponse(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	if err := clientFor(fake, mmtest.GoodKey).Remove(ctx, 2); err != nil {
		t.Fatal(err)
	}
	del, found := fake.Find("DELETE", "/api/v3/movie/2")
	if !found {
		t.Fatal("no DELETE")
	}
	if del.Query.Get("deleteFiles") != "true" || del.Query.Get("addImportListExclusion") != "false" {
		t.Errorf("query = %v", del.Query)
	}
	for _, m := range fake.Movies() {
		if mmtest.Int(m, "id") == 2 {
			t.Error("movie 2 is still there")
		}
	}
}

func TestRescanUsesRescanMovie(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	if err := clientFor(fake, mmtest.GoodKey).Rescan(ctx, 3); err != nil {
		t.Fatal(err)
	}
	post, found := fake.Find("POST", "/api/v3/command")
	if !found || !reflect.DeepEqual(post.Body, map[string]any{"name": "RescanMovie", "movieId": 3.0}) {
		t.Errorf("post = %+v", post)
	}
	if post.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content type = %q", post.Header.Get("Content-Type"))
	}
}

func TestStatusNormalizes(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	st, err := clientFor(fake, mmtest.GoodKey).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "5.26.2.10099" || st.QueueCount != 0 || st.Indexers.Total == 0 {
		t.Errorf("status = %+v", st)
	}
	if !reflect.DeepEqual(st.Indexers.Issues, []string{"Example health message for IndexerLongTermStatusCheck"}) {
		t.Errorf("issues = %v", st.Indexers.Issues)
	}
	if len(st.Health) != 3 {
		t.Errorf("health = %v", st.Health)
	}
}

func TestEmptyHealthEncodesAsAnEmptyList(t *testing.T) {
	fake := mmtest.StartRadarr(t, mmtest.WithHealth(nil))
	st, err := clientFor(fake, mmtest.GoodKey).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(st)
	var back struct {
		Health   json.RawMessage `json:"health"`
		Indexers struct {
			Issues json.RawMessage `json:"issues"`
		} `json:"indexers"`
	}
	_ = json.Unmarshal(b, &back)
	if string(back.Health) != "[]" || string(back.Indexers.Issues) != "[]" {
		t.Errorf("status = %s", b)
	}
}

func TestResponseCache(t *testing.T) {
	t.Run("serves the library list from cache, then refetches after a write", func(t *testing.T) {
		fake := mmtest.StartRadarr(t)
		client := clientFor(fake, mmtest.GoodKey)

		first, err := client.Movies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		second, _ := client.Movies(ctx)
		if !reflect.DeepEqual(first, second) {
			t.Error("cached list differs")
		}
		if n := fake.Count("GET", "/api/v3/movie"); n != 1 {
			t.Errorf("GET /movie = %d", n)
		}

		if err := client.Remove(ctx, first[0].ID); err != nil {
			t.Fatal(err)
		}
		after, _ := client.Movies(ctx)
		if n := fake.Count("GET", "/api/v3/movie"); n != 2 {
			t.Errorf("GET /movie = %d", n)
		}
		for _, m := range after {
			if m.ID == first[0].ID {
				t.Error("deleted movie still listed")
			}
		}
	})

	t.Run("a cached list cannot be changed by a caller", func(t *testing.T) {
		fake := mmtest.StartRadarr(t)
		client := clientFor(fake, mmtest.GoodKey)
		a, _ := client.Movies(ctx)
		a[0].Title = "changed"
		b, _ := client.Movies(ctx)
		if b[0].Title == "changed" {
			t.Error("cache shared a mutable value")
		}
	})

	t.Run("clears on add and on rescan too", func(t *testing.T) {
		fake := mmtest.StartRadarr(t)
		client := clientFor(fake, mmtest.GoodKey)

		_, _ = client.Movies(ctx)
		if _, err := client.Add(ctx, mm.LookupResult{Title: "Example Movie 91", TmdbID: 900091}); err != nil {
			t.Fatal(err)
		}
		movies, _ := client.Movies(ctx)
		found := false
		for _, m := range movies {
			found = found || m.TmdbID == 900091
		}
		if !found || fake.Count("GET", "/api/v3/movie") != 2 {
			t.Errorf("found = %v, GETs = %d", found, fake.Count("GET", "/api/v3/movie"))
		}

		if err := client.Rescan(ctx, 1); err != nil {
			t.Fatal(err)
		}
		_, _ = client.Movies(ctx)
		if n := fake.Count("GET", "/api/v3/movie"); n != 3 {
			t.Errorf("GET /movie = %d", n)
		}
	})

	t.Run("a failed write clears it as well", func(t *testing.T) {
		fake := mmtest.StartRadarr(t)
		client := clientFor(fake, mmtest.GoodKey)
		_, _ = client.Movies(ctx)
		if err := client.Remove(ctx, 424242); err != nil && fake.Count("DELETE", "/api/v3/movie/424242") != 1 {
			t.Fatal(err)
		}
		_, _ = client.Movies(ctx)
		if n := fake.Count("GET", "/api/v3/movie"); n != 2 {
			t.Errorf("GET /movie = %d", n)
		}
	})

	t.Run("does not cache details, status or lookups", func(t *testing.T) {
		fake := mmtest.StartRadarr(t)
		client := clientFor(fake, mmtest.GoodKey)
		for range 2 {
			_, _ = client.MovieDetail(ctx, 1)
			_, _ = client.Status(ctx)
			_, _ = client.Lookup(ctx, "x")
		}
		if n := fake.Count("GET", "/api/v3/movie/1"); n != 2 {
			t.Errorf("GET /movie/1 = %d", n)
		}
		if n := fake.Count("GET", "/api/v3/system/status"); n != 2 {
			t.Errorf("GET /system/status = %d", n)
		}
		if n := fake.Count("GET", "/api/v3/movie/lookup"); n != 2 {
			t.Errorf("GET /movie/lookup = %d", n)
		}
	})

	t.Run("keeps each instance's cache separate", func(t *testing.T) {
		a := mmtest.StartRadarr(t)
		b := mmtest.StartRadarr(t, mmtest.WithMovies(nil))
		ma, _ := clientFor(a, mmtest.GoodKey).Movies(ctx)
		mb, err := clientFor(b, mmtest.GoodKey).Movies(ctx)
		if err != nil || len(ma) == 0 || len(mb) != 0 {
			t.Errorf("a = %d, b = %d, err = %v", len(ma), len(mb), err)
		}
	})
}

func TestSubPathURLs(t *testing.T) {
	fake := mmtest.StartRadarr(t, mmtest.WithPathPrefix("/radarr"))
	client := clientFor(fake, mmtest.GoodKey)
	if _, err := client.Movies(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Remove(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if fake.Count("GET", "/api/v3/movie") != 1 || fake.Count("DELETE", "/api/v3/movie/1") != 1 {
		t.Errorf("requests = %+v", fake.Requests())
	}
}

func TestTwoInstancesStaySeparate(t *testing.T) {
	hd := mmtest.StartRadarr(t)
	uhd := mmtest.StartRadarr(t, mmtest.WithMovies(mmtest.FourKMovies()), mmtest.WithRootFolders([]mmtest.Doc{
		{"id": 1, "path": "/movies-4k", "accessible": true, "freeSpace": 1000},
	}))
	a, _ := clientFor(hd, mmtest.GoodKey).Movies(ctx)
	b, _ := clientFor(uhd, mmtest.GoodKey).Movies(ctx)
	if len(a) != 5 || len(b) != 2 || b[0].RootFolderPath != "/movies-4k" || b[0].ID != 101 || b[0].TmdbID != 900101 {
		t.Errorf("hd = %d movies, 4k = %+v", len(a), b)
	}
}

func TestClientsAreSafeForConcurrentUse(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	client := clientFor(fake, mmtest.GoodKey)
	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = client.Movies(ctx)
			_, _ = client.Status(ctx)
			_ = client.Rescan(ctx, 1)
		}()
	}
	for range 8 {
		<-done
	}
}
