package mediamanager

import (
	"context"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// Kind is what a media manager manages. "music" and "books" would join these if
// Lidarr or Readarr ever land.
type Kind string

// The kinds.
const (
	KindMovies Kind = "movies"
	KindSeries Kind = "series"
)

// Capabilities is static, per provider: the Definition's Capabilities value.
type Capabilities struct {
	Kind Kind `json:"kind"`
}

// Health issue types, as the arr apps report them.
const (
	HealthOK      = "ok"
	HealthNotice  = "notice"
	HealthWarning = "warning"
	HealthError   = "error"
)

// HealthIssue is one entry of /health.
type HealthIssue struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl,omitempty"`
}

// SystemStatus is what /system/status reports (only the version is used).
type SystemStatus struct {
	Version string `json:"version"`
}

// QueueRecord is one item of the download queue. Sizes are bytes; they are
// floats because the services compute sizeleft from a percentage and a JSON
// number may carry a fraction.
type QueueRecord struct {
	ID                    int     `json:"id"`
	Title                 string  `json:"title"`
	Size                  float64 `json:"size"`
	SizeLeft              float64 `json:"sizeleft"`
	Status                string  `json:"status"`
	TrackedDownloadStatus string  `json:"trackedDownloadStatus,omitempty"`
	TimeLeft              string  `json:"timeleft,omitempty"`
}

// QueueSummary is the first page of the queue.
type QueueSummary struct {
	TotalRecords int           `json:"totalRecords"`
	Records      []QueueRecord `json:"records"`
}

// Indexer is one configured indexer.
type Indexer struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	EnableRss bool   `json:"enableRss"`
	Protocol  string `json:"protocol"`
}

// IndexerSummary counts indexers and lists the health messages that concern them.
type IndexerSummary struct {
	Total   int `json:"total"`
	Enabled int `json:"enabled"`
	// Issues are the messages of the health issues that concern indexers.
	Issues []string `json:"issues"`
}

// Status is version, health, queue length and indexers in one value.
type Status struct {
	Version    string         `json:"version"`
	Health     []HealthIssue  `json:"health"`
	QueueCount int            `json:"queueCount"`
	Indexers   IndexerSummary `json:"indexers"`
}

// RootFolder is a library root; FreeSpace is bytes.
type RootFolder struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  int64  `json:"freeSpace"`
}

// QualityProfile is a quality profile to pick when adding.
type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Tag is a label that can be attached to library items.
type Tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// SeriesStatistics is the part of a series' statistics the app uses.
type SeriesStatistics struct {
	SizeOnDisk       int64 `json:"sizeOnDisk"`
	EpisodeFileCount int   `json:"episodeFileCount"`
}

// Series is a Sonarr library entry (the list shape).
type Series struct {
	ID             int               `json:"id"`
	Title          string            `json:"title"`
	Monitored      bool              `json:"monitored"`
	Path           string            `json:"path"`
	RootFolderPath string            `json:"rootFolderPath"`
	Added          string            `json:"added"`
	Statistics     *SeriesStatistics `json:"statistics,omitempty"`
}

// MediaInfo is the technical description of a file. An empty string or a nil
// bit depth means the service did not report it. VideoDynamicRange and
// VideoDynamicRangeType ("HDR", "HDR10", ...) are empty for SDR; the TypeScript
// types do not name them but the services send them, so they are kept here.
type MediaInfo struct {
	VideoCodec            string `json:"videoCodec,omitempty"`
	Resolution            string `json:"resolution,omitempty"`
	VideoBitDepth         *int   `json:"videoBitDepth,omitempty"`
	VideoDynamicRange     string `json:"videoDynamicRange,omitempty"`
	VideoDynamicRangeType string `json:"videoDynamicRangeType,omitempty"`
}

// MovieFile is the file of a movie that has one.
type MovieFile struct {
	Size      int64      `json:"size"`
	Path      string     `json:"path"`
	MediaInfo *MediaInfo `json:"mediaInfo,omitempty"`
}

// Movie is a Radarr library entry (the list shape).
type Movie struct {
	ID             int        `json:"id"`
	Title          string     `json:"title"`
	Monitored      bool       `json:"monitored"`
	Path           string     `json:"path"`
	RootFolderPath string     `json:"rootFolderPath"`
	Added          string     `json:"added"`
	SizeOnDisk     int64      `json:"sizeOnDisk"`
	HasFile        bool       `json:"hasFile"`
	TmdbID         int        `json:"tmdbId"`
	MovieFile      *MovieFile `json:"movieFile,omitempty"`
}

// Image is one artwork entry.
type Image struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl,omitempty"`
}

// RatingSource is a single rating (Sonarr has one, under "ratings").
type RatingSource struct {
	Votes int     `json:"votes"`
	Value float64 `json:"value"`
}

// MovieRating is one entry of Radarr's "ratings" map (imdb, tmdb, ...).
type MovieRating struct {
	Votes int     `json:"votes"`
	Value float64 `json:"value"`
	Type  string  `json:"type,omitempty"`
}

// SeriesDetail is a series with the fields the details page shows. It is read
// only: it is not written back, so unknown fields are not kept.
type SeriesDetail struct {
	Series
	Overview      string        `json:"overview,omitempty"`
	Year          int           `json:"year,omitempty"`
	Genres        []string      `json:"genres,omitempty"`
	Runtime       int           `json:"runtime,omitempty"`
	Certification string        `json:"certification,omitempty"`
	Status        string        `json:"status,omitempty"`
	Network       string        `json:"network,omitempty"`
	Images        []Image       `json:"images,omitempty"`
	Ratings       *RatingSource `json:"ratings,omitempty"`
}

// MovieDetail is a movie with the fields the details page shows (read only).
type MovieDetail struct {
	Movie
	Overview      string                 `json:"overview,omitempty"`
	Year          int                    `json:"year,omitempty"`
	Genres        []string               `json:"genres,omitempty"`
	Runtime       int                    `json:"runtime,omitempty"`
	Certification string                 `json:"certification,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Studio        string                 `json:"studio,omitempty"`
	Images        []Image                `json:"images,omitempty"`
	Ratings       map[string]MovieRating `json:"ratings,omitempty"`
}

// SeasonStatistics counts a season's episodes and bytes.
type SeasonStatistics struct {
	EpisodeFileCount  int   `json:"episodeFileCount"`
	EpisodeCount      int   `json:"episodeCount"`
	TotalEpisodeCount int   `json:"totalEpisodeCount"`
	SizeOnDisk        int64 `json:"sizeOnDisk"`
}

// Season is one season of a series. Fields the service sent that are not
// modelled here survive a round trip (see the package comment).
type Season struct {
	SeasonNumber int               `json:"seasonNumber"`
	Monitored    bool              `json:"monitored"`
	Statistics   *SeasonStatistics `json:"statistics,omitempty"`

	doc rawDoc
}

// UnmarshalJSON keeps the fields it does not model.
func (s *Season) UnmarshalJSON(data []byte) error {
	type plain Season
	var p plain
	doc, err := decodeDoc(data, &p)
	if err != nil {
		return err
	}
	*s = Season(p)
	s.doc = doc
	return nil
}

// MarshalJSON writes the unmodelled fields back unchanged.
func (s Season) MarshalJSON() ([]byte, error) {
	type plain Season
	return s.doc.encode(plain(s))
}

// SeriesFull is a series with its seasons: what a PUT needs. It keeps every
// field the service sent, so updating a season's monitoring never strips the
// rest of the series.
type SeriesFull struct {
	Series
	Seasons []Season `json:"seasons"`

	doc rawDoc
}

// UnmarshalJSON keeps the fields it does not model.
func (s *SeriesFull) UnmarshalJSON(data []byte) error {
	type plain SeriesFull
	var p plain
	doc, err := decodeDoc(data, &p)
	if err != nil {
		return err
	}
	*s = SeriesFull(p)
	s.doc = doc
	return nil
}

// MarshalJSON writes the unmodelled fields back unchanged.
func (s SeriesFull) MarshalJSON() ([]byte, error) {
	type plain SeriesFull
	return s.doc.encode(plain(s))
}

// EpisodeFile is a file of an episode; MediaInfo carries the codec.
type EpisodeFile struct {
	ID           int        `json:"id"`
	SeriesID     int        `json:"seriesId"`
	SeasonNumber int        `json:"seasonNumber"`
	Size         int64      `json:"size"`
	Path         string     `json:"path"`
	MediaInfo    *MediaInfo `json:"mediaInfo,omitempty"`
}

// Episode links an episode number and title to its file. These live on Sonarr's
// separate `episode` resource, not `episodefile`: a file is tied to its
// episode(s) through EpisodeFileID (several episodes can share one file, as an
// aired double episode does).
type Episode struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	EpisodeFileID int    `json:"episodeFileId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
}

// SeasonList is a series' seasons, ordered by season number.
type SeasonList struct {
	Title   string   `json:"title"`
	Seasons []Season `json:"seasons"`
}

// Manager is what every Sonarr/Radarr-family client can do, whatever the kind.
// A client is also a MovieManager or a SeriesManager (see AsMovies, AsSeries).
//
// Every method returns a *core.ProviderError (already redacted) on failure.
type Manager interface {
	Kind() Kind
	TestConnection(ctx context.Context) core.ConnectionResult
	// Status is version, health, queue length and indexers in one call.
	Status(ctx context.Context) (Status, error)
	SystemStatus(ctx context.Context) (SystemStatus, error)
	Health(ctx context.Context) ([]HealthIssue, error)
	QueueSummary(ctx context.Context) (QueueSummary, error)
	Indexers(ctx context.Context) ([]Indexer, error)
	// RootFolders also carries the free space, so it doubles as disk space.
	RootFolders(ctx context.Context) ([]RootFolder, error)
	QualityProfiles(ctx context.Context) ([]QualityProfile, error)
	Tags(ctx context.Context) ([]Tag, error)
	// Lookup is a free-text search for things to add.
	Lookup(ctx context.Context, term string) ([]LookupResult, error)
	// LookupByExternalID uses the id the add flow uses: tmdbId for movies, tvdbId
	// for series. It returns nil, nil when there is no match.
	LookupByExternalID(ctx context.Context, externalID int) (*LookupResult, error)
	// Add adds a library item; the payload is a lookup result plus the user's
	// choices (see LookupResult.MovieAddPayload and SeriesAddPayload).
	Add(ctx context.Context, payload LookupResult) (LookupResult, error)
	// Remove removes an item and its files from disk. The caller must have
	// confirmed with the user.
	Remove(ctx context.Context, id int) error
	// Rescan asks the app to rescan an item's files. Called after an in-place
	// file conversion so its own database (size, mediaInfo, codec) does not go
	// stale; otherwise the next codec analysis would still show the old codec.
	Rescan(ctx context.Context, id int) error
}

// MovieManager is a Radarr-family client.
type MovieManager interface {
	Manager
	Movies(ctx context.Context) ([]Movie, error)
	MovieDetail(ctx context.Context, id int) (MovieDetail, error)
}

// SeriesManager is a Sonarr-family client.
type SeriesManager interface {
	Manager
	Series(ctx context.Context) ([]Series, error)
	SeriesByID(ctx context.Context, id int) (SeriesFull, error)
	SeriesDetail(ctx context.Context, id int) (SeriesDetail, error)
	EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error)
	Episodes(ctx context.Context, seriesID int) ([]Episode, error)
	DeleteEpisodeFile(ctx context.Context, id int) error
	UpdateSeries(ctx context.Context, series SeriesFull) (SeriesFull, error)
	// Seasons lists a series' seasons ordered by season number (the seasons
	// route).
	Seasons(ctx context.Context, seriesID int) (SeasonList, error)
	// DeleteSeason deletes the files of one season, then sets the season to
	// unmonitored so Sonarr does not fetch it again (the delete-season route). It
	// returns how many files it deleted.
	DeleteSeason(ctx context.Context, seriesID, seasonNumber int) (filesDeleted int, err error)
}

// AsMovies narrows a client of the role to a MovieManager.
func AsMovies(client any) (MovieManager, bool) {
	m, ok := client.(MovieManager)
	if ok && m.Kind() == KindMovies {
		return m, true
	}
	return nil, false
}

// AsSeries narrows a client of the role to a SeriesManager.
func AsSeries(client any) (SeriesManager, bool) {
	m, ok := client.(SeriesManager)
	if ok && m.Kind() == KindSeries {
		return m, true
	}
	return nil, false
}
