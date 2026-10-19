package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// cheap makes hashing fast in tests; the parameters are still stored and checked.
var cheap = Params{Memory: 8, Time: 1, Threads: 1}

const (
	goodPW  = "correct-battery-staple-1"
	otherPW = "another-long-passphrase-2"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t   *testing.T
	s   *Service
	db  *store.DB
	clk *clock
	dir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "wheelhouse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := &clock{t: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	return &env{t: t, db: db, clk: clk, dir: dir, s: New(db, Options{Clock: clk.now, Params: cheap, ConfigDir: dir})}
}

func (e *env) ctx() context.Context { return context.Background() }

var client = ClientMeta{Addr: "192.0.2.10", UserAgent: "test-agent"}

// admin creates the first administrator through the setup flow.
func (e *env) admin(name string) User {
	e.t.Helper()
	if err := e.s.PrepareSetup(e.ctx()); err != nil {
		e.t.Fatal(err)
	}
	tok, err := os.ReadFile(filepath.Join(e.dir, SetupTokenFile))
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.s.Setup(e.ctx(), strings.TrimSpace(string(tok)), name, goodPW, client)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) user(name string, role Role) User {
	e.t.Helper()
	u, err := e.s.CreateUser(e.ctx(), Actor{Name: "test"}, NewUser{Username: name, Password: goodPW, Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) login(name, pw string) (*LoginResult, error) {
	return e.s.Login(e.ctx(), name, pw, client)
}

func (e *env) mustLogin(name string) *LoginResult {
	e.t.Helper()
	r, err := e.login(name, goodPW)
	if err != nil {
		e.t.Fatalf("login %s: %v", name, err)
	}
	return r
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(e.ctx(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) actions() []string {
	e.t.Helper()
	es, err := e.s.ListAudit(e.ctx(), 1000, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for i := len(es) - 1; i >= 0; i-- {
		out = append(out, es[i].Action)
	}
	return out
}

// ---------------------------------------------------------------- passwords

func TestHashRoundTripStoresItsParameters(t *testing.T) {
	h := newHasher(Params{Memory: 16, Time: 2, Threads: 1})
	enc, err := h.Hash(context.Background(), goodPW)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=16,t=2,p=1$") || strings.Contains(enc, goodPW) {
		t.Fatalf("unexpected encoding %q", enc)
	}
	ok, p, err := h.Verify(context.Background(), enc, goodPW, nil)
	if err != nil || !ok || p != (Params{Memory: 16, Time: 2, Threads: 1}) {
		t.Fatalf("verify: %v %v %+v", ok, err, p)
	}
	if ok, _, _ := h.Verify(context.Background(), enc, goodPW+"x", nil); ok {
		t.Fatal("a wrong password verified")
	}
	enc2, _ := h.Hash(context.Background(), goodPW)
	if enc == enc2 {
		t.Fatal("two hashes of one password are identical: no per-hash salt")
	}
	// A hash made with other parameters still verifies; it is reported as weaker.
	old := newHasher(Params{Memory: 8, Time: 1, Threads: 1})
	oldEnc, _ := old.Hash(context.Background(), goodPW)
	ok, p, _ = h.Verify(context.Background(), oldEnc, goodPW, nil)
	if !ok || !p.Weaker(h.params) || h.params.Weaker(p) {
		t.Fatalf("old hash: ok=%v params=%+v", ok, p)
	}
}

func TestMalformedStoredHashesNeverMatchAndNeverCrash(t *testing.T) {
	h := newHasher(cheap)
	for _, bad := range []string{
		"", "plaintext", "$argon2id$", "$argon2i$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=18$m=8,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5", // would allocate 95 GiB
		"$argon2id$v=19$m=8,t=999999,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=8,t=1,p=0$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$!!!$a2V5a2V5a2V5a2V5a2V5",
	} {
		ok, _, err := h.Verify(context.Background(), bad, "x", nil)
		if ok || err == nil {
			t.Errorf("%q: ok=%v err=%v", bad, ok, err)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	long := strings.Repeat("ab1", 400) // 1200 bytes
	for pw, want := range map[string]string{
		"short":                        "at least 12",
		"elevenchars":                  "at least 12",
		"ÄÄÄÄÄÄÄÄÄÄÄ":                  "at least 12", // 11 characters, 22 bytes: length counts characters
		"password1234":                 "most commonly used",
		"PassWord1234":                 "most commonly used", // case does not help
		"wheelhouse1234":               "most commonly used",
		"alice@example.com":            "", // fine (and not the username below)
		"aaaaaaaaaaaaaaaa":             "too simple",
		"abababababababab":             "too simple",
		long:                           "too long",
		"has\x00nul-byte-here-123":     "not allowed",
		"bad\xffutf8-bytes-here-12":    "not allowed",
		"correct horse battery staple": "",
		"ÄÖÜäöüßÄÖÜäöüß":               "",
	} {
		err := ValidatePassword(pw, "alice")
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", pw, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: %v, want %q", pw, err, want)
		}
	}
	if err := ValidatePassword("Alice-Alice-Alice", "alice-alice-alice"); err == nil || !strings.Contains(err.Error(), "username") {
		t.Errorf("password equal to the username (any case): %v", err)
	}
	var pe *PolicyError
	if !errors.As(ValidatePassword("short", "x"), &pe) {
		t.Error("policy failures are PolicyError")
	}
	if n := len(commonPasswords()); n < 1000 {
		t.Errorf("the common password list has %d entries", n)
	}
	for p := range commonPasswords() {
		if p != strings.ToLower(p) || len(p) < MinPasswordLength {
			t.Errorf("list entry %q is not lowercase or is shorter than the minimum", p)
		}
	}
}

func TestUsernameRules(t *testing.T) {
	for name, ok := range map[string]bool{
		"alice": true, "Al_ice-1.x@example.com": true, "abc": true, "ab": false, "": false,
		"-alice": false, "al ice": false, "alice;drop": false, "ali\x00ce": false, "аlice": false /* Cyrillic a */, strings.Repeat("a", 65): false, strings.Repeat("a", 64): true,
	} {
		if got := ValidateUsername(name) == nil; got != ok {
			t.Errorf("%q: valid=%v, want %v", name, got, ok)
		}
	}
}

func TestReservedUsernamesAreRefusedAtCreateAndRenameButStillLogIn(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	for _, n := range []string{"local-network", "CLI", "System", "anonymous", "Local-Network"} {
		if err := ValidateUsername(n); err == nil {
			t.Errorf("%q accepted", n)
		}
		if _, err := e.s.CreateUser(e.ctx(), Actor{Name: "t"}, NewUser{Username: n, Password: goodPW, Role: RoleUser}); err == nil {
			t.Errorf("created %q", n)
		}
	}
	bob := e.user("bobby", RoleUser)
	if _, err := e.s.UpdateUser(e.ctx(), Actor{Name: "t"}, bob.ID, UserPatch{Username: ptr("system")}); err == nil {
		t.Error("renamed to a reserved name")
	}
	// An account that already has such a name (from an older database) keeps working.
	if _, err := e.db.Exec(e.ctx(), "UPDATE users SET username = 'cli' WHERE id = ?", bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.login("cli", goodPW); err != nil {
		t.Fatalf("existing account: %v", err)
	}
}

func TestGeneratedPasswordsPassThePolicyAndDiffer(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw, err := GeneratePassword()
		if err != nil || len(pw) != 20 || ValidatePassword(pw, "alice") != nil || seen[pw] {
			t.Fatalf("%q %v", pw, err)
		}
		seen[pw] = true
	}
}

// ---------------------------------------------------------------- setup

// Without a prepared token (the server prepares one at start whenever no account
// exists) nothing opens setup: an empty token is not a match for an empty token.
func TestSetupWithoutAPreparedTokenIsRefused(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "anything"} {
		if _, err := e.s.Setup(e.ctx(), tok, "alice", goodPW, client); !errors.Is(err, ErrBadSetupToken) {
			t.Errorf("token %q with none prepared: %v", tok, err)
		}
	}
	if e.count("SELECT count(*) FROM users") != 0 {
		t.Fatal("a user was created")
	}
}

func TestSetupNeedsTheTokenAndWorksOnce(t *testing.T) {
	e := newEnv(t)
	if need, _ := e.s.NeedsSetup(e.ctx()); !need {
		t.Fatal("a fresh database does not need setup")
	}
	if err := e.s.PrepareSetup(e.ctx()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dir, SetupTokenFile)
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", st, err)
	}
	raw, _ := os.ReadFile(path)
	token := strings.TrimSpace(string(raw))
	if len(token) != tokenLen {
		t.Fatalf("token %q", token)
	}

	for _, bad := range []string{"", "wrong", token + "x", token[:len(token)-1], strings.ToUpper(token)} {
		if _, err := e.s.Setup(e.ctx(), bad, "alice", goodPW, client); !errors.Is(err, ErrBadSetupToken) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	if n := e.count("SELECT count(*) FROM users"); n != 0 {
		t.Fatalf("%d users created without the token", n)
	}
	// With the token, a bad username or password is refused and the token survives.
	if _, err := e.s.Setup(e.ctx(), token, "a b", goodPW, client); err == nil {
		t.Fatal("bad username accepted")
	}
	if _, err := e.s.Setup(e.ctx(), token, "alice", "short", client); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the token file went away before setup succeeded")
	}

	u, err := e.s.Setup(e.ctx(), token, "alice", goodPW, client)
	if err != nil || u.Role != RoleAdmin || u.Username != "alice" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the token file survived a successful setup")
	}
	// Once there is a user, setup is over for good, token or not.
	if _, err := e.s.Setup(e.ctx(), token, "mallory", goodPW, client); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("second setup: %v", err)
	}
	if need, _ := e.s.NeedsSetup(e.ctx()); need {
		t.Fatal("still needs setup")
	}
	if got := e.actions(); len(got) != 1 || got[0] != "setup" {
		t.Fatalf("audit: %v", got)
	}
}

func TestSetupTokensAreFreshAtEveryStartAndOldFilesAreRemoved(t *testing.T) {
	e := newEnv(t)
	_ = e.s.PrepareSetup(e.ctx())
	first, _ := os.ReadFile(filepath.Join(e.dir, SetupTokenFile))
	_ = e.s.PrepareSetup(e.ctx())
	second, _ := os.ReadFile(filepath.Join(e.dir, SetupTokenFile))
	if bytes.Equal(first, second) {
		t.Fatal("a restart reused the setup token")
	}
	if _, err := e.s.Setup(e.ctx(), strings.TrimSpace(string(first)), "alice", goodPW, client); !errors.Is(err, ErrBadSetupToken) {
		t.Fatalf("the previous run's token still works: %v", err)
	}
	// With users present, a leftover file is deleted at start.
	e.user("bob", RoleAdmin)
	if err := os.WriteFile(filepath.Join(e.dir, SetupTokenFile), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := New(e.db, Options{Clock: e.clk.now, Params: cheap, ConfigDir: e.dir})
	if err := s2.PrepareSetup(e.ctx()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, SetupTokenFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a stale token file was left with users present")
	}
}

func TestSetupTokenGuessesLockTheAddress(t *testing.T) {
	e := newEnv(t)
	_ = e.s.PrepareSetup(e.ctx())
	raw, _ := os.ReadFile(filepath.Join(e.dir, SetupTokenFile))
	for range setupThreshold {
		_, _ = e.s.Setup(e.ctx(), "guess", "alice", goodPW, client)
	}
	// Locked: even the right token is refused from that address until it passes.
	if _, err := e.s.Setup(e.ctx(), strings.TrimSpace(string(raw)), "alice", goodPW, client); !errors.Is(err, ErrBadSetupToken) {
		t.Fatalf("locked address: %v", err)
	}
	e.clk.advance(lockBase + time.Second)
	if _, err := e.s.Setup(e.ctx(), strings.TrimSpace(string(raw)), "alice", goodPW, client); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
}

// The race, made deterministic: another request creates the account after this
// one passed every check but before its transaction. The transaction's own
// count is what must stop it.
func TestSetupLosingTheRaceCreatesNothing(t *testing.T) {
	e := newEnv(t)
	_ = e.s.PrepareSetup(e.ctx())
	raw, _ := os.ReadFile(filepath.Join(e.dir, SetupTokenFile))
	e.s.beforeSetupTx = func() {
		mustExec(t, e.db, `INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at)
			VALUES (?, 'winner', '$argon2id$x', 'admin', 1, 1)`, strings.Repeat("e", 32))
	}
	if _, err := e.s.Setup(e.ctx(), strings.TrimSpace(string(raw)), "loser-user", goodPW, client); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("%v", err)
	}
	if n := e.count("SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("%d users", n)
	}
}

func TestConcurrentSetupCreatesExactlyOneAdmin(t *testing.T) {
	e := newEnv(t)
	_ = e.s.PrepareSetup(e.ctx())
	raw, _ := os.ReadFile(filepath.Join(e.dir, SetupTokenFile))
	token := strings.TrimSpace(string(raw))
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := "admin" + string(rune('a'+i))
			if _, err := e.s.Setup(e.ctx(), token, name, goodPW, ClientMeta{Addr: "192.0.2." + string(rune('1'+i))}); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount != 1 || e.count("SELECT count(*) FROM users") != 1 {
		t.Fatalf("%d setups succeeded, %d users", okCount, e.count("SELECT count(*) FROM users"))
	}
}

// ---------------------------------------------------------------- login

func TestLoginAndTheStoredFormOfTheSession(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	r := e.mustLogin("ALICE") // names are case-insensitive
	if r.User.Username != "alice" || len(r.Token) != tokenLen || r.CSRF == "" || r.CSRF == r.Token {
		t.Fatalf("%+v", r)
	}
	// Only a hash of the token is stored, and the CSRF token is not stored at all.
	var stored []byte
	if err := e.db.QueryRow(e.ctx(), "SELECT token_hash FROM sessions WHERE id = ?", r.Session.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	// Computed here, not with the package's own helper, so a change to that helper cannot hide.
	if want := sha256.Sum256([]byte(r.Token)); !bytes.Equal(stored, want[:]) || bytes.Contains(stored, []byte(r.Token[:16])) {
		t.Fatal("token_hash is not the SHA-256 of the token")
	}
	for _, table := range []string{"sessions", "users", "audit_log"} {
		rows, err := e.db.Query(e.ctx(), "SELECT * FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for _, v := range vals {
				var s string
				switch x := v.(type) {
				case string:
					s = x
				case []byte:
					s = string(x)
				}
				for _, secret := range []string{r.Token, r.CSRF, goodPW} {
					if strings.Contains(s, secret) {
						t.Errorf("table %s holds a secret in the clear", table)
					}
				}
			}
		}
		_ = rows.Close()
	}
	id, err := e.s.Authenticate(e.ctx(), r.Token)
	if err != nil || id.Username != "alice" || id.Role != RoleAdmin || id.CSRF != r.CSRF || id.SessionID != r.Session.ID {
		t.Fatalf("%+v %v", id, err)
	}
	if got := id.ExpiresAt.Sub(e.clk.now()); got != IdleTimeout {
		t.Fatalf("expires in %v", got)
	}
	if got := id.AbsoluteExpiresAt.Sub(e.clk.now()); got != AbsoluteTimeout {
		t.Fatalf("absolute %v", got)
	}
}

func TestEveryFailedLoginLooksTheSameAndComputesAHash(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	e.user("bob", RoleUser)
	if _, err := e.s.UpdateUser(e.ctx(), Actor{Name: "t"}, e.userID("bob"), UserPatch{Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	// Lock carol's name before she exists, and mallory's address would be separate.
	for range accountThreshold {
		_, _ = e.login("carol", "wrong-password-1")
	}
	cases := map[string][2]string{
		"unknown user":   {"nobody", goodPW},
		"wrong password": {"alice", "wrong-password-1"},
		"disabled":       {"bob", goodPW},
	}
	for name, c := range cases {
		before := e.s.h.computed.Load()
		_, err := e.login(c[0], c[1])
		if err != ErrInvalidCredentials { //nolint:errorlint // the exact value is the point
			t.Errorf("%s: %v", name, err)
		}
		if got := e.s.h.computed.Load() - before; got != 1 {
			t.Errorf("%s: %d hashes computed, want 1 (no timing difference between real and made-up users)", name, got)
		}
	}
	// A locked account answers the same way but spends no hash at all: nothing
	// observable differs, and a flood against it costs nothing.
	before := e.s.h.computed.Load()
	if _, err := e.login("carol", goodPW); err != ErrInvalidCredentials { //nolint:errorlint
		t.Errorf("locked: %v", err)
	}
	if got := e.s.h.computed.Load() - before; got != 0 {
		t.Errorf("a locked account still cost %d hashes", got)
	}
	if _, err := e.login("alice", goodPW); err != nil {
		t.Fatalf("a failure of another name locked alice: %v", err)
	}
}

func (e *env) userID(name string) string {
	e.t.Helper()
	us, err := e.s.ListUsers(e.ctx())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, u := range us {
		if strings.EqualFold(u.Username, name) {
			return u.ID
		}
	}
	e.t.Fatalf("no user %s", name)
	return ""
}

func ptr[T any](v T) *T { return &v }

func TestAccountLockoutBacksOffAndLiftsAfterTheTime(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	for i := 1; i < accountThreshold; i++ {
		if _, err := e.login("alice", "wrong-password-1"); err != ErrInvalidCredentials { //nolint:errorlint
			t.Fatal(err)
		}
	}
	// One more typing error in the free allowance would still allow the right password.
	if _, err := e.login("alice", goodPW); err != nil {
		t.Fatalf("the allowance of typing errors: %v", err)
	}
	// (A success cleared the count.) Now fail until locked.
	for range accountThreshold {
		_, _ = e.login("alice", "wrong-password-1")
	}
	if _, err := e.login("alice", goodPW); err != ErrInvalidCredentials { //nolint:errorlint
		t.Fatalf("the right password worked while locked: %v", err)
	}
	e.clk.advance(lockBase - time.Second)
	if _, err := e.login("alice", goodPW); err == nil {
		t.Fatal("still locked just before the end of the lock: it ended early")
	}
	e.clk.advance(2 * time.Second)
	// Unlocked; the next failure doubles the lock.
	if _, err := e.login("alice", "wrong-password-1"); err != ErrInvalidCredentials { //nolint:errorlint
		t.Fatal(err)
	}
	e.clk.advance(lockBase + time.Second)
	if _, err := e.login("alice", goodPW); err == nil {
		t.Fatal("the second lock was not longer than the first")
	}
	e.clk.advance(lockBase + time.Second)
	if _, err := e.login("alice", goodPW); err != nil {
		t.Fatalf("after twice the base: %v", err)
	}
	got := e.actions()
	if !contains(got, "login.lockout") || !contains(got, "login.fail") || !contains(got, "login.ok") {
		t.Fatalf("audit: %v", got)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestLockDurationIsCapped(t *testing.T) {
	th := newThrottle(time.Now)
	var last time.Duration
	for range 40 {
		_, until := th.fail("u:x", 1)
		last = time.Until(until)
	}
	if last > lockMax+time.Second || last < lockMax-time.Second {
		t.Fatalf("lock after many failures: %v", last)
	}
}

func TestAddressLockoutCoversManyNamesAndIPv6Prefixes(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	attacker := ClientMeta{Addr: "2001:db8:1:2:aaaa::1"}
	for i := range addressThreshold {
		_, _ = e.s.Login(e.ctx(), "user"+string(rune('a'+i)), "wrong-password-1", attacker)
	}
	// Same /64, other host bits, real name and the right password: still refused.
	if _, err := e.s.Login(e.ctx(), "alice", goodPW, ClientMeta{Addr: "2001:db8:1:2:bbbb::9"}); err != ErrInvalidCredentials { //nolint:errorlint
		t.Fatalf("same /64: %v", err)
	}
	// A different client is unaffected.
	if _, err := e.s.Login(e.ctx(), "alice", goodPW, ClientMeta{Addr: "192.0.2.77"}); err != nil {
		t.Fatalf("other client: %v", err)
	}
}

func TestThrottleTableIsBoundedAndKeepsLockouts(t *testing.T) {
	th := newThrottle(time.Now)
	th.fail("u:victim", 1) // locked
	for i := range maxEntries + 100 {
		th.fail("u:x"+string(rune(0x100+i%50000))+string(rune('a'+i%26)), 1000)
	}
	if locked, _ := th.locked("u:victim"); !locked {
		t.Fatal("a flood of made-up names evicted a live lockout")
	}
	th.mu.Lock()
	n := len(th.m)
	th.mu.Unlock()
	if n > maxEntries {
		t.Fatalf("%d entries", n)
	}
}

func TestPasswordResetLiftsTheLockout(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	for range accountThreshold {
		_, _ = e.login("alice", "wrong-password-1")
	}
	if _, err := e.login("alice", goodPW); err == nil {
		t.Fatal("not locked")
	}
	e.clk.advance(2 * time.Second)
	// The operator resets the password from the CLI: another process, so another Service over the same file.
	cli := New(e.db, Options{Clock: e.clk.now, Params: cheap})
	pw, err := cli.ResetPasswordCLI(e.ctx(), "Alice")
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.login("alice", pw)
	if err != nil {
		t.Fatalf("a reset password does not unlock the account: %v", err)
	}
	if !r.User.MustChangePassword {
		t.Fatal("the reset password is not marked for change")
	}
}

func TestLoginRehashesWeakerHashes(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	weak, _ := newHasher(Params{Memory: 8, Time: 1, Threads: 1}).Hash(e.ctx(), goodPW)
	s := New(e.db, Options{Clock: e.clk.now, Params: Params{Memory: 16, Time: 2, Threads: 1}})
	mustExec(t, e.db, "UPDATE users SET password_hash = ?", weak)
	if _, err := s.Login(e.ctx(), "alice", goodPW, client); err != nil {
		t.Fatal(err)
	}
	var enc string
	if err := e.db.QueryRow(e.ctx(), "SELECT password_hash FROM users").Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(enc, "m=16,t=2,p=1") {
		t.Fatalf("not rehashed: %s", enc)
	}
	if _, err := s.Login(e.ctx(), "alice", goodPW, client); err != nil {
		t.Fatalf("after the rehash: %v", err)
	}
}

func mustExec(t *testing.T, db *store.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- sessions

func TestEverySessionIsNewAndOldOnesKeepWorking(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	a, b := e.mustLogin("alice"), e.mustLogin("alice")
	if a.Token == b.Token || a.Session.ID == b.Session.ID || a.CSRF == b.CSRF {
		t.Fatal("logins share a token, id or CSRF token: a session is not rotated at login")
	}
	for _, r := range []*LoginResult{a, b} {
		if _, err := e.s.Authenticate(e.ctx(), r.Token); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionsExpireWhenIdleAndAtTheAbsoluteLimit(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	r := e.mustLogin("alice")
	ok := func(r *LoginResult) bool {
		_, err := e.s.Authenticate(e.ctx(), r.Token)
		return err == nil
	}

	e.clk.advance(IdleTimeout - time.Minute)
	if !ok(r) {
		t.Fatal("expired before the idle timeout")
	}
	// That request counted as activity: idle again from now.
	e.clk.advance(IdleTimeout - time.Minute)
	if !ok(r) {
		t.Fatal("activity did not extend the session")
	}
	e.clk.advance(IdleTimeout)
	if ok(r) {
		t.Fatal("an idle session was accepted after the idle timeout")
	}
	if e.count("SELECT count(*) FROM sessions") != 0 {
		t.Fatal("an expired session was kept")
	}

	// Absolute: kept alive by activity, still ends 30 days after login.
	r = e.mustLogin("alice")
	for elapsed := time.Duration(0); elapsed < AbsoluteTimeout-6*time.Hour; elapsed += 6 * time.Hour {
		e.clk.advance(6 * time.Hour)
		if !ok(r) {
			t.Fatalf("expired early after %v", elapsed)
		}
	}
	e.clk.advance(7 * time.Hour)
	if ok(r) {
		t.Fatal("a session older than the absolute limit was accepted")
	}
}

func TestLastSeenIsWrittenAtMostOncePerMinute(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	r := e.mustLogin("alice")
	seen := func() int64 {
		var v int64
		_ = e.db.QueryRow(e.ctx(), "SELECT last_seen FROM sessions WHERE id = ?", r.Session.ID).Scan(&v)
		return v
	}
	start := seen()
	for range 30 {
		e.clk.advance(time.Second)
		if _, err := e.s.Authenticate(e.ctx(), r.Token); err != nil {
			t.Fatal(err)
		}
	}
	if seen() != start {
		t.Fatal("last_seen written within the first minute")
	}
	e.clk.advance(31 * time.Second)
	_, _ = e.s.Authenticate(e.ctx(), r.Token)
	if seen() != start+61 {
		t.Fatalf("last_seen %d, want %d", seen()-start, 61)
	}
	e.clk.advance(10 * time.Second)
	_, _ = e.s.Authenticate(e.ctx(), r.Token)
	if seen() != start+61 {
		t.Fatal("written again within a minute")
	}
}

func TestAuthenticateRejectsGarbageAndRevokedSessions(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	r := e.mustLogin("alice")
	for _, tok := range []string{"", "x", strings.Repeat("a", tokenLen), strings.Repeat("a", tokenLen+1), r.Token[:tokenLen-1] + "!", r.CSRF, r.Session.ID} {
		if _, err := e.s.Authenticate(e.ctx(), tok); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%q: %v", tok, err)
		}
	}
	if err := e.s.Logout(e.ctx(), Actor{ID: r.User.ID, Name: "alice"}, r.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Authenticate(e.ctx(), r.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after logout: %v", err)
	}
}

func TestPasswordChangeRotatesAndRevokesEverySession(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	bob := e.user("bob", RoleUser)
	bobSession := e.mustLogin("bob")
	a1, a2 := e.mustLogin("alice"), e.mustLogin("alice")
	id, _ := e.s.Authenticate(e.ctx(), a1.Token)

	if _, err := e.s.ChangePassword(e.ctx(), id, "wrong-current-pw-1", otherPW, client); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong current password: %v", err)
	}
	if _, err := e.s.ChangePassword(e.ctx(), id, goodPW, "short", client); err == nil {
		t.Fatal("weak new password accepted")
	}
	if _, err := e.s.ChangePassword(e.ctx(), id, goodPW, goodPW, client); err == nil {
		t.Fatal("unchanged password accepted")
	}
	n, err := e.s.ChangePassword(e.ctx(), id, goodPW, otherPW, client)
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*LoginResult{"the changing session": a1, "another session": a2} {
		if _, err := e.s.Authenticate(e.ctx(), r.Token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s survived a password change: %v", name, err)
		}
	}
	if n.Token == a1.Token || n.CSRF == a1.CSRF {
		t.Fatal("the session was not rotated")
	}
	if _, err := e.s.Authenticate(e.ctx(), n.Token); err != nil {
		t.Fatalf("the new session: %v", err)
	}
	if _, err := e.s.Authenticate(e.ctx(), bobSession.Token); err != nil {
		t.Fatalf("someone else's session was revoked: %v", err)
	}
	_ = bob
	if _, err := e.login("alice", goodPW); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := e.login("alice", otherPW); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordGuessesAreThrottledAndClearTheMustChangeFlag(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	e.user("bob", RoleUser)
	if err := e.s.SetPassword(e.ctx(), Actor{Name: "t"}, e.userID("bob"), otherPW); err != nil {
		t.Fatal(err)
	}
	r, err := e.login("bob", otherPW)
	if err != nil || !r.User.MustChangePassword {
		t.Fatalf("%+v %v", r, err)
	}
	id, _ := e.s.Authenticate(e.ctx(), r.Token)
	if !id.MustChangePassword {
		t.Fatal("identity lost the flag")
	}
	for range accountThreshold {
		_, _ = e.s.ChangePassword(e.ctx(), id, "wrong-current-pw-1", goodPW, client)
	}
	if _, err := e.s.ChangePassword(e.ctx(), id, otherPW, goodPW, client); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("guessing the current password is not throttled: %v", err)
	}
	e.clk.advance(lockBase + time.Second)
	n, err := e.s.ChangePassword(e.ctx(), id, otherPW, goodPW, client)
	if err != nil || n.User.MustChangePassword {
		t.Fatalf("%+v %v", n, err)
	}
}

// ---------------------------------------------------------------- users

func TestAdminResetEndsSessionsAndForcesAChange(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	e.user("bob", RoleUser)
	b := e.mustLogin("bob")
	if err := e.s.SetPassword(e.ctx(), Actor{Name: "alice"}, e.userID("bob"), "short"); err == nil {
		t.Fatal("weak admin-set password accepted")
	}
	if err := e.s.SetPassword(e.ctx(), Actor{Name: "alice"}, e.userID("bob"), otherPW); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Authenticate(e.ctx(), b.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a reset left the session alive: %v", err)
	}
	if err := e.s.SetPassword(e.ctx(), Actor{Name: "alice"}, strings.Repeat("0", 32), otherPW); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestTheLastAdminSurvivesCodeAndTriggers(t *testing.T) {
	for _, dropTriggers := range []bool{false, true} {
		name := "with the trigger"
		if dropTriggers {
			name = "code check alone"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			alice := e.admin("alice")
			bob := e.user("bob", RoleUser)
			if dropTriggers {
				mustExec(t, e.db, "DROP TRIGGER users_keep_last_admin_delete")
				mustExec(t, e.db, "DROP TRIGGER users_keep_last_admin_update")
			}
			a := Actor{Name: "t"}
			if err := e.s.DeleteUser(e.ctx(), a, alice.ID); !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("delete: %v", err)
			}
			if _, err := e.s.UpdateUser(e.ctx(), a, alice.ID, UserPatch{Role: ptr(RoleUser)}); !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("demote: %v", err)
			}
			if _, err := e.s.UpdateUser(e.ctx(), a, alice.ID, UserPatch{Disabled: ptr(true)}); !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("disable: %v", err)
			}
			if _, err := e.s.UpdateUser(e.ctx(), a, alice.ID, UserPatch{Role: ptr(RoleUser), Disabled: ptr(true)}); !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("both: %v", err)
			}
			// Promote bob, and either may go.
			if _, err := e.s.UpdateUser(e.ctx(), a, bob.ID, UserPatch{Role: ptr(RoleAdmin)}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.s.UpdateUser(e.ctx(), a, alice.ID, UserPatch{Role: ptr(RoleUser)}); err != nil {
				t.Fatal(err)
			}
			if err := e.s.DeleteUser(e.ctx(), a, bob.ID); !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("bob is now the last admin: %v", err)
			}
		})
	}
}

func TestUserManagement(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	a := Actor{Name: "alice"}
	if _, err := e.s.CreateUser(e.ctx(), a, NewUser{Username: "Bob", Password: goodPW, Role: RoleUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateUser(e.ctx(), a, NewUser{Username: "bob", Password: goodPW, Role: RoleUser}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate name differing in case: %v", err)
	}
	if _, err := e.s.CreateUser(e.ctx(), a, NewUser{Username: "carol", Password: goodPW, Role: "root"}); err == nil {
		t.Fatal("unknown role accepted")
	}
	if _, err := e.s.CreateUser(e.ctx(), a, NewUser{Username: "carol", Password: "carol-carol-carol", Role: RoleUser}); err != nil {
		t.Fatal(err)
	}
	bob := e.userID("bob")
	b := e.mustLogin("bob")
	if _, err := e.s.UpdateUser(e.ctx(), a, bob, UserPatch{Username: ptr("carol")}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("rename onto another: %v", err)
	}
	if _, err := e.s.UpdateUser(e.ctx(), a, bob, UserPatch{}); err == nil {
		t.Fatal("empty patch accepted")
	}
	// A role change applies to the live session at once (the role is read per request).
	if _, err := e.s.UpdateUser(e.ctx(), a, bob, UserPatch{Role: ptr(RoleAdmin)}); err != nil {
		t.Fatal(err)
	}
	if id, _ := e.s.Authenticate(e.ctx(), b.Token); id == nil || id.Role != RoleAdmin {
		t.Fatalf("%+v", id)
	}
	if _, err := e.s.UpdateUser(e.ctx(), a, bob, UserPatch{Role: ptr(RoleUser)}); err != nil {
		t.Fatal(err)
	}
	if id, _ := e.s.Authenticate(e.ctx(), b.Token); id == nil || id.Role != RoleUser {
		t.Fatalf("demotion not immediate: %+v", id)
	}
	// Disabling ends the sessions and blocks login; enabling restores login.
	if _, err := e.s.UpdateUser(e.ctx(), a, bob, UserPatch{Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Authenticate(e.ctx(), b.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("a disabled user's session works")
	}
	if _, err := e.login("bob", goodPW); err == nil {
		t.Fatal("a disabled user logged in")
	}
	if _, err := e.s.UpdateUser(e.ctx(), a, bob, UserPatch{Disabled: ptr(false), Username: ptr("robert")}); err != nil {
		t.Fatal(err)
	}
	// Enabling the account again does not bring the old session back to life.
	if _, err := e.s.Authenticate(e.ctx(), b.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("a session from before the account was disabled works again")
	}
	if e.count("SELECT count(*) FROM sessions WHERE user_id = ?", bob) != 0 {
		t.Fatal("a disabled user's sessions were kept")
	}
	if _, err := e.login("robert", goodPW); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteUser(e.ctx(), a, bob); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteUser(e.ctx(), a, bob); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if e.count("SELECT count(*) FROM sessions WHERE user_id = ?", bob) != 0 {
		t.Fatal("a deleted user's sessions remain")
	}
	if _, err := e.s.GetUser(e.ctx(), bob); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSessionListingAndRevocationAreScopedToTheUser(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	e.user("bob", RoleUser)
	a, b := e.mustLogin("alice"), e.mustLogin("bob")
	list, err := e.s.ListSessions(e.ctx(), a.User.ID)
	if err != nil || len(list) != 1 || list[0].ID != a.Session.ID || list[0].UserAgent != "test-agent" || list[0].IP != "192.0.2.10" {
		t.Fatalf("%+v %v", list, err)
	}
	// Bob cannot end alice's session by naming it.
	if err := e.s.RevokeSession(e.ctx(), Actor{Name: "bob"}, b.User.ID, a.Session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user revoke: %v", err)
	}
	if _, err := e.s.Authenticate(e.ctx(), a.Token); err != nil {
		t.Fatal("alice's session was revoked by bob")
	}
	if err := e.s.RevokeSession(e.ctx(), Actor{Name: "alice"}, a.User.ID, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	e.mustLogin("bob")
	n, err := e.s.RevokeAllSessions(e.ctx(), Actor{Name: "alice"}, b.User.ID)
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := e.s.RevokeAllSessions(e.ctx(), Actor{Name: "alice"}, strings.Repeat("0", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Expired sessions do not show.
	r := e.mustLogin("alice")
	e.clk.advance(IdleTimeout + time.Second)
	if list, _ := e.s.ListSessions(e.ctx(), r.User.ID); len(list) != 0 {
		t.Fatalf("expired session listed: %+v", list)
	}
}

func TestAuditTrailHasNoSecretsAndCannotBeEdited(t *testing.T) {
	e := newEnv(t)
	e.admin("alice")
	e.user("bob", RoleUser)
	_, _ = e.login("alice", "wrong-password-1")
	r := e.mustLogin("alice")
	id, _ := e.s.Authenticate(e.ctx(), r.Token)
	_, _ = e.s.ChangePassword(e.ctx(), id, goodPW, otherPW, client)
	e.s.Record(e.ctx(), id.Actor("192.0.2.9"), "library.delete", "radarr/7", map[string]any{"instanceId": "radarr", "id": 7})

	entries, err := e.s.ListAudit(e.ctx(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"library.delete", "password.change", "login.ok", "login.fail", "user.create", "setup"}
	if got := e.actions(); strings.Join(got, ",") != strings.Join(reverse(want), ",") {
		t.Fatalf("audit actions %v", got)
	}
	for _, en := range entries {
		all := en.Action + en.Target + en.ActorName + en.Address + string(en.Detail)
		for _, secret := range []string{goodPW, otherPW, "wrong-password-1", r.Token, r.CSRF} {
			if strings.Contains(all, secret) {
				t.Errorf("audit entry %d holds a secret", en.ID)
			}
		}
	}
	if entries[0].Address != "192.0.2.9" || entries[0].ActorName != "alice" || entries[0].ActorID == "" {
		t.Fatalf("%+v", entries[0])
	}
	if _, err := e.db.Exec(e.ctx(), "DELETE FROM audit_log"); err == nil {
		t.Fatal("audit rows can be deleted")
	}
	// Paging.
	page, _ := e.s.ListAudit(e.ctx(), 2, 0)
	next, _ := e.s.ListAudit(e.ctx(), 2, page[1].ID)
	if len(page) != 2 || len(next) != 2 || next[0].ID >= page[1].ID {
		t.Fatalf("paging: %+v %+v", page, next)
	}
}

func reverse(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[len(xs)-1-i] = x
	}
	return out
}

func TestCSRFTokenIsDerivedNotStoredAndPerSession(t *testing.T) {
	a, b := newToken(), newToken()
	if CSRFToken(a) == CSRFToken(b) || CSRFToken(a) != CSRFToken(string(append([]byte(nil), a...))) || CSRFToken(a) == a {
		t.Fatal("CSRF token derivation")
	}
	if bytes.Equal(hashToken(a), []byte(CSRFToken(a))) {
		t.Fatal("the CSRF token equals the stored hash: a database copy would reveal it")
	}
}

func TestHealthyReflectsTheDatabase(t *testing.T) {
	e := newEnv(t)
	if err := e.s.Healthy(e.ctx()); err != nil {
		t.Fatal(err)
	}
	_ = e.db.Close()
	if err := e.s.Healthy(e.ctx()); err == nil {
		t.Fatal("healthy with a closed database")
	}
}

var _ = sql.ErrNoRows

// ---------------------------------------------------------------- throttle races

// slow makes one verification take a few milliseconds, so a burst of parallel
// attempts really overlaps.
var slow = Params{Memory: 8 * 1024, Time: 2, Threads: 1}

func newSlowEnv(t *testing.T) *env {
	e := newEnv(t)
	e.s = New(e.db, Options{Clock: e.clk.now, Params: slow, ConfigDir: e.dir})
	return e
}

// burst runs n logins at once, released together, and returns how many
// succeeded.
func (e *env) burst(n int, f func(i int) error) (ok int) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if f(i) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	return ok
}

func (e *env) assertNoLeaks() {
	e.t.Helper()
	e.s.th.mu.Lock()
	n := len(e.s.th.inflight)
	e.s.th.mu.Unlock()
	if n != 0 || len(e.s.h.sem) != 0 || e.s.h.waiting.Load() != 0 {
		e.t.Fatalf("leaked: %d reservations, %d hash slots, %d waiting", n, len(e.s.h.sem), e.s.h.waiting.Load())
	}
}

func TestParallelGuessesCannotPassTheAccountLock(t *testing.T) {
	e := newSlowEnv(t)
	e.admin("alice")
	before := e.s.h.computed.Load()
	// 100 parallel wrong guesses: exactly the allowance is verified.
	ok := e.burst(100, func(i int) error {
		_, err := e.login("alice", fmt.Sprintf("wrong-guess-%d-xxxx", i))
		return err
	})
	if ok != 0 {
		t.Fatal("a wrong password logged in")
	}
	if got := e.s.h.computed.Load() - before; got != accountThreshold {
		t.Fatalf("%d verifications for 100 parallel guesses, want exactly %d", got, accountThreshold)
	}
	// The account is locked now: the right password is refused and costs no hash.
	before = e.s.h.computed.Load()
	if _, err := e.login("alice", goodPW); err != ErrInvalidCredentials { //nolint:errorlint
		t.Fatalf("the right password after the burst: %v", err)
	}
	if e.s.h.computed.Load() != before {
		t.Fatal("a locked account was verified")
	}
	// One failure row per verified failure, one lockout row, not one per guess.
	if n := e.count("SELECT count(*) FROM audit_log WHERE action = 'login.fail'"); n != accountThreshold {
		t.Fatalf("%d login.fail rows, want %d", n, accountThreshold)
	}
	if n := e.count("SELECT count(*) FROM audit_log WHERE action = 'login.lockout'"); n != 1 {
		t.Fatalf("%d login.lockout rows, want 1", n)
	}
	e.assertNoLeaks()
	// The lock still lifts, and the slots work again.
	e.clk.advance(lockBase + time.Second)
	if _, err := e.login("alice", goodPW); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
	e.assertNoLeaks()
}

func TestParallelGuessesWithTheRightPasswordLastStillRespectTheLimit(t *testing.T) {
	e := newSlowEnv(t)
	e.admin("alice")
	before := e.s.h.computed.Load()
	ok := e.burst(100, func(i int) error {
		pw := fmt.Sprintf("wrong-guess-%d-xxxx", i)
		if i == 99 {
			pw = goodPW
		}
		_, err := e.login("alice", pw)
		return err
	})
	// The right password is guess 100, but the goroutines race: when it is
	// verified among the first allowed attempts it logs in, and a successful
	// login resets the failure count, which legitimately allows one more window
	// of guesses. Either way the attacker already has the password then. When it
	// is not among them, nothing past the allowance is verified and it is refused.
	limit := int64(accountThreshold)
	if ok > 0 {
		limit = 2 * accountThreshold
	}
	if got := e.s.h.computed.Load() - before; got > limit {
		t.Fatalf("%d verifications, more than the %d allowed", got, limit)
	}
	e.assertNoLeaks()
}

func TestParallelGuessesAreLimitedPerAddressAcrossNames(t *testing.T) {
	e := newSlowEnv(t)
	e.admin("alice")
	before := e.s.h.computed.Load()
	e.burst(100, func(i int) error {
		_, err := e.login(fmt.Sprintf("nobody%d", i), "wrong-guess-xxxx")
		return err
	})
	if got := e.s.h.computed.Load() - before; got != addressThreshold {
		t.Fatalf("%d verifications from one address, want %d", got, addressThreshold)
	}
	e.assertNoLeaks()
}

func TestParallelChangePasswordGuessesAreLimited(t *testing.T) {
	e := newSlowEnv(t)
	e.admin("alice")
	r := e.mustLogin("alice")
	id, _ := e.s.Authenticate(e.ctx(), r.Token)
	before := e.s.h.computed.Load()
	ok := e.burst(60, func(i int) error {
		_, err := e.s.ChangePassword(e.ctx(), id, fmt.Sprintf("wrong-current-%d-xx", i), otherPW, client)
		return err
	})
	if ok != 0 {
		t.Fatal("a wrong current password was accepted")
	}
	if got := e.s.h.computed.Load() - before; got != accountThreshold {
		t.Fatalf("%d verifications, want %d", got, accountThreshold)
	}
	if _, err := e.s.ChangePassword(e.ctx(), id, goodPW, otherPW, client); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("right current password while locked: %v", err)
	}
	e.assertNoLeaks()
}

func TestAFloodAgainstOneAccountDoesNotSlowOtherClients(t *testing.T) {
	e := newSlowEnv(t)
	e.admin("alice")
	e.user("bob", RoleUser)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cm := ClientMeta{Addr: fmt.Sprintf("198.51.100.%d", g+1), UserAgent: "flood"}
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = e.s.Login(e.ctx(), "alice", "wrong-guess-xxxx", cm)
				}
			}
		}()
	}
	time.Sleep(100 * time.Millisecond) // alice is locked and being hammered
	start := time.Now()
	_, err := e.s.Login(e.ctx(), "bob", goodPW, ClientMeta{Addr: "203.0.113.7"})
	took := time.Since(start)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if took > 2*time.Second {
		t.Fatalf("a legitimate login took %v during a flood against another account", took)
	}
	e.assertNoLeaks()
}

func TestVerificationQueueIsBounded(t *testing.T) {
	h := newHasher(cheap)
	for range maxConcurrentHashes {
		h.sem <- struct{}{} // every slot busy
	}
	errs := make(chan error, maxQueuedVerifies)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range maxQueuedVerifies {
		go func() {
			_, _, err := h.Verify(ctx, "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0$QUJDREVGR0hJSktMTU5PUA", "x", nil)
			errs <- err
		}()
	}
	for h.waiting.Load() != maxQueuedVerifies {
		time.Sleep(time.Millisecond)
	}
	if _, _, err := h.Verify(ctx, "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0$QUJDREVGR0hJSktMTU5PUA", "x", nil); !errors.Is(err, errBusy) {
		t.Fatalf("the queue is not bounded: %v", err)
	}
	cancel()
	for range maxQueuedVerifies {
		if err := <-errs; err == nil {
			t.Error("a queued verification of a stored hash succeeded after cancel")
		}
	}
	if h.waiting.Load() != 0 {
		t.Fatal("waiters leaked")
	}
}
