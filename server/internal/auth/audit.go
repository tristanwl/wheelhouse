package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

func subtleEqual(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

func utf8Valid(s string) bool { return utf8.ValidString(s) }

func writeTokenFile(dir, token string) error {
	path := filepath.Join(dir, SetupTokenFile)
	_ = os.Remove(path)                                                   // a token from an earlier start is dead
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the operator-configured config directory
	if err != nil {
		return err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func removeTokenFile(dir string) {
	_ = os.Remove(filepath.Join(dir, SetupTokenFile))
}

// Entry is one row of the audit log.
type Entry struct {
	ID        int64
	At        time.Time
	ActorID   string
	ActorName string
	Action    string
	Target    string
	Address   string
	Detail    json.RawMessage
}

// auditTx appends an event inside a transaction, so the change and its record
// commit together. Nothing secret may be passed in: callers give names, ids and
// counts, never passwords, tokens or request bodies.
func (s *Service) auditTx(ctx context.Context, tx *sql.Tx, a Actor, action, target string, detail map[string]any) error {
	d := []byte("{}")
	if len(detail) > 0 {
		if b, err := json.Marshal(detail); err == nil && len(b) <= 4000 {
			d = b
		}
	}
	var actorID any
	if a.ID != "" {
		actorID = a.ID
	}
	name := a.Name
	if name == "" {
		name = "system"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (at, actor_id, actor_name, action, target, address, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.unix(), actorID, clean(name, 64), clean(action, 64), clean(target, 256), clip(a.Addr, 64), string(d))
	return err
}

// Record appends an event on its own. A failure is logged, not returned: the
// caller has done (or refused) its work already and the request should not
// fail because the record could not be written.
func (s *Service) Record(ctx context.Context, a Actor, action, target string, detail map[string]any) {
	s.record(ctx, a, action, target, detail)
}

func (s *Service) record(ctx context.Context, a Actor, action, target string, detail map[string]any) {
	err := s.db.Tx(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		return s.auditTx(ctx, tx, a, action, target, detail)
	})
	if err != nil {
		s.log.Error("could not write an audit entry", "action", action, "error", err)
	}
}

// ListAudit returns up to limit entries newest first, starting below the entry
// with id before (0 for the newest).
func (s *Service) ListAudit(ctx context.Context, limit int, before int64) ([]Entry, error) {
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := s.db.Query(ctx, `SELECT id, at, coalesce(actor_id, ''), actor_name, action, target, address, detail
		FROM audit_log WHERE id < ? ORDER BY id DESC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var at int64
		var detail string
		if err := rows.Scan(&e.ID, &at, &e.ActorID, &e.ActorName, &e.Action, &e.Target, &e.Address, &detail); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, rows.Err()
}
