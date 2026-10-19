package auth

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

const day = 24 * time.Hour

// pruneEnv is a service whose clock starts at the real time (the delete trigger
// judges age by SQLite's own clock, so the rows' ages must be real) and can be
// moved by the test. Rows are inserted with an explicit age.
type pruneEnv struct {
	t   *testing.T
	s   *Service
	db  *store.DB
	clk *clock
}

func newPruneEnv(t *testing.T, retentionDays int) *pruneEnv {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "wheelhouse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := &clock{t: time.Now()}
	return &pruneEnv{t: t, db: db, clk: clk, s: New(db, Options{Clock: clk.now, Params: cheap, AuditRetentionDays: retentionDays})}
}

// row inserts an audit row that is age old at the service's clock.
func (e *pruneEnv) row(age time.Duration, action string) {
	e.t.Helper()
	at := e.clk.now().Add(-age).Unix()
	if _, err := e.db.Exec(context.Background(), `INSERT INTO audit_log (at, actor_name, action) VALUES (?, 'test', ?)`, at, action); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pruneEnv) actions() map[string]int {
	e.t.Helper()
	es, err := e.s.ListAudit(context.Background(), 200, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]int{}
	for _, en := range es {
		out[en.Action]++
	}
	return out
}

func TestPruneDeletesOnlyRowsOlderThanTheRetentionAndRecordsIt(t *testing.T) {
	e := newPruneEnv(t, 180)
	e.row(400*day, "ancient")
	e.row(200*day, "old")
	e.row(180*day+time.Minute, "just-over")
	e.row(180*day-time.Minute, "just-under")
	e.row(31*day, "month")
	e.row(time.Hour, "fresh")

	n, err := e.s.PruneAudit(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	got := e.actions()
	if got["ancient"]+got["old"]+got["just-over"] != 0 || got["just-under"] != 1 || got["month"] != 1 || got["fresh"] != 1 {
		t.Fatalf("after pruning: %v", got)
	}
	// One entry says what happened: how many, the cutoff, the retention.
	if got["audit.prune"] != 1 {
		t.Fatalf("audit.prune entries: %v", got)
	}
	es, _ := e.s.ListAudit(context.Background(), 1, 0)
	if es[0].Action != "audit.prune" || es[0].ActorName != "system" {
		t.Fatalf("%+v", es[0])
	}
	var d map[string]any
	if err := json.Unmarshal(es[0].Detail, &d); err != nil {
		t.Fatal(err)
	}
	wantCutoff := e.clk.now().Add(-180 * day).UTC().Format(time.RFC3339)
	if d["deleted"] != float64(3) || d["cutoff"] != wantCutoff || d["retentionDays"] != float64(180) {
		t.Errorf("detail = %v, want cutoff %s", d, wantCutoff)
	}
	last := e.s.AuditRetention()
	if last.Days != 180 || last.FloorDays != 30 || last.Last.Deleted != 3 || last.Last.Failed || last.Last.At.IsZero() {
		t.Errorf("status = %+v", last)
	}

	// Nothing left to prune: nothing is deleted and no entry is written, so a
	// quiet server does not grow its own log every day.
	n, err = e.s.PruneAudit(context.Background())
	if err != nil || n != 0 || e.actions()["audit.prune"] != 1 {
		t.Fatalf("second run: %d, %v, %v", n, err, e.actions())
	}
	// Time passes (injected clock): the next-oldest rows fall out.
	e.clk.advance(40 * day)
	n, err = e.s.PruneAudit(context.Background())
	if err != nil || n != 1 { // just-under is 220 days old now; month is 71 and stays
		t.Fatalf("after 40 days: %d, %v, %v", n, err, e.actions())
	}
	if got := e.actions(); got["just-under"] != 0 || got["month"] != 1 {
		t.Errorf("after 40 days: %v", got)
	}
}

func TestPruneWorksInBoundedBatches(t *testing.T) {
	e := newPruneEnv(t, 90)
	e.s.pruneBatch = 7
	for range 50 {
		e.row(100*day, "old")
	}
	e.row(day, "fresh")
	var batches []int
	e.s.afterPruneBatch = func(n int) { batches = append(batches, n) }

	n, err := e.s.PruneAudit(context.Background())
	if err != nil || n != 50 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if len(batches) != 8 {
		t.Fatalf("batches = %v, want seven of 7 and one of 1", batches)
	}
	for i, b := range batches {
		if b > 7 || (i < 7 && b != 7) {
			t.Errorf("batch %d deleted %d rows, the bound is 7", i, b)
		}
	}
	if got := e.actions(); got["old"] != 0 || got["fresh"] != 1 || got["audit.prune"] != 1 {
		t.Errorf("rows = %v", got)
	}
}

func TestPruneStopsBetweenBatchesWhenCancelled(t *testing.T) {
	e := newPruneEnv(t, 90)
	e.s.pruneBatch = 5
	for range 20 {
		e.row(100*day, "old")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.s.afterPruneBatch = func(int) { cancel() }
	n, err := e.s.PruneAudit(ctx)
	if err == nil || n != 5 {
		t.Fatalf("pruned %d, %v; want one batch and an error", n, err)
	}
	if !e.s.AuditRetention().Last.Failed {
		t.Error("an interrupted prune is reported as failed")
	}
	// What was deleted is recorded; the rest is for the next run.
	if got := e.actions(); got["old"] != 15 || got["audit.prune"] != 1 {
		t.Errorf("rows = %v", got)
	}
}

// The retention can never be set below the floor, and the database refuses
// anyway: even a service whose clock is far ahead (so its cutoff would reach
// recent rows) deletes nothing and says so.
func TestRetentionBelowTheFloorIsRaisedAndTheDatabaseHasTheLastWord(t *testing.T) {
	e := newPruneEnv(t, 5) // below the floor
	if got := e.s.AuditRetention().Days; got != AuditFloorDays {
		t.Fatalf("retention %d, want it raised to the floor %d", got, AuditFloorDays)
	}
	e.row(10*day, "ten-days")
	e.row(29*day, "twenty-nine-days")
	e.row(31*day, "thirty-one-days")
	if n, err := e.s.PruneAudit(context.Background()); err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	if got := e.actions(); got["ten-days"] != 1 || got["twenty-nine-days"] != 1 || got["thirty-one-days"] != 0 {
		t.Errorf("rows = %v", got)
	}

	// A clock 100 days ahead of the database's would delete everything it can see.
	e.clk.advance(100 * day)
	n, err := e.s.PruneAudit(context.Background())
	if err == nil || n != 0 {
		t.Fatalf("a skewed clock deleted %d rows (err %v)", n, err)
	}
	if got := e.actions(); got["ten-days"] != 1 || got["twenty-nine-days"] != 1 {
		t.Errorf("recent rows were lost: %v", got)
	}
	if !e.s.AuditRetention().Last.Failed {
		t.Error("the refused prune is reported as failed")
	}
}

func TestTheDefaultRetentionIs180Days(t *testing.T) {
	e := newPruneEnv(t, 0)
	if r := e.s.AuditRetention(); r.Days != 180 || r.FloorDays != 30 || !r.Last.At.IsZero() {
		t.Errorf("%+v", r)
	}
}

func TestRunAuditPruningRunsAtStartAndAgainLater(t *testing.T) {
	e := newPruneEnv(t, 60)
	e.s.pruneEvery = 20 * time.Millisecond
	e.row(100*day, "before-start")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.RunAuditPruning(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("RunAuditPruning did not stop with its context")
		}
	}()
	waitFor(t, func() bool { return e.actions()["before-start"] == 0 }, "the first run at start")
	e.row(100*day, "added-later")
	waitFor(t, func() bool { return e.actions()["added-later"] == 0 }, "a later run")
	// The audit.prune entry is written just after the rows are deleted.
	waitFor(t, func() bool { return e.actions()["audit.prune"] >= 2 }, "both prune entries")
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
