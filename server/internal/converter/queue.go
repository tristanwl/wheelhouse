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

	"github.com/tristanwl/wheelhouse/server/internal/codecs"
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
	// HDR is the class of the source file (see ProbeHDR): "none" for SDR.
	HDR codecs.HDR `json:"hdr"`
	// Owner is who started the job: a user id, "bypass" (the local-network
	// bypass) or "cli". OwnerName is the name shown for it.
	Owner     string `json:"owner"`
	OwnerName string `json:"ownerName"`
	// AcknowledgedHardlink and AcknowledgedHDR are the explicit confirmations the
	// request carried.
	AcknowledgedHardlink bool `json:"acknowledgedHardlink"`
	AcknowledgedHDR      bool `json:"acknowledgedHdr"`
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
	// HDR is the class ProbeHDR gave the file when it was requested.
	HDR codecs.HDR
	// Owner and OwnerName: see Job.
	Owner, OwnerName string
	// AckHardlink and AckHDR record the acknowledgements the request carried. An
	// HDR job runs only with AckHDR (and while HDR conversion is allowed).
	AckHardlink, AckHDR bool
}

// Queue is the job store and runner the routes use. JobQueue implements it, in
// memory or backed by SQLite (issue #33).
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

// Config is what the queue needs.
type Config struct {
	// Roots returns the media roots in force; it is called whenever a path is
	// checked, so a change of the settings applies to the next check. MediaRoots
	// is the fixed list used when Roots is nil.
	Roots      func() []string
	MediaRoots []string
	// Store persists the jobs (nil: in memory only).
	Store JobStore
	// AllowHDR reports whether HDR conversion is currently allowed (nil: no).
	AllowHDR func() bool
	// OnFinish is called once for every job that reaches success, failed or
	// canceled (including jobs found interrupted at start), outside the queue's
	// lock.
	OnFinish func(Job)
	// RetainFor and RetainMax bound the finished jobs kept (defaults
	// RetainFinishedFor and RetainFinishedMax).
	RetainFor time.Duration
	RetainMax int
	// Ffmpeg and Ffprobe are the binaries (default "ffmpeg" and "ffprobe").
	Ffmpeg, Ffprobe string
	Rescan          Rescan
	Logger          *slog.Logger
	Now             func() time.Time
}

const (
	// RetainFinishedFor is how long a finished job stays in the list and the
	// database; RetainFinishedMax caps how many finished jobs are kept, oldest
	// dropped first. Active jobs are never pruned.
	RetainFinishedFor = 30 * 24 * time.Hour
	RetainFinishedMax = 200

	// InterruptedError is the error of a job that was running when the server
	// stopped.
	InterruptedError = "interrupted: the server stopped while this conversion was running"

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

// JobQueue keeps the jobs (in memory, written through to a JobStore when one is
// configured) and runs one at a time: transcoding is CPU heavy and the host
// usually also runs Sonarr, Radarr and a media server.
//
// Restart policy with a store (issue #33): a job that was probing or running
// when the process stopped is marked failed with InterruptedError and the
// partial files are cleaned (CleanupOrphans); queued jobs are kept and run, in
// the order they were added, once the clean-up is done; finished jobs are pruned
// by age and count. Nothing read back from the store is trusted: the enums are
// validated, and the path is resolved against the media roots in force when the
// job runs, exactly as for a job queued a second ago.
type JobQueue struct {
	cfg  Config
	base context.Context

	mu         sync.Mutex
	jobs       []*job
	processing bool

	// recovered is closed once the startup clean-up has finished; no job runs
	// before then, so the clean-up cannot undo a swap in progress.
	recovered chan struct{}
}

// NewMemoryQueue returns a queue that keeps nothing across restarts.
func NewMemoryQueue(ctx context.Context, cfg Config) *JobQueue {
	cfg.Store = nil
	return NewQueue(ctx, cfg)
}

// NewQueue returns a queue whose ffmpeg processes are killed when ctx is
// cancelled. With a store it first loads the saved jobs and applies the restart
// policy; it starts the clean-up of what an earlier process left behind.
func NewQueue(ctx context.Context, cfg Config) *JobQueue {
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
	if cfg.RetainFor <= 0 {
		cfg.RetainFor = RetainFinishedFor
	}
	if cfg.RetainMax <= 0 {
		cfg.RetainMax = RetainFinishedMax
	}
	q := &JobQueue{cfg: cfg, base: ctx, recovered: make(chan struct{})}
	interrupted := q.load()
	for _, j := range interrupted {
		q.notify(j)
	}
	go func() {
		defer close(q.recovered)
		CleanupOrphans(q.roots(), cfg.Logger)
	}()
	if q.hasQueued() {
		q.processing = true
		go q.loop()
	}
	return q
}

// roots returns the media roots in force.
func (q *JobQueue) roots() []string {
	if q.cfg.Roots != nil {
		return q.cfg.Roots()
	}
	return q.cfg.MediaRoots
}

func (q *JobQueue) hasQueued() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.Status == StatusQueued {
			return true
		}
	}
	return false
}

func (q *JobQueue) notify(j Job) {
	if q.cfg.OnFinish != nil {
		q.cfg.OnFinish(j)
	}
}

// validJob checks what a job needs before anything runs for it; it is the check
// for jobs read back from the store, and it runs again before each job starts.
func validJob(j *Job, resolved string) string {
	switch {
	case !j.Backend.Valid() || !j.TargetFamily.Valid():
		return "The stored conversion settings are not valid"
	case !j.HDR.Valid():
		return "The stored HDR class is not valid"
	case resolved == "" || !filepath.IsAbs(resolved):
		return "The stored file path is not valid"
	}
	return ""
}

// load reads the saved jobs and applies the restart policy. It returns the jobs
// it found interrupted or invalid, so the caller can report them.
func (q *JobQueue) load() []Job {
	if q.cfg.Store == nil {
		return nil
	}
	rows, err := q.cfg.Store.Load(q.base)
	if err != nil {
		q.cfg.Logger.Error("could not load the saved conversion jobs", "error", err)
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.nowMS()
	var done []Job
	for _, r := range rows {
		j := &job{Job: r.Job, resolved: r.Path}
		j.ctx, j.cancel = context.WithCancel(q.base)
		if j.Status.active() {
			switch reason := validJob(&j.Job, j.resolved); {
			case reason != "":
				q.markFailed(j, now, reason)
				done = append(done, j.Job)
			case j.Status != StatusQueued:
				q.markFailed(j, now, InterruptedError)
				done = append(done, j.Job)
			}
		}
		q.jobs = append(q.jobs, j)
	}
	q.prune(now)
	return done
}

// markFailed finishes a job as failed and saves it. The caller holds q.mu.
func (q *JobQueue) markFailed(j *job, now int64, msg string) {
	j.Status, j.Error, j.FinishedAt = StatusFailed, msg, now
	j.Log = tail(j.Log+"Failed: "+msg+"\n", maxLogBytes)
	j.cancel()
	q.save(j)
}

// save writes the job through to the store. A failure is logged, not fatal: the
// conversion in progress matters more than its record.
func (q *JobQueue) save(j *job) {
	if q.cfg.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(q.base), 10*time.Second)
	defer cancel()
	if err := q.cfg.Store.Save(ctx, Stored{Job: j.Job, Path: j.resolved}); err != nil {
		q.cfg.Logger.Error("could not save a conversion job", "job", j.ID, "error", err)
	}
}

// prune drops finished jobs older than the retention or beyond the cap, oldest
// first; active jobs are never dropped. The caller holds q.mu.
func (q *JobQueue) prune(now int64) {
	cutoff := now - q.cfg.RetainFor.Milliseconds()
	var finished []*job // in list order, oldest first
	for _, j := range q.jobs {
		if !j.Status.active() {
			finished = append(finished, j)
		}
	}
	drop := map[*job]bool{}
	for _, j := range finished {
		if j.FinishedAt > 0 && j.FinishedAt < cutoff {
			drop[j] = true
		}
	}
	kept := 0
	for i := len(finished) - 1; i >= 0; i-- { // newest first
		if drop[finished[i]] {
			continue
		}
		kept++
		if kept > q.cfg.RetainMax {
			drop[finished[i]] = true
		}
	}
	if len(drop) == 0 {
		return
	}
	var ids []string
	keep := q.jobs[:0:0]
	for _, j := range q.jobs {
		if drop[j] {
			ids = append(ids, j.ID)
			continue
		}
		keep = append(keep, j)
	}
	q.jobs = keep
	if q.cfg.Store != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(q.base), 10*time.Second)
		defer cancel()
		if err := q.cfg.Store.Delete(ctx, ids); err != nil {
			q.cfg.Logger.Error("could not prune old conversion jobs", "error", err)
		}
	}
}

// finish is called when a job has reached a final state: it prunes and reports.
func (q *JobQueue) finish(j *job) {
	q.mu.Lock()
	snap := j.Job
	q.prune(q.nowMS())
	q.mu.Unlock()
	q.notify(snap)
}

func (q *JobQueue) nowMS() int64 { return q.cfg.Now().UnixMilli() }

// List implements Queue.
func (q *JobQueue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, len(q.jobs))
	for i, j := range q.jobs {
		out[i] = j.Job
	}
	return out
}

// Get implements Queue.
func (q *JobQueue) Get(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j := q.find(id); j != nil {
		return j.Job, true
	}
	return Job{}, false
}

func (q *JobQueue) find(id string) *job {
	for _, j := range q.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// FindActive implements Queue.
func (q *JobQueue) FindActive(resolvedPath string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j := q.findActive(resolvedPath); j != nil {
		return j.Job, true
	}
	return Job{}, false
}

func (q *JobQueue) findActive(resolvedPath string) *job {
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
func (q *JobQueue) Enqueue(req Request) Job {
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

			HDR:                  req.HDR.Norm(),
			Owner:                req.Owner,
			OwnerName:            req.OwnerName,
			AcknowledgedHardlink: req.AckHardlink,
			AcknowledgedHDR:      req.AckHDR,
		},
		resolved: req.ResolvedPath,
		ctx:      ctx,
		cancel:   cancel,
	}
	q.jobs = append(q.jobs, j)
	q.save(j)
	if !q.processing {
		q.processing = true
		go q.loop()
	}
	return j.Job
}

// Cancel implements Queue. A queued job is marked canceled outright (nothing
// to kill yet); a running one has its process killed and runJob does the
// clean-up.
func (q *JobQueue) Cancel(id string) bool {
	q.mu.Lock()
	j := q.find(id)
	if j == nil {
		q.mu.Unlock()
		return false
	}
	switch j.Status {
	case StatusQueued:
		j.Status = StatusCanceled
		j.FinishedAt = q.nowMS()
		j.cancel()
		q.save(j)
		snap := j.Job
		q.prune(q.nowMS())
		q.mu.Unlock()
		q.notify(snap)
		return true
	case StatusProbing, StatusRunning:
		j.canceled = true
		j.cancel()
		q.mu.Unlock()
		return true
	}
	q.mu.Unlock()
	return false
}

func (q *JobQueue) loop() {
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
		q.save(next)
		q.mu.Unlock()
		q.runJob(next)
	}
}

// update runs f on the job under the lock.
func (q *JobQueue) update(j *job, f func(*Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	f(&j.Job)
	q.save(j)
}

func (q *JobQueue) logf(j *job, format string, args ...any) {
	q.appendLog(j, fmt.Sprintf(format, args...))
}

// appendLog adds to the job's log in memory only; the log is saved with the
// next state or progress change, so a chatty ffmpeg does not write the database
// on every line.
func (q *JobQueue) appendLog(j *job, text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.Log = tail(j.Log+text, maxLogBytes)
}

func (q *JobQueue) isCanceled(j *job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return j.canceled
}

var errCanceled = errors.New("canceled")

// fail is an error whose text is shown to the user.
type fail string

func (f fail) Error() string { return string(f) }

func (q *JobQueue) runJob(j *job) {
	if reason := validJob(&j.Job, j.resolved); reason != "" {
		q.logf(j, "Failed: %s\n", reason)
		q.update(j, func(jj *Job) { jj.Status, jj.Error, jj.FinishedAt = StatusFailed, reason, q.nowMS() })
		q.finish(j)
		return
	}
	q.logf(j, "Probing %s\n", j.FilePath)
	q.logf(j, "Target: %s via %s\n", j.TargetFamily, j.Backend)

	var tempPath string
	err := func() error {
		// The path was checked when the job was queued; the files may have
		// changed since, so it is resolved and checked again before use.
		src, err := ResolveInRoots(q.roots(), j.resolved)
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

		// Conversion writes SDR 8-bit: an HDR or Dolby Vision source runs only if
		// that was acknowledged when it was requested and is still allowed. The
		// file is looked at again, not taken from the request or the stored row.
		hdr := codecs.Worse(j.HDR, ProbeHDR(j.ctx, q.cfg.Ffprobe, src))
		q.update(j, func(jj *Job) { jj.HDR = hdr })
		if hdr.IsHDR() {
			q.logf(j, "HDR class: %s\n", hdr)
			if !j.AcknowledgedHDR || q.cfg.AllowHDR == nil || !q.cfg.AllowHDR() {
				return fail("This file is HDR or Dolby Vision, and converting it was not acknowledged or is not allowed (the result would be SDR 8-bit without that metadata)")
			}
		}
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
		if again, err := ResolveInRoots(q.roots(), src); err != nil || again != src {
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
		q.finish(j)
		return
	}

	// Update the status before the cleanup unlink, not after: deleting a large
	// partial file on a network mount can take real time, and the job must stop
	// reporting "running" the moment it is canceled.
	now := q.nowMS()
	userCanceled := q.isCanceled(j)
	switch {
	case userCanceled || (errors.Is(err, errCanceled) && q.base.Err() == nil):
		q.update(j, func(jj *Job) { jj.Status = StatusCanceled; jj.FinishedAt = now })
		q.appendLog(j, "Canceled.\n")
	case q.base.Err() != nil:
		// The server is shutting down: not the user's choice, so it is the same
		// "interrupted" failure a crash leaves behind.
		q.update(j, func(jj *Job) { jj.Status = StatusFailed; jj.Error = InterruptedError; jj.FinishedAt = now })
		q.appendLog(j, "Failed: "+InterruptedError+"\n")
	default:
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
	q.finish(j)
}

func (q *JobQueue) probeDuration(ctx context.Context, path string) (float64, error) {
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

func (q *JobQueue) runFfmpeg(j *job, src, tempPath string, duration float64) error {
	args := Args(j.Backend, j.TargetFamily, src, tempPath)
	q.logf(j, "$ ffmpeg %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(j.ctx, q.cfg.Ffmpeg, args...) //nolint:gosec // arguments come from Args: validated enums and resolved absolute paths
	// stderr is ffmpeg's human-readable banner and encoding log, shown verbatim
	// as the verbose output; stdout is the -progress key=value stream.
	cmd.Stderr = writerFunc(func(p []byte) { q.appendLog(j, string(p)) })
	last := -1
	cmd.Stdout = &progressWriter{onTime: func(us float64) {
		pct := int(math.Max(0, math.Min(99, math.Floor(us/1e6/duration*100+0.5))))
		if pct == last {
			return
		}
		last = pct
		q.update(j, func(jj *Job) { jj.ProgressPercent = pct })
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
