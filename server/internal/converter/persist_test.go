package converter

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/codecs"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "wheelhouse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// finishes collects OnFinish calls.
type finishes struct {
	mu   sync.Mutex
	jobs []Job
}

func (f *finishes) fn(j Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, j)
}

func (f *finishes) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, j := range f.jobs {
		out = append(out, j.ID+":"+string(j.Status))
	}
	return out
}

// row is a saved job as an earlier process could have left it.
func row(id, path string, status Status) Stored {
	return Stored{
		Job: Job{
			ID: id, InstanceID: "radarr", ItemID: 4, FilePath: path, Title: "Example Movie 1", OriginalSize: 8,
			Backend: BackendSoftware, TargetFamily: FamilyHEVC, Status: status, HDR: codecs.HDRNone,
			Owner: "user-1", OwnerName: "example-user", CreatedAt: time.Now().UnixMilli(),
		},
		Path: path,
	}
}

func saveAll(t *testing.T, db *store.DB, rows ...Stored) {
	t.Helper()
	s := NewSQLStore(db)
	for _, r := range rows {
		if err := s.Save(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
}

func startPersistent(t *testing.T, db *store.DB, root string, ft fakeTools, mut func(*Config)) (*JobQueue, *finishes) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fin := &finishes{}
	cfg := Config{MediaRoots: []string{root}, Ffmpeg: ft.ffmpeg, Ffprobe: ft.ffprobe, Store: NewSQLStore(db), OnFinish: fin.fn}
	if mut != nil {
		mut(&cfg)
	}
	return NewQueue(ctx, cfg), fin
}

func TestRestartMarksInterruptedCleansUpAndResumesQueuedJobsInOrder(t *testing.T) {
	root, fileA := library(t, "a.mkv", "original-a")
	fileB := filepath.Join(root, "b.mkv")
	fileC := filepath.Join(root, "c.mkv")
	for _, f := range []string{fileB, fileC} {
		if err := os.WriteFile(f, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// What the dead process left: A was running (partial output beside it), B and
	// C were queued, D finished earlier.
	partial := filepath.Join(root, TempPrefix+"a-job.mkv")
	if err := os.WriteFile(partial, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	done := row("job-d", fileA, StatusSuccess)
	done.FinishedAt = time.Now().UnixMilli()
	saveAll(t, db, done, row("job-a", fileA, StatusRunning), row("job-b", fileB, StatusQueued), row("job-c", fileC, StatusQueued))

	ft := newFakeTools(t, "ok", probeTwoSeconds)
	rs := &rescans{}
	q, fin := startPersistent(t, db, root, ft, func(c *Config) { c.Rescan = rs.fn })

	a, ok := q.Get("job-a")
	if !ok || a.Status != StatusFailed || a.Error != InterruptedError || a.FinishedAt == 0 {
		t.Fatalf("interrupted job: %+v", a)
	}
	wait(t, q, "job-b", StatusSuccess)
	wait(t, q, "job-c", StatusSuccess)

	// Queued jobs resumed in the order they were added; the interrupted one did not run.
	b, _ := q.Get("job-b")
	c, _ := q.Get("job-c")
	if b.StartedAt > c.StartedAt || !slices.Equal(rs.got(), []string{"radarr/4", "radarr/4"}) {
		t.Errorf("order: b started %d, c started %d, rescans %v", b.StartedAt, c.StartedAt, rs.got())
	}
	if got := read(t, fileA); got != "original-a" {
		t.Errorf("the interrupted job's file changed: %q", got)
	}
	// Its temp file was cleaned.
	if _, err := os.Stat(partial); err == nil {
		t.Error("the partial output of the interrupted job is still there")
	}
	// Finished jobs are listed too, in the order they were saved.
	var ids []string
	for _, j := range q.List() {
		ids = append(ids, j.ID)
	}
	if !slices.Equal(ids, []string{"job-d", "job-a", "job-b", "job-c"}) {
		t.Errorf("list: %v", ids)
	}
	got := fin.ids()
	if got[0] != "job-a:failed" {
		t.Errorf("OnFinish order: %v", got)
	}

	// A third process sees all of it as it was left.
	q2, _ := startPersistent(t, db, root, ft, nil)
	for id, want := range map[string]Status{"job-a": StatusFailed, "job-b": StatusSuccess, "job-c": StatusSuccess, "job-d": StatusSuccess} {
		if j, ok := q2.Get(id); !ok || j.Status != want {
			t.Errorf("%s after a second restart: %+v", id, j)
		}
	}
	if j, _ := q2.Get("job-b"); j.Owner != "user-1" || j.OwnerName != "example-user" || j.NewSize != int64(len("converted")) {
		t.Errorf("persisted fields: %+v", j)
	}
}

func swapRow(id, file, backup string) Stored {
	r := row(id, file, StatusRunning)
	r.SwapPath, r.SwapBackup = file, backup
	return r
}

func TestRecordedInterruptedSwapIsRestored(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	if err := os.Rename(file, file+".bak"); err != nil { // died between the two renames
		t.Fatal(err)
	}
	db := openDB(t)
	saveAll(t, db, swapRow("job-a", file, file+".bak"))
	q, _ := startPersistent(t, db, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	<-q.recovered
	if got := read(t, file); got != "x" {
		t.Errorf("restored content %q", got)
	}
	// The record is cleared, so a second restart does nothing more.
	rows, _ := NewSQLStore(db).Load(context.Background())
	if rows[0].SwapPath != "" || rows[0].SwapBackup != "" {
		t.Errorf("swap record not cleared: %+v", rows[0])
	}
}

func TestStrayBackupWithoutARecordIsNotRestored(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	if err := os.Rename(file, file+".bak"); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	saveAll(t, db, row("job-a", file, StatusRunning)) // legacy row: no swap record
	q, _ := startPersistent(t, db, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	<-q.recovered
	if _, err := os.Lstat(file); err == nil {
		t.Error("a .bak was resurrected without a recorded swap")
	}
}

func TestRecordedSwapWithBadPathsIsIgnored(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	_, secret := library(t, "s.mkv", "secret")
	if err := os.Rename(secret, secret+".bak"); err != nil {
		t.Fatal(err)
	}
	zz := filepath.Join(root, "zz")
	if err := os.Rename(file, zz); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	saveAll(t, db,
		swapRow("outside", secret, secret+".bak"), // outside the roots
		swapRow("odd-name", file, zz),             // not a backup name of the file
	)
	q, _ := startPersistent(t, db, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	<-q.recovered
	if _, err := os.Lstat(secret); err == nil {
		t.Error("restored a file outside the roots")
	}
	if _, err := os.Lstat(file); err == nil {
		t.Error("restored from a name that is not a backup of the file")
	}
}

func TestSwapRecordedByTheQueueSurvivesACrash(t *testing.T) {
	// The queue records the swap, the process dies after the first step, and the
	// next process restores from what the first one wrote.
	root, file := library(t, "a.mkv", "orig")
	db := openDB(t)
	q, _ := startPersistent(t, db, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	<-q.recovered
	enq := q.Enqueue(request(file))
	wait(t, q, enq.ID, StatusSuccess)
	// Re-create the crash window for a new job row.
	j := &job{Job: row("crash", file, StatusRunning).Job, resolved: file}
	if err := q.recordSwap(j, file, file+".bak.9"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, file+".bak.9"); err != nil {
		t.Fatal(err)
	}
	rows, _ := NewSQLStore(db).Load(context.Background())
	var found bool
	for _, r := range rows {
		if r.ID == "crash" {
			found = r.SwapPath == file && r.SwapBackup == file+".bak.9"
		}
	}
	if !found {
		t.Fatalf("the swap was not persisted before the first rename: %+v", rows)
	}
	q2, _ := startPersistent(t, db, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	<-q2.recovered
	if got := read(t, file); got != "converted" {
		t.Errorf("not restored: %q", got)
	}
}

func TestInterruptedHardLinkSwapDropsTheDuplicateBackup(t *testing.T) {
	root, file := library(t, "a.mkv", "orig")
	if err := os.Link(file, file+".bak"); err != nil {
		t.Skip(err)
	}
	db := openDB(t)
	saveAll(t, db, swapRow("job-a", file, file+".bak"))
	q, _ := startPersistent(t, db, root, newFakeTools(t, "ok", probeTwoSeconds), nil)
	<-q.recovered
	if read(t, file) != "orig" {
		t.Error("original changed")
	}
	if _, err := os.Lstat(file + ".bak"); err == nil {
		t.Error("duplicate backup link kept")
	}
}

func TestStaleRowOutsideTheRootsNeverRuns(t *testing.T) {
	root, _ := library(t, "a.mkv", "x")
	outside, secret := library(t, "secret.mkv", "secret")
	_ = outside
	db := openDB(t)
	saveAll(t, db, row("stale", secret, StatusQueued))
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	q, fin := startPersistent(t, db, root, ft, nil)
	j := wait(t, q, "stale", StatusFailed)
	if !strings.Contains(j.Error, "no longer available inside the media folders") {
		t.Errorf("error: %q", j.Error)
	}
	if _, err := os.Stat(ft.argsFile); err == nil {
		t.Error("ffmpeg ran for a path outside the media roots")
	}
	if got := read(t, secret); got != "secret" {
		t.Errorf("the file outside was touched: %q", got)
	}
	eventually(t, "OnFinish", func() bool { return len(fin.ids()) == 1 })
}

func TestStaleRowWithTraversalOrRelativePathNeverRuns(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	db := openDB(t)
	saveAll(t, db, row("dotdot", root+"/../"+filepath.Base(root)+"/a.mkv", StatusQueued), row("rel", "a.mkv", StatusQueued))
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	q, _ := startPersistent(t, db, root, ft, nil)
	wait(t, q, "dotdot", StatusFailed)
	wait(t, q, "rel", StatusFailed)
	if _, err := os.Stat(ft.argsFile); err == nil {
		t.Error("ffmpeg ran")
	}
	if got := read(t, file); got != "x" {
		t.Errorf("file %q", got)
	}
}

func TestRowWithInvalidParamsIsNotTrusted(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	db := openDB(t)
	bad := row("bad-backend", file, StatusQueued)
	bad.Backend = "libx265 -i /etc/passwd"
	bad2 := row("bad-family", file+"2", StatusQueued)
	bad2.TargetFamily = "av1"
	saveAll(t, db, bad, bad2)
	// Corrupt params JSON that still parses as an object with other keys.
	if _, err := db.Exec(context.Background(), `UPDATE conversion_jobs SET params = '{}' WHERE id = 'bad-family'`); err != nil {
		t.Fatal(err)
	}
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	q, _ := startPersistent(t, db, root, ft, nil)
	for _, id := range []string{"bad-backend", "bad-family"} {
		j, _ := q.Get(id)
		if j.Status != StatusFailed || !strings.Contains(j.Error, "not valid") {
			t.Errorf("%s: %+v", id, j)
		}
	}
	if _, err := os.Stat(ft.argsFile); err == nil {
		t.Error("ffmpeg ran for a stored job with invalid parameters")
	}
}

func TestStoredJobWithoutHDRAcknowledgementDoesNotRunHDRFile(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	db := openDB(t)
	// The row says SDR and no acknowledgement; the file is HDR now.
	saveAll(t, db, row("job", file, StatusQueued))
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	ft.setStream(t, streamHDR10)
	q, _ := startPersistent(t, db, root, ft, func(c *Config) { c.AllowHDR = func() bool { return true } })
	j := wait(t, q, "job", StatusFailed)
	if !strings.Contains(j.Error, "HDR") || j.HDR != codecs.HDRHDR10 {
		t.Errorf("%+v", j)
	}
	if _, err := os.Stat(ft.argsFile); err == nil {
		t.Error("ffmpeg ran for an HDR file that was not acknowledged")
	}
}

func TestAcknowledgedHDRJobRunsOnlyWhileAllowed(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	ft.setStream(t, streamDV)
	allowed := true
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q := NewMemoryQueue(ctx, Config{MediaRoots: []string{root}, Ffmpeg: ft.ffmpeg, Ffprobe: ft.ffprobe, AllowHDR: func() bool { return allowed }})
	req := request(file)
	req.HDR, req.AckHDR = codecs.HDRDolbyVision, true
	j := wait(t, q, q.Enqueue(req).ID, StatusSuccess)
	if j.HDR != codecs.HDRDolbyVision || !j.AcknowledgedHDR {
		t.Errorf("%+v", j)
	}

	allowed = false
	root2, file2 := library(t, "b.mkv", "x")
	q2 := NewMemoryQueue(ctx, Config{MediaRoots: []string{root2}, Ffmpeg: ft.ffmpeg, Ffprobe: ft.ffprobe, AllowHDR: func() bool { return allowed }})
	j2 := wait(t, q2, q2.Enqueue(Request{
		InstanceID: "radarr", ItemID: 1, FilePath: file2, ResolvedPath: file2, Backend: BackendSoftware, TargetFamily: FamilyH264, HDR: codecs.HDRDolbyVision, AckHDR: true,
	}).ID, StatusFailed)
	if !strings.Contains(j2.Error, "not allowed") {
		t.Errorf("%+v", j2)
	}
}

func TestEnqueueIsPersistedAndCancelOfQueuedJobIsFinal(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	other := filepath.Join(root, "b.mkv")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	ft := newFakeTools(t, "slow", probeTwoSeconds)
	q, fin := startPersistent(t, db, root, ft, nil)
	first := q.Enqueue(request(file))
	r2 := request(other)
	r2.Owner, r2.OwnerName, r2.AckHardlink = "bypass", "local-network", true
	second := q.Enqueue(r2)
	wait(t, q, first.ID, StatusRunning)

	rows, err := NewSQLStore(db).Load(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[1].Owner != "bypass" || !rows[1].AcknowledgedHardlink || rows[1].Status != StatusQueued || rows[0].Status != StatusRunning {
		t.Errorf("rows: %+v", rows)
	}
	// The same file again reattaches (and the database's unique index agrees).
	if again := q.Enqueue(request(file)); again.ID != first.ID {
		t.Error("a second job for the same path was created")
	}
	if !q.Cancel(second.ID) {
		t.Fatal("cancel")
	}
	rows, _ = NewSQLStore(db).Load(context.Background())
	if rows[1].Status != StatusCanceled || rows[1].FinishedAt == 0 {
		t.Errorf("canceled row: %+v", rows[1])
	}
	if !q.Cancel(first.ID) {
		t.Fatal("cancel running")
	}
	wait(t, q, first.ID, StatusCanceled)
	eventually(t, "both finish notices", func() bool { return len(fin.ids()) == 2 })
}

func TestShutdownMidConversionLeavesAnInterruptedFailure(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	db := openDB(t)
	ft := newFakeTools(t, "slow", probeTwoSeconds)
	ctx, cancel := context.WithCancel(context.Background())
	q := NewQueue(ctx, Config{MediaRoots: []string{root}, Ffmpeg: ft.ffmpeg, Ffprobe: ft.ffprobe, Store: NewSQLStore(db)})
	job := q.Enqueue(request(file))
	wait(t, q, job.ID, StatusRunning)
	cancel() // SIGTERM
	j := wait(t, q, job.ID, StatusFailed)
	if j.Error != InterruptedError {
		t.Errorf("%+v", j)
	}
	eventually(t, "the row to be saved", func() bool {
		rows, _ := NewSQLStore(db).Load(context.Background())
		return len(rows) == 1 && rows[0].Status == StatusFailed && rows[0].Error == InterruptedError
	})
	if got := read(t, file); got != "x" {
		t.Errorf("file %q", got)
	}
}

func TestFinishedJobsArePrunedByCountAndAge(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	db := openDB(t)
	now := time.Now()
	var rows []Stored
	for i, age := range []time.Duration{90 * 24 * time.Hour, 2 * time.Hour, time.Hour, 30 * time.Minute, 10 * time.Minute} {
		r := row("old-"+string(rune('a'+i)), file, StatusSuccess)
		r.FinishedAt = now.Add(-age).UnixMilli()
		rows = append(rows, r)
	}
	active := row("queued", file, StatusQueued)
	saveAll(t, db, append(rows, active)...)
	ft := newFakeTools(t, "slow", probeTwoSeconds)
	q, _ := startPersistent(t, db, root, ft, func(c *Config) { c.RetainMax = 3 })
	var ids []string
	for _, j := range q.List() {
		ids = append(ids, j.ID)
	}
	// "old-a" is past the age limit; of the rest only the 3 newest finished stay;
	// the queued job is never pruned.
	if !slices.Equal(ids, []string{"old-c", "old-d", "old-e", "queued"}) {
		t.Errorf("kept: %v", ids)
	}
	saved, _ := NewSQLStore(db).Load(context.Background())
	if len(saved) != 4 {
		t.Errorf("rows left: %d", len(saved))
	}
}

func TestOnlyOneActiveRowPerPathInTheDatabase(t *testing.T) {
	_, file := library(t, "a.mkv", "x")
	db := openDB(t)
	s := NewSQLStore(db)
	if err := s.Save(context.Background(), row("one", file, StatusQueued)); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), row("two", file, StatusRunning)); err == nil {
		t.Error("the database allowed two active jobs for one path")
	}
	if err := s.Save(context.Background(), row("three", file, StatusSuccess)); err != nil {
		t.Errorf("a finished job must not conflict: %v", err)
	}
}

func TestClassifyProbe(t *testing.T) {
	for doc, want := range map[string]codecs.HDR{
		streamSDR:   codecs.HDRNone,
		streamHDR10: codecs.HDRHDR10,
		streamDV:    codecs.HDRDolbyVision,
		`{"streams":[{"color_transfer":"arib-std-b67"}]}`:                               codecs.HDRHLG,
		`{"streams":[{"color_transfer":"bt709","color_primaries":"bt2020"}]}`:           codecs.HDRUnknown,
		`{"streams":[{"side_data_list":[{"side_data_type":"Dolby Vision RPU Data"}]}]}`: codecs.HDRDolbyVision,
		`{"streams":[{}]}`: codecs.HDRNone,
		`{"streams":[]}`:   codecs.HDRUnknown,
		`2.000000`:         codecs.HDRUnknown,
		``:                 codecs.HDRUnknown,
		`{"streams":[{"color_transfer":"SMPTE2084"}]}`: codecs.HDRHDR10,
	} {
		if got := ClassifyProbe([]byte(doc)); got != want {
			t.Errorf("%s: %s, want %s", doc, got, want)
		}
	}
}

func TestProbeHDRFailureIsUnknownNotSDR(t *testing.T) {
	_, file := library(t, "a.mkv", "x")
	ft := newFakeTools(t, "ok", probeBroken)
	// The fake answers stream queries from stream.json, so use a probe that fails for everything.
	if err := os.WriteFile(ft.ffprobe, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProbeHDR(context.Background(), ft.ffprobe, file); got != codecs.HDRUnknown {
		t.Errorf("failed probe: %s", got)
	}
	if got := ProbeHDR(context.Background(), filepath.Join(t.TempDir(), "missing"), file); got != codecs.HDRUnknown {
		t.Errorf("missing ffprobe: %s", got)
	}
}

func TestValidateRoots(t *testing.T) {
	a, _ := filepath.EvalSymlinks(t.TempDir())
	b, _ := filepath.EvalSymlinks(t.TempDir())
	file := filepath.Join(a, "f.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(a, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(b, "link-to-a")
	if err := os.Symlink(a, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	cfg := t.TempDir()

	got, err := ValidateRoots([]string{a + "/", b, a}, nil)
	if err != nil || !slices.Equal(got, []string{a, b}) {
		t.Errorf("valid: %v %v", got, err)
	}
	for name, in := range map[string][]string{
		"none":             {},
		"relative":         {"movies"},
		"dotdot":           {b + "/../" + filepath.Base(a)},
		"root":             {"/"},
		"etc":              {"/etc"},
		"etc subfolder":    {"/etc/ssl"},
		"proc":             {"/proc"},
		"sys":              {"/sys"},
		"dev":              {"/dev"},
		"usr":              {"/usr"},
		"usr lib":          {"/usr/lib"},
		"var contains lib": {"/var"},
		"var lib":          {"/var/lib"},
		"var lib sub":      {"/var/lib/docker"},
		"missing":          {a + "/missing"},
		"file":             {file},
		"nested":           {a, sub},
		"nested reversed":  {sub, a},
		"symlink dup":      {a, link},
		"config dir":       {cfg},
		"config subdir":    {filepath.Join(cfg, "x")},
		"empty":            {""},
		"nul":              {a + "\x00"},
	} {
		if _, err := ValidateRoots(in, []string{cfg}); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		} else if _, ok := err.(*ValidationError); !ok { //nolint:errorlint // the concrete type is the contract
			t.Errorf("%s: %T", name, err)
		}
	}
	// A symlink into a protected folder is refused through its target.
	etcLink := filepath.Join(b, "etc-link")
	if err := os.Symlink("/etc", etcLink); err == nil {
		if _, err := ValidateRoots([]string{etcLink}, nil); err == nil {
			t.Error("a symlink to /etc was accepted")
		}
	}
	many := make([]string, MaxMediaRoots+1)
	for i := range many {
		many[i] = filepath.Join(b, "d"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		_ = os.Mkdir(many[i], 0o755)
	}
	if _, err := ValidateRoots(many, nil); err == nil {
		t.Error("too many roots accepted")
	}
	if _, err := ValidateRoots(many[:MaxMediaRoots], nil); err != nil {
		t.Errorf("exactly the limit: %v", err)
	}
}

func TestSettingsPrecedencePersistenceAndRefusal(t *testing.T) {
	a, _ := filepath.EvalSymlinks(t.TempDir())
	b, _ := filepath.EvalSymlinks(t.TempDir())
	db := openDB(t)
	ctx := context.Background()

	s, err := NewSettings(ctx, db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Source != SourceDefault || !slices.Equal(st.EffectiveRoots, DefaultMediaRoots) || st.IncludeHDR {
		t.Errorf("default: %+v", st)
	}
	s, _ = NewSettings(ctx, db, []string{a}, nil)
	if st := s.State(); st.Source != SourceEnvironment || !slices.Equal(st.EffectiveRoots, []string{a}) {
		t.Errorf("environment: %+v", st)
	}
	yes := true
	if _, err := s.Update(ctx, []string{b}, &yes); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Source != SourceDatabase || !slices.Equal(st.EffectiveRoots, []string{b}) || !s.IncludeHDR() {
		t.Errorf("database: %+v", st)
	}
	// A refused update changes nothing, in memory or stored.
	if _, err := s.Update(ctx, []string{"/etc"}, nil); err == nil {
		t.Error("accepted /etc")
	}
	// A new process (same database, environment seed a) sees the database win.
	s2, _ := NewSettings(ctx, db, []string{a}, nil)
	if st := s2.State(); st.Source != SourceDatabase || !slices.Equal(st.EffectiveRoots, []string{b}) || !st.IncludeHDR {
		t.Errorf("after restart: %+v", st)
	}
	// Removing the saved roots falls back to the environment, the switch stays.
	if _, err := s2.Update(ctx, []string{}, nil); err != nil {
		t.Fatal(err)
	}
	s3, _ := NewSettings(ctx, db, []string{a}, nil)
	if st := s3.State(); st.Source != SourceEnvironment || !st.IncludeHDR {
		t.Errorf("cleared: %+v", st)
	}
}
