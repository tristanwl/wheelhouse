package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/config"
)

// The floor exists in three places that must agree: the constant, the
// trigger's SQL and the smallest retention the configuration accepts.
func TestAuditFloorMatchesTheMigrationAndTheConfiguration(t *testing.T) {
	ms, err := loadMigrations(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, m := range ms {
		if m.name == "0008_audit_retention" {
			sql = m.sql
		}
	}
	if want := fmt.Sprintf("- %d * 86400", AuditFloorDays); !strings.Contains(sql, want) {
		t.Errorf("migration 0008 does not contain %q", want)
	}
	if config.MinAuditRetentionDays != AuditFloorDays {
		t.Errorf("config.MinAuditRetentionDays = %d, store.AuditFloorDays = %d", config.MinAuditRetentionDays, AuditFloorDays)
	}
}

// Retention (issue #84, migration 0008): rows younger than the 30-day floor can
// never be deleted, by anything, judged by SQLite's own clock and the row's own
// time; older ones can; updates stay refused for every row.
func TestAuditLogRowsYoungerThanTheFloorCannotBeDeleted(t *testing.T) {
	db := openTemp(t)
	const day = 86400
	ins := "INSERT INTO audit_log (at, actor_name, action) VALUES (CAST(strftime('%s','now') AS INTEGER) - ?, 'cli', ?)"
	mustExec(t, db, ins, 0, "now")
	mustExec(t, db, ins, 7*day, "week")
	mustExec(t, db, ins, 30*day-120, "just-inside-the-floor")
	mustExec(t, db, ins, 30*day+120, "just-outside-the-floor")
	mustExec(t, db, ins, 400*day, "ancient")

	// Deleting everything, one recent row, or a mix of recent and old rows
	// fails as a whole and removes nothing.
	expectFail(t, db, "append-only", "DELETE FROM audit_log")
	expectFail(t, db, "append-only", "DELETE FROM audit_log WHERE action IN ('now', 'week')")
	expectFail(t, db, "append-only", "DELETE FROM audit_log WHERE action = 'just-inside-the-floor'")
	expectFail(t, db, "append-only", "DELETE FROM audit_log WHERE action != 'x'")
	if n := count(t, db, "audit_log"); n != 5 {
		t.Fatalf("%d rows after refused deletes", n)
	}

	// Rows past the floor go, and only those.
	mustExec(t, db, "DELETE FROM audit_log WHERE action IN ('just-outside-the-floor', 'ancient')")
	if n := count(t, db, "audit_log"); n != 3 {
		t.Fatalf("%d rows after the old ones were deleted", n)
	}

	// Updating is refused for every row, old or young, so a row cannot be made to
	// look old, nor a recent entry rewritten.
	mustExec(t, db, ins, 500*day, "old-again")
	expectFail(t, db, "append-only", "UPDATE audit_log SET action = 'y' WHERE action = 'old-again'")
	expectFail(t, db, "append-only", "UPDATE audit_log SET at = 0 WHERE action = 'now'")
	if n := count(t, db, "audit_log"); n != 4 {
		t.Fatalf("%d rows at the end", n)
	}
}
