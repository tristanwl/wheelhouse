package sonarr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/sonarr"
)

const label = "sonarr"

var ctx = context.Background()

func clientFor(f *mmtest.Fake, key string) *sonarr.Client {
	return sonarr.New(sonarr.Config{URL: f.URL, APIKey: key}, label, mm.ArrOptions{})
}

func TestContract(t *testing.T) {
	mmtest.RunContract(t, "sonarr", mm.KindSeries, func(t *testing.T) mmtest.Subject {
		return mmtest.Subject{Client: clientFor(mmtest.StartSonarr(t), mmtest.GoodKey), NewExternalID: 800099}
	})
}

func TestRobustness(t *testing.T) {
	mmtest.RunRobustness(t, label, mmtest.StartSonarr,
		func(cfg mm.ArrConfig, label string, opts mm.ArrOptions) mm.Manager {
			return sonarr.New(cfg, label, opts)
		})
}

func TestDefinition(t *testing.T) {
	reg, err := core.NewRegistry(sonarr.Definition())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get("sonarr")
	if d.Role != core.RoleMediaManager || d.DisplayName != "Sonarr" {
		t.Errorf("definition = %+v", d)
	}
	if !reflect.DeepEqual(d.Capabilities, mm.Capabilities{Kind: mm.KindSeries}) {
		t.Errorf("capabilities = %#v", d.Capabilities)
	}
	if !reflect.DeepEqual(d.SecretKeys(), []string{"apiKey"}) {
		t.Errorf("secret keys = %v", d.SecretKeys())
	}
	b, _ := json.Marshal(d)
	var pub struct {
		Capabilities map[string]string `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &pub); err != nil || pub.Capabilities["kind"] != "series" {
		t.Errorf("public description = %s", b)
	}
}

func TestConfigValidation(t *testing.T) {
	reg, _ := core.NewRegistry(sonarr.Definition())
	d, _ := reg.Get("sonarr")
	cases := []struct {
		name string
		cfg  map[string]any
		ok   bool
	}{
		{"valid", map[string]any{"url": "http://sonarr:8989", "apiKey": "k"}, true},
		{"valid with a path prefix", map[string]any{"url": "https://example.com/sonarr", "apiKey": "k"}, true},
		{"no scheme", map[string]any{"url": "sonarr", "apiKey": "k"}, false},
		{"empty key", map[string]any{"url": "http://sonarr:8989", "apiKey": ""}, false},
		{"missing key", map[string]any{"url": "http://sonarr:8989"}, false},
	}
	for _, c := range cases {
		_, err := d.Decode(c.cfg)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestCreateYieldsASeriesManager(t *testing.T) {
	reg, _ := core.NewRegistry(sonarr.Definition())
	d, _ := reg.Get("sonarr")
	fake := mmtest.StartSonarr(t)
	cfg, err := d.Decode(map[string]any{"url": fake.URL, "apiKey": mmtest.GoodKey})
	if err != nil {
		t.Fatal(err)
	}
	client := d.Create(cfg, core.ProviderContext{InstanceID: "sonarr", InstanceName: "Sonarr"})
	if _, ok := mm.AsSeries(client); !ok {
		t.Fatalf("client is %T", client)
	}
	if _, ok := mm.AsMovies(client); ok {
		t.Error("a Sonarr client must not be a MovieManager")
	}
}

func TestSendsTheKeyAsXAPIKey(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	if _, err := clientFor(fake, mmtest.GoodKey).Series(ctx); err != nil {
		t.Fatal(err)
	}
	r := fake.Requests()[0]
	if r.Method != "GET" || r.Path != "/api/v3/series" || r.Header.Get("X-Api-Key") != mmtest.GoodKey {
		t.Errorf("request = %+v", r)
	}
}

func TestStatusNormalizes(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	st, err := clientFor(fake, mmtest.GoodKey).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	indexers := mmtest.FixtureDocs("sonarr/indexer.json")
	enabled := 0
	for _, i := range indexers {
		if on, _ := i["enableRss"].(bool); on {
			enabled++
		}
	}
	if st.Version != "4.0.15.2941" || st.QueueCount != 2 {
		t.Errorf("status = %+v", st)
	}
	if st.Indexers.Total != len(indexers) || st.Indexers.Enabled != enabled {
		t.Errorf("indexers = %+v, want %d/%d", st.Indexers, enabled, len(indexers))
	}
	if !reflect.DeepEqual(st.Indexers.Issues, []string{"Example health message for IndexerLongTermStatusCheck"}) {
		t.Errorf("issues = %v", st.Indexers.Issues)
	}
}

func TestQueueSummaryAsksForTheFirstPage(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	q, err := clientFor(fake, mmtest.GoodKey).QueueSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := fake.Last()
	if last.Query.Get("page") != "1" || last.Query.Get("pageSize") != "25" {
		t.Errorf("query = %v", last.Query)
	}
	if q.TotalRecords != 2 || len(q.Records) != 2 {
		t.Fatalf("queue = %+v", q)
	}
	r := q.Records[0]
	if r.Title == "" || r.Size != 2828170382 || r.Status != "completed" || r.TrackedDownloadStatus != "warning" {
		t.Errorf("record = %+v", r)
	}
}

func TestAsksForSeriesByTvdbTerm(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	found, err := clientFor(fake, mmtest.GoodKey).LookupByExternalID(ctx, 800099)
	if err != nil || found == nil {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got := fake.Last().Query.Get("term"); got != "tvdb:800099" {
		t.Errorf("term = %q", got)
	}
	if found.TvdbID != 800099 || found.ID != nil || found.Network == "" {
		t.Errorf("found = %+v", found)
	}
}

func TestLookupByExternalIDWithNoResultsIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c := sonarr.New(sonarr.Config{URL: srv.URL, APIKey: "k"}, label, mm.ArrOptions{})
	got, err := c.LookupByExternalID(ctx, 1)
	if err != nil || got != nil {
		t.Errorf("got = %v, err = %v", got, err)
	}
}

func TestReadsEpisodeFilesAndEpisodesPerSeries(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	client := clientFor(fake, mmtest.GoodKey)
	files, err := client.EpisodeFiles(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	episodes, err := client.Episodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	f := files[0]
	if f.ID != 101 || f.SeasonNumber != 1 || f.Size != 4610490430 || f.Path == "" {
		t.Errorf("file = %+v", f)
	}
	if f.MediaInfo == nil || f.MediaInfo.VideoCodec != "x264" || f.MediaInfo.Resolution != "1920x1080" ||
		f.MediaInfo.VideoBitDepth == nil || *f.MediaInfo.VideoBitDepth != 8 {
		t.Errorf("media info = %+v", f.MediaInfo)
	}
	linked := false
	for _, e := range episodes {
		if e.SeriesID != 1 {
			t.Errorf("episode of another series: %+v", e)
		}
		linked = linked || e.EpisodeFileID == f.ID
	}
	if !linked || len(episodes) == 0 {
		t.Errorf("no episode links file %d: %+v", f.ID, episodes)
	}
	if got := fake.Requests()[0].Query.Get("seriesId"); got != "1" {
		t.Errorf("seriesId = %q", got)
	}
}

func TestSeriesDetail(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	d, err := clientFor(fake, mmtest.GoodKey).SeriesDetail(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != 1 || d.Overview == "" || d.Year == 0 || d.Network == "" || len(d.Genres) == 0 || d.Runtime == 0 ||
		d.Status == "" || d.Ratings == nil || d.Statistics == nil || d.Statistics.SizeOnDisk <= 0 {
		t.Errorf("detail = %+v", d)
	}
	hasPoster := slices.ContainsFunc(d.Images, func(i mm.Image) bool { return i.CoverType == "poster" && i.RemoteURL != "" })
	if !hasPoster {
		t.Errorf("images = %+v", d.Images)
	}
}

func TestDeleteSeriesWithFilesAnEpisodeFileAndRescan(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	client := clientFor(fake, mmtest.GoodKey)

	if err := client.Remove(ctx, 2); err != nil {
		t.Fatal(err)
	}
	del, found := fake.Find("DELETE", "/api/v3/series/2")
	if !found || del.Query.Get("deleteFiles") != "true" || del.Query.Get("addImportListExclusion") != "false" {
		t.Errorf("delete = %+v", del)
	}

	files, _ := client.EpisodeFiles(ctx, 1)
	if err := client.DeleteEpisodeFile(ctx, files[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, found := fake.Find("DELETE", "/api/v3/episodefile/101"); !found {
		t.Error("episode file not deleted")
	}

	if err := client.Rescan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := fake.Last().Body; !reflect.DeepEqual(got, map[string]any{"name": "RescanSeries", "seriesId": 1.0}) {
		t.Errorf("rescan body = %#v", got)
	}
}

func TestAddPostsThePayload(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	client := clientFor(fake, mmtest.GoodKey)
	base, _ := client.LookupByExternalID(ctx, 800099)
	payload, err := base.SeriesAddPayload(mm.AddOptions{QualityProfileID: 4, RootFolderPath: "/tv", Monitored: true, SearchNow: false})
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.Add(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == nil || *created.ID != 1000 || created.TvdbID != 800099 {
		t.Errorf("created = %+v", created)
	}
	post, _ := fake.Find("POST", "/api/v3/series")
	body := post.Body.(map[string]any)
	want := map[string]any{
		"qualityProfileId":  4.0,
		"languageProfileId": 1.0,
		"rootFolderPath":    "/tv",
		"monitored":         true,
		"seasonFolder":      true,
		"monitorNewItems":   "all",
		"addOptions":        map[string]any{"searchForMissingEpisodes": false},
	}
	for k, v := range want {
		if !reflect.DeepEqual(body[k], v) {
			t.Errorf("%s = %#v, want %#v", k, body[k], v)
		}
	}
	for _, k := range []string{"title", "tvdbId", "seasons", "images", "titleSlug", "ratings", "tags"} {
		if _, ok := body[k]; !ok {
			t.Errorf("payload lost %q", k)
		}
	}
	if _, ok := body["id"]; ok {
		t.Error("payload carries an id")
	}
}

func TestSeasonsAreOrdered(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	list, err := clientFor(fake, mmtest.GoodKey).Seasons(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if list.Title == "" || len(list.Seasons) < 2 {
		t.Fatalf("list = %+v", list)
	}
	for i := 1; i < len(list.Seasons); i++ {
		if list.Seasons[i-1].SeasonNumber >= list.Seasons[i].SeasonNumber {
			t.Errorf("not ordered: %d then %d", list.Seasons[i-1].SeasonNumber, list.Seasons[i].SeasonNumber)
		}
	}
	s1 := list.Seasons[1]
	if s1.SeasonNumber != 1 || s1.Statistics == nil || s1.Statistics.EpisodeFileCount != 1 ||
		s1.Statistics.TotalEpisodeCount != 10 || s1.Statistics.SizeOnDisk != 4610490430 {
		t.Errorf("season 1 = %+v", s1)
	}
}

func TestSeasonsSortsWhatTheServiceSendsOutOfOrder(t *testing.T) {
	series := mmtest.FixtureDocs("sonarr/series.json")
	seasons := series[0]["seasons"].([]any)
	slices.Reverse(seasons)
	fake := mmtest.StartSonarr(t, mmtest.WithSeries(series))
	list, err := clientFor(fake, mmtest.GoodKey).Seasons(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, s := range list.Seasons {
		numbers = append(numbers, s.SeasonNumber)
	}
	if !slices.IsSorted(numbers) || numbers[0] != 0 {
		t.Errorf("numbers = %v", numbers)
	}
}

func TestUpdatingASeriesPutsEveryFieldBack(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	client := clientFor(fake, mmtest.GoodKey)
	series, err := client.SeriesByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range series.Seasons {
		series.Seasons[i].Monitored = false
	}
	if _, err := client.UpdateSeries(ctx, series); err != nil {
		t.Fatal(err)
	}
	put, found := fake.Find("PUT", "/api/v3/series/1")
	if !found {
		t.Fatal("no PUT")
	}
	body := put.Body.(map[string]any)

	// Every key the service sent is still there, modelled or not.
	original := mmtest.FixtureDocs("sonarr/series.json")[0]
	for k := range original {
		if _, ok := body[k]; !ok {
			t.Errorf("PUT lost %q", k)
		}
	}
	if len(body) != len(original) {
		t.Errorf("PUT has %d keys, want %d", len(body), len(original))
	}
	// Nested fields this package does not model survive too.
	origSeasons := original["seasons"].([]any)
	for i, raw := range body["seasons"].([]any) {
		s := raw.(map[string]any)
		if s["monitored"] != false {
			t.Errorf("season %d still monitored", i)
		}
		stats := s["statistics"].(map[string]any)
		wantStats := origSeasons[i].(map[string]any)["statistics"].(map[string]any)
		if len(stats) != len(wantStats) {
			t.Errorf("season %d statistics = %v, want %v", i, stats, wantStats)
		}
	}
	// Unchanged scalar and nested values are byte-for-byte what was sent.
	for _, k := range []string{"title", "tvdbId", "images", "ratings", "statistics", "alternateTitles", "added"} {
		want, _ := json.Marshal(original[k])
		got, _ := json.Marshal(body[k])
		var a, b any
		_ = json.Unmarshal(want, &a)
		_ = json.Unmarshal(got, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s changed: %s -> %s", k, want, got)
		}
	}
}

func TestDeleteSeason(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	client := clientFor(fake, mmtest.GoodKey)

	n, err := client.DeleteSeason(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("files deleted = %d", n)
	}
	if _, found := fake.Find("DELETE", "/api/v3/episodefile/101"); !found {
		t.Error("the season's file was not deleted")
	}
	var seasons []any
	for _, s := range fake.SeriesDocs() {
		if mmtest.Int(s, "id") == 1 {
			seasons = s["seasons"].([]any)
		}
	}
	for _, raw := range seasons {
		s := raw.(map[string]any)
		monitored := s["monitored"].(bool)
		if mmtest.Int(s, "seasonNumber") == 1 && monitored {
			t.Error("season 1 is still monitored")
		}
	}
	// Order: files first, then the update.
	var order []string
	for _, r := range fake.Requests() {
		if r.Method == "DELETE" || r.Method == "PUT" {
			order = append(order, r.Method)
		}
	}
	if !reflect.DeepEqual(order, []string{"DELETE", "PUT"}) {
		t.Errorf("write order = %v", order)
	}
}

func TestDeleteSeasonWithNoFilesStillUnmonitors(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	n, err := clientFor(fake, mmtest.GoodKey).DeleteSeason(ctx, 1, 7)
	if err != nil || n != 0 {
		t.Errorf("n = %d, err = %v", n, err)
	}
	if fake.Count("PUT", "/api/v3/series/1") != 1 {
		t.Error("series not updated")
	}
}

func TestResponseCache(t *testing.T) {
	fake := mmtest.StartSonarr(t)
	client := clientFor(fake, mmtest.GoodKey)

	_, _ = client.Series(ctx)
	_, _ = client.Series(ctx)
	if n := fake.Count("GET", "/api/v3/series"); n != 1 {
		t.Errorf("GET /series = %d", n)
	}

	if err := client.Remove(ctx, 3); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Series(ctx)
	if n := fake.Count("GET", "/api/v3/series"); n != 2 {
		t.Errorf("after remove: GET /series = %d", n)
	}

	files, _ := client.EpisodeFiles(ctx, 1)
	if err := client.DeleteEpisodeFile(ctx, files[0].ID); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Series(ctx)
	if n := fake.Count("GET", "/api/v3/series"); n != 3 {
		t.Errorf("after file delete: GET /series = %d", n)
	}

	_, _ = client.Series(ctx)
	if n := fake.Count("GET", "/api/v3/series"); n != 3 {
		t.Errorf("cached again: GET /series = %d", n)
	}

	// Updates and rescans clear it as well.
	full, _ := client.SeriesByID(ctx, 1)
	if _, err := client.UpdateSeries(ctx, full); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Series(ctx)
	if err := client.Rescan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Series(ctx)
	if n := fake.Count("GET", "/api/v3/series"); n != 5 {
		t.Errorf("after update and rescan: GET /series = %d", n)
	}
}

func TestSubPathURLs(t *testing.T) {
	fake := mmtest.StartSonarr(t, mmtest.WithPathPrefix("/sonarr"))
	client := clientFor(fake, mmtest.GoodKey)
	if _, err := client.Series(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteSeason(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if fake.Count("PUT", "/api/v3/series/1") != 1 {
		t.Errorf("requests = %+v", fake.Requests())
	}
}
