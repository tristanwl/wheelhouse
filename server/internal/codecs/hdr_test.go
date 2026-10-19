package codecs

import "testing"

func TestClassifyDynamicRange(t *testing.T) {
	for _, c := range []struct {
		rng, typ string
		want     HDR
	}{
		{"", "", HDRNone},
		{"SDR", "", HDRNone},
		{"HDR", "HDR10", HDRHDR10},
		{"HDR", "HDR10Plus", HDRHDR10},
		{"HDR", "PQ", HDRHDR10},
		{"HDR", "HLG", HDRHLG},
		{"HDR", "DV", HDRDolbyVision},
		{"HDR", "DV HDR10", HDRDolbyVision},
		{"HDR", "DV HLG", HDRDolbyVision},
		{"", "DV SDR", HDRDolbyVision},
		{"HDR", "", HDRUnknown},
		{"HDR", "Something New", HDRUnknown},
	} {
		if got := ClassifyDynamicRange(c.rng, c.typ); got != c.want {
			t.Errorf("(%q, %q) = %s, want %s", c.rng, c.typ, got, c.want)
		}
	}
}

func TestAnalyzeLeavesHDRCandidatesOutUnlessAllowed(t *testing.T) {
	res := "1920x1080"
	files := []FileInfo{
		{Title: "sdr", Codec: "xvid", Resolution: &res, Size: 1, HDR: HDRNone},
		{Title: "unclassified", Codec: "xvid", Resolution: &res, Size: 2},
		{Title: "hdr legacy", Codec: "vc1", Resolution: &res, Size: 3, HDR: HDRHDR10},
		{Title: "dv mismatch", Codec: "hevc", Resolution: &res, Size: 4, HDR: HDRDolbyVision},
		{Title: "hdr matching", Codec: "x264", Resolution: &res, Size: 5, HDR: HDRHLG},
		{Title: "unknown", Codec: "xvid", Resolution: &res, Size: 6, HDR: HDRUnknown},
	}
	a := Analyze(files, DefaultPreferences)
	if len(a.Candidates) != 2 || a.HDRExcluded != 3 || a.TotalCount != 6 {
		t.Fatalf("default: %d candidates, %d excluded", len(a.Candidates), a.HDRExcluded)
	}
	// The matching HDR file is not a candidate either way, and is not "excluded".
	if len(a.Matching) != 1 || a.Matching[0].Title != "hdr matching" {
		t.Errorf("matching: %+v", a.Matching)
	}
	a = AnalyzeWith(files, DefaultPreferences, AnalyzeOptions{IncludeHDR: true})
	if len(a.Candidates) != 5 || a.HDRExcluded != 0 {
		t.Fatalf("allowed: %d candidates, %d excluded", len(a.Candidates), a.HDRExcluded)
	}
	if a.Candidates[0].HDR != HDRUnknown {
		t.Errorf("the HDR class is carried to the candidate: %+v", a.Candidates[0])
	}
}
