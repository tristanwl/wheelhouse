package auth

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// Audit retention (issue #84). The audit table is append-only: triggers refuse
// every UPDATE, and every DELETE of a row younger than store.AuditFloorDays
// (migration 0008, judged by SQLite's own clock). Pruning is the one controlled
// way rows leave: this routine deletes rows older than the configured
// retention, in bounded batches, at start and then daily. There is no other
// delete path, and even this one cannot touch recent history: a retention below
// the floor is refused at construction and the trigger refuses it anyway.

// AuditFloorDays is the age below which the database refuses to delete an audit
// row, whatever is configured (migration 0008).
const AuditFloorDays = store.AuditFloorDays

// DefaultAuditRetentionDays is how long audit rows are kept unless configured.
const DefaultAuditRetentionDays = 180

// pruneBatch bounds one DELETE, so a large backlog never holds the single
// writer connection for long and other writes interleave.
const pruneBatch = 1000

// pruneInterval is how often the routine runs after the first run at start.
const pruneInterval = 24 * time.Hour

// AuditPrune says what the last prune did.
type AuditPrune struct {
	// At is when it ran; the zero time if it has not run yet.
	At time.Time
	// Deleted is how many rows it removed.
	Deleted int
	// Cutoff is the time rows older than which it removed.
	Cutoff time.Time
	// Failed is true when it stopped on an error (the log has the details).
	Failed bool
}

// AuditRetention is the retention in force and the state of pruning.
type AuditRetention struct {
	Days      int
	FloorDays int
	Last      AuditPrune
}

type pruneState struct {
	mu   sync.Mutex
	last AuditPrune
}

// AuditRetention reports the configured retention and the last prune.
func (s *Service) AuditRetention() AuditRetention {
	s.prune.mu.Lock()
	defer s.prune.mu.Unlock()
	return AuditRetention{Days: s.retentionDays, FloorDays: AuditFloorDays, Last: s.prune.last}
}

// PruneAudit deletes audit rows older than the retention, in batches, and
// records one audit.prune entry (how many, and the cutoff) when it deleted
// any. A run that deletes nothing writes nothing, so a quiet server does not
// grow its own log every day. It returns the number of rows removed.
func (s *Service) PruneAudit(ctx context.Context) (int, error) {
	now := s.now()
	cutoff := now.Add(-time.Duration(s.retentionDays) * 24 * time.Hour)
	total := 0
	var err error
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var res sql.Result
		res, err = s.db.Exec(ctx, `DELETE FROM audit_log WHERE id IN (SELECT id FROM audit_log WHERE at < ? ORDER BY id LIMIT ?)`, cutoff.Unix(), s.pruneBatch)
		if err != nil {
			break
		}
		n, _ := res.RowsAffected()
		total += int(n)
		if s.afterPruneBatch != nil {
			s.afterPruneBatch(int(n))
		}
		if int(n) < s.pruneBatch {
			break
		}
	}
	s.prune.mu.Lock()
	s.prune.last = AuditPrune{At: now.UTC(), Deleted: total, Cutoff: cutoff.UTC(), Failed: err != nil}
	s.prune.mu.Unlock()
	if total > 0 {
		s.record(context.WithoutCancel(ctx), Actor{Name: "system"}, "audit.prune", "", map[string]any{
			"deleted":       total,
			"cutoff":        cutoff.UTC().Format(time.RFC3339),
			"retentionDays": s.retentionDays,
		})
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("pruning the audit log failed", "error", err, "deleted", total)
	}
	return total, err
}

// RunAuditPruning prunes once now and then every 24 hours until ctx ends. It
// blocks; start it in a goroutine. Run it after the server is up so a large
// backlog never delays start.
func (s *Service) RunAuditPruning(ctx context.Context) {
	for {
		if n, err := s.PruneAudit(ctx); err == nil && n > 0 {
			s.log.Info("audit log pruned", "deleted", n, "retentionDays", s.retentionDays)
		}
		t := time.NewTimer(s.pruneEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
