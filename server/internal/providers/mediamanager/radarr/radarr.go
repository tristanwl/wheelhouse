// Package radarr is the Radarr provider of the media-manager role (Radarr v5).
package radarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Config is the Radarr instance config: URL and API key.
type Config = mm.ArrConfig

// Definition is the provider: id "radarr", role mediaManager, kind movies. Its
// clients implement mediamanager.MovieManager.
func Definition() core.Definition {
	return core.Definition{
		ID:           "radarr",
		Role:         core.RoleMediaManager,
		DisplayName:  "Radarr",
		Config:       Config{},
		Capabilities: mm.Capabilities{Kind: mm.KindMovies},
		Create: func(config any, ctx core.ProviderContext) any {
			return New(config.(Config), ctx.InstanceID, mm.ArrOptions{})
		},
	}
}

// Client talks to one Radarr instance.
type Client struct {
	mm.Base
}

var _ mm.MovieManager = (*Client)(nil)

// New builds a client. The label (the instance id) prefixes its error messages.
func New(cfg Config, label string, opts mm.ArrOptions) *Client {
	return &Client{Base: mm.NewBase(mm.NewArr(cfg, label, opts), mm.KindMovies)}
}

func get[T any](ctx context.Context, c *Client, path string, req core.Request) (T, error) {
	return mm.Get[T](ctx, c.Arr(), path, req)
}

func send[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any) (T, error) {
	return mm.Send[T](ctx, c.Arr(), method, path, query, body)
}

// Movies lists the library (cached for a few seconds; any write clears it).
func (c *Client) Movies(ctx context.Context) ([]mm.Movie, error) {
	m, err := get[[]mm.Movie](ctx, c, "movie", core.Request{CacheTTL: mm.LibraryCacheTTL, MaxBytes: core.LargeListBytes})
	if m == nil {
		m = []mm.Movie{}
	}
	return m, err
}

// MovieDetail returns the fields the details page shows.
func (c *Client) MovieDetail(ctx context.Context, id int) (mm.MovieDetail, error) {
	return get[mm.MovieDetail](ctx, c, "movie/"+strconv.Itoa(id), core.Request{})
}

// Lookup searches for movies to add. Results already in the library carry an id.
func (c *Client) Lookup(ctx context.Context, term string) ([]mm.LookupResult, error) {
	r, err := get[[]mm.LookupResult](ctx, c, "movie/lookup", core.Request{Query: url.Values{"term": {term}}, Limits: mm.LookupLimits})
	if r == nil {
		r = []mm.LookupResult{}
	}
	return r, err
}

// LookupByExternalID finds a movie by tmdb id; nil when the service answers
// with nothing. Unlike a term lookup, the result never carries an id.
func (c *Client) LookupByExternalID(ctx context.Context, tmdbID int) (*mm.LookupResult, error) {
	r, err := get[*mm.LookupResult](ctx, c, "movie/lookup/tmdb", core.Request{
		Query: url.Values{"tmdbId": {strconv.Itoa(tmdbID)}},
	})
	return r, err
}

// Add adds a movie (see LookupResult.MovieAddPayload).
func (c *Client) Add(ctx context.Context, payload mm.LookupResult) (mm.LookupResult, error) {
	return send[mm.LookupResult](ctx, c, http.MethodPost, "movie", nil, payload)
}

// Remove deletes a movie and its files, without an import-list exclusion. Radarr
// answers 200 with an empty body, which is fine.
func (c *Client) Remove(ctx context.Context, id int) error {
	_, err := send[json.RawMessage](ctx, c, http.MethodDelete, "movie/"+strconv.Itoa(id), url.Values{
		"deleteFiles":            {"true"},
		"addImportListExclusion": {"false"},
	}, nil)
	return err
}

// Rescan runs the RescanMovie command.
func (c *Client) Rescan(ctx context.Context, movieID int) error {
	_, err := send[json.RawMessage](ctx, c, http.MethodPost, "command",
		nil, map[string]any{"name": "RescanMovie", "movieId": movieID})
	return err
}
