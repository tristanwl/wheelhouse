package converter

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFfmpegAndFfprobeOnlyReadLocalFilesAndNeverStdin(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	q := newQueue(t, root, ft, nil)
	wait(t, q, q.Enqueue(request(file)).ID, StatusSuccess)

	args := ft.recordedArgs(t)
	i := slices.Index(args, "-i")
	if i < 2 || args[i-2] != "-protocol_whitelist" || args[i-1] != "file" || !slices.Contains(args[:i], "-nostdin") {
		t.Errorf("ffmpeg arguments: %v", args)
	}
	raw, err := os.ReadFile(ft.probeLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("ffprobe calls: %q", lines)
	}
	for _, l := range lines {
		if !strings.Contains(l, "-protocol_whitelist file ") {
			t.Errorf("ffprobe without a protocol whitelist: %q", l)
		}
	}
}

func TestEveryBackendArgsHaveTheWhitelistBeforeTheInput(t *testing.T) {
	for _, b := range []Backend{BackendSoftware, BackendVAAPI, BackendNVENC} {
		args := Args(b, FamilyHEVC, "/m/a.mkv", "/m/t.mkv")
		i := slices.Index(args, "-i")
		if i < 2 || args[i-2] != "-protocol_whitelist" || args[i-1] != "file" || !slices.Contains(args, "-nostdin") {
			t.Errorf("%s: %v", b, args)
		}
	}
}

func TestHungDurationProbeTimesOut(t *testing.T) {
	old := hdrProbeTimeout
	hdrProbeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { hdrProbeTimeout = old })
	root, file := library(t, "a.mkv", "original")
	q := newQueue(t, root, newFakeTools(t, "ok", "exec sleep 30"), nil)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
	if !strings.Contains(job.Error, "duration") {
		t.Errorf("%+v", job)
	}
}
