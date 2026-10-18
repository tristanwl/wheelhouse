package tmdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// The Explore-specific helpers, ported from lib/tmdb-client.ts.

// AttributionText is what TMDB's API terms require wherever their data is shown
// (components/TmdbAttribution.tsx shows it with a link to AttributionURL).
const (
	AttributionText = "This product uses the TMDB API but is not endorsed or certified by TMDB."
	AttributionURL  = "https://www.themoviedb.org"
)

// Image base URLs (TMDB's image CDN, w342 posters and w780 backdrops, as the
// Explore UI shows them).
const (
	posterBase   = "https://image.tmdb.org/t/p/w342"
	backdropBase = "https://image.tmdb.org/t/p/w780"
)

// Movie is a TMDB list entry (the fields Explore reads).
type Movie struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Overview    string  `json:"overview"`
	ReleaseDate string  `json:"release_date"`
	PosterPath  *string `json:"poster_path"`
	VoteAverage float64 `json:"vote_average"`
}

// Year is the release year, or nil when there is no usable release date.
func (m Movie) Year() *int { return yearOf(m.ReleaseDate) }

func yearOf(releaseDate string) *int {
	if len(releaseDate) < 4 {
		return nil
	}
	y, err := strconv.Atoi(releaseDate[:4])
	if err != nil {
		return nil
	}
	return &y
}

// Video is one of a movie's promo videos.
type Video struct {
	Key      string `json:"key"`
	Site     string `json:"site"`
	Type     string `json:"type"`
	Official bool   `json:"official"`
	Name     string `json:"name"`
}

// Genre is a TMDB genre.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// MovieDetail is /movie/{id} with the videos appended.
type MovieDetail struct {
	Movie
	Genres       []Genre `json:"genres"`
	Runtime      *int    `json:"runtime"`
	BackdropPath *string `json:"backdrop_path"`
	Videos       *struct {
		Results []Video `json:"results"`
	} `json:"videos"`
}

// TrailerKey is the YouTube key Explore plays, or "" and false when there is none.
func (d MovieDetail) TrailerKey() (string, bool) {
	if d.Videos == nil {
		return "", false
	}
	return PickTrailerKey(d.Videos.Results)
}

// ListResult is one Explore batch.
type ListResult struct {
	Results []Movie
	HasMore bool
}

const (
	pageSize = 20
	// maxPage: TMDB refuses pages past 500, so HasMore also stops there.
	maxPage = 500
)

type listPage struct {
	Results    []Movie `json:"results"`
	TotalPages int     `json:"total_pages"`
}

// list serves more than one TMDB page's worth (the Explore "All" view loads 40
// per batch) by fetching as many pages as needed in parallel and concatenating;
// TMDB has no larger page size. page is 1-based in units of count, so page 2 of
// count=40 is TMDB pages 3 and 4. A count or page below 1 is treated as 1 (the
// routes clamp them anyway).
func (c *Client) list(ctx context.Context, path string, params map[string]string, count, page int) (ListResult, error) {
	count, page = max(count, 1), max(page, 1)
	pages := max(1, (count+pageSize-1)/pageSize)
	first := (page-1)*pages + 1
	var wanted []int
	for i := range pages {
		if p := first + i; p <= maxPage {
			wanted = append(wanted, p)
		}
	}
	if len(wanted) == 0 {
		return ListResult{}, nil
	}

	fetched := make([]listPage, len(wanted))
	errs := make([]error, len(wanted))
	var wg sync.WaitGroup
	for i, p := range wanted {
		wg.Go(func() {
			q := make(map[string]string, len(params)+1)
			for k, v := range params {
				q[k] = v
			}
			q["page"] = strconv.Itoa(p)
			errs[i] = c.Get(ctx, path, q, &fetched[i])
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return ListResult{}, err
		}
	}

	var results []Movie
	for _, f := range fetched {
		results = append(results, f.Results...)
	}
	if len(results) > count {
		results = results[:count]
	}
	last := wanted[len(wanted)-1]
	total := min(fetched[0].TotalPages, maxPage)
	return ListResult{Results: results, HasMore: last < total}, nil
}

// NowPlaying is what is in theaters.
func (c *Client) NowPlaying(ctx context.Context, count, page int) (ListResult, error) {
	return c.list(ctx, "/movie/now_playing", map[string]string{"region": "US"}, count, page)
}

// Popular is TMDB's popular movies.
func (c *Client) Popular(ctx context.Context, count, page int) (ListResult, error) {
	return c.list(ctx, "/movie/popular", map[string]string{"region": "US"}, count, page)
}

// RecentPhysicalReleases: TMDB has no "Blu-ray" concept specifically, but
// release_type=5 is the physical-media release date in its per-region
// release_dates data, the closest real equivalent. Sorted newest first and
// capped at today (UTC) so announced-but-unreleased physical dates do not show
// up as "recent".
func (c *Client) RecentPhysicalReleases(ctx context.Context, count, page int) (ListResult, error) {
	today := c.now().UTC().Format("2006-01-02")
	return c.list(ctx, "/discover/movie", map[string]string{
		"region":            "US",
		"with_release_type": "5",
		"sort_by":           "primary_release_date.desc",
		"release_date.lte":  today,
		"vote_count.gte":    "10",
	}, count, page)
}

// MovieDetail fetches one movie with its videos.
func (c *Client) MovieDetail(ctx context.Context, id int) (MovieDetail, error) {
	var d MovieDetail
	err := c.Get(ctx, fmt.Sprintf("/movie/%d", id), map[string]string{"append_to_response": "videos"}, &d)
	return d, err
}

// PickTrailerKey prefers an official trailer named like "Official Trailer" over
// other trailers and teasers: TMDB movies often have dozens of promo videos (TV
// spots, featurettes, regional teasers), so picking well matters. Only YouTube
// videos count.
func PickTrailerKey(videos []Video) (string, bool) {
	var youtube []Video
	for _, v := range videos {
		if v.Site == "YouTube" {
			youtube = append(youtube, v)
		}
	}
	find := func(match func(Video) bool) (string, bool) {
		for _, v := range youtube {
			if match(v) {
				return v.Key, true
			}
		}
		return "", false
	}
	for _, match := range []func(Video) bool{
		func(v Video) bool {
			return v.Type == "Trailer" && v.Official && strings.Contains(strings.ToLower(v.Name), "official trailer")
		},
		func(v Video) bool { return v.Type == "Trailer" && v.Official },
		func(v Video) bool { return v.Type == "Trailer" },
		func(v Video) bool { return v.Type == "Teaser" },
	} {
		if key, ok := find(match); ok {
			return key, true
		}
	}
	return "", false
}

// PosterURL is the poster image URL, or "" and false when there is no poster.
func PosterURL(path *string) (string, bool) { return imageURL(posterBase, path) }

// BackdropURL is the backdrop image URL, or "" and false when there is none.
func BackdropURL(path *string) (string, bool) { return imageURL(backdropBase, path) }

func imageURL(base string, path *string) (string, bool) {
	if path == nil || *path == "" {
		return "", false
	}
	return base + *path, true
}
