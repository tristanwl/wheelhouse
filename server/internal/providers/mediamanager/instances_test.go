package mediamanager_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/radarr"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/sonarr"
)

var ctx = context.Background()

func runtimeFor(t *testing.T, env map[string]string) *core.Runtime {
	t.Helper()
	reg, err := core.NewRegistry(sonarr.Definition(), radarr.Definition())
	if err != nil {
		t.Fatal(err)
	}
	return core.NewRuntime(reg, core.NewEnvConfigSource(env))
}

func ids(entries []mm.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.InstanceID)
	}
	return out
}

func TestListManagers(t *testing.T) {
	radarrHD := mmtest.StartRadarr(t)
	radarr4K := mmtest.StartRadarr(t, mmtest.WithMovies(mmtest.FourKMovies()))
	sonarrFake := mmtest.StartSonarr(t)
	rt := runtimeFor(t, map[string]string{
		"SONARR_URL": sonarrFake.URL, "SONARR_API_KEY": mmtest.GoodKey,
		"RADARR_URL": radarrHD.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_4K_URL": radarr4K.URL, "RADARR_4K_API_KEY": mmtest.GoodKey,
	})

	all, err := mm.ListManagers(ctx, rt, "")
	if err != nil {
		t.Fatal(err)
	}
	// Configured order: Sonarr, Radarr, then extra instances.
	if !reflect.DeepEqual(ids(all), []string{"sonarr", "radarr", "radarr-4k"}) {
		t.Fatalf("ids = %v", ids(all))
	}
	if all[2].Name != "Radarr 4K" || all[2].Kind != mm.KindMovies || all[0].Kind != mm.KindSeries {
		t.Errorf("entries = %+v", all)
	}
	for _, e := range all {
		if e.Client == nil || e.Error != "" {
			t.Errorf("entry %s: client = %v, error = %q", e.InstanceID, e.Client, e.Error)
		}
	}
	if _, ok := mm.AsSeries(all[0].Client); !ok {
		t.Error("sonarr is not a SeriesManager")
	}

	movies, _ := mm.ListManagers(ctx, rt, mm.KindMovies)
	if !reflect.DeepEqual(ids(movies), []string{"radarr", "radarr-4k"}) {
		t.Errorf("movies = %v", ids(movies))
	}
	series, _ := mm.ListManagers(ctx, rt, mm.KindSeries)
	if !reflect.DeepEqual(ids(series), []string{"sonarr"}) {
		t.Errorf("series = %v", ids(series))
	}
}

func TestListManagersNothingConfigured(t *testing.T) {
	entries, err := mm.ListManagers(ctx, runtimeFor(t, nil), "")
	if err != nil || len(entries) != 0 {
		t.Errorf("entries = %v, err = %v", entries, err)
	}
	if mm.NoManagerMessage(mm.KindMovies) != "No Radarr instance is configured" ||
		mm.NoManagerMessage(mm.KindSeries) != "No Sonarr instance is configured" {
		t.Error("no-manager messages")
	}
	if mm.KindLabel[mm.KindMovies] != "Movies" || mm.KindLabel[mm.KindSeries] != "TV" {
		t.Error("kind labels")
	}
}

func TestBrokenInstancesAreListedWithoutAClientAndNeverLeakTheirSecrets(t *testing.T) {
	good := mmtest.StartRadarr(t)
	rt := runtimeFor(t, map[string]string{
		"RADARR_URL": good.URL, "RADARR_API_KEY": mmtest.GoodKey,
		// A URL that is not one, and a key we must not see in the error.
		"RADARR_4K_URL": "not a url", "RADARR_4K_API_KEY": mmtest.CanaryKey,
		// A URL without its key.
		"SONARR_URL": "http://192.0.2.10:8989",
	})
	entries, err := mm.ListManagers(ctx, rt, "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]mm.Entry{}
	for _, e := range entries {
		byID[e.InstanceID] = e
	}
	if len(byID) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if byID["radarr"].Client == nil {
		t.Error("radarr should be ready")
	}
	for _, id := range []string{"radarr-4k", "sonarr"} {
		e := byID[id]
		if e.Client != nil || e.Error == "" {
			t.Errorf("%s: client = %v, error = %q", id, e.Client, e.Error)
		}
		if mmtest.Leak(e.Error) != "" || strings.Contains(e.Error, "not a url") {
			t.Errorf("%s: error leaks config: %q", id, e.Error)
		}
	}
	// The kind of a broken instance is still known, so it can be filtered.
	if byID["sonarr"].Kind != mm.KindSeries {
		t.Errorf("kind = %q", byID["sonarr"].Kind)
	}
	movies, _ := mm.ListManagers(ctx, rt, mm.KindMovies)
	if !reflect.DeepEqual(ids(movies), []string{"radarr", "radarr-4k"}) {
		t.Errorf("movies = %v", ids(movies))
	}
}

func TestCollectFromManagersReportsProblemsWithoutHidingTheOthers(t *testing.T) {
	good := mmtest.StartRadarr(t)
	wrongKey := mmtest.StartRadarr(t)
	down := mmtest.StartRadarr(t)
	rt := runtimeFor(t, map[string]string{
		"RADARR_URL": good.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_BAD_URL": wrongKey.URL, "RADARR_BAD_API_KEY": "wrong-key-value-123",
		"RADARR_DOWN_URL": down.URL, "RADARR_DOWN_API_KEY": mmtest.GoodKey,
		"RADARR_BROKEN_URL": "http://192.0.2.10:7878",
	})
	down.Close()

	entries, _ := mm.ListManagers(ctx, rt, mm.KindMovies)
	results, problems := mm.CollectFromManagers(ctx, entries,
		func(ctx context.Context, c mm.Manager, _ mm.Entry) (int, error) {
			m, _ := mm.AsMovies(c)
			movies, err := m.Movies(ctx)
			return len(movies), err
		})

	if len(results) != 1 || results[0].Entry.InstanceID != "radarr" || results[0].Value != 5 {
		t.Errorf("results = %+v", results)
	}
	got := map[string]string{}
	for _, p := range problems {
		got[p.InstanceID] = p.Error
	}
	if got["radarr-bad"] != "radarr-bad: API key incorrect" || got["radarr-down"] != "radarr-down: connection refused" {
		t.Errorf("problems = %v", got)
	}
	// radarr-broken has a URL but no key: invalid config, reported as the problem.
	if len(problems) != 3 || !strings.Contains(got["radarr-broken"], "apiKey: is required") {
		t.Errorf("problems = %v", got)
	}

	summaries := mm.Summarize(entries, problems)
	if len(summaries) != len(entries) {
		t.Fatalf("summaries = %+v", summaries)
	}
	for _, s := range summaries {
		if s.Up != (s.InstanceID == "radarr") || (s.Up && s.Error != "") || (!s.Up && s.Error == "") {
			t.Errorf("summary = %+v", s)
		}
	}
	b, _ := json.Marshal(summaries[0])
	if string(b) != `{"instanceId":"radarr","name":"Radarr","kind":"movies","up":true}` {
		t.Errorf("json = %s", b)
	}
}

func TestCollectFromManagersRedactsErrorsFromTheCallback(t *testing.T) {
	good := mmtest.StartRadarr(t)
	rt := runtimeFor(t, map[string]string{"RADARR_URL": good.URL, "RADARR_API_KEY": mmtest.GoodKey})
	entries, _ := mm.ListManagers(ctx, rt, "")
	_, problems := mm.CollectFromManagers(ctx, entries,
		func(context.Context, mm.Manager, mm.Entry) (int, error) {
			return 0, errors.New("failed at http://user:secretpass@example.com/?apikey=abcd1234efgh")
		})
	if len(problems) != 1 || strings.Contains(problems[0].Error, "secretpass") || strings.Contains(problems[0].Error, "abcd1234efgh") {
		t.Errorf("problems = %+v", problems)
	}
}

func TestCollectRunsInstancesInParallel(t *testing.T) {
	a := mmtest.StartRadarr(t, mmtest.WithDelay(150*time.Millisecond))
	b := mmtest.StartRadarr(t, mmtest.WithDelay(150*time.Millisecond))
	rt := runtimeFor(t, map[string]string{
		"RADARR_URL": a.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_B_URL": b.URL, "RADARR_B_API_KEY": mmtest.GoodKey,
	})
	entries, _ := mm.ListManagers(ctx, rt, "")
	results, problems := mm.CollectFromManagers(ctx, entries,
		func(ctx context.Context, c mm.Manager, _ mm.Entry) (string, error) {
			s, err := c.SystemStatus(ctx)
			return s.Version, err
		})
	if len(problems) != 0 || len(results) != 2 || results[0].Value != "5.26.2.10099" {
		t.Errorf("results = %+v, problems = %+v", results, problems)
	}
	// Order follows the entries.
	if results[0].Entry.InstanceID != "radarr" || results[1].Entry.InstanceID != "radarr-b" {
		t.Errorf("order = %v, %v", results[0].Entry.InstanceID, results[1].Entry.InstanceID)
	}
}

func TestStatusEndpointShape(t *testing.T) {
	// Status is what /api/status relays for a media manager: its JSON shape
	// must match the TypeScript MediaManagerStatus.
	fake := mmtest.StartSonarr(t)
	st, err := sonarr.New(sonarr.Config{URL: fake.URL, APIKey: mmtest.GoodKey}, "sonarr", mm.ArrOptions{}).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(st)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"version", "health", "queueCount", "indexers"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %s", k, b)
		}
	}
	idx := m["indexers"].(map[string]any)
	for _, k := range []string{"total", "enabled", "issues"} {
		if _, ok := idx[k]; !ok {
			t.Errorf("missing indexers.%s in %s", k, b)
		}
	}
	h := m["health"].([]any)[0].(map[string]any)
	for _, k := range []string{"source", "type", "message", "wikiUrl"} {
		if _, ok := h[k]; !ok {
			t.Errorf("missing health.%s in %s", k, b)
		}
	}
}

type staticSource []core.Instance

func (s staticSource) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func TestDisabledInstancesAreNotListed(t *testing.T) {
	fake := mmtest.StartRadarr(t)
	cfg := map[string]any{"url": fake.URL, "apiKey": mmtest.GoodKey}
	reg, _ := core.NewRegistry(sonarr.Definition(), radarr.Definition())
	rt := core.NewRuntime(reg, staticSource{
		{ID: "radarr", ProviderID: "radarr", Name: "Radarr", Enabled: true, Config: cfg},
		{ID: "radarr-off", ProviderID: "radarr", Name: "Radarr off", Enabled: false, Config: cfg},
		{ID: "mystery", ProviderID: "no-such-provider", Name: "Mystery", Enabled: true, Config: cfg},
	})
	entries, err := mm.ListManagers(ctx, rt, "")
	if err != nil || !reflect.DeepEqual(ids(entries), []string{"radarr"}) {
		t.Errorf("entries = %v, err = %v", ids(entries), err)
	}
}
