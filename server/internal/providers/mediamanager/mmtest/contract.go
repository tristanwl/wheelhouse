package mmtest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Subject is what a contract run needs: a client pointed at a service whose
// library has at least one item, and the external id (tmdbId or tvdbId) of an
// item that is NOT in the library yet.
type Subject struct {
	Client        mm.Manager
	NewExternalID int
}

// RunContract runs the invariants every media-manager adapter must satisfy (the
// Go port of describeMediaManagerContract). setup is called once.
func RunContract(t *testing.T, name string, kind mm.Kind, setup func(t *testing.T) Subject) {
	t.Helper()
	t.Run(name+" satisfies the MediaManager contract", func(t *testing.T) {
		s := setup(t)
		ctx := context.Background()
		client := s.Client

		t.Run("knows its kind", func(t *testing.T) {
			if client.Kind() != kind {
				t.Fatalf("kind = %q, want %q", client.Kind(), kind)
			}
			switch kind {
			case mm.KindMovies:
				if _, ok := mm.AsMovies(client); !ok {
					t.Fatal("not a MovieManager")
				}
			case mm.KindSeries:
				if _, ok := mm.AsSeries(client); !ok {
					t.Fatal("not a SeriesManager")
				}
			}
		})

		t.Run("testConnection reports ok with a version", func(t *testing.T) {
			r := client.TestConnection(ctx)
			if !r.OK || r.Error != "" || r.Version == "" {
				t.Fatalf("result = %+v", r)
			}
		})

		t.Run("status returns a version, health, queue length and indexers", func(t *testing.T) {
			st, err := client.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.Version == "" {
				t.Error("no version")
			}
			if st.QueueCount < 0 {
				t.Errorf("queue count = %d", st.QueueCount)
			}
			messages := map[string]bool{}
			for _, h := range st.Health {
				if !slices.Contains([]string{mm.HealthOK, mm.HealthNotice, mm.HealthWarning, mm.HealthError}, h.Type) {
					t.Errorf("health type %q", h.Type)
				}
				if h.Source == "" || h.Message == "" {
					t.Errorf("health issue without source or message: %+v", h)
				}
				messages[h.Message] = true
			}
			if st.Indexers.Total < st.Indexers.Enabled || st.Indexers.Enabled < 0 {
				t.Errorf("indexers = %+v", st.Indexers)
			}
			// Only indexer problems are listed, and each is one of the health messages.
			for _, issue := range st.Indexers.Issues {
				if !messages[issue] {
					t.Errorf("indexer issue %q is not a health message", issue)
				}
			}
		})

		t.Run("root folders have a path and non-negative free space in bytes", func(t *testing.T) {
			roots, err := client.RootFolders(ctx)
			if err != nil || len(roots) == 0 {
				t.Fatalf("roots = %v, err = %v", roots, err)
			}
			for _, r := range roots {
				if !strings.HasPrefix(r.Path, "/") || r.FreeSpace < 0 {
					t.Errorf("root %+v", r)
				}
			}
		})

		t.Run("quality profiles have an id and a name", func(t *testing.T) {
			profiles, err := client.QualityProfiles(ctx)
			if err != nil || len(profiles) == 0 {
				t.Fatalf("profiles = %v, err = %v", profiles, err)
			}
			for _, p := range profiles {
				if p.ID == 0 || p.Name == "" {
					t.Errorf("profile %+v", p)
				}
			}
		})

		t.Run("tags have an id and a label", func(t *testing.T) {
			tags, err := client.Tags(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, tag := range tags {
				if tag.ID == 0 || tag.Label == "" {
					t.Errorf("tag %+v", tag)
				}
			}
		})

		t.Run("library items have unique ids, a path inside a root folder and byte sizes", func(t *testing.T) {
			roots, err := client.RootFolders(ctx)
			if err != nil {
				t.Fatal(err)
			}
			type item struct {
				id               int
				title, path, add string
				size             int64
				monitored        bool
			}
			var items []item
			if m, ok := mm.AsMovies(client); ok {
				movies, err := m.Movies(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, x := range movies {
					items = append(items, item{x.ID, x.Title, x.Path, x.Added, x.SizeOnDisk, x.Monitored})
				}
			} else if sm, ok := mm.AsSeries(client); ok {
				series, err := sm.Series(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, x := range series {
					var size int64
					if x.Statistics != nil {
						size = x.Statistics.SizeOnDisk
					}
					items = append(items, item{x.ID, x.Title, x.Path, x.Added, size, x.Monitored})
				}
			}
			if len(items) == 0 {
				t.Fatal("empty library")
			}
			seen := map[int]bool{}
			for _, it := range items {
				if seen[it.id] {
					t.Errorf("duplicate id %d", it.id)
				}
				seen[it.id] = true
				if it.title == "" || it.size < 0 {
					t.Errorf("item %+v", it)
				}
				if !slices.ContainsFunc(roots, func(r mm.RootFolder) bool { return strings.HasPrefix(it.path, r.Path+"/") }) {
					t.Errorf("path %q is not inside a root folder", it.path)
				}
				if _, err := time.Parse(time.RFC3339Nano, it.add); err != nil {
					t.Errorf("added %q is not a date", it.add)
				}
			}
		})

		t.Run("details of a library item come back for its id", func(t *testing.T) {
			if m, ok := mm.AsMovies(client); ok {
				movies, err := m.Movies(ctx)
				if err != nil || len(movies) == 0 {
					t.Fatalf("movies = %v, err = %v", movies, err)
				}
				d, err := m.MovieDetail(ctx, movies[0].ID)
				if err != nil || d.ID != movies[0].ID {
					t.Fatalf("detail = %+v, err = %v", d.Movie, err)
				}
				return
			}
			sm, _ := mm.AsSeries(client)
			series, err := sm.Series(ctx)
			if err != nil || len(series) == 0 {
				t.Fatalf("series = %v, err = %v", series, err)
			}
			d, err := sm.SeriesDetail(ctx, series[0].ID)
			if err != nil || d.ID != series[0].ID {
				t.Fatalf("detail = %+v, err = %v", d.Series, err)
			}
			full, err := sm.SeriesByID(ctx, series[0].ID)
			if err != nil || full.Seasons == nil {
				t.Fatalf("full = %+v, err = %v", full.Series, err)
			}
		})

		t.Run("lookup by the external id finds what is not yet in the library", func(t *testing.T) {
			r, err := client.LookupByExternalID(ctx, s.NewExternalID)
			if err != nil || r == nil {
				t.Fatalf("result = %v, err = %v", r, err)
			}
			if r.Title == "" || r.ID != nil {
				t.Errorf("result %+v", r)
			}
			got := r.TmdbID
			if kind == mm.KindSeries {
				got = r.TvdbID
			}
			if got != s.NewExternalID {
				t.Errorf("external id = %d, want %d", got, s.NewExternalID)
			}
		})

		t.Run("free-text lookup returns titled results", func(t *testing.T) {
			results, err := client.Lookup(ctx, "example")
			if err != nil || len(results) == 0 {
				t.Fatalf("results = %v, err = %v", results, err)
			}
			for _, r := range results {
				if r.Title == "" {
					t.Errorf("untitled result %+v", r)
				}
			}
		})
	})
}
