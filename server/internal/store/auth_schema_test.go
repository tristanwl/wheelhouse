package store

import (
	"strings"
	"testing"
)

const (
	idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	idC = "cccccccccccccccccccccccccccccccc"
)

func addUser(t *testing.T, db *DB, id, name, role string, disabled int) {
	t.Helper()
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, role, disabled, created_at, password_changed_at)
		VALUES (?, ?, '$argon2id$fake', ?, ?, 1, 1)`, id, name, role, disabled)
}

func expectFail(t *testing.T, db *DB, want, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(ctx(t), q, args...)
	if err == nil {
		t.Fatalf("%s: succeeded, want an error containing %q", q, want)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: %v, want %q", q, err, want)
	}
}

func TestUsernamesAreUniqueIgnoringCase(t *testing.T) {
	db := openTemp(t)
	addUser(t, db, idA, "Alice", "admin", 0)
	expectFail(t, db, "UNIQUE", `INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at)
		VALUES (?, 'alice', '$argon2id$fake', 'user', 1, 1)`, idB)
	expectFail(t, db, "", `INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at)
		VALUES (?, 'ab', '$argon2id$fake', 'user', 1, 1)`, idB)
	expectFail(t, db, "", `INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at)
		VALUES (?, 'bob', 'plaintext-password', 'user', 1, 1)`, idB)
	expectFail(t, db, "", `INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at)
		VALUES (?, 'bob', '$argon2id$fake', 'root', 1, 1)`, idB)
}

func TestTheLastEnabledAdminIsProtectedByTheDatabase(t *testing.T) {
	db := openTemp(t)
	addUser(t, db, idA, "alice", "admin", 0)
	addUser(t, db, idB, "bob", "user", 0)

	expectFail(t, db, "last admin", "DELETE FROM users WHERE id = ?", idA)
	expectFail(t, db, "last admin", "UPDATE users SET role = 'user' WHERE id = ?", idA)
	expectFail(t, db, "last admin", "UPDATE users SET disabled = 1 WHERE id = ?", idA)

	// A second admin lifts the protection from either; a disabled admin does not count.
	addUser(t, db, idC, "carol", "admin", 1)
	expectFail(t, db, "last admin", "DELETE FROM users WHERE id = ?", idA)
	mustExec(t, db, "UPDATE users SET disabled = 0 WHERE id = ?", idC)
	mustExec(t, db, "UPDATE users SET role = 'user' WHERE id = ?", idA)
	expectFail(t, db, "last admin", "DELETE FROM users WHERE id = ?", idC)
	mustExec(t, db, "DELETE FROM users WHERE id = ?", idA)
	// Deleting or changing a plain user is never blocked.
	mustExec(t, db, "UPDATE users SET role = 'user', disabled = 1 WHERE id = ?", idB)
	mustExec(t, db, "DELETE FROM users WHERE id = ?", idB)
	if n := count(t, db, "users"); n != 1 {
		t.Fatalf("%d users left", n)
	}
}

func TestSessionsFollowTheirUserAndStoreOnlyAHash(t *testing.T) {
	db := openTemp(t)
	addUser(t, db, idA, "alice", "admin", 0)
	addUser(t, db, idB, "bob", "user", 0)
	ins := "INSERT INTO sessions (id, token_hash, user_id, created_at, last_seen, expires_at) VALUES (?, ?, ?, 1, 1, 2)"
	mustExec(t, db, ins, idA, make([]byte, 32), idB)
	expectFail(t, db, "", ins, idB, make([]byte, 31), idB)                       // not a SHA-256
	expectFail(t, db, "UNIQUE", ins, idB, make([]byte, 32), idB)                 // same token
	expectFail(t, db, "FOREIGN KEY", ins, idB, append(make([]byte, 31), 1), idC) // no such user
	if n := count(t, db, "sessions"); n != 1 {
		t.Fatalf("%d sessions", n)
	}
	mustExec(t, db, "DELETE FROM users WHERE id = ?", idB)
	if n := count(t, db, "sessions"); n != 0 {
		t.Fatalf("a deleted user's session survived (%d)", n)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, "INSERT INTO audit_log (at, actor_name, action) VALUES (strftime('%s','now'), 'cli', 'x')")
	expectFail(t, db, "append-only", "UPDATE audit_log SET action = 'y'")
	expectFail(t, db, "append-only", "DELETE FROM audit_log")
	expectFail(t, db, "", "INSERT INTO audit_log (at, actor_name, action, detail) VALUES (1, 'cli', 'x', 'not json')")
	expectFail(t, db, "", "INSERT INTO audit_log (at, actor_name, action) VALUES (1, 'cli', '')")
	// A deleted user does not take their history with them.
	addUser(t, db, idA, "alice", "admin", 0)
	addUser(t, db, idB, "bob", "admin", 0)
	mustExec(t, db, "INSERT INTO audit_log (at, actor_id, actor_name, action) VALUES (strftime('%s','now'), ?, 'alice', 'x')", idA)
	mustExec(t, db, "DELETE FROM users WHERE id = ?", idA)
	if n := count(t, db, "audit_log"); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestHealth(t *testing.T) {
	db := openTemp(t)
	if err := db.Health(ctx(t)); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := db.Health(ctx(t)); err == nil {
		t.Fatal("a closed database reported healthy")
	}
}
