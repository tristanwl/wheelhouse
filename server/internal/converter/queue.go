package converter

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status is where a job is in its life.
type Status string

// The statuses.
const (
	StatusQueued   Status = "queued"
	StatusProbing  Status = "probing"
	StatusRunning  Status = "running"
	StatusSuccess  Status = "success"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

func (s Status) active() bool {
	return s == StatusQueued || s == StatusProbing || s == StatusRunning
}

// Job is a conversion as the API shows it. Times are milliseconds since the
// epoch.
type Job struct {
	ID           string  `json:"id"`
	InstanceID   string  `json:"instanceId"`
	ItemID       int     `json:"itemId"`
	FilePath     string  `json:"filePath"`
	Title        string  `json:"title"`
	OriginalSize float64 `json:"originalSize"`
	Backend      Backend `json:"backend"`
	TargetFamily Family  `json:"targetFamily"`
	Status       Status  `json:"status"`
	// ProgressPercent is 0..100, and never 100 before the swap is done.
	ProgressPercent int    `json:"progressPercent"`
	Error           string `json:"error,omitempty"`
	NewSize         int64  `json:"newSize,omitempty"`
	BackupPath      string `json:"backupPath,omitempty"`
	Log             string `json:"log"`
	CreatedAt       int64  `json:"createdAt"`
	StartedAt       int64  `json:"startedAt,omitempty"`
	FinishedAt      int64  `json:"finishedAt,omitempty"`
}

// Request is what Enqueue needs. The caller has validated the enums and
// resolved the path with ResolveInRoots.
type Request struct {
	InstanceID   string
	ItemID       int
	FilePath     string
	Title        string
	OriginalSize float64
	Backend      Backend
	TargetFamily Family
	// ResolvedPath is the real path of FilePath, inside the media roots.
	ResolvedPath string
}

// Queue is the job store and runner the routes use. The in-memory
// implementation is the only one today; a persistent one (issue #33) replaces
// it behind this interface.
type Queue interface {
	// List returns every job, oldest first.
	List() []Job
	Get(id string) (Job, bool)
	// FindActive returns the queued or running job for a resolved path.
	FindActive(resolvedPath string) (Job, bool)
	// Enqueue adds a job. A file that already has an active job returns that job
	// instead of starting a second one: two ffmpeg jobs on one path would race on
	// the same temp and backup names, and the second swap would overwrite the
	// first job's backup with the already converted file, losing the original.
	Enqueue(req Request) Job
	// Cancel stops a queued or running job and reports whether there was one.
	Cancel(id string) bool
}

// Rescan asks the media manager to rescan an item after its file changed. Its
// errors are ignored: the conversion succeeded either way.
type Rescan func(ctx context.Context, instanceID string, itemID int) error

// Config is what the in-memory queue needs.
type Config struct {
	MediaRoots []string
	// Ffmpeg and Ffprobe are the binaries (default "ffmpeg" and "ffprobe").
	Ffmpeg, Ffprobe string
	Rescan          Rescan
	Logger          *slog.Logger
	Now             func() time.Time
}

const (
	maxLogBytes = 8000
	// TempPrefix starts the name of an in-progress output file. The leading dot
	// hides it from Radarr and Sonarr, whose scanners ignore dotfiles, so a crawl
	// mid-conversion cannot pick a half-written file up as a second copy.
	TempPrefix = ".wheelhouse-convert-"
	bakSuffix  = ".bak"
)

type job struct {
	Job
	resolved string
	ctx      context.Context
	cancel   context.CancelFunc
	canceled bool
}

// MemoryQueue keeps jobs in memory for the life of the process and runs one at
// a time: transcoding is CPU heavy and the host usually also runs Sonarr,
// Radarr and a media server.
type MemoryQueue struct {
	cfg  Config
	base context.Context

	mu         sync.Mutex
	jobs       []*job
	processing bool

	// recovered is closed once the startup clean-up has finished; no job runs
	// before then, so the clean-up cannot undo a swap in progress.
	recovered chan struct{}
}

// NewMemoryQueue returns a queue whose ffmpeg processes are killed when ctx is
// cancelled. It starts the clean-up of what an earlier process left behind.
func NewMemoryQueue(ctx context.Context, cfg Config) *MemoryQueue {
	if cfg.Ffmpeg == "" {
		cfg.Ffmpeg = "ffmpeg"
	}
	if cfg.Ffprobe == "" {
		cfg.Ffprobe = "ffprobe"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	q := &MemoryQueue{cfg: cfg, base: ctx, recovered: make(chan struct{})}
	go func() {
		defer close(q.recovered)
		CleanupOrphans(cfg.MediaRoots, cfg.Logger)
	}()
	return q
}

func (q *MemoryQueue) nowMS() int64 { return q.cfg.Now().UnixMilli() }

// List implements Queue.
func (q *MemoryQueue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, len(q.jobs))
	for i, j := range q.jobs {
		out[i] = j.Job
	}
	return out
}

// Get implements Queue.
func (q *MemoryQueue) Get(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j := q.find(id); j != nil {
		return j.Job, true
	}
	return Job{}, false
}

func (q *MemoryQueue) find(id string) *job {
	for _, j := range q.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// FindActive implements Queue.
func (q *MemoryQueue) FindActive(resolvedPath string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j := q.findActive(resolvedPath); j != nil {
		return j.Job, true
	}
	return Job{}, false
}

func (q *MemoryQueue) findActive(resolvedPath string) *job {
	for _, j := range q.jobs {
		if j.resolved == resolvedPath && j.Status.active() {
			return j
		}
	}
	return nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Enqueue implements Queue.
func (q *MemoryQueue) Enqueue(req Request) Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	if existing := q.findActive(req.ResolvedPath); existing != nil {
		return existing.Job
	}
	ctx, cancel := context.WithCancel(q.base)
	j := &job{
		Job: Job{
			ID:           newID(),
			InstanceID:   req.InstanceID,
			ItemID:       req.ItemID,
			FilePath:     req.FilePath,
			Title:        req.Title,
			OriginalSize: req.OriginalSize,
			Backend:      req.Backend,
			TargetFamily: req.TargetFamily,
			Status:       StatusQueued,
			CreatedAt:    q.nowMS(),
		},
		resolved: req.ResolvedPath,
		ctx:      ctx,
		cancel:   cancel,
	}
	q.jobs = append(q.jobs, j)
	if !q.processing {
		q.processing = true
		go q.loop()
	}
	return j.Job
}

// Cancel implements Queue. A queued job is marked canceled outright (nothing
// to kill yet); a running one has its process killed and runJob does the
// clean-up.
func (q *MemoryQueue) Cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.find(id)
	if j == nil {
		return false
	}
	switch j.Status {
	case StatusQueued:
		j.Status = StatusCanceled
		j.FinishedAt = q.nowMS()
		j.cancel()
		return true
	case StatusProbing, StatusRunning:
		j.canceled = true
		j.cancel()
		return true
	}
	return false
}

func (q *MemoryQueue) loop() {
	<-q.recovered
	for {
		q.mu.Lock()
		var next *job
		for _, j := range q.jobs {
			if j.Status == StatusQueued {
				next = j
				break
			}
		}
		if next == nil {
			q.processing = false
			q.mu.Unlock()
			return
		}
		// Claim it under the lock, so Cancel sees a started job.
		next.Status = StatusProbing
		next.StartedAt = q.nowMS()
		q.mu.Unlock()
		q.runJob(next)
	}
}

// update runs f on the job under the lock.
func (q *MemoryQueue) update(j *job, f func(*Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	f(&j.Job)
}

func (q *MemoryQueue) logf(j *job, format string, args ...any) {
	q.appendLog(j, fmt.Sprintf(format, args...))
}

func (q *MemoryQueue) appendLog(j *job, text string) {
	q.update(j, func(jj *Job) { jj.Log = tail(jj.Log+text, maxLogBytes) })
}

func (q *MemoryQueue) isCanceled(j *job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return j.canceled
}

var errCanceled = errors.New("canceled")

// fail is an error whose text is shown to the user.
type fail string

func (f fail) Error() string { return string(f) }

func (q *MemoryQueue) runJob(j *job) {
	q.logf(j, "Probing %s\n", j.FilePath)
	q.logf(j, "Target: %s via %s\n", j.TargetFamily, j.Backend)

	var tempPath string
	err := func() error {
		// The path was checked when the job was queued; the files may have
		// changed since, so it is resolved and checked again before use.
		src, err := ResolveInRoots(q.cfg.MediaRoots, j.resolved)
		if err != nil {
			return fail("The file is no longer available inside the media folders")
		}
		dir, ext := filepath.Dir(src), filepath.Ext(src)
		// Leading dot: see TempPrefix.
		tempPath = filepath.Join(dir, TempPrefix+j.ID+ext)
		// Same-folder, non-video-extension backup rather than deleting the
		// original: Radarr and Sonarr match known video extensions, so a trailing
		// ".bak" keeps it from being picked up as an alternate version. Nothing
		// deletes it automatically.
		backupPath := src + bakSuffix

		if q.isCanceled(j) {
			return errCanceled
		}
		duration, err := q.probeDuration(j.ctx, src)
		if err != nil {
			if q.isCanceled(j) {
				return errCanceled
			}
			return err
		}
		q.logf(j, "Duration: %.1fs\n", duration)
		if q.isCanceled(j) {
			return errCanceled
		}

		q.update(j, func(jj *Job) { jj.Status = StatusRunning })
		if err := q.runFfmpeg(j, src, tempPath, duration); err != nil {
			return err
		}

		st, err := os.Stat(tempPath)
		if err != nil {
			return fail("The converted file is missing")
		}
		if st.Size() <= 0 {
			return fail("Converted file came out empty")
		}
		if q.isCanceled(j) {
			return errCanceled
		}

		// Nothing may have been swapped under us while ffmpeg ran.
		if again, err := ResolveInRoots(q.cfg.MediaRoots, src); err != nil || again != src {
			return fail("The file is no longer available inside the media folders")
		}
		if err := os.Rename(src, backupPath); err != nil {
			return fail("Could not move the original to its backup")
		}
		if err := os.Rename(tempPath, src); err != nil {
			// Put the original back rather than leave it stranded at the backup.
			_ = os.Rename(backupPath, src)
			return fail("Could not move the converted file into place")
		}
		q.logf(j, "Swapped in converted file; original kept at %s\n", backupPath)

		if q.cfg.Rescan != nil {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(q.base), 30*time.Second)
			_ = q.cfg.Rescan(ctx, j.InstanceID, j.ItemID)
			cancel()
		}

		now := q.nowMS()
		q.update(j, func(jj *Job) {
			jj.Status = StatusSuccess
			jj.ProgressPercent = 100
			jj.NewSize = st.Size()
			jj.BackupPath = backupPath
			jj.FinishedAt = now
		})
		return nil
	}()
	if err == nil {
		return
	}

	// Update the status before the cleanup unlink, not after: deleting a large
	// partial file on a network mount can take real time, and the job must stop
	// reporting "running" the moment it is canceled.
	now := q.nowMS()
	if errors.Is(err, errCanceled) || q.base.Err() != nil {
		q.update(j, func(jj *Job) { jj.Status = StatusCanceled; jj.FinishedAt = now })
		q.appendLog(j, "Canceled.\n")
	} else {
		msg := err.Error()
		var f fail
		if !errors.As(err, &f) {
			msg = "conversion failed"
		}
		q.update(j, func(jj *Job) { jj.Status = StatusFailed; jj.Error = msg; jj.FinishedAt = now })
		q.logf(j, "Failed: %s\n", msg)
	}
	if tempPath != "" {
		_ = os.Remove(tempPath)
	}
}

func (q *MemoryQueue) probeDuration(ctx context.Context, path string) (float64, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, q.cfg.Ffprobe, //nolint:gosec // the path is absolute and resolved inside the media roots
		"-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	cmd.Stdout = &out
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	seconds, perr := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil || perr != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, fail("ffprobe couldn't read the file's duration")
	}
	return seconds, nil
}

func (q *MemoryQueue) runFfmpeg(j *job, src, tempPath string, duration float64) error {
	args := Args(j.Backend, j.TargetFamily, src, tempPath)
	q.logf(j, "$ ffmpeg %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(j.ctx, q.cfg.Ffmpeg, args...) //nolint:gosec // arguments come from Args: validated enums and resolved absolute paths
	// stderr is ffmpeg's human-readable banner and encoding log, shown verbatim
	// as the verbose output; stdout is the -progress key=value stream.
	cmd.Stderr = writerFunc(func(p []byte) { q.appendLog(j, string(p)) })
	cmd.Stdout = &progressWriter{onTime: func(us float64) {
		pct := math.Floor(us/1e6/duration*100 + 0.5)
		pct = math.Max(0, math.Min(99, pct))
		q.update(j, func(jj *Job) { jj.ProgressPercent = int(pct) })
	}}
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if q.isCanceled(j) {
		return errCanceled
	}
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return fail(fmt.Sprintf("ffmpeg exited with code %d", exitErr.ExitCode()))
	}
	if q.base.Err() != nil {
		return errCanceled
	}
	return fail("ffmpeg could not be started")
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

// progressWriter reads ffmpeg's -progress output and reports each out_time_ms
// (which, despite the name, is in microseconds).
type progressWriter struct {
	onTime func(micros float64)
	buf    []byte
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if key, value, ok := strings.Cut(line, "="); ok && key == "out_time_ms" {
			if us, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && !math.IsNaN(us) && !math.IsInf(us, 0) {
				w.onTime(us)
			}
		}
	}
	if len(w.buf) > 1<<16 { // a line that long is not progress output
		w.buf = nil
	}
	return len(p), nil
}

// CleanupOrphans repairs what a killed process left in the media roots, once
// per process. Job state is in memory only, so a restart mid-conversion loses
// track of what was running. Two artifacts can remain:
//
//  1. ".wheelhouse-convert-*" files, an interrupted encode's partial output.
//     Always safe to delete; nothing references them.
//  2. "<file>.bak" with no "<file>" next to it: the process died between the
//     two renames of a swap, so the real content is stranded at the backup and
//     the expected path is empty. It is renamed back. A ".bak" next to a live
//     file is a normal backup and is left alone.
//
// Symlinks are never followed or touched.
func CleanupOrphans(roots []string, log *slog.Logger) {
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		walkAndClean(root, log)
	}
}

func walkAndClean(dir string, log *slog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			walkAndClean(full, log)
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasPrefix(name, TempPrefix):
			if err := os.Remove(full); err == nil {
				log.Info("removed orphaned conversion temp file", "path", full)
			}
		case strings.HasSuffix(name, bakSuffix):
			original := strings.TrimSuffix(full, bakSuffix)
			if _, err := os.Lstat(original); err == nil {
				continue
			}
			if err := os.Rename(full, original); err != nil {
				log.Error("could not restore interrupted conversion", "path", full)
			} else {
				log.Info("restored interrupted conversion", "from", full, "to", original)
			}
		}
	}
}
