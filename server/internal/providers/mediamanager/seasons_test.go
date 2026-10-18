package mediamanager

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// stubSonarr is a seasonSource that records what was done to it.
type stubSonarr struct {
	mu       sync.Mutex
	series   SeriesFull
	files    []EpisodeFile
	failFile int
	deleted  []int
	updated  *SeriesFull
}

func (s *stubSonarr) SeriesByID(context.Context, int) (SeriesFull, error) { return s.series, nil }

func (s *stubSonarr) EpisodeFiles(context.Context, int) ([]EpisodeFile, error) { return s.files, nil }

func (s *stubSonarr) DeleteEpisodeFile(_ context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.failFile {
		return errors.New("boom")
	}
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *stubSonarr) UpdateSeries(_ context.Context, series SeriesFull) (SeriesFull, error) {
	s.updated = &series
	return series, nil
}

func newStub() *stubSonarr {
	return &stubSonarr{
		series: SeriesFull{
			Series:  Series{ID: 1, Title: "Example Series 1"},
			Seasons: []Season{{SeasonNumber: 2, Monitored: true}, {SeasonNumber: 1, Monitored: true}},
		},
		files: []EpisodeFile{
			{ID: 10, SeasonNumber: 1}, {ID: 11, SeasonNumber: 1}, {ID: 20, SeasonNumber: 2},
		},
	}
}

func TestDeleteSeasonDeletesThenUnmonitorsOnlyThatSeason(t *testing.T) {
	s := newStub()
	n, err := DeleteSeason(context.Background(), s, 1, 1)
	if err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if len(s.deleted) != 2 {
		t.Errorf("deleted = %v", s.deleted)
	}
	if s.updated == nil {
		t.Fatal("series not updated")
	}
	for _, season := range s.updated.Seasons {
		if want := season.SeasonNumber != 1; season.Monitored != want {
			t.Errorf("season %d monitored = %v", season.SeasonNumber, season.Monitored)
		}
	}
	// The stub's own seasons were not mutated in place.
	if !s.series.Seasons[1].Monitored {
		t.Error("the fetched series was modified")
	}
}

func TestDeleteSeasonDoesNotUpdateWhenAFileFails(t *testing.T) {
	s := newStub()
	s.failFile = 11
	n, err := DeleteSeason(context.Background(), s, 1, 1)
	if err == nil || n != 0 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if s.updated != nil {
		t.Error("the series was updated although a file could not be deleted")
	}
}

func TestListSeasonsSorts(t *testing.T) {
	s := newStub()
	list, err := ListSeasons(context.Background(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if list.Title != "Example Series 1" || list.Seasons[0].SeasonNumber != 1 || list.Seasons[1].SeasonNumber != 2 {
		t.Errorf("list = %+v", list)
	}
	if s.series.Seasons[0].SeasonNumber != 2 {
		t.Error("sorting changed the fetched series")
	}
}
