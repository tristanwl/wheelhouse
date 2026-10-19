// Package explore builds the bodies of GET /api/explore and GET /api/explore/{id}:
// TMDB lists and details, with "already in the library" marked from every Radarr
// instance. It mirrors app/api/explore/route.ts and app/api/explore/[id]/route.ts.
//
// A movie instance that cannot be reached just does not contribute to the
// library mark, instead of failing the page; a TMDB failure fails the request,
// and the caller reports it as a 502 with core.ErrorText.
package explore

import (
	"context"
	"errors"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb"
)

// Category is a list the Explore page shows.
type Category string

// The categories a request may ask for.
const (
	Theaters Category = "theaters"
	Bluray   Category = "bluray"
	Popular  Category = "popular"
)

// ParseCategory validates the category query parameter.
func ParseCategory(s string) (Category, bool) {
	switch c := Category(s); c {
	case Theaters, Bluray, Popular:
		return c, true
	}
	return "", false
}

// Hit is one movie in a list.
type Hit struct {
	ID           int  `json:"id"`
	AlreadyAdded bool `json:"alreadyAdded"`
	// LibraryInstances are the movie instances that have it (empty when none),
	// in configured order, so the UI can say which.
	LibraryInstances []mm.Ref `json:"libraryInstances"`
	Title            string   `json:"title"`
	Year             *int     `json:"year"`
	Overview         string   `json:"overview"`
	PosterURL        *string  `json:"posterUrl"`
	VoteAverage      float64  `json:"voteAverage"`
}

// List is the body of GET /api/explore.
type List struct {
	Results []Hit `json:"results"`
	HasMore bool  `json:"hasMore"`
}

// Detail is the body of GET /api/explore/{id}.
type Detail struct {
	ID           int  `json:"id"`
	AlreadyAdded bool `json:"alreadyAdded"`
	// LibraryInstances are the movie instances that have it (see Hit).
	LibraryInstances []mm.Ref `json:"libraryInstances"`
	Title            string   `json:"title"`
	Overview         string   `json:"overview"`
	Year             *int     `json:"year"`
	Genres           []string `json:"genres"`
	Runtime          *int     `json:"runtime"`
	VoteAverage      float64  `json:"voteAverage"`
	PosterURL        *string  `json:"posterUrl"`
	BackdropURL      *string  `json:"backdropUrl"`
	TrailerKey       *string  `json:"trailerKey"`
}

// client is the configured TMDB client. The errors are safe to show: the same
// messages as getTmdbClient in lib/tmdb-client.ts.
func client(ctx context.Context, rt *core.Runtime) (*tmdb.Client, error) {
	clients, err := rt.Clients(ctx, core.RoleMetadata)
	if err != nil {
		return nil, err
	}
	for _, c := range clients {
		if t, ok := c.Client.(*tmdb.Client); ok {
			return t, nil
		}
	}
	resolved, err := rt.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range resolved {
		if r.Definition != nil && r.Definition.Role == core.RoleMetadata && r.Error != "" {
			return nil, &core.ProviderError{Message: core.Redact("TMDB: " + r.Error)}
		}
	}
	return nil, &core.ProviderError{Message: "TMDB is not configured. Set TMDB_API_KEY."}
}

// libraryIDs maps each TMDB id to the movie instances that have it. Instances
// that fail are left out.
func libraryIDs(ctx context.Context, rt *core.Runtime) (map[int][]mm.Ref, error) {
	entries, err := mm.ListManagers(ctx, rt, mm.KindMovies)
	if err != nil {
		return nil, err
	}
	results, _ := mm.CollectFromManagers(ctx, entries, func(ctx context.Context, c mm.Manager, _ mm.Entry) ([]mm.Movie, error) {
		movies, ok := mm.AsMovies(c)
		if !ok {
			return nil, nil
		}
		return movies.Movies(ctx)
	})
	ids := map[int][]mm.Ref{}
	for _, r := range results {
		ref := mm.Ref{InstanceID: r.Entry.InstanceID, Name: r.Entry.Name}
		seen := map[int]bool{}
		for _, m := range r.Value {
			if !seen[m.TmdbID] {
				seen[m.TmdbID] = true
				ids[m.TmdbID] = append(ids[m.TmdbID], ref)
			}
		}
	}
	return ids, nil
}

// withLibrary runs fetch (the TMDB call) while the library is being read.
func withLibrary(ctx context.Context, rt *core.Runtime, fetch func() error) (map[int][]mm.Ref, error) {
	var (
		added  map[int][]mm.Ref
		libErr error
		done   = make(chan struct{})
	)
	go func() {
		defer close(done)
		added, libErr = libraryIDs(ctx, rt)
	}()
	fetchErr := fetch()
	<-done
	return added, errors.Join(fetchErr, libErr)
}

// Browse lists one category. count is the number of movies wanted and page is
// 1-based in units of count; the caller has clamped both.
func Browse(ctx context.Context, rt *core.Runtime, cat Category, count, page int) (List, error) {
	t, err := client(ctx, rt)
	if err != nil {
		return List{}, err
	}
	var res tmdb.ListResult
	added, err := withLibrary(ctx, rt, func() (err error) {
		switch cat {
		case Theaters:
			res, err = t.NowPlaying(ctx, count, page)
		case Bluray:
			res, err = t.RecentPhysicalReleases(ctx, count, page)
		default:
			res, err = t.Popular(ctx, count, page)
		}
		return err
	})
	if err != nil {
		return List{}, err
	}
	out := List{Results: make([]Hit, 0, len(res.Results)), HasMore: res.HasMore}
	for _, m := range res.Results {
		out.Results = append(out.Results, Hit{
			ID:               m.ID,
			AlreadyAdded:     len(added[m.ID]) > 0,
			LibraryInstances: orEmpty(added[m.ID]),
			Title:            m.Title,
			Year:             m.Year(),
			Overview:         m.Overview,
			PosterURL:        nullable(tmdb.PosterURL(m.PosterPath)),
			VoteAverage:      m.VoteAverage,
		})
	}
	return out, nil
}

// Movie is the detail of one TMDB movie.
func Movie(ctx context.Context, rt *core.Runtime, id int) (Detail, error) {
	t, err := client(ctx, rt)
	if err != nil {
		return Detail{}, err
	}
	var d tmdb.MovieDetail
	added, err := withLibrary(ctx, rt, func() (err error) {
		d, err = t.MovieDetail(ctx, id)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	genres := make([]string, 0, len(d.Genres))
	for _, g := range d.Genres {
		genres = append(genres, g.Name)
	}
	return Detail{
		ID:               d.ID,
		AlreadyAdded:     len(added[d.ID]) > 0,
		LibraryInstances: orEmpty(added[d.ID]),
		Title:            d.Title,
		Overview:         d.Overview,
		Year:             d.Year(),
		Genres:           genres,
		Runtime:          d.Runtime,
		VoteAverage:      d.VoteAverage,
		PosterURL:        nullable(tmdb.PosterURL(d.PosterPath)),
		BackdropURL:      nullable(tmdb.BackdropURL(d.BackdropPath)),
		TrailerKey:       nullable(d.TrailerKey()),
	}, nil
}

// nullable turns the (value, ok) helpers into a JSON null or string.
func nullable(s string, ok bool) *string {
	if !ok {
		return nil
	}
	return &s
}

// orEmpty keeps an empty list as [] in the JSON, never null.
func orEmpty(refs []mm.Ref) []mm.Ref {
	if refs == nil {
		return []mm.Ref{}
	}
	return refs
}
