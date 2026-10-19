package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"
)

// Backend is how a file is encoded.
type Backend string

// The backends.
const (
	BackendSoftware Backend = "software"
	BackendVAAPI    Backend = "vaapi"
	BackendNVENC    Backend = "nvenc"
)

// Valid reports whether b is one of the backends.
func (b Backend) Valid() bool {
	return b == BackendSoftware || b == BackendVAAPI || b == BackendNVENC
}

// Family is the codec family to convert to.
type Family string

// The families.
const (
	FamilyH264 Family = "h264"
	FamilyHEVC Family = "hevc"
)

// Valid reports whether f is one of the families.
func (f Family) Valid() bool { return f == FamilyH264 || f == FamilyHEVC }

// EncoderOption is one backend and family and whether it works here.
type EncoderOption struct {
	Backend   Backend `json:"backend"`
	Family    Family  `json:"family"`
	Label     string  `json:"label"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
}

// vaapiDevice is the render node VAAPI encodes use. The only device the
// original was tested against is an Intel iGPU; VAAPI also covers AMD GPUs.
const vaapiDevice = "/dev/dri/renderD128"

// probeTimeout bounds one throwaway encode, so a wedged driver cannot hang the
// route forever.
const probeTimeout = 60 * time.Second

func vaapiEncoder(f Family) string {
	if f == FamilyH264 {
		return "h264_vaapi"
	}
	return "hevc_vaapi"
}

func nvencEncoder(f Family) string {
	if f == FamilyH264 {
		return "h264_nvenc"
	}
	return "hevc_nvenc"
}

func familyName(f Family) string {
	if f == FamilyH264 {
		return "H.264"
	}
	return "HEVC"
}

// tail returns the last n bytes of s, without splitting a UTF-8 character.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// trimSpace trims ASCII whitespace the way String.prototype.trim does for the
// output ffmpeg produces.
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' }

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// run runs ffmpeg with args and reports its exit code (-1 when it could not
// start) and the tail of its stderr.
func run(ctx context.Context, bin string, args []string) (code int, stderr string) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out := &tailWriter{max: 4096}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed arguments; bin is the configured ffmpeg
	cmd.Stderr = out
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if err == nil {
		return 0, out.String()
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), out.String()
	}
	return -1, "failed to spawn " + bin
}

// Detector finds out which encoders work on this host. Every hardware option
// is confirmed with a real one-second throwaway encode rather than inferred
// from a device node or `ffmpeg -encoders`, because both can be true while the
// driver, the container permissions or the GPU generation make it fail. The
// result is cached for the process: what a GPU can do does not change while the
// server runs.
type Detector struct {
	// Ffmpeg is the ffmpeg binary (default "ffmpeg", found on PATH).
	Ffmpeg string
	// Device reports whether the VAAPI render node exists (default: os.Stat).
	Device func(path string) bool

	once   sync.Once
	result []EncoderOption
}

func (d *Detector) ffmpeg() string {
	if d.Ffmpeg == "" {
		return "ffmpeg"
	}
	return d.Ffmpeg
}

func (d *Detector) deviceExists(path string) bool {
	if d.Device != nil {
		return d.Device(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

func (d *Detector) probeVAAPI(ctx context.Context, f Family) EncoderOption {
	o := EncoderOption{Backend: BackendVAAPI, Family: f, Label: fmt.Sprintf("VAAPI (%s)", familyName(f))}
	if !d.deviceExists(vaapiDevice) {
		o.Reason = vaapiDevice + " not present in this container"
		return o
	}
	code, stderr := run(ctx, d.ffmpeg(), []string{
		"-y", "-hide_banner",
		"-vaapi_device", vaapiDevice,
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=5",
		"-vf", "format=nv12,hwupload",
		"-c:v", vaapiEncoder(f),
		"-f", "null", "-",
	})
	return finish(o, code, stderr)
}

// NVENC needs no device path: it picks the first CUDA-capable NVIDIA GPU the
// ffmpeg build can see.
func (d *Detector) probeNVENC(ctx context.Context, f Family) EncoderOption {
	o := EncoderOption{Backend: BackendNVENC, Family: f, Label: fmt.Sprintf("NVIDIA NVENC (%s)", familyName(f))}
	code, stderr := run(ctx, d.ffmpeg(), []string{
		"-y", "-hide_banner",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=5",
		"-c:v", nvencEncoder(f),
		"-f", "null", "-",
	})
	return finish(o, code, stderr)
}

func finish(o EncoderOption, code int, stderr string) EncoderOption {
	if code == 0 {
		o.Available = true
		return o
	}
	o.Reason = tail(trimSpace(stderr), 300)
	if o.Reason == "" {
		o.Reason = fmt.Sprintf("ffmpeg exited %d", code)
	}
	return o
}

// probeSoftware confirms the software encoder with a real one-second encode as
// well: without ffmpeg (or with a build that lacks libx264 or libx265) it is
// not available, which keeps this list consistent with /api/features marking
// Codecs unavailable when ffmpeg is missing.
func (d *Detector) probeSoftware(ctx context.Context, f Family) EncoderOption {
	label, enc := "Software (x265)", "libx265"
	if f == FamilyH264 {
		label, enc = "Software (x264)", "libx264"
	}
	o := EncoderOption{Backend: BackendSoftware, Family: f, Label: label}
	code, stderr := run(ctx, d.ffmpeg(), []string{
		"-y", "-hide_banner",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=5",
		"-c:v", enc, "-preset", "ultrafast",
		"-f", "null", "-",
	})
	return finish(o, code, stderr)
}

// Detect returns the encoders in a fixed order: software first, then VAAPI and
// NVENC for each family. Every entry is confirmed by a real probe. The first call probes; later calls return the cached
// result. The probes are not tied to ctx, so a client that gives up does not
// cache a half-finished result; it only stops waiting.
func (d *Detector) Detect(ctx context.Context) []EncoderOption {
	d.once.Do(func() {
		bg := context.WithoutCancel(ctx)
		families := []Family{FamilyH264, FamilyHEVC}
		hw := make([]EncoderOption, 4)
		sw := make([]EncoderOption, 2)
		var wg sync.WaitGroup
		for i, f := range families {
			wg.Go(func() { sw[i] = d.probeSoftware(bg, f) })
			wg.Go(func() { hw[i] = d.probeVAAPI(bg, f) })
			wg.Go(func() { hw[2+i] = d.probeNVENC(bg, f) })
		}
		wg.Wait()
		// Same order as the Next app: vaapi h264, vaapi hevc, nvenc h264, nvenc hevc.
		d.result = append(sw, hw...)
	})
	return d.result
}

// Args builds the ffmpeg arguments for a conversion. filePath and tempPath
// must be absolute paths (so neither can be read as an option); backend and
// family must be valid, which Queue.Enqueue guarantees.
//
// -qp / -cq are each backend's constant-quality knob: none is an exact
// equivalent of libx264/libx265's CRF scale, but each is the usual default for
// a comparable-quality encode there. Audio, subtitles and attachments are
// copied untouched, and the output is forced to 8-bit (yuv420p) because the
// point of the tool is playback compatibility.
func Args(backend Backend, family Family, filePath, tempPath string) []string {
	switch backend {
	case BackendVAAPI:
		return []string{
			"-y", "-vaapi_device", vaapiDevice,
			"-i", filePath,
			"-map", "0",
			"-vf", "format=nv12,hwupload",
			"-c:v", vaapiEncoder(family),
			"-qp", "20",
			"-c:a", "copy", "-c:s", "copy",
			"-progress", "pipe:1", "-nostats",
			tempPath,
		}
	case BackendNVENC:
		return []string{
			"-y",
			"-i", filePath,
			"-map", "0",
			"-c:v", nvencEncoder(family),
			"-cq", "20", "-preset", "p5",
			"-pix_fmt", "yuv420p",
			"-c:a", "copy", "-c:s", "copy",
			"-progress", "pipe:1", "-nostats",
			tempPath,
		}
	default:
		// -map 0 -c copy pulls every stream through untouched; -c:v overrides
		// just the video stream to re-encode.
		enc := "libx265"
		if family == FamilyH264 {
			enc = "libx264"
		}
		return []string{
			"-y",
			"-i", filePath,
			"-map", "0",
			"-c", "copy",
			"-c:v", enc,
			"-preset", "medium", "-crf", "20",
			"-pix_fmt", "yuv420p",
			"-progress", "pipe:1", "-nostats",
			tempPath,
		}
	}
}
