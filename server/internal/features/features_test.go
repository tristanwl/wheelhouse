package features

import (
	"encoding/json"
	"slices"
	"testing"
)

func availableIDs(r Response) []string {
	var ids []string
	for _, f := range r.Features {
		if f.Available {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

func TestNoProvidersMatchesNextApp(t *testing.T) {
	got, err := json.Marshal(Compute(nil, Environment{}))
	if err != nil {
		t.Fatal(err)
	}
	// Captured from the Next app booted with no environment variables and no ffmpeg.
	const want = `{"features":[` +
		`{"id":"dashboard","label":"Dashboard","route":"/","available":true,"missing":[]},` +
		`{"id":"library","label":"Library","route":"/library","available":false,"missing":["a Sonarr or Radarr instance"]},` +
		`{"id":"explore","label":"Explore","route":"/explore","available":false,"missing":["a TMDB API key","a Radarr instance"]},` +
		`{"id":"codecs","label":"Codecs","route":"/codecs","available":false,"missing":["a Sonarr or Radarr instance","ffmpeg"]}]}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestDisabledFeaturesReportTheReasonAndDashboardCannotBeDisabled(t *testing.T) {
	radarr := Instance{Role: RoleMediaManager, Kind: KindMovies, Ready: true}
	r := Compute([]Instance{radarr}, Environment{Ffmpeg: true, Disabled: map[string]bool{"library": true, "dashboard": true}})
	by := map[string]Feature{}
	for _, f := range r.Features {
		by[f.ID] = f
	}
	lib := by["library"]
	if lib.Available || !lib.Disabled || !slices.Equal(lib.Missing, []string{"disabled by admin"}) {
		t.Errorf("library = %+v", lib)
	}
	if d := by["dashboard"]; !d.Available || d.Disabled {
		t.Errorf("dashboard = %+v", d)
	}
	if !by["codecs"].Available {
		t.Error("an unrelated feature was affected")
	}
}

func TestFfmpegOnlyClearsTheToolRequirement(t *testing.T) {
	r := Compute(nil, Environment{Ffmpeg: true})
	var codecs Feature
	for _, f := range r.Features {
		if f.ID == "codecs" {
			codecs = f
		}
	}
	if !slices.Equal(codecs.Missing, []string{"a Sonarr or Radarr instance"}) {
		t.Fatalf("codecs.Missing = %v", codecs.Missing)
	}
}

func TestInstancesUnlockFeatures(t *testing.T) {
	radarr := Instance{Role: RoleMediaManager, Kind: KindMovies, Ready: true}
	sonarr := Instance{Role: RoleMediaManager, Kind: KindSeries, Ready: true}
	tmdb := Instance{Role: RoleMetadata, Ready: true}
	env := Environment{Ffmpeg: true}

	cases := []struct {
		name      string
		instances []Instance
		want      []string
	}{
		{"sonarr only", []Instance{sonarr}, []string{"dashboard", "library", "codecs"}},
		{"radarr only", []Instance{radarr}, []string{"dashboard", "library", "codecs"}},
		{"radarr and tmdb", []Instance{radarr, tmdb}, []string{"dashboard", "library", "explore", "codecs"}},
		{"sonarr and tmdb", []Instance{sonarr, tmdb}, []string{"dashboard", "library", "codecs"}},
		{"not ready", []Instance{{Role: RoleMediaManager, Kind: KindMovies}}, []string{"dashboard"}},
	}
	for _, c := range cases {
		if got := availableIDs(Compute(c.instances, env)); !slices.Equal(got, c.want) {
			t.Errorf("%s: available = %v, want %v", c.name, got, c.want)
		}
	}
}
