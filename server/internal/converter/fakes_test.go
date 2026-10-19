package converter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTools writes a fake ffmpeg and ffprobe (POSIX sh scripts) into a temp dir
// and returns their paths and the file the fake ffmpeg records its arguments in.
// The scripts print realistic -progress output and use real exit codes, so the
// queue is tested against the same process boundary as the real tools.
type fakeTools struct {
	ffmpeg, ffprobe, argsFile string
	// streamFile holds the JSON the fake ffprobe prints for the stream query
	// (-show_entries stream=...) that classifies HDR; see setStream.
	streamFile string
}

// ffmpegModes are the behaviours of the fake ffmpeg. The last argument is the
// output path, as with the real one.
var ffmpegModes = map[string]string{
	// Succeeds, writing progress for a 2 second file and a small output.
	"ok": `
echo "frame=1 fps=0.0 q=-1.0 size=0kB" >&2
echo "out_time_ms=500000"
echo "progress=continue"
echo "out_time_ms=N/A"
echo "out_time_ms=1000000"
echo "progress=continue"
printf 'converted' > "$out"
echo "out_time_ms=2000000"
echo "progress=end"
exit 0`,
	// Writes some output, complains and exits non-zero.
	"fail": `
printf 'partial' > "$out"
echo "Error while opening encoder for output stream #0:0" >&2
exit 3`,
	// Exits zero but writes nothing.
	"empty": `
: > "$out"
exit 0`,
	// Reports half the file and then waits to be killed.
	"slow": `
printf 'partial' > "$out"
echo "out_time_ms=1000000"
echo "progress=continue"
exec sleep 30`,
	// Exits zero without creating the output at all.
	"nooutput": `
exit 0`,
}

func newFakeTools(t *testing.T, mode, probe string) fakeTools {
	t.Helper()
	dir := t.TempDir()
	ft := fakeTools{
		ffmpeg:     filepath.Join(dir, "ffmpeg"),
		ffprobe:    filepath.Join(dir, "ffprobe"),
		argsFile:   filepath.Join(dir, "args.txt"),
		streamFile: filepath.Join(dir, "stream.json"),
	}
	if err := os.WriteFile(ft.streamFile, []byte(streamSDR), 0o644); err != nil {
		t.Fatal(err)
	}
	body, ok := ffmpegModes[mode]
	if raw, isRaw := strings.CutPrefix(mode, "raw:"); isRaw {
		body, ok = raw, true
	}
	if !ok {
		t.Fatalf("no fake ffmpeg mode %q", mode)
	}
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\"; done > '%s'\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\n%s\n", ft.argsFile, body)
	if err := os.WriteFile(ft.ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stream query (HDR classification) answers from stream.json; every
	// other call is the duration probe the test scripted.
	probeScript := "#!/bin/sh\ncase \"$*\" in *stream=*) cat '" + ft.streamFile + "'; exit 0;; esac\n" + probe + "\n"
	if err := os.WriteFile(ft.ffprobe, []byte(probeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return ft
}

const (
	streamSDR       = `{"streams":[{"codec_type":"video","color_transfer":"bt709","color_primaries":"bt709"}]}`
	streamHDR10     = `{"streams":[{"color_transfer":"smpte2084","color_primaries":"bt2020"}]}`
	streamDV        = `{"streams":[{"color_transfer":"smpte2084","side_data_list":[{"side_data_type":"DOVI configuration record"}]}]}`
	probeTwoSeconds = "echo 2.000000"
	probeBroken     = "echo 'moov atom not found' >&2; exit 1"
)

// setStream changes what the fake ffprobe says about the video stream.
func (f fakeTools) setStream(t *testing.T, doc string) {
	t.Helper()
	if err := os.WriteFile(f.streamFile, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordedArgs returns the arguments the fake ffmpeg was last called with.
func (f fakeTools) recordedArgs(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(f.argsFile)
	if err != nil {
		t.Fatalf("fake ffmpeg was not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// eventually polls cond until it holds or the time runs out.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// library makes a media root with a file in it and returns the root (real path)
// and the file.
func library(t *testing.T, name, content string) (root, file string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(root, name)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, file
}
