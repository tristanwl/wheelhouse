package codecs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

func TestClassify(t *testing.T) {
	for in, want := range map[string]Family{
		"x264": H264, "H264": H264, " AVC ": H264, "x265": HEVC, "h265": HEVC, "HEVC": HEVC,
		"vc1": Legacy, "XviD": Legacy, "mpeg2": Legacy, "": Legacy, "av1": Legacy,
	} {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestTierOf(t *testing.T) {
	for in, want := range map[string]Tier{
		"1920x1080": HD, "3840x2160": UHD, "4096x2160": UHD, "2160x3840": UHD,
		"2999x1": HD, "3000x1": UHD, "garbage": HD, "": HD, " 3840x2160 ": UHD,
	} {
		if got := TierOf(&in); got != want {
			t.Errorf("TierOf(%q) = %s, want %s", in, got, want)
		}
	}
	if TierOf(nil) != HD {
		t.Error("unknown resolution is HD")
	}
}

func file(title, codec, res string, size int64) FileInfo {
	f := FileInfo{Title: title, Codec: codec, Size: size, InstanceID: "radarr", ItemID: 1, FilePath: "/movies/" + title}
	if res != "" {
		f.Resolution = &res
	}
	return f
}

func TestAnalyzeDefaultPreferences(t *testing.T) {
	files := []FileInfo{
		file("hd-h264", "x264", "1920x1080", 100),
		file("hd-hevc", "x265", "1920x1080", 300),
		file("hd-legacy", "xvid", "1280x720", 200),
		file("uhd-hevc", "HEVC", "3840x2160", 400),
		file("uhd-h264", "h264", "3840x2160", 500),
		file("unknown-res-legacy", "mpeg2", "", 50),
	}
	a := Analyze(files, DefaultPreferences)
	if a.TotalCount != 6 || a.TotalSize != 1550 {
		t.Fatalf("totals: %d %d", a.TotalCount, a.TotalSize)
	}
	var cands []string
	for _, c := range a.Candidates {
		cands = append(cands, c.Title+":"+string(c.Reason)+":"+string(c.TargetFamily)+":"+string(c.Tier))
	}
	want := []string{
		"uhd-h264:codec-mismatch:hevc:uhd",
		"hd-hevc:codec-mismatch:h264:hd",
		"hd-legacy:legacy-codec:h264:hd",
		"unknown-res-legacy:legacy-codec:h264:hd",
	}
	if strings.Join(cands, "|") != strings.Join(want, "|") {
		t.Errorf("candidates (biggest first):\n got %v\nwant %v", cands, want)
	}
	if len(a.Matching) != 2 || a.Matching[0].Title != "uhd-hevc" || a.Matching[1].Title != "hd-h264" {
		t.Errorf("matching: %+v", a.Matching)
	}
	if b := a.Buckets[H264]; b.Count != 2 || b.TotalSize != 600 {
		t.Errorf("h264 bucket %+v", b)
	}
	if b := a.Buckets[Legacy]; b.Count != 2 || b.TotalSize != 250 {
		t.Errorf("legacy bucket %+v", b)
	}
	if p := a.Buckets[HEVC].PercentOfLibrary; p < 45.16 || p > 45.17 {
		t.Errorf("hevc percent %v", p)
	}
}

func TestAnalyzeHonoursPreferencesAndEmptyLibrary(t *testing.T) {
	a := Analyze([]FileInfo{file("x", "x264", "1920x1080", 10)}, Preferences{HD: "hevc", UHD: "hevc"})
	if len(a.Candidates) != 1 || a.Candidates[0].TargetFamily != "hevc" {
		t.Errorf("%+v", a.Candidates)
	}
	empty := Analyze(nil, DefaultPreferences)
	raw, _ := json.Marshal(empty)
	for _, want := range []string{`"candidates":[]`, `"matching":[]`, `"percentOfLibrary":0`, `"totalCount":0`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("empty analysis lacks %s: %s", want, raw)
		}
	}
}

func TestFileInfoJSONShape(t *testing.T) {
	d := 8
	f := file("Example Movie 1", "x264", "1920x1080", 5)
	f.BitDepth = &d
	raw, _ := json.Marshal(Analyze([]FileInfo{f, file("No Res", "x264", "", 1)}, Preferences{HD: "hevc", UHD: "hevc"}))
	for _, want := range []string{`"resolution":"1920x1080"`, `"bitDepth":8`, `"resolution":null`, `"bitDepth":null`, `"tier":"hd"`, `"targetFamily":"hevc"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("lacks %s: %s", want, raw)
		}
	}
}

func TestEpisodeLabel(t *testing.T) {
	ep := func(n int, title string) mediamanager.Episode {
		return mediamanager.Episode{EpisodeNumber: n, Title: title}
	}
	for _, c := range []struct {
		eps  []mediamanager.Episode
		want string
	}{
		{nil, "Example Show - Season 3"},
		{[]mediamanager.Episode{ep(7, "The Pilot")}, `Example Show - S03E07 "The Pilot"`},
		{[]mediamanager.Episode{ep(7, "")}, "Example Show - S03E07"},
		{[]mediamanager.Episode{ep(8, "b"), ep(7, "a")}, "Example Show - S03E07-E08"},
		{[]mediamanager.Episode{ep(12, "a"), ep(10, "b"), ep(11, "c")}, "Example Show - S03E10-E12"},
	} {
		if got := EpisodeLabel("Example Show", 3, c.eps); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "config")) // created on first save
	if s.Get() != DefaultPreferences {
		t.Fatalf("defaults: %+v", s.Get())
	}
	if err := s.Set(Preferences{HD: "hevc", UHD: "h264"}); err != nil {
		t.Fatal(err)
	}
	if got := NewStore(filepath.Join(dir, "config")).Get(); got != (Preferences{HD: "hevc", UHD: "h264"}) {
		t.Errorf("saved: %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config", "settings.json"))
	if string(raw) != "{\n  \"codecPreferences\": {\n    \"hd\": \"hevc\",\n    \"uhd\": \"h264\"\n  }\n}" {
		t.Errorf("file format: %q", raw)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "config")); len(entries) != 1 {
		t.Errorf("a temp file was left behind: %v", entries)
	}

	// Corrupt or partly invalid files fall back per field.
	for body, want := range map[string]Preferences{
		"not json":                           DefaultPreferences,
		`{}`:                                 DefaultPreferences,
		`{"codecPreferences":{"hd":"hevc"}}`: {HD: "hevc", UHD: "hevc"},
		`{"codecPreferences":{"hd":"av1","uhd":"h264"}}`: {HD: "h264", UHD: "h264"},
	} {
		if err := os.WriteFile(filepath.Join(dir, "config", "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := s.Get(); got != want {
			t.Errorf("%q: %+v, want %+v", body, got, want)
		}
	}
}

func TestStoreSetFailsWhenTheDirectoryIsNotWritable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(file).Set(DefaultPreferences); err == nil {
		t.Fatal("expected an error")
	}
}

// A read-only /config directory around a writable settings.json (or a
// settings.json bind-mounted as a file) worked with the Next app, which wrote
// in place; it must keep working.
func TestStoreSetWritesInPlaceWhenTheDirectoryIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	s := NewStore(dir)
	if err := s.Set(Preferences{HD: "hevc", UHD: "h264"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got != (Preferences{HD: "hevc", UHD: "h264"}) {
		t.Errorf("saved: %+v", got)
	}
}
