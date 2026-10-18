// Package library builds the bodies of the library routes: the merged library
// (GET /api/library), storage (GET /api/storage), search (GET /api/search),
// the add options (GET /api/add-meta) and the per-item details and seasons. It
// mirrors app/api/{library,storage,search,add-meta} in the Next app. Every
// route that spans instances lets one broken instance be reported without
// affecting the others.
package library

import (
	"cmp"
	"context"
	"slices"
	"strings"

	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Item is one library entry, tagged with the instance it came from.
type Item struct {
	InstanceID     string  `json:"instanceId"`
	InstanceName   string  `json:"instanceName"`
	Kind           mm.Kind `json:"kind"`
	ID             int     `json:"id"`
	Title          string  `json:"title"`
	SizeOnDisk     int64   `json:"sizeOnDisk"`
	Monitored      bool    `json:"monitored"`
	RootFolderPath string  `json:"rootFolderPath"`
	Added          string  `json:"added"`
}

// LibraryResponse is the body of GET /api/library. Instances lists every
// configured manager so the UI can say which are down.
type LibraryResponse struct {
	Items     []Item       `json:"items"`
	Instances []mm.Summary `json:"instances"`
}

// Library merges the library of every media manager, biggest first.
func Library(ctx context.Context, entries []mm.Entry) LibraryResponse {
	results, problems := mm.CollectFromManagers(ctx, entries,
		func(ctx context.Context, client mm.Manager, e mm.Entry) ([]Item, error) {
			base := Item{InstanceID: e.InstanceID, InstanceName: e.Name, Kind: client.Kind()}
			if s, ok := mm.AsSeries(client); ok {
				list, err := s.Series(ctx)
				if err != nil {
					return nil, err
				}
				items := make([]Item, 0, len(list))
				for _, x := range list {
					it := base
					it.ID, it.Title, it.Monitored, it.RootFolderPath, it.Added = x.ID, x.Title, x.Monitored, x.RootFolderPath, x.Added
					if x.Statistics != nil {
						it.SizeOnDisk = x.Statistics.SizeOnDisk
					}
					items = append(items, it)
				}
				return items, nil
			}
			m, ok := mm.AsMovies(client)
			if !ok {
				return nil, errUnsupported
			}
			list, err := m.Movies(ctx)
			if err != nil {
				return nil, err
			}
			items := make([]Item, 0, len(list))
			for _, x := range list {
				it := base
				it.ID, it.Title, it.Monitored, it.RootFolderPath, it.Added, it.SizeOnDisk = x.ID, x.Title, x.Monitored, x.RootFolderPath, x.Added, x.SizeOnDisk
				items = append(items, it)
			}
			return items, nil
		})

	items := []Item{}
	for _, r := range results {
		items = append(items, r.Value...)
	}
	slices.SortStableFunc(items, func(a, b Item) int { return cmp.Compare(b.SizeOnDisk, a.SizeOnDisk) })
	return LibraryResponse{Items: items, Instances: mm.Summarize(entries, problems)}
}

type unsupported struct{}

func (unsupported) Error() string { return "unsupported media manager" }

var errUnsupported error = unsupported{}

// StorageEntry is one root folder of one instance.
type StorageEntry struct {
	InstanceID       string `json:"instanceId"`
	Label            string `json:"label"`
	Path             string `json:"path"`
	FreeBytes        int64  `json:"freeBytes"`
	LibraryUsedBytes int64  `json:"libraryUsedBytes"`
}

// StorageResponse is the body of GET /api/storage.
type StorageResponse struct {
	Entries   []StorageEntry `json:"entries"`
	Instances []mm.Summary   `json:"instances"`
}

type rootUse struct {
	root mm.RootFolder
	used int64
}

type sized struct {
	path string
	size int64
}

// isWithin reports whether itemPath is root or below it.
func isWithin(root, itemPath string) bool {
	base := strings.TrimRight(root, "/")
	return itemPath == base || strings.HasPrefix(itemPath, base+"/")
}

// Storage reports free space and library size per root folder. An item counts
// toward the deepest root folder that contains it. An item without its own path
// is placed by its root folder path.
func Storage(ctx context.Context, entries []mm.Entry) StorageResponse {
	results, problems := mm.CollectFromManagers(ctx, entries,
		func(ctx context.Context, client mm.Manager, _ mm.Entry) ([]rootUse, error) {
			var (
				roots    []mm.RootFolder
				items    []sized
				rootsErr error
				itemsErr error
				done     = make(chan struct{})
			)
			go func() {
				defer close(done)
				defer func() {
					if recover() != nil {
						itemsErr = errUnsupported
					}
				}()
				items, itemsErr = listSized(ctx, client)
			}()
			roots, rootsErr = client.RootFolders(ctx)
			<-done
			if rootsErr != nil {
				return nil, rootsErr
			}
			if itemsErr != nil {
				return nil, itemsErr
			}

			used := make(map[string]int64, len(roots))
			for _, item := range items {
				var owner *mm.RootFolder
				for i := range roots {
					r := &roots[i]
					if isWithin(r.Path, item.path) && (owner == nil || len(r.Path) > len(owner.Path)) {
						owner = r
					}
				}
				if owner != nil {
					used[owner.Path] += item.size
				}
			}
			out := make([]rootUse, len(roots))
			for i, r := range roots {
				out[i] = rootUse{root: r, used: used[r.Path]}
			}
			return out, nil
		})

	perKind := map[mm.Kind]int{}
	for _, e := range entries {
		perKind[e.Kind]++
	}
	storage := []StorageEntry{}
	for _, r := range results {
		label := mm.KindLabel[r.Entry.Kind]
		if perKind[r.Entry.Kind] > 1 {
			label = r.Entry.Name // the name tells instances of one kind apart
		}
		for _, v := range r.Value {
			storage = append(storage, StorageEntry{
				InstanceID:       r.Entry.InstanceID,
				Label:            label,
				Path:             v.root.Path,
				FreeBytes:        v.root.FreeSpace,
				LibraryUsedBytes: v.used,
			})
		}
	}
	return StorageResponse{Entries: storage, Instances: mm.Summarize(entries, problems)}
}

func listSized(ctx context.Context, client mm.Manager) ([]sized, error) {
	if s, ok := mm.AsSeries(client); ok {
		list, err := s.Series(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]sized, len(list))
		for i, x := range list {
			out[i] = sized{path: cmp.Or(x.Path, x.RootFolderPath)}
			if x.Statistics != nil {
				out[i].size = x.Statistics.SizeOnDisk
			}
		}
		return out, nil
	}
	m, ok := mm.AsMovies(client)
	if !ok {
		return nil, errUnsupported
	}
	list, err := m.Movies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sized, len(list))
	for i, x := range list {
		out[i] = sized{path: cmp.Or(x.Path, x.RootFolderPath), size: x.SizeOnDisk}
	}
	return out, nil
}

// maxResults caps the hits per instance, so a second instance does not crowd
// out the first.
const maxResults = 8

// Hit is one search result.
type Hit struct {
	InstanceID   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	// ID is the stable external id used to add: tvdbId (series) or tmdbId (movies).
	ID           int     `json:"id"`
	AlreadyAdded bool    `json:"alreadyAdded"`
	Title        string  `json:"title"`
	Year         int     `json:"year"`
	Overview     string  `json:"overview"`
	PosterURL    *string `json:"posterUrl"`
	// Subtitle is the network (series) or studio (movies).
	Subtitle *string `json:"subtitle"`
}

// SearchResponse is the body of GET /api/search. Problems is omitted when every
// instance answered.
type SearchResponse struct {
	Results  []Hit        `json:"results"`
	Problems []mm.Problem `json:"problems,omitempty"`
}

func toHit(r mm.LookupResult, kind mm.Kind, e mm.Entry) (Hit, bool) {
	externalID := r.TmdbID
	subtitle := r.Studio
	if kind == mm.KindSeries {
		externalID, subtitle = r.TvdbID, r.Network
	}
	if externalID == 0 {
		return Hit{}, false
	}
	h := Hit{
		InstanceID:   e.InstanceID,
		InstanceName: e.Name,
		ID:           externalID,
		// The services omit the id of a result that is not in the library yet.
		AlreadyAdded: r.ID != nil,
		Title:        r.Title,
		Year:         r.Year,
		Overview:     r.Overview,
	}
	for _, img := range r.Images {
		if img.CoverType == "poster" {
			if img.RemoteURL != "" {
				u := img.RemoteURL
				h.PosterURL = &u
			}
			break
		}
	}
	if subtitle != "" {
		h.Subtitle = &subtitle
	}
	return h, true
}

// Search asks every entry to look up term. entries must not be empty.
func Search(ctx context.Context, entries []mm.Entry, kind mm.Kind, term string) SearchResponse {
	results, problems := mm.CollectFromManagers(ctx, entries,
		func(ctx context.Context, client mm.Manager, e mm.Entry) ([]Hit, error) {
			raw, err := client.Lookup(ctx, term)
			if err != nil {
				return nil, err
			}
			hits := []Hit{}
			for _, r := range raw {
				if h, ok := toHit(r, kind, e); ok {
					hits = append(hits, h)
				}
			}
			return hits[:min(len(hits), maxResults)], nil
		})
	out := SearchResponse{Results: []Hit{}, Problems: problems}
	for _, r := range results {
		out.Results = append(out.Results, r.Value...)
	}
	return out
}

// Meta is the body of GET /api/add-meta: what the add dialog offers.
type Meta struct {
	InstanceID      string    `json:"instanceId"`
	InstanceName    string    `json:"instanceName"`
	QualityProfiles []Profile `json:"qualityProfiles"`
	RootFolderPath  *string   `json:"rootFolderPath"`
}

// Profile is a quality profile.
type Profile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// AddMeta loads the quality profiles and the first root folder of one ready entry.
func AddMeta(ctx context.Context, e mm.Entry) (Meta, error) {
	var (
		profiles []mm.QualityProfile
		roots    []mm.RootFolder
		pErr     error
		done     = make(chan struct{})
	)
	go func() {
		defer close(done)
		defer func() {
			if recover() != nil {
				pErr = errUnsupported
			}
		}()
		profiles, pErr = e.Client.QualityProfiles(ctx)
	}()
	roots, rErr := e.Client.RootFolders(ctx)
	<-done
	if pErr != nil {
		return Meta{}, pErr
	}
	if rErr != nil {
		return Meta{}, rErr
	}
	meta := Meta{InstanceID: e.InstanceID, InstanceName: e.Name, QualityProfiles: make([]Profile, len(profiles))}
	for i, p := range profiles {
		meta.QualityProfiles[i] = Profile{ID: p.ID, Name: p.Name}
	}
	if len(roots) > 0 {
		meta.RootFolderPath = &roots[0].Path
	}
	return meta, nil
}

// Rating is one rating source of an item.
type Rating struct {
	Source string  `json:"source"`
	Value  float64 `json:"value"`
	Votes  int     `json:"votes"`
}

// Details is the body of GET /api/library/{instanceId}/{id}/details.
type Details struct {
	Title         string   `json:"title"`
	Overview      *string  `json:"overview"`
	Year          *int     `json:"year"`
	Genres        []string `json:"genres"`
	Runtime       *int     `json:"runtime"`
	Certification *string  `json:"certification"`
	Status        *string  `json:"status"`
	Network       *string  `json:"network"`
	Studio        *string  `json:"studio"`
	PosterURL     *string  `json:"posterUrl"`
	FanartURL     *string  `json:"fanartUrl"`
	Ratings       []Rating `json:"ratings"`
}

func str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func num(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func pickImage(images []mm.Image, coverType string) *string {
	for _, i := range images {
		if i.CoverType == coverType {
			return str(i.RemoteURL)
		}
	}
	return nil
}

// ItemDetails loads the details of one item. The client must be a series or a
// movie manager.
func ItemDetails(ctx context.Context, client mm.Manager, id int) (Details, error) {
	if s, ok := mm.AsSeries(client); ok {
		d, err := s.SeriesDetail(ctx, id)
		if err != nil {
			return Details{}, err
		}
		out := Details{
			Title: d.Title, Overview: str(d.Overview), Year: num(d.Year), Genres: orEmpty(d.Genres),
			Runtime: num(d.Runtime), Certification: str(d.Certification), Status: str(d.Status),
			Network: str(d.Network), PosterURL: pickImage(d.Images, "poster"),
			FanartURL: pickImage(d.Images, "fanart"), Ratings: []Rating{},
		}
		if d.Ratings != nil && d.Ratings.Value != 0 {
			out.Ratings = append(out.Ratings, Rating{Source: "TVDB", Value: d.Ratings.Value, Votes: d.Ratings.Votes})
		}
		return out, nil
	}
	m, ok := mm.AsMovies(client)
	if !ok {
		return Details{}, errUnsupported
	}
	d, err := m.MovieDetail(ctx, id)
	if err != nil {
		return Details{}, err
	}
	out := Details{
		Title: d.Title, Overview: str(d.Overview), Year: num(d.Year), Genres: orEmpty(d.Genres),
		Runtime: num(d.Runtime), Certification: str(d.Certification), Status: str(d.Status),
		Studio: str(d.Studio), PosterURL: pickImage(d.Images, "poster"),
		FanartURL: pickImage(d.Images, "fanart"), Ratings: []Rating{},
	}
	sources := make([]string, 0, len(d.Ratings))
	for source := range d.Ratings {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	for _, source := range sources {
		r := d.Ratings[source]
		out.Ratings = append(out.Ratings, Rating{Source: source, Value: r.Value, Votes: r.Votes})
	}
	return out, nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// SeasonSummary is one season in the seasons route.
type SeasonSummary struct {
	SeasonNumber      int   `json:"seasonNumber"`
	Monitored         bool  `json:"monitored"`
	EpisodeFileCount  int   `json:"episodeFileCount"`
	EpisodeCount      int   `json:"episodeCount"`
	TotalEpisodeCount int   `json:"totalEpisodeCount"`
	SizeOnDisk        int64 `json:"sizeOnDisk"`
}

// SeasonsResponse is the body of GET /api/library/{instanceId}/{id}/seasons.
type SeasonsResponse struct {
	Title   string          `json:"title"`
	Seasons []SeasonSummary `json:"seasons"`
}

// Seasons lists a series' seasons ordered by season number.
func Seasons(ctx context.Context, s mm.SeriesManager, id int) (SeasonsResponse, error) {
	list, err := s.Seasons(ctx, id)
	if err != nil {
		return SeasonsResponse{}, err
	}
	out := SeasonsResponse{Title: list.Title, Seasons: make([]SeasonSummary, len(list.Seasons))}
	for i, x := range list.Seasons {
		out.Seasons[i] = SeasonSummary{SeasonNumber: x.SeasonNumber, Monitored: x.Monitored}
		if st := x.Statistics; st != nil {
			out.Seasons[i].EpisodeFileCount, out.Seasons[i].EpisodeCount = st.EpisodeFileCount, st.EpisodeCount
			out.Seasons[i].TotalEpisodeCount, out.Seasons[i].SizeOnDisk = st.TotalEpisodeCount, st.SizeOnDisk
		}
	}
	return out, nil
}
