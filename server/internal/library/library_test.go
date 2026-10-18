package library

import (
	"testing"

	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

func TestIsWithin(t *testing.T) {
	for _, tc := range []struct {
		root, path string
		want       bool
	}{
		{"/movies", "/movies", true},
		{"/movies/", "/movies/a", true},
		{"/movies", "/movies-4k/a", false},
		{"/movies", "/movie", false},
		{"/", "/anything", true},
		{"/movies", "", false},
	} {
		if got := isWithin(tc.root, tc.path); got != tc.want {
			t.Errorf("isWithin(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestToHitMarksAddedAndSkipsItemsWithoutExternalID(t *testing.T) {
	id := 3
	e := mm.Entry{InstanceID: "radarr", Name: "Radarr"}
	h, ok := toHit(mm.LookupResult{ID: &id, TmdbID: 5, Title: "Example Movie 1"}, mm.KindMovies, e)
	if !ok || !h.AlreadyAdded || h.ID != 5 || h.InstanceID != "radarr" {
		t.Errorf("hit %+v ok=%v", h, ok)
	}
	h, ok = toHit(mm.LookupResult{TmdbID: 6}, mm.KindMovies, e)
	if !ok || h.AlreadyAdded {
		t.Errorf("a result without id is not added: %+v", h)
	}
	if _, ok := toHit(mm.LookupResult{TvdbID: 9}, mm.KindMovies, e); ok {
		t.Error("a movie hit needs a tmdb id")
	}
}
