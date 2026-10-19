package codecs

import (
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the values Radarr and
// Sonarr report (the cases of the codec tests) and hostile ones:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/codecs
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	for _, c := range [][2]string{
		{"", ""}, {"SDR", ""}, {"SDR", "SDR"}, {"HDR", ""}, {"HDR", "HDR10"}, {"HDR", "HDR10Plus"}, {"HDR", "HLG"}, {"HDR", "PQ"},
		{"HDR", "DV"}, {"HDR", "DV HDR10"}, {"HDR", "DV HLG"}, {"HDR", "DV SDR"}, {"", "dolby vision"}, {"", "ST2084"}, {"", "SMPTE2084"},
		{"  sdr  ", " SDR "}, {"SDR", "HDR10"}, {"sdr", "unknown"}, {"HLG", "DV"}, {"x", ""}, {"", "x"}, {"hdr 10", ""}, {"ＨＤＲ", ""},
		{"SDR", ""}, {"HDR10\x00", ""}, {"Kelvin", "SDR"}, {"sdr", "sdr\n"},
	} {
		w.Add("FuzzClassifyDynamicRange", "pair", c[0], c[1])
	}
	for _, r := range []string{
		"3840x2160", "1920x1080", "1280x720", "4096x2160", "2160x3840", "3000x1", "2999x2999", " 3840x2160 ", "3840X2160", "3840 x 2160",
		"", "x", "0x0", "99999999999999999999x1", "1x99999999999999999999", "-3840x2160", "3840x", "3840", "٣٨٤٠x٢١٦٠", "03840x00002160",
		strings.Repeat("9", 300) + "x1", "3840x2160\n", "3840x2160x24",
	} {
		w.Add("FuzzTierOf", "res", r)
	}
	for _, c := range []string{"x264", "H264", " avc ", "x265", "HEVC", "h265", "vc1", "xvid", "mpeg2", "mpeg4", "", "h.264", "AV1", "x264\x00", "hevc\n", "ｈｅｖｃ"} {
		w.Add("FuzzClassifyCodec", "codec", c)
	}
	for _, c := range []string{
		`{"codecPreferences":{"hd":"h264","uhd":"hevc"}}`, `{"codecPreferences":{"hd":"hevc","uhd":"h264"}}`, `{}`, ``, `null`, `[]`,
		`{"codecPreferences":null}`, `{"codecPreferences":{"hd":"av1","uhd":"x"}}`, `{"codecPreferences":{"hd":5}}`, `{"codecPreferences":"x"}`,
		`{"codecPreferences":{"hd":"hevc"}`, "\xff\xfe", `{"codecPreferences":{"hd":"hevc","hd":"nope"}}`, `{"CodecPreferences":{"HD":"hevc","UHD":"h264"}}`,
		`{"codecPreferences":` + strings.Repeat(`{"a":`, 300) + `1` + strings.Repeat("}", 300) + `}`,
	} {
		w.Add("FuzzSettingsFile", "file", []byte(c))
	}
}
