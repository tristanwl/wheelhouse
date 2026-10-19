package codecs

import "strings"

// HDR is the high dynamic range class of a file, for display and for the
// conversion policy (issue #32). Conversion always writes 8-bit SDR, so an HDR
// or Dolby Vision file would come out flattened (washed out colours); such files
// are not conversion candidates unless an administrator allows it, and then only
// with an explicit acknowledgement. Tone mapping is out of scope.
type HDR string

// The classes. "unknown" is anything that looks like HDR or wide gamut but is
// not recognised, or a probe that gave no answer: it is treated like HDR, never
// like SDR.
const (
	HDRNone        HDR = "none"
	HDRHDR10       HDR = "hdr10"
	HDRHLG         HDR = "hlg"
	HDRDolbyVision HDR = "dolby-vision"
	HDRUnknown     HDR = "unknown"
)

// Valid reports whether h is one of the classes.
func (h HDR) Valid() bool {
	switch h {
	case HDRNone, HDRHDR10, HDRHLG, HDRDolbyVision, HDRUnknown:
		return true
	}
	return false
}

// IsHDR reports whether converting the file would lose something: everything
// but SDR (and the empty value of a file nobody classified).
func (h HDR) IsHDR() bool { return h != HDRNone && h != "" }

// Norm returns HDRNone for the empty value.
func (h HDR) Norm() HDR {
	if h == "" {
		return HDRNone
	}
	return h
}

// Worse returns the class that matters more for the policy: Dolby Vision, then
// HDR10 and HLG, then unknown, then none.
func Worse(a, b HDR) HDR {
	rank := func(h HDR) int {
		switch h {
		case HDRDolbyVision:
			return 4
		case HDRHDR10, HDRHLG:
			return 3
		case HDRUnknown:
			return 2
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// ClassifyDynamicRange maps what Radarr and Sonarr report in mediaInfo
// (videoDynamicRange: "" or "HDR"; videoDynamicRangeType: "HDR10", "HDR10Plus",
// "HLG", "PQ", "DV", "DV HDR10", "DV HLG", "DV SDR", ...) to a class. Both empty
// is SDR. A non-empty value that is not recognised is "unknown".
func ClassifyDynamicRange(dynamicRange, dynamicRangeType string) HDR {
	r := strings.ToLower(strings.TrimSpace(dynamicRange))
	t := strings.ToLower(strings.TrimSpace(dynamicRangeType))
	if r == "" && t == "" {
		return HDRNone
	}
	both := r + " " + t
	switch {
	case strings.Contains(both, "dv") || strings.Contains(both, "dolby"):
		return HDRDolbyVision
	case strings.Contains(both, "hlg"):
		return HDRHLG
	case strings.Contains(both, "hdr10") || strings.Contains(both, "pq") || strings.Contains(both, "st2084") || strings.Contains(both, "smpte2084"):
		return HDRHDR10
	case r == "sdr" && (t == "" || t == "sdr"):
		return HDRNone
	}
	return HDRUnknown
}
