package mediamanager

import (
	"context"
	"slices"
	"sync"
)

// seasonSource is what the season operations need from a Sonarr client.
type seasonSource interface {
	SeriesByID(ctx context.Context, id int) (SeriesFull, error)
	EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error)
	DeleteEpisodeFile(ctx context.Context, id int) error
	UpdateSeries(ctx context.Context, series SeriesFull) (SeriesFull, error)
}

// ListSeasons implements SeriesManager.Seasons for a Sonarr client.
func ListSeasons(ctx context.Context, m seasonSource, seriesID int) (SeasonList, error) {
	series, err := m.SeriesByID(ctx, seriesID)
	if err != nil {
		return SeasonList{}, err
	}
	seasons := slices.Clone(series.Seasons)
	slices.SortStableFunc(seasons, func(a, b Season) int { return a.SeasonNumber - b.SeasonNumber })
	if seasons == nil {
		seasons = []Season{}
	}
	return SeasonList{Title: series.Title, Seasons: seasons}, nil
}

// DeleteSeason implements SeriesManager.DeleteSeason for a Sonarr client: the
// season's episode files are deleted concurrently, and only when all of them
// went is the season set to unmonitored (so Sonarr does not download it again).
func DeleteSeason(ctx context.Context, m seasonSource, seriesID, seasonNumber int) (int, error) {
	files, err := m.EpisodeFiles(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	var doomed []EpisodeFile
	for _, f := range files {
		if f.SeasonNumber == seasonNumber {
			doomed = append(doomed, f)
		}
	}

	errs := make([]error, len(doomed))
	var wg sync.WaitGroup
	for i, f := range doomed {
		wg.Go(func() { errs[i] = m.DeleteEpisodeFile(ctx, f.ID) })
	}
	wg.Wait()
	if err := firstError(errs); err != nil {
		return 0, err
	}

	series, err := m.SeriesByID(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	seasons := slices.Clone(series.Seasons)
	for i := range seasons {
		if seasons[i].SeasonNumber == seasonNumber {
			seasons[i].Monitored = false
		}
	}
	series.Seasons = seasons
	if _, err := m.UpdateSeries(ctx, series); err != nil {
		return 0, err
	}
	return len(doomed), nil
}
