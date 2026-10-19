// Package codecs analyses the library's video codecs and keeps the codec
// preferences. It mirrors lib/codec.ts, lib/settings.ts and the scanning half
// of app/api/codec-analysis/route.ts.
package codecs

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Family is the codec family a file is classified as.
type Family string

// The families. "legacy" is everything that is neither H.264 nor HEVC.
const (
	H264   Family = "h264"
	HEVC   Family = "hevc"
	Legacy Family = "legacy"
)

// Tier is the resolution class a preference applies to.
type Tier string

// The tiers.
const (
	HD  Tier = "hd"
	UHD Tier = "uhd"
)

// Preferable is a family a tier can prefer.
type Preferable string

// Valid reports whether p is "h264" or "hevc".
func (p Preferable) Valid() bool { return p == "h264" || p == "hevc" }

// Preferences are the families each tier should be in.
type Preferences struct {
	HD  Preferable `json:"hd"`
	UHD Preferable `json:"uhd"`
}

// DefaultPreferences: HD defaults to H.264 for maximum playback compatibility;
// UHD to HEVC, whose compression advantage over H.264 is largest at 4K.
var DefaultPreferences = Preferences{HD: "h264", UHD: "hevc"}

// Classify maps the encoder name Radarr and Sonarr report in mediaInfo to a
// family: "x264", "h264" and "avc" are H.264; "x265", "h265" and "hevc" are
// HEVC; anything else (vc1, xvid, mpeg2, mpeg4, ...) is legacy.
func Classify(videoCodec string) Family {
	switch strings.ToLower(strings.TrimSpace(videoCodec)) {
	case "x264", "h264", "avc":
		return H264
	case "x265", "h265", "hevc":
		return HEVC
	}
	return Legacy
}

// FourKThresholdPx separates HD from UHD by the long edge of the picture.
const FourKThresholdPx = 3000

var resolutionRE = regexp.MustCompile(`^(\d+)x(\d+)$`)

func longEdge(resolution string) int {
	m := resolutionRE.FindStringSubmatch(strings.TrimSpace(resolution))
	if m == nil {
		return 0
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return max(w, h)
}

// TierOf classifies a resolution such as "3840x2160"; unknown is HD.
func TierOf(resolution *string) Tier {
	if resolution != nil && longEdge(*resolution) >= FourKThresholdPx {
		return UHD
	}
	return HD
}

// FileInfo is one library file. InstanceID and ItemID (the movie or series id
// in that instance) are used to ask for a rescan afterwards; FilePath is the
// absolute path as Radarr and Sonarr see it, which only works if Wheelhouse
// mounts the media at the same paths.
//
// HDR is classified from the videoDynamicRange and videoDynamicRangeType that
// Radarr and Sonarr report (ClassifyDynamicRange); the file itself is probed
// again with ffprobe when a conversion is requested (issue #32).
type FileInfo struct {
	Title      string  `json:"title"`
	Codec      string  `json:"codec"`
	Resolution *string `json:"resolution"`
	BitDepth   *int    `json:"bitDepth"`
	Size       int64   `json:"size"`
	InstanceID string  `json:"instanceId"`
	ItemID     int     `json:"itemId"`
	FilePath   string  `json:"filePath"`
	HDR        HDR     `json:"hdr"`
}

// BucketSummary is the share of the library in one family.
type BucketSummary struct {
	Count            int     `json:"count"`
	TotalSize        int64   `json:"totalSize"`
	PercentOfLibrary float64 `json:"percentOfLibrary"`
}

// Reason is why a file is a conversion candidate.
type Reason string

// The reasons.
const (
	ReasonLegacy   Reason = "legacy-codec"
	ReasonMismatch Reason = "codec-mismatch"
)

// Candidate is a file that should be converted.
type Candidate struct {
	FileInfo
	Tier Tier `json:"tier"`
	// TargetFamily is the codec this file's tier prefers: what Convert targets.
	TargetFamily Preferable `json:"targetFamily"`
	Reason       Reason     `json:"reason"`
}

// Matching is a file already in its tier's preferred family.
type Matching struct {
	FileInfo
	Tier Tier `json:"tier"`
}

// Analysis is the library's codec breakdown.
type Analysis struct {
	TotalCount  int                      `json:"totalCount"`
	TotalSize   int64                    `json:"totalSize"`
	Preferences Preferences              `json:"preferences"`
	Buckets     map[Family]BucketSummary `json:"buckets"`
	Candidates  []Candidate              `json:"candidates"`
	Matching    []Matching               `json:"matching"`
	// HDRExcluded counts the files that would be candidates but are left out
	// because they are HDR or Dolby Vision and the administrator has not allowed
	// converting them (always 0 when IncludeHDR was set).
	HDRExcluded int `json:"hdrExcluded"`
}

// Analyze sorts files into buckets and splits them into conversion candidates
// and files that already match the preferences. Legacy codecs (essentially no
// hardware decode anywhere) are always candidates toward their tier's
// preference; H.264 and HEVC files only when they do not match it. With the
// default preferences a 4K HEVC file is left alone and HEVC below 4K is
// flagged. Both lists are sorted by size, biggest first.
//
// HDR and Dolby Vision files that would be candidates are left out (counted in
// HDRExcluded) unless IncludeHDR is set; see AnalyzeWith.
func Analyze(files []FileInfo, prefs Preferences) Analysis {
	return AnalyzeWith(files, prefs, AnalyzeOptions{})
}

// AnalyzeOptions are the policy switches of the analysis.
type AnalyzeOptions struct {
	// IncludeHDR lets HDR and Dolby Vision files be candidates (the converter
	// then writes SDR 8-bit and says so; see HDR).
	IncludeHDR bool
}

// AnalyzeWith is Analyze with options.
func AnalyzeWith(files []FileInfo, prefs Preferences, opts AnalyzeOptions) Analysis {
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}
	counts := map[Family]*BucketSummary{H264: {}, HEVC: {}, Legacy: {}}
	out := Analysis{
		TotalCount:  len(files),
		TotalSize:   totalSize,
		Preferences: prefs,
		Candidates:  []Candidate{},
		Matching:    []Matching{},
	}
	for _, f := range files {
		family := Classify(f.Codec)
		b := counts[family]
		b.Count++
		b.TotalSize += f.Size

		tier := TierOf(f.Resolution)
		target := prefs.HD
		if tier == UHD {
			target = prefs.UHD
		}
		wouldConvert := family == Legacy || string(family) != string(target)
		if wouldConvert && f.HDR.IsHDR() && !opts.IncludeHDR {
			out.HDRExcluded++
			continue
		}
		switch {
		case family == Legacy:
			out.Candidates = append(out.Candidates, Candidate{FileInfo: f, Tier: tier, TargetFamily: target, Reason: ReasonLegacy})
		case string(family) != string(target):
			out.Candidates = append(out.Candidates, Candidate{FileInfo: f, Tier: tier, TargetFamily: target, Reason: ReasonMismatch})
		default:
			out.Matching = append(out.Matching, Matching{FileInfo: f, Tier: tier})
		}
	}
	out.Buckets = make(map[Family]BucketSummary, 3)
	for fam, b := range counts {
		if totalSize > 0 {
			b.PercentOfLibrary = float64(b.TotalSize) / float64(totalSize) * 100
		}
		out.Buckets[fam] = *b
	}
	slices.SortStableFunc(out.Candidates, func(a, b Candidate) int { return cmpSizeDesc(a.Size, b.Size) })
	slices.SortStableFunc(out.Matching, func(a, b Matching) int { return cmpSizeDesc(a.Size, b.Size) })
	return out
}

func cmpSizeDesc(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}
