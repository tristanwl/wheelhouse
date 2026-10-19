package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"time"
)

// Migrations are forward-only SQL files in migrations/, named
// NNNN_description.sql (four digits, starting at 0001, no gaps). They are
// embedded in the binary and applied in order, all the pending ones in a single
// transaction at open: a failure leaves the database exactly as it was.
//
// An applied migration must never be edited (add a new one instead); its
// SHA-256 is recorded and checked at every start. There is no "down": to go
// back, restore a backup taken before the upgrade.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew is returned when the database was migrated by a newer
// Wheelhouse than this binary. Running anyway could misread or corrupt data a
// newer version changed, so start the newer version again or restore a backup.
var ErrSchemaTooNew = errors.New("the database schema is newer than this version of Wheelhouse understands")

type migration struct {
	version int
	name    string // file name without extension, e.g. 0001_meta
	sql     string
	sum     string // hex SHA-256 of sql
}

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// loadMigrations parses and validates the embedded set.
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q: expected NNNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if v != len(out)+1 { // ReadDir sorts by name
			return nil, fmt.Errorf("migration file %q: expected version %04d (no gaps, no duplicates)", e.Name(), len(out)+1)
		}
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, migration{version: v, name: e.Name()[:len(e.Name())-len(".sql")], sql: string(body), sum: hex.EncodeToString(sum[:])})
	}
	return out, nil
}

// SchemaVersion is the newest schema this binary knows.
func SchemaVersion() int {
	ms, err := loadMigrations(migrationFS, "migrations")
	if err != nil {
		return 0
	}
	return len(ms)
}

func (db *DB) migrate(ctx context.Context) error {
	ms, err := loadMigrations(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("embedded migrations: %w", err)
	}
	return db.migrateWith(ctx, ms)
}

func (db *DB) migrateWith(ctx context.Context, ms []migration) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		// The first writer on a fresh file creates the bookkeeping table.
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER NOT NULL PRIMARY KEY,
			name       TEXT    NOT NULL,
			sha256     TEXT    NOT NULL,
			applied_at TEXT    NOT NULL
		) STRICT`); err != nil {
			return err
		}
		// Read inside the write transaction (BEGIN IMMEDIATE), so two processes
		// starting together cannot both apply the same migration.
		rows, err := tx.QueryContext(ctx, "SELECT version, name, sha256 FROM schema_migrations ORDER BY version")
		if err != nil {
			return err
		}
		type row struct {
			version   int
			name, sum string
		}
		var have []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.version, &r.name, &r.sum); err != nil {
				_ = rows.Close()
				return err
			}
			have = append(have, r)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if n := len(have); n > 0 && have[n-1].version > len(ms) {
			return fmt.Errorf("%w (database is at version %d, this binary knows %d)", ErrSchemaTooNew, have[n-1].version, len(ms))
		}
		applied := 0
		for _, r := range have {
			if r.version != applied+1 {
				return fmt.Errorf("schema_migrations has version %d where %d was expected; the database is damaged", r.version, applied+1)
			}
			if r.name != ms[r.version-1].name || r.sum != ms[r.version-1].sum {
				return fmt.Errorf("migration %d (%s) was changed after it was applied; never edit an applied migration, add a new one", r.version, r.name)
			}
			applied = r.version
		}
		now := time.Now().UTC().Format(time.RFC3339)
		for _, m := range ms[applied:] {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("migration %s: %w", m.name, err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, sha256, applied_at) VALUES (?, ?, ?, ?)",
				m.version, m.name, m.sum, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Version is the highest migration applied to this database.
func (db *DB) Version(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.r.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}
