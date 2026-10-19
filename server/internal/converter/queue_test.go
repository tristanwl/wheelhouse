package converter

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type rescans struct {
	mu    sync.Mutex
	calls []string
}

func (r *rescans) fn(_ context.Context, instance string, item int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, instance+"/"+strconv.Itoa(item))
	return nil
}

func (r *rescans) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func newQueue(t *testing.T, root string, ft fakeTools, rs *rescans) *JobQueue {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := Config{MediaRoots: []string{root}, Ffmpeg: ft.ffmpeg, Ffprobe: ft.ffprobe}
	if rs != nil {
		cfg.Rescan = rs.fn
	}
	return NewMemoryQueue(ctx, cfg)
}

func request(file string) Request {
	return Request{
		InstanceID: "radarr", ItemID: 4, FilePath: file, Title: "Example Movie 1",
		OriginalSize: 8, Backend: BackendSoftware, TargetFamily: FamilyHEVC, ResolvedPath: file,
	}
}

func wait(t *testing.T, q Queue, id string, want Status) Job {
	t.Helper()
	var job Job
	eventually(t, "job "+string(want), func() bool {
		job, _ = q.Get(id)
		return job.Status == want
	})
	return job
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConversionSucceedsAndKeepsTheOriginalAsBak(t *testing.T) {
	root, file := library(t, "Example Movie 1 (2025).mkv", "original")
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	rs := &rescans{}
	q := newQueue(t, root, ft, rs)

	queued := q.Enqueue(request(file))
	if queued.Status != StatusQueued || queued.ProgressPercent != 0 || queued.ID == "" || queued.Log != "" {
		t.Fatalf("queued job: %+v", queued)
	}
	job := wait(t, q, queued.ID, StatusSuccess)

	// The swap: converted content in place, the original beside it as .bak.
	if got := read(t, file); got != "converted" {
		t.Errorf("file content %q", got)
	}
	if got := read(t, file+".bak"); got != "original" {
		t.Errorf(".bak content %q", got)
	}
	if job.BackupPath != file+".bak" || job.NewSize != int64(len("converted")) || job.ProgressPercent != 100 {
		t.Errorf("job: %+v", job)
	}
	if job.StartedAt == 0 || job.FinishedAt < job.StartedAt || job.CreatedAt == 0 || job.Error != "" {
		t.Errorf("times: %+v", job)
	}
	// No temp file is left behind, and the rescan was asked for.
	names := leftovers(t, root)
	slices.Sort(names)
	if want := []string{"Example Movie 1 (2025).mkv", "Example Movie 1 (2025).mkv.bak"}; !slices.Equal(names, want) {
		t.Errorf("directory: %v", names)
	}
	if got := rs.got(); !slices.Equal(got, []string{"radarr/4"}) {
		t.Errorf("rescans: %v", got)
	}
	// The log shows the command and the tool's own output.
	for _, want := range []string{"Probing " + file, "Target: hevc via software", "Duration: 2.0s", "$ ffmpeg -nostdin -y -protocol_whitelist file -i " + file, "frame=1", "Swapped in converted file; original kept at " + file + ".bak"} {
		if !strings.Contains(job.Log, want) {
			t.Errorf("log lacks %q:\n%s", want, job.Log)
		}
	}
}

func TestConversionArgumentsForcePixFmtAndCopyStreams(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	q := newQueue(t, root, ft, nil)
	job := q.Enqueue(request(file))
	wait(t, q, job.ID, StatusSuccess)

	args := ft.recordedArgs(t)
	temp := args[len(args)-1]
	if filepath.Dir(temp) != root || !strings.HasPrefix(filepath.Base(temp), TempPrefix+job.ID) || filepath.Ext(temp) != ".mkv" {
		t.Errorf("temp output %q: want a dotfile with the job id next to the original", temp)
	}
	want := []string{"-nostdin", "-y", "-protocol_whitelist", "file", "-i", file, "-map", "0", "-c", "copy", "-c:v", "libx265", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p", "-progress", "pipe:1", "-nostats", temp}
	if !slices.Equal(args, want) {
		t.Errorf("args:\n got %q\nwant %q", args, want)
	}
}

func TestProgressFollowsFfmpegOutput(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	ft := newFakeTools(t, "slow", probeTwoSeconds)
	q := newQueue(t, root, ft, nil)
	job := q.Enqueue(request(file))
	// out_time_ms=1000000 of a 2 s file is 50%, and 100 is never shown before the swap.
	got := Job{}
	eventually(t, "50% progress", func() bool {
		got, _ = q.Get(job.ID)
		return got.Status == StatusRunning && got.ProgressPercent == 50
	})
	q.Cancel(job.ID)
	wait(t, q, job.ID, StatusCanceled)
}

func TestFailedConversionLeavesTheOriginalAndCleansUp(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	ft := newFakeTools(t, "fail", probeTwoSeconds)
	rs := &rescans{}
	q := newQueue(t, root, ft, rs)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)

	if job.Error != "ffmpeg exited with code 3" {
		t.Errorf("error %q", job.Error)
	}
	if !strings.Contains(job.Log, "Error while opening encoder") || !strings.Contains(job.Log, "Failed: ffmpeg exited with code 3") {
		t.Errorf("log:\n%s", job.Log)
	}
	if got := read(t, file); got != "original" {
		t.Errorf("original touched: %q", got)
	}
	eventually(t, "temp file removed", func() bool { return len(leftovers(t, root)) == 1 })
	if len(rs.got()) != 0 || job.BackupPath != "" || job.NewSize != 0 {
		t.Errorf("a failed job must not rescan or report a backup: %+v %v", job, rs.got())
	}
}

func TestEmptyOrMissingOutputNeverReplacesTheOriginal(t *testing.T) {
	for mode, wantErr := range map[string]string{"empty": "Converted file came out empty", "nooutput": "The converted file is missing"} {
		t.Run(mode, func(t *testing.T) {
			root, file := library(t, "a.mkv", "original")
			q := newQueue(t, root, newFakeTools(t, mode, probeTwoSeconds), nil)
			job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
			if job.Error != wantErr {
				t.Errorf("error %q, want %q", job.Error, wantErr)
			}
			if got := read(t, file); got != "original" {
				t.Errorf("original replaced: %q", got)
			}
			eventually(t, "no leftovers", func() bool { return len(leftovers(t, root)) == 1 })
		})
	}
}

func TestUnreadableDurationFailsBeforeEncoding(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	ft := newFakeTools(t, "ok", probeBroken)
	q := newQueue(t, root, ft, nil)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
	if job.Error != "ffprobe couldn't read the file's duration" {
		t.Errorf("error %q", job.Error)
	}
	if _, err := os.Stat(ft.argsFile); err == nil {
		t.Error("ffmpeg ran although the probe failed")
	}
}

func TestMissingToolsFailTheJobNotTheServer(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q := NewMemoryQueue(ctx, Config{MediaRoots: []string{root}, Ffmpeg: "/nonexistent/ffmpeg", Ffprobe: "/nonexistent/ffprobe"})
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
	if job.Error == "" || read(t, file) != "original" {
		t.Errorf("%+v", job)
	}
}

func TestCancelRunningJobKillsFfmpegAndCleansUp(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	q := newQueue(t, root, newFakeTools(t, "slow", probeTwoSeconds), nil)
	job := q.Enqueue(request(file))
	wait(t, q, job.ID, StatusRunning)
	eventually(t, "the output file", func() bool { return len(leftovers(t, root)) == 2 })

	if !q.Cancel(job.ID) {
		t.Fatal("a running job can be canceled")
	}
	done := wait(t, q, job.ID, StatusCanceled)
	if done.FinishedAt == 0 || !strings.Contains(done.Log, "Canceled.") || done.Error != "" {
		t.Errorf("canceled job: %+v", done)
	}
	if got := read(t, file); got != "original" {
		t.Errorf("original touched: %q", got)
	}
	eventually(t, "temp file removed", func() bool { return len(leftovers(t, root)) == 1 })
	if q.Cancel(job.ID) {
		t.Error("a finished job cannot be canceled")
	}
	if q.Cancel("nope") {
		t.Error("an unknown job cannot be canceled")
	}
}

func TestQueuedJobsRunOneAtATimeAndAQueuedOneCanBeCanceled(t *testing.T) {
	root, first := library(t, "a.mkv", "one")
	second := filepath.Join(root, "b.mkv")
	third := filepath.Join(root, "c.mkv")
	for _, p := range []string{second, third} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	q := newQueue(t, root, newFakeTools(t, "slow", probeTwoSeconds), nil)
	j1 := q.Enqueue(request(first))
	j2 := q.Enqueue(request(second))
	j3 := q.Enqueue(request(third))
	wait(t, q, j1.ID, StatusRunning)
	if got, _ := q.Get(j2.ID); got.Status != StatusQueued {
		t.Errorf("the second job must wait: %s", got.Status)
	}
	if !q.Cancel(j2.ID) {
		t.Fatal("a queued job can be canceled")
	}
	if got, _ := q.Get(j2.ID); got.Status != StatusCanceled || got.FinishedAt == 0 || got.StartedAt != 0 {
		t.Errorf("canceled queued job: %+v", got)
	}
	// Finishing the first moves on to the third, skipping the canceled one.
	q.Cancel(j1.ID)
	wait(t, q, j1.ID, StatusCanceled)
	wait(t, q, j3.ID, StatusRunning)
	q.Cancel(j3.ID)
	wait(t, q, j3.ID, StatusCanceled)
	if ids := jobIDs(q.List()); !slices.Equal(ids, []string{j1.ID, j2.ID, j3.ID}) {
		t.Errorf("order: %v", ids)
	}
}

func jobIDs(jobs []Job) []string {
	var ids []string
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func TestAFileWithAnActiveJobReturnsThatJob(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	q := newQueue(t, root, newFakeTools(t, "slow", probeTwoSeconds), nil)
	first := q.Enqueue(request(file))
	again := q.Enqueue(request(file))
	if again.ID != first.ID || len(q.List()) != 1 {
		t.Fatalf("a second job for the same file: %+v", q.List())
	}
	if active, ok := q.FindActive(file); !ok || active.ID != first.ID {
		t.Errorf("FindActive: %+v %v", active, ok)
	}
	q.Cancel(first.ID)
	wait(t, q, first.ID, StatusCanceled)
	if _, ok := q.FindActive(file); ok {
		t.Error("a canceled job is not active")
	}
	// Once it is over the file can be queued again, as a new job.
	if next := q.Enqueue(request(file)); next.ID == first.ID {
		t.Error("a finished job must not be reused")
		q.Cancel(next.ID)
	} else {
		q.Cancel(next.ID)
	}
}

func TestPathIsCheckedAgainWhenTheJobRuns(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	outside, secret := library(t, "secret.mkv", "secret")

	// Queue behind a running job, then swap the file for a symlink out of the
	// root before the queued one starts.
	blocker := filepath.Join(root, "blocker.mkv")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	slow := newFakeTools(t, "slow", probeTwoSeconds)
	slowQueue := newQueue(t, root, slow, nil)
	b := slowQueue.Enqueue(request(blocker))
	wait(t, slowQueue, b.ID, StatusRunning)
	victim := slowQueue.Enqueue(request(file))

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, file); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	slowQueue.Cancel(b.ID)
	job := wait(t, slowQueue, victim.ID, StatusFailed)
	if !strings.Contains(job.Error, "no longer available inside the media folders") {
		t.Errorf("error %q", job.Error)
	}
	if got := read(t, secret); got != "secret" {
		t.Errorf("the file outside the root was touched: %q", got)
	}
	if names := leftovers(t, outside); len(names) != 1 {
		t.Errorf("files appeared outside the root: %v", names)
	}
}

func TestFileSwappedForASymlinkDuringEncodingIsNotReplaced(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	_, secret := library(t, "secret.mkv", "secret")
	// While "encoding", the library file is replaced by a link out of the root.
	body := "printf converted > \"$out\"; rm -f '" + file + "'; ln -s '" + secret + "' '" + file + "'"
	q := newQueue(t, root, newFakeTools(t, "raw:"+body, probeTwoSeconds), nil)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
	if !strings.Contains(job.Error, "no longer available inside the media folders") || job.BackupPath != "" {
		t.Errorf("%+v", job)
	}
	if got := read(t, secret); got != "secret" {
		t.Errorf("the file outside the root was touched: %q", got)
	}
	if _, err := os.Stat(file + ".bak"); err == nil {
		t.Error("a backup was made of a path that left the root")
	}
	eventually(t, "temp file removed", func() bool { return len(leftovers(t, root)) == 1 })
}

func TestStartupCleanupRestoresAndRemoves(t *testing.T) {
	root, _ := library(t, "keep.mkv", "keep")
	// A normal finished conversion: file plus .bak, both left alone.
	mustWrite(t, filepath.Join(root, "done.mkv"), "new")
	mustWrite(t, filepath.Join(root, "done.mkv.bak"), "old")
	// A stray .bak with no file beside it: Wheelhouse did not record it, so it
	// must not be resurrected.
	mustWrite(t, filepath.Join(root, "stranded.mkv.bak"), "real content")
	// An interrupted encode's partial output, in a subfolder.
	sub := filepath.Join(root, "Show", "Season 1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(sub, TempPrefix+"abc.mkv"), "partial")
	mustWrite(t, filepath.Join(sub, "ep.mkv"), "ep")
	// Symlinks are never followed or renamed.
	outside, secret := library(t, "secret.mkv.bak", "secret")
	if err := os.Symlink(secret, filepath.Join(root, "link.mkv.bak")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}

	CleanupOrphans([]string{root}, slog.New(slog.DiscardHandler))

	if _, err := os.Lstat(filepath.Join(root, "stranded.mkv")); err == nil {
		t.Error("a stray .bak was resurrected")
	}
	if got := read(t, filepath.Join(root, "stranded.mkv.bak")); got != "real content" {
		t.Errorf("the stray .bak was touched: %q", got)
	}
	if got := read(t, filepath.Join(root, "done.mkv.bak")); got != "old" {
		t.Errorf("a normal .bak must stay: %q", got)
	}
	if got := read(t, filepath.Join(root, "done.mkv")); got != "new" {
		t.Errorf("done.mkv: %q", got)
	}
	if _, err := os.Stat(filepath.Join(sub, TempPrefix+"abc.mkv")); err == nil {
		t.Error("orphaned temp file not removed")
	}
	if read(t, filepath.Join(sub, "ep.mkv")) != "ep" {
		t.Error("an unrelated file was touched")
	}
	if _, err := os.Lstat(filepath.Join(root, "link.mkv")); err == nil {
		t.Error("a symlinked .bak was renamed")
	}
	if read(t, secret) != "secret" {
		t.Error("a file outside the root was touched")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLogKeepsTheTail(t *testing.T) {
	long := strings.Repeat("é", 5000) // 10000 bytes
	got := tail(long, maxLogBytes)
	if len(got) > maxLogBytes || len(got) < maxLogBytes-1 || !strings.HasPrefix(got, "é") {
		t.Errorf("tail: %d bytes", len(got))
	}
	if tail("short", 100) != "short" {
		t.Error("short text unchanged")
	}
}

func TestConvertingTwiceKeepsEveryOriginal(t *testing.T) {
	// The reviewer's repro: the second conversion used to overwrite the first
	// run's .bak with the already converted file, destroying the original.
	root, file := library(t, "a.mkv", "original")
	q := newQueue(t, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	first := wait(t, q, q.Enqueue(request(file)).ID, StatusSuccess)
	if first.BackupPath != file+".bak" {
		t.Fatalf("first backup %q", first.BackupPath)
	}
	second := wait(t, q, q.Enqueue(request(file)).ID, StatusSuccess)
	if second.BackupPath != file+".bak.1" {
		t.Errorf("second backup %q", second.BackupPath)
	}
	if got := read(t, file+".bak"); got != "original" {
		t.Errorf("the first original was lost: %q", got)
	}
	if got := read(t, file+".bak.1"); got != "converted" {
		t.Errorf(".bak.1: %q", got)
	}
	if got := read(t, file); got != "converted" {
		t.Errorf("file: %q", got)
	}
}

func TestExistingBackupDirectoryIsSkipped(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	if err := os.Mkdir(file+".bak", 0o755); err != nil {
		t.Fatal(err)
	}
	q := newQueue(t, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusSuccess)
	if job.BackupPath != file+".bak.1" || read(t, file+".bak.1") != "original" {
		t.Errorf("%+v", job)
	}
}

func TestAllBackupNamesTakenRefusesTheJob(t *testing.T) {
	root, file := library(t, "a.mkv", "original")
	mustWrite(t, file+".bak", "b0")
	for i := 1; i < maxBackupNames; i++ {
		mustWrite(t, backupName(file, i), "b")
	}
	q := newQueue(t, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
	if !strings.Contains(job.Error, "Too many backups") || read(t, file) != "original" || read(t, file+".bak") != "b0" {
		t.Errorf("%+v", job)
	}
}

func TestFailureMidSwapLeavesTheOriginalIntact(t *testing.T) {
	// The "converted" output is a directory, so moving it over the file fails
	// after the backup was made.
	root, file := library(t, "a.mkv", "original")
	q := newQueue(t, root, newFakeTools(t, "raw:mkdir \"$out\"", probeTwoSeconds), nil)
	job := wait(t, q, q.Enqueue(request(file)).ID, StatusFailed)
	if job.Error != "Could not move the converted file into place" || job.BackupPath != "" {
		t.Errorf("%+v", job)
	}
	if got := read(t, file); got != "original" {
		t.Errorf("the original changed: %q", got)
	}
	if _, err := os.Lstat(file + ".bak"); err == nil {
		t.Error("a dangling backup was left behind")
	}
}

func TestRenameFallbackNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	src, bak := filepath.Join(dir, "a"), filepath.Join(dir, "a.bak")
	mustWrite(t, src, "one")
	mustWrite(t, bak, "two")
	if _, err := createBackup(src, bak); err != errBackupExists {
		t.Errorf("createBackup over an existing file: %v", err)
	}
	if err := renameNoClobber(src, bak); err == nil {
		t.Error("renameNoClobber replaced a file")
	}
	if read(t, bak) != "two" || read(t, src) != "one" {
		t.Error("contents changed")
	}
}

func TestBackupNames(t *testing.T) {
	for _, c := range []struct {
		b  string
		ok bool
	}{{"/m/a.mkv.bak", true}, {"/m/a.mkv.bak.7", true}, {"/m/a.mkv.bak.0", false}, {"/m/a.mkv.bak.07", false}, {"/m/a.mkv.bak.100", false}, {"/m/b.mkv.bak", false}, {"/x/a.mkv.bak", false}, {"/m/a.mkv.bakx", false}} {
		if isBackupNameOf("/m/a.mkv", c.b) != c.ok {
			t.Errorf("%s: want %v", c.b, c.ok)
		}
	}
}
