package codecs

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Scan is the files one manager instance has, and how many of its items were
// left out.
type Scan struct {
	Files []FileInfo
	// Skipped counts library items with no file yet, or a file the manager has
	// not scanned mediaInfo for: excluded rather than guessed at.
	Skipped int
}

// Response is the body of GET /api/codec-analysis.
type Response struct {
	Analysis
	SkippedCount int                    `json:"skippedCount"`
	Problems     []mediamanager.Problem `json:"problems,omitempty"`
}

// ScanFiles lists one instance's files for the analysis.
func ScanFiles(ctx context.Context, client mediamanager.Manager, entry mediamanager.Entry) (Scan, error) {
	if m, ok := mediamanager.AsMovies(client); ok {
		return movieFiles(ctx, m, entry)
	}
	if s, ok := mediamanager.AsSeries(client); ok {
		return tvFiles(ctx, s, entry)
	}
	return Scan{}, fmt.Errorf("not a movie or series manager")
}

func bitDepth(mi *mediamanager.MediaInfo) *int {
	if mi == nil {
		return nil
	}
	return mi.VideoBitDepth
}

func resolution(mi *mediamanager.MediaInfo) *string {
	if mi == nil || mi.Resolution == "" {
		return nil
	}
	return &mi.Resolution
}

func movieFiles(ctx context.Context, client mediamanager.MovieManager, entry mediamanager.Entry) (Scan, error) {
	movies, err := client.Movies(ctx)
	if err != nil {
		return Scan{}, err
	}
	var scan Scan
	for _, m := range movies {
		if m.MovieFile == nil || m.MovieFile.MediaInfo == nil || m.MovieFile.MediaInfo.VideoCodec == "" {
			scan.Skipped++
			continue
		}
		mi := m.MovieFile.MediaInfo
		scan.Files = append(scan.Files, FileInfo{
			Title:      m.Title,
			Codec:      mi.VideoCodec,
			Resolution: resolution(mi),
			BitDepth:   bitDepth(mi),
			Size:       m.MovieFile.Size,
			InstanceID: entry.InstanceID,
			ItemID:     m.ID,
			FilePath:   m.MovieFile.Path,
		})
	}
	return scan, nil
}

func pad2(n int) string { return fmt.Sprintf("%02d", n) }

// EpisodeLabel is "Show - S03E07 "Title"", zero-padded like Sonarr shows it.
// Sonarr's episodefile resource only carries a season number: episode number
// and title live on the separate episode resource, linked by episodeFileId (an
// aired double episode can share one file, hence a list).
func EpisodeLabel(seriesTitle string, seasonNumber int, episodes []mediamanager.Episode) string {
	if len(episodes) == 0 {
		// No matching episode record (a file imported before Sonarr's episode
		// list caught up): fall back to the season rather than show nothing.
		return fmt.Sprintf("%s - Season %d", seriesTitle, seasonNumber)
	}
	sorted := slices.Clone(episodes)
	slices.SortStableFunc(sorted, func(a, b mediamanager.Episode) int { return a.EpisodeNumber - b.EpisodeNumber })
	season := pad2(seasonNumber)
	if len(sorted) == 1 {
		e := sorted[0]
		suffix := ""
		if e.Title != "" {
			suffix = ` "` + e.Title + `"`
		}
		return fmt.Sprintf("%s - S%sE%s%s", seriesTitle, season, pad2(e.EpisodeNumber), suffix)
	}
	first, last := sorted[0], sorted[len(sorted)-1]
	return fmt.Sprintf("%s - S%sE%s-E%s", seriesTitle, season, pad2(first.EpisodeNumber), pad2(last.EpisodeNumber))
}

// fanOut bounds the per-series requests in flight: Sonarr has no bulk "all
// episode files" endpoint, only per series.
const fanOut = 8

type seriesData struct {
	series   mediamanager.Series
	files    []mediamanager.EpisodeFile
	episodes []mediamanager.Episode
}

func tvFiles(ctx context.Context, client mediamanager.SeriesManager, entry mediamanager.Entry) (Scan, error) {
	series, err := client.Series(ctx)
	if err != nil {
		return Scan{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	data := make([]seriesData, len(series))
	sem := make(chan struct{}, fanOut)
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) {
		once.Do(func() { firstErr = err; cancel() })
	}
	for i, s := range series {
		data[i].series = s
		for _, job := range []func(){
			func() {
				f, err := client.EpisodeFiles(ctx, s.ID)
				if err != nil {
					fail(err)
					return
				}
				data[i].files = f
			},
			func() {
				e, err := client.Episodes(ctx, s.ID)
				if err != nil {
					fail(err)
					return
				}
				data[i].episodes = e
			},
		} {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				if ctx.Err() != nil {
					return
				}
				job()
			})
		}
	}
	wg.Wait()
	if firstErr != nil {
		return Scan{}, firstErr
	}

	var scan Scan
	for _, d := range data {
		for _, f := range d.files {
			if f.MediaInfo == nil || f.MediaInfo.VideoCodec == "" {
				scan.Skipped++
				continue
			}
			var matching []mediamanager.Episode
			for _, e := range d.episodes {
				if e.EpisodeFileID == f.ID {
					matching = append(matching, e)
				}
			}
			scan.Files = append(scan.Files, FileInfo{
				Title:      EpisodeLabel(d.series.Title, f.SeasonNumber, matching),
				Codec:      f.MediaInfo.VideoCodec,
				Resolution: resolution(f.MediaInfo),
				BitDepth:   bitDepth(f.MediaInfo),
				Size:       f.Size,
				InstanceID: entry.InstanceID,
				ItemID:     d.series.ID,
				FilePath:   f.Path,
			})
		}
	}
	return scan, nil
}
