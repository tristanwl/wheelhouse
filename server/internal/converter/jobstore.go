package converter

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/tristanwl/wheelhouse/server/internal/codecs"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// Stored is a job as it is persisted: the API view plus the resolved path the
// job will touch.
type Stored struct {
	Job
	// Path is the resolved real path that was inside the media roots when the
	// job was queued. It is only a record: it is resolved against the roots in
	// force again before anything runs.
	Path string
}

// JobStore persists the queue's jobs.
type JobStore interface {
	// Load returns every saved job, oldest first. A row that cannot be understood
	// is returned with empty enums (so the queue refuses to run it), not as an
	// error.
	Load(ctx context.Context) ([]Stored, error)
	// Save inserts or updates a job.
	Save(ctx context.Context, j Stored) error
	// Delete removes jobs by id.
	Delete(ctx context.Context, ids []string) error
}

// SQLStore is the SQLite JobStore (table conversion_jobs).
type SQLStore struct{ db *store.DB }

// NewSQLStore returns a JobStore over the database.
func NewSQLStore(db *store.DB) *SQLStore { return &SQLStore{db: db} }

// storedParams is the params column: the request fields the job was made from.
type storedParams struct {
	FilePath     string  `json:"filePath"`
	Title        string  `json:"title"`
	OriginalSize float64 `json:"originalSize"`
	Backend      Backend `json:"backend"`
	TargetFamily Family  `json:"targetFamily"`
}

// Save implements JobStore.
func (s *SQLStore) Save(ctx context.Context, j Stored) error {
	params, err := json.Marshal(storedParams{
		FilePath: j.FilePath, Title: j.Title, OriginalSize: j.OriginalSize,
		Backend: j.Backend, TargetFamily: j.TargetFamily,
	})
	if err != nil {
		return err
	}
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	hdr := j.HDR.Norm()
	if !hdr.Valid() {
		hdr = codecs.HDRUnknown
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO conversion_jobs
			(id, instance_id, item_id, path, params, hdr, state, progress, error, new_size, backup_path, log,
			 owner, owner_name, ack_hardlink, ack_hdr, created_at, started_at, finished_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET hdr = excluded.hdr, state = excluded.state, progress = excluded.progress,
			 error = excluded.error, new_size = excluded.new_size, backup_path = excluded.backup_path,
			 log = excluded.log, started_at = excluded.started_at, finished_at = excluded.finished_at`,
			j.ID, j.InstanceID, j.ItemID, j.Path, string(params), string(hdr), string(j.Status),
			clampPercent(j.ProgressPercent), j.Error, j.NewSize, j.BackupPath, j.Log,
			j.Owner, j.OwnerName, b2i(j.AcknowledgedHardlink), b2i(j.AcknowledgedHDR),
			j.CreatedAt, j.StartedAt, j.FinishedAt)
		return err
	})
}

func clampPercent(p int) int { return max(0, min(100, p)) }

// Delete implements JobStore.
func (s *SQLStore) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM conversion_jobs WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Load implements JobStore.
func (s *SQLStore) Load(ctx context.Context) ([]Stored, error) {
	rows, err := s.db.Query(ctx, `SELECT id, instance_id, item_id, path, params, hdr, state, progress, error,
		new_size, backup_path, log, owner, owner_name, ack_hardlink, ack_hdr, created_at, started_at, finished_at
		FROM conversion_jobs ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Stored
	for rows.Next() {
		var j Stored
		var params, hdr, state string
		var ackH, ackD int
		if err := rows.Scan(&j.ID, &j.InstanceID, &j.ItemID, &j.Path, &params, &hdr, &state, &j.ProgressPercent,
			&j.Error, &j.NewSize, &j.BackupPath, &j.Log, &j.Owner, &j.OwnerName, &ackH, &ackD,
			&j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		j.Status, j.HDR = Status(state), codecs.HDR(hdr)
		j.AcknowledgedHardlink, j.AcknowledgedHDR = ackH == 1, ackD == 1
		var p storedParams
		if json.Unmarshal([]byte(params), &p) == nil {
			j.FilePath, j.Title, j.OriginalSize = p.FilePath, p.Title, p.OriginalSize
			// Left as stored, valid or not: the queue validates before it runs.
			j.Backend, j.TargetFamily = p.Backend, p.TargetFamily
		}
		j.Log = strings.ToValidUTF8(j.Log, "")
		out = append(out, j)
	}
	return out, rows.Err()
}
