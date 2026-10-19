package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/codecs"
)

// Conversion writes SDR, 8-bit video (-pix_fmt yuv420p, see Args). It does not
// tone map, and it cannot carry HDR10 static metadata, HLG signalling or the
// Dolby Vision RPU through a re-encode to that format. So an HDR or Dolby Vision
// source loses its HDR on conversion, and the picture comes out flattened
// (grey, washed out). The policy (issue #32):
//
//   - such files are not conversion candidates unless an administrator turns on
//     "include HDR" in the converter settings;
//   - even then, a conversion of one needs an explicit acknowledgement that the
//     result is SDR 8-bit and the HDR / Dolby Vision metadata is lost;
//   - the server decides from the file itself (ProbeHDR), not from what the
//     caller or Radarr and Sonarr said, and checks again before running a job.
//
// Tone mapping is out of scope.

// hdrProbeTimeout bounds one ffprobe run for classification.
var hdrProbeTimeout = 30 * time.Second // a variable only so a test can shorten it

// ProbeHDR classifies the first video stream of the file with ffprobe. Anything
// that cannot be read or understood is HDRUnknown, never HDRNone: an answer the
// server could not get must not let an HDR file through.
func ProbeHDR(ctx context.Context, ffprobe, path string) codecs.HDR {
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	ctx, cancel := context.WithTimeout(ctx, hdrProbeTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, ffprobe, //nolint:gosec // the path is absolute and resolved inside the media roots
		"-v", "error", "-protocol_whitelist", "file", "-select_streams", "v:0",
		"-show_entries", "stream=color_transfer,color_primaries:stream_side_data=side_data_type",
		"-of", "json", path)
	cmd.Stdout = &limitWriter{w: &out, n: 1 << 20}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return codecs.HDRUnknown
	}
	return ClassifyProbe(out.Bytes())
}

type limitWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		p = p[:l.n]
	}
	l.n -= len(p)
	l.w.Write(p)
	return len(p), nil // never fail the process; ClassifyProbe rejects a cut-off document
}

// ClassifyProbe reads ffprobe's JSON for one video stream: Dolby Vision side
// data is "dolby-vision"; the PQ transfer (smpte2084) is "hdr10"; the HLG
// transfer (arib-std-b67) is "hlg"; wide-gamut BT.2020 primaries with any other
// transfer are "unknown" (they may be an HDR file with its signalling stripped,
// and conversion would not preserve the gamut either); no colour signalling or
// a plain SDR one is "none". Output that is not the expected document is
// "unknown".
func ClassifyProbe(raw []byte) codecs.HDR {
	var doc struct {
		Streams []struct {
			ColorTransfer  string `json:"color_transfer"`
			ColorPrimaries string `json:"color_primaries"`
			SideData       []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Streams) == 0 {
		return codecs.HDRUnknown
	}
	s := doc.Streams[0]
	for _, sd := range s.SideData {
		t := strings.ToLower(sd.Type)
		if strings.Contains(t, "dovi") || strings.Contains(t, "dolby vision") {
			return codecs.HDRDolbyVision
		}
	}
	switch strings.ToLower(s.ColorTransfer) {
	case "smpte2084":
		return codecs.HDRHDR10
	case "arib-std-b67":
		return codecs.HDRHLG
	}
	if strings.HasPrefix(strings.ToLower(s.ColorPrimaries), "bt2020") {
		return codecs.HDRUnknown
	}
	return codecs.HDRNone
}
