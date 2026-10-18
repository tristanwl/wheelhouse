package mediamanager

import (
	"encoding/json"
	"reflect"
	"testing"
)

const lookupJSON = `{
  "title": "Example Movie 90",
  "year": 2026,
  "tmdbId": 900090,
  "studio": "Example Studio",
  "overview": "An example.",
  "images": [{"coverType":"poster","remoteUrl":"https://example.com/p.jpg","url":"/x"}],
  "ratings": {"imdb": {"votes": 10, "value": 7.5, "type": "user"}},
  "future": {"nested": [1, 2, {"a": null}]},
  "monitored": false
}`

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v in %s", err, b)
	}
	return m
}

func TestLookupResultRoundTripKeepsEveryField(t *testing.T) {
	var r LookupResult
	if err := json.Unmarshal([]byte(lookupJSON), &r); err != nil {
		t.Fatal(err)
	}
	if r.Title != "Example Movie 90" || r.Year != 2026 || r.TmdbID != 900090 || r.Studio != "Example Studio" || r.ID != nil {
		t.Errorf("typed fields = %+v", r)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodeMap(t, out), decodeMap(t, []byte(lookupJSON))) {
		t.Errorf("round trip changed the document:\n%s", out)
	}
}

func TestLookupResultTypedEditsWin(t *testing.T) {
	var r LookupResult
	_ = json.Unmarshal([]byte(lookupJSON), &r)
	r.Title = "Renamed"
	out, _ := json.Marshal(r)
	m := decodeMap(t, out)
	if m["title"] != "Renamed" || m["future"] == nil || m["tmdbId"] != 900090.0 {
		t.Errorf("document = %s", out)
	}
}

func TestLookupResultSetAndUnset(t *testing.T) {
	var r LookupResult
	_ = json.Unmarshal([]byte(`{"id": 5, "title": "T", "year": 2000, "extra": 1}`), &r)
	if r.ID == nil || *r.ID != 5 {
		t.Fatalf("id = %v", r.ID)
	}
	stripped, err := r.Unset("id")
	if err != nil {
		t.Fatal(err)
	}
	if stripped.ID != nil {
		t.Error("typed id still set")
	}
	out, _ := json.Marshal(stripped)
	if m := decodeMap(t, out); m["id"] != nil || m["extra"] != 1.0 || m["title"] != "T" {
		t.Errorf("document = %s", out)
	}
	if r.ID == nil {
		t.Error("Unset changed the original")
	}

	changed, err := stripped.Set("title", "New")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Title != "New" || stripped.Title != "T" {
		t.Errorf("titles = %q, %q", changed.Title, stripped.Title)
	}
	withNew, _ := changed.Set("brandNew", map[string]any{"a": []int{1}})
	out, _ = json.Marshal(withNew)
	if m := decodeMap(t, out); !reflect.DeepEqual(m["brandNew"], map[string]any{"a": []any{1.0}}) || m["extra"] != 1.0 {
		t.Errorf("document = %s", out)
	}
	if _, err := changed.Set("bad", make(chan int)); err == nil {
		t.Error("expected an error for an unencodable value")
	}
}

func TestSeriesAddPayload(t *testing.T) {
	var base LookupResult
	_ = json.Unmarshal([]byte(`{"id": 9, "title": "T", "year": 2000, "tvdbId": 77, "seasons": [{"seasonNumber": 1, "monitored": true}], "images": []}`), &base)
	p, err := base.SeriesAddPayload(AddOptions{QualityProfileID: 3, RootFolderPath: "/tv", Monitored: true, SearchNow: true})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(p)
	want := map[string]any{
		"title": "T", "year": 2000.0, "tvdbId": 77.0,
		"seasons": []any{map[string]any{"seasonNumber": 1.0, "monitored": true}}, "images": []any{},
		"qualityProfileId": 3.0, "languageProfileId": 1.0, "rootFolderPath": "/tv", "monitored": true,
		"seasonFolder": true, "monitorNewItems": "all",
		"addOptions": map[string]any{"searchForMissingEpisodes": true},
	}
	if got := decodeMap(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %s", out)
	}
}

func TestMovieAddPayload(t *testing.T) {
	var base LookupResult
	_ = json.Unmarshal([]byte(`{"title": "T", "year": 2000, "tmdbId": 77, "minimumAvailability": "announced"}`), &base)
	p, _ := base.MovieAddPayload(AddOptions{QualityProfileID: 3, RootFolderPath: "/movies"})
	m := decodeMap(t, mustMarshal(t, p))
	if m["minimumAvailability"] != "released" || m["monitored"] != false ||
		!reflect.DeepEqual(m["addOptions"], map[string]any{"searchForMovie": false}) {
		t.Errorf("payload = %v", m)
	}
	p, _ = base.MovieAddPayload(AddOptions{QualityProfileID: 3, RootFolderPath: "/movies", MinimumAvailability: "inCinemas", SearchNow: true})
	m = decodeMap(t, mustMarshal(t, p))
	if m["minimumAvailability"] != "inCinemas" || !reflect.DeepEqual(m["addOptions"], map[string]any{"searchForMovie": true}) {
		t.Errorf("payload = %v", m)
	}
	// The lookup result itself is not changed.
	if m := decodeMap(t, mustMarshal(t, base)); m["minimumAvailability"] != "announced" || m["rootFolderPath"] != nil {
		t.Errorf("base = %v", m)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const seriesJSON = `{
  "id": 1, "title": "Example Series 1", "monitored": true, "path": "/tv/Example Series 1",
  "rootFolderPath": "/tv", "added": "2025-01-01T00:00:00Z",
  "statistics": {"sizeOnDisk": 100, "episodeFileCount": 2, "seasonCount": 3},
  "tags": [1, 2],
  "addOptions": {"x": true},
  "seasons": [
    {"seasonNumber": 1, "monitored": true, "images": [{"coverType":"poster"}],
     "statistics": {"episodeFileCount": 1, "episodeCount": 1, "totalEpisodeCount": 10, "sizeOnDisk": 50, "percentOfEpisodes": 100}},
    {"seasonNumber": 2, "monitored": true}
  ]
}`

func TestSeriesFullRoundTripAndEdit(t *testing.T) {
	var s SeriesFull
	if err := json.Unmarshal([]byte(seriesJSON), &s); err != nil {
		t.Fatal(err)
	}
	if s.ID != 1 || len(s.Seasons) != 2 || s.Statistics.SizeOnDisk != 100 {
		t.Fatalf("typed fields = %+v", s)
	}
	out := mustMarshal(t, s)
	if !reflect.DeepEqual(decodeMap(t, out), decodeMap(t, []byte(seriesJSON))) {
		t.Errorf("unchanged round trip differs:\n%s", out)
	}

	s.Seasons[0].Monitored = false
	s.Monitored = false
	got := decodeMap(t, mustMarshal(t, s))
	if got["monitored"] != false {
		t.Errorf("series monitored = %v", got["monitored"])
	}
	seasons := got["seasons"].([]any)
	s1 := seasons[0].(map[string]any)
	if s1["monitored"] != false || s1["images"] == nil {
		t.Errorf("season 1 = %v", s1)
	}
	if stats := s1["statistics"].(map[string]any); stats["percentOfEpisodes"] != 100.0 {
		t.Errorf("season statistics lost a field: %v", stats)
	}
	if s2 := seasons[1].(map[string]any); s2["monitored"] != true {
		t.Errorf("season 2 = %v", s2)
	}
	if got["tags"] == nil || got["addOptions"] == nil {
		t.Errorf("unmodelled fields lost: %v", got)
	}
	// Changing the number of seasons is a typed edit too.
	s.Seasons = s.Seasons[:1]
	if n := len(decodeMap(t, mustMarshal(t, s))["seasons"].([]any)); n != 1 {
		t.Errorf("seasons = %d", n)
	}
	// Clearing an optional field removes it.
	s.Statistics = nil
	if _, has := decodeMap(t, mustMarshal(t, s))["statistics"]; has {
		t.Error("cleared statistics still present")
	}
}

func TestZeroValuesMarshalWithoutADocument(t *testing.T) {
	b := mustMarshal(t, SeriesFull{Series: Series{ID: 4, Title: "T"}})
	m := decodeMap(t, b)
	if m["id"] != 4.0 || m["title"] != "T" {
		t.Errorf("document = %s", b)
	}
	b = mustMarshal(t, LookupResult{Title: "T"})
	if m := decodeMap(t, b); m["title"] != "T" {
		t.Errorf("document = %s", b)
	}
}

func TestMalformedDocumentsAreErrors(t *testing.T) {
	var r LookupResult
	err := json.Unmarshal([]byte(`{"title": 5}`), &r)
	if err == nil {
		t.Fatal("expected an error")
	}
	// (The Arr client never shows this error; it reports "returned invalid JSON".)
	var s SeriesFull
	if err := json.Unmarshal([]byte(`[1,2]`), &s); err == nil {
		t.Error("expected an error for an array")
	}
}

func TestQueueRecordSizesMayBeFractional(t *testing.T) {
	var q QueueSummary
	if err := json.Unmarshal([]byte(`{"totalRecords":1,"records":[{"id":1,"title":"T","size":1000.5,"sizeleft":12.25,"status":"downloading"}]}`), &q); err != nil {
		t.Fatal(err)
	}
	if q.Records[0].SizeLeft != 12.25 || q.Records[0].Size != 1000.5 {
		t.Errorf("record = %+v", q.Records[0])
	}
}
