package codecs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// FuzzClassifyDynamicRange: the HDR class from what Radarr and Sonarr report.
//
// Invariants: the class is always valid; "none" (the only class that lets a file
// convert by default) comes only from two empty values or an explicit SDR; every
// value that mentions Dolby Vision (DV, Dolby), HLG, HDR10, PQ or SMPTE ST 2084
// in any letter case and padding is that HDR class; and anything else that is
// not empty is "unknown", never "none".
func FuzzClassifyDynamicRange(f *testing.F) {
	f.Fuzz(func(t *testing.T, dynamicRange, dynamicRangeType string) {
		got := ClassifyDynamicRange(dynamicRange, dynamicRangeType)
		if !got.Valid() {
			t.Fatalf("invalid class %q", got)
		}
		r := strings.ToLower(strings.TrimSpace(dynamicRange))
		ty := strings.ToLower(strings.TrimSpace(dynamicRangeType))
		both := r + " " + ty
		explicitSDR := r == "sdr" && (ty == "" || ty == "sdr")
		if got == HDRNone && (r != "" || ty != "") && !explicitSDR {
			t.Fatalf("(%q, %q) is %q: neither empty nor SDR", dynamicRange, dynamicRangeType, got)
		}
		if (r == "" && ty == "") || explicitSDR {
			if strings.Contains(both, "dv") || strings.Contains(both, "dolby") || strings.Contains(both, "hlg") || strings.Contains(both, "hdr10") {
				return
			}
			if got != HDRNone {
				t.Fatalf("(%q, %q) is %q, want none", dynamicRange, dynamicRangeType, got)
			}
			return
		}
		for _, mark := range []struct {
			text string
			want HDR
		}{{"dv", HDRDolbyVision}, {"dolby", HDRDolbyVision}} {
			if strings.Contains(both, mark.text) && got != mark.want {
				t.Fatalf("(%q, %q) mentions %q but is %q", dynamicRange, dynamicRangeType, mark.text, got)
			}
		}
		if !strings.Contains(both, "dv") && !strings.Contains(both, "dolby") {
			if strings.Contains(both, "hlg") && got != HDRHLG {
				t.Fatalf("(%q, %q) mentions HLG but is %q", dynamicRange, dynamicRangeType, got)
			}
			if !strings.Contains(both, "hlg") && (strings.Contains(both, "hdr10") || strings.Contains(both, "pq") || strings.Contains(both, "st2084")) && got != HDRHDR10 {
				t.Fatalf("(%q, %q) mentions PQ but is %q", dynamicRange, dynamicRangeType, got)
			}
		}
	})
}

var digitsOnly = regexp.MustCompile(`^\s*(\d+)x(\d+)\s*$`)

// FuzzTierOf: the resolution tier from the string a manager reports.
//
// Invariants: only a "WxH" string whose longer side is at least the 4K threshold
// is UHD; an unparsable or missing resolution is HD, never a panic; and an
// absurdly long number does not wrap into a small one.
func FuzzTierOf(f *testing.F) {
	f.Fuzz(func(t *testing.T, res string) {
		got := TierOf(&res)
		m := digitsOnly.FindStringSubmatch(strings.TrimSpace(res))
		if m == nil {
			if got != HD || longEdge(res) != 0 {
				t.Fatalf("%q is not WxH but tier %q edge %d", res, got, longEdge(res))
			}
			return
		}
		longest := func(a, b string) string {
			a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
			if len(a) != len(b) {
				if len(a) > len(b) {
					return a
				}
				return b
			}
			return max(a, b)
		}(m[1], m[2])
		huge := len(longest) > 4 // anything of five digits or more is far above 3000
		big := huge || (longest != "" && atoi(longest) >= FourKThresholdPx)
		if big != (got == UHD) {
			t.Fatalf("%q has long side %q but tier %q", res, longest, got)
		}
		if TierOf(nil) != HD {
			t.Fatal("a missing resolution is not HD")
		}
	})
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// FuzzClassifyCodec: the family from the codec name a manager reports.
//
// Invariants: only the known H.264 and HEVC names (any case, padded) are those
// families; everything else is legacy; no panic.
func FuzzClassifyCodec(f *testing.F) {
	f.Fuzz(func(t *testing.T, codec string) {
		got := Classify(codec)
		switch strings.ToLower(strings.TrimSpace(codec)) {
		case "x264", "h264", "avc":
			if got != H264 {
				t.Fatalf("%q is %q", codec, got)
			}
		case "x265", "h265", "hevc":
			if got != HEVC {
				t.Fatalf("%q is %q", codec, got)
			}
		default:
			if got != Legacy {
				t.Fatalf("%q is %q, want legacy", codec, got)
			}
		}
	})
}

// FuzzSettingsFile: settings.json is operator-editable and may hold anything.
//
// Invariants: reading it never panics, and what comes back is always a valid
// preference for each tier (the default for anything missing, mistyped or not
// one of h264 and hevc), so no stored text reaches the converter; saving what was
// read and reading it again gives the same.
func FuzzSettingsFile(f *testing.F) {
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, raw []byte) {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
			t.Skip()
		}
		s := NewStore(dir)
		got := s.Get()
		if !got.HD.Valid() || !got.UHD.Valid() {
			t.Fatalf("invalid preferences %+v from %q", got, raw)
		}
		if err := s.Set(got); err != nil {
			t.Fatal(err)
		}
		if again := s.Get(); again != got {
			t.Fatalf("saved %+v and read %+v", got, again)
		}
	})
}
