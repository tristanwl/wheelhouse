package converter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestArgsPerBackend(t *testing.T) {
	const in, out = "/movies/a.mkv", "/movies/.wheelhouse-convert-x.mkv"
	progress := []string{"-progress", "pipe:1", "-nostats", out}
	cases := []struct {
		backend Backend
		family  Family
		want    []string
	}{
		{BackendSoftware, FamilyH264, append([]string{"-y", "-i", in, "-map", "0", "-c", "copy", "-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p"}, progress...)},
		{BackendSoftware, FamilyHEVC, append([]string{"-y", "-i", in, "-map", "0", "-c", "copy", "-c:v", "libx265", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p"}, progress...)},
		{BackendVAAPI, FamilyH264, append([]string{"-y", "-vaapi_device", "/dev/dri/renderD128", "-i", in, "-map", "0", "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-qp", "20", "-c:a", "copy", "-c:s", "copy"}, progress...)},
		{BackendVAAPI, FamilyHEVC, append([]string{"-y", "-vaapi_device", "/dev/dri/renderD128", "-i", in, "-map", "0", "-vf", "format=nv12,hwupload", "-c:v", "hevc_vaapi", "-qp", "20", "-c:a", "copy", "-c:s", "copy"}, progress...)},
		{BackendNVENC, FamilyH264, append([]string{"-y", "-i", in, "-map", "0", "-c:v", "h264_nvenc", "-cq", "20", "-preset", "p5", "-pix_fmt", "yuv420p", "-c:a", "copy", "-c:s", "copy"}, progress...)},
		{BackendNVENC, FamilyHEVC, append([]string{"-y", "-i", in, "-map", "0", "-c:v", "hevc_nvenc", "-cq", "20", "-preset", "p5", "-pix_fmt", "yuv420p", "-c:a", "copy", "-c:s", "copy"}, progress...)},
	}
	for _, c := range cases {
		got := Args(c.backend, c.family, in, out)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s/%s:\n got %q\nwant %q", c.backend, c.family, got, c.want)
		}
		// Audio and subtitles are never re-encoded; software copies everything
		// but video, the others copy those streams explicitly.
		joined := strings.Join(got, " ")
		if c.backend != BackendSoftware && !strings.Contains(joined, "-c:a copy -c:s copy") {
			t.Errorf("%s: audio/subs not copied", c.backend)
		}
	}
}

func TestEnumsRejectAnythingElse(t *testing.T) {
	for _, b := range []Backend{"", "quicksync", "Software", "software ", "-y"} {
		if b.Valid() {
			t.Errorf("backend %q accepted", b)
		}
	}
	for _, f := range []Family{"", "av1", "HEVC", "h265"} {
		if f.Valid() {
			t.Errorf("family %q accepted", f)
		}
	}
}

func TestDetectorProbesOnceWithRealExitCodes(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	// Succeeds only for the h264 encoders; the failures say why on stderr.
	script := "#!/bin/sh\necho x >> '" + count + "'\ncase \"$*\" in\n*h264_*|*libx26*) exit 0;;\n*) echo \"Unknown encoder 'hevc'\" >&2; exit 1;;\nesac\n"
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Detector{Ffmpeg: ffmpeg, Device: func(string) bool { return true }}
	got := d.Detect(context.Background())

	type key struct {
		b Backend
		f Family
	}
	var order []key
	byKey := map[key]EncoderOption{}
	for _, o := range got {
		order = append(order, key{o.Backend, o.Family})
		byKey[key{o.Backend, o.Family}] = o
	}
	wantOrder := []key{
		{BackendSoftware, FamilyH264}, {BackendSoftware, FamilyHEVC},
		{BackendVAAPI, FamilyH264}, {BackendVAAPI, FamilyHEVC},
		{BackendNVENC, FamilyH264}, {BackendNVENC, FamilyHEVC},
	}
	if !slices.Equal(order, wantOrder) {
		t.Fatalf("order: %v", order)
	}
	for k, avail := range map[key]bool{
		{BackendSoftware, FamilyH264}: true, {BackendSoftware, FamilyHEVC}: true,
		{BackendVAAPI, FamilyH264}: true, {BackendVAAPI, FamilyHEVC}: false,
		{BackendNVENC, FamilyH264}: true, {BackendNVENC, FamilyHEVC}: false,
	} {
		o := byKey[k]
		if o.Available != avail || (avail && o.Reason != "") || (!avail && !strings.Contains(o.Reason, "Unknown encoder")) {
			t.Errorf("%v: %+v", k, o)
		}
	}
	if o := byKey[key{BackendVAAPI, FamilyHEVC}]; o.Label != "VAAPI (HEVC)" {
		t.Errorf("label %q", o.Label)
	}
	if o := byKey[key{BackendNVENC, FamilyH264}]; o.Label != "NVIDIA NVENC (H.264)" {
		t.Errorf("label %q", o.Label)
	}
	if o := byKey[key{BackendSoftware, FamilyHEVC}]; o.Label != "Software (x265)" {
		t.Errorf("label %q", o.Label)
	}

	// Cached: a second call runs nothing. (6 probes ran the first time.)
	before, _ := os.ReadFile(count)
	d.Detect(context.Background())
	after, _ := os.ReadFile(count)
	if strings.Count(string(before), "x") != 6 || string(before) != string(after) {
		t.Errorf("probes: first %d, then %d", strings.Count(string(before), "x"), strings.Count(string(after), "x"))
	}
}

func TestDetectorWithoutDeviceOrFfmpeg(t *testing.T) {
	d := &Detector{Ffmpeg: "/nonexistent/ffmpeg", Device: func(string) bool { return false }}
	for _, o := range d.Detect(context.Background()) {
		switch o.Backend {
		case BackendSoftware:
			if o.Available || o.Reason != "failed to spawn /nonexistent/ffmpeg" {
				t.Errorf("software without ffmpeg must not be available: %+v", o)
			}
		case BackendVAAPI:
			if o.Available || o.Reason != "/dev/dri/renderD128 not present in this container" {
				t.Errorf("vaapi without a device: %+v", o)
			}
		case BackendNVENC:
			if o.Available || o.Reason != "failed to spawn /nonexistent/ffmpeg" {
				t.Errorf("nvenc without ffmpeg: %+v", o)
			}
		}
	}
}

func TestRealFfmpegConversion(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed on this machine; the fake-tool tests cover the queue")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed on this machine")
	}
	enc, _ := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output()
	if !strings.Contains(string(enc), "libx264") || !strings.Contains(string(enc), "mpeg4") {
		t.Skip("this ffmpeg build lacks libx264 or mpeg4")
	}
	root, file := library(t, "Example Movie 1 (2025).mkv", "")
	gen := exec.Command(ffmpeg, "-y", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=128x96:rate=10",
		"-f", "lavfi", "-i", "sine=duration=2", "-c:v", "mpeg4", "-c:a", "aac", "-shortest", file)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not generate a sample video: %v: %s", err, out)
	}
	original, _ := os.ReadFile(file)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q := NewMemoryQueue(ctx, Config{MediaRoots: []string{root}, Ffmpeg: ffmpeg, Ffprobe: ffprobe})
	job := q.Enqueue(Request{InstanceID: "radarr", ItemID: 1, FilePath: file, Title: "Example Movie 1", OriginalSize: float64(len(original)),
		Backend: BackendSoftware, TargetFamily: FamilyH264, ResolvedPath: file})
	done := wait(t, q, job.ID, StatusSuccess)

	if bak, _ := os.ReadFile(file + ".bak"); string(bak) != string(original) {
		t.Error(".bak is not the original")
	}
	probe, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_name,pix_fmt", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(probe), "h264,yuv420p") || !strings.Contains(string(probe), "aac") {
		t.Errorf("converted streams: %s\n%s", probe, done.Log)
	}
}
