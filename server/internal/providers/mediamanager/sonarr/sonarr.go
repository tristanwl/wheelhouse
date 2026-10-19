// Package sonarr is the Sonarr provider of the media-manager role (Sonarr v4).
package sonarr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Config is the Sonarr instance config: URL and API key.
type Config = mm.ArrConfig

// Definition is the provider: id "sonarr", role mediaManager, kind series. Its
// clients implement mediamanager.SeriesManager.
func Definition() core.Definition {
	return core.Definition{
		ID:           "sonarr",
		Role:         core.RoleMediaManager,
		DisplayName:  "Sonarr",
		Config:       Config{},
		Capabilities: mm.Capabilities{Kind: mm.KindSeries},
		Create: func(config any, ctx core.ProviderContext) any {
			return New(config.(Config), ctx.InstanceID, mm.ArrOptions{})
		},
	}
}

// Client talks to one Sonarr instance.
type Client struct {
	mm.Base
}

var _ mm.SeriesManager = (*Client)(nil)

// New builds a client. The label (the instance id) prefixes its error messages.
func New(cfg Config, label string, opts mm.ArrOptions) *Client {
	return &Client{Base: mm.NewBase(mm.NewArr(cfg, label, opts), mm.KindSeries)}
}

func get[T any](ctx context.Context, c *Client, path string, req core.Request) (T, error) {
	return mm.Get[T](ctx, c.Arr(), path, req)
}

func send[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any) (T, error) {
	return mm.Send[T](ctx, c.Arr(), method, path, query, body)
}

// Series lists the library (cached for a few seconds; any write clears it).
func (c *Client) Series(ctx context.Context) ([]mm.Series, error) {
	s, err := get[[]mm.Series](ctx, c, "series", core.Request{CacheTTL: mm.LibraryCacheTTL, MaxBytes: core.LargeListBytes})
	if s == nil {
		s = []mm.Series{}
	}
	return s, err
}

// SeriesByID returns a series with its seasons, ready to be edited and put back.
func (c *Client) SeriesByID(ctx context.Context, id int) (mm.SeriesFull, error) {
	return get[mm.SeriesFull](ctx, c, "series/"+strconv.Itoa(id), core.Request{})
}

// SeriesDetail returns the fields the details page shows.
func (c *Client) SeriesDetail(ctx context.Context, id int) (mm.SeriesDetail, error) {
	return get[mm.SeriesDetail](ctx, c, "series/"+strconv.Itoa(id), core.Request{})
}

func seriesQuery(seriesID int) url.Values {
	return url.Values{"seriesId": {strconv.Itoa(seriesID)}}
}

// EpisodeFiles lists a series' files.
func (c *Client) EpisodeFiles(ctx context.Context, seriesID int) ([]mm.EpisodeFile, error) {
	f, err := get[[]mm.EpisodeFile](ctx, c, "episodefile", core.Request{Query: seriesQuery(seriesID)})
	if f == nil {
		f = []mm.EpisodeFile{}
	}
	return f, err
}

// Episodes lists a series' episodes.
func (c *Client) Episodes(ctx context.Context, seriesID int) ([]mm.Episode, error) {
	e, err := get[[]mm.Episode](ctx, c, "episode", core.Request{Query: seriesQuery(seriesID)})
	if e == nil {
		e = []mm.Episode{}
	}
	return e, err
}

// Lookup searches for series to add.
func (c *Client) Lookup(ctx context.Context, term string) ([]mm.LookupResult, error) {
	r, err := get[[]mm.LookupResult](ctx, c, "series/lookup", core.Request{Query: url.Values{"term": {term}}, Limits: mm.LookupLimits})
	if r == nil {
		r = []mm.LookupResult{}
	}
	return r, err
}

// LookupByExternalID finds a series by tvdb id; nil when there is none.
func (c *Client) LookupByExternalID(ctx context.Context, tvdbID int) (*mm.LookupResult, error) {
	results, err := get[[]mm.LookupResult](ctx, c, "series/lookup", core.Request{
		Query: url.Values{"term": {fmt.Sprintf("tvdb:%d", tvdbID)}},
	})
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return &results[0], nil
}

// Add adds a series (see LookupResult.SeriesAddPayload).
func (c *Client) Add(ctx context.Context, payload mm.LookupResult) (mm.LookupResult, error) {
	return send[mm.LookupResult](ctx, c, http.MethodPost, "series", nil, payload)
}

// Remove deletes a series and its files, without an import-list exclusion.
func (c *Client) Remove(ctx context.Context, id int) error {
	_, err := send[json.RawMessage](ctx, c, http.MethodDelete, "series/"+strconv.Itoa(id), url.Values{
		"deleteFiles":            {"true"},
		"addImportListExclusion": {"false"},
	}, nil)
	return err
}

// Rescan runs the RescanSeries command.
func (c *Client) Rescan(ctx context.Context, seriesID int) error {
	_, err := send[json.RawMessage](ctx, c, http.MethodPost, "command",
		nil, map[string]any{"name": "RescanSeries", "seriesId": seriesID})
	return err
}

// DeleteEpisodeFile deletes one episode file from disk.
func (c *Client) DeleteEpisodeFile(ctx context.Context, id int) error {
	_, err := send[json.RawMessage](ctx, c, http.MethodDelete, "episodefile/"+strconv.Itoa(id), nil, nil)
	return err
}

// UpdateSeries puts a whole series back (for example with a season unmonitored).
func (c *Client) UpdateSeries(ctx context.Context, series mm.SeriesFull) (mm.SeriesFull, error) {
	return send[mm.SeriesFull](ctx, c, http.MethodPut, "series/"+strconv.Itoa(series.ID), nil, series)
}

// Seasons lists a series' seasons ordered by season number.
func (c *Client) Seasons(ctx context.Context, seriesID int) (mm.SeasonList, error) {
	return mm.ListSeasons(ctx, c, seriesID)
}

// DeleteSeason deletes a season's files and unmonitors the season.
func (c *Client) DeleteSeason(ctx context.Context, seriesID, seasonNumber int) (int, error) {
	return mm.DeleteSeason(ctx, c, seriesID, seasonNumber)
}
