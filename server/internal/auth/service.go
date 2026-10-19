// Package auth is Wheelhouse's accounts: users, Argon2id passwords, sessions,
// login throttling, the one-time setup token and the audit log. It owns the SQL
// for those tables (schema in store/migrations/0002_auth.sql). HTTP is not here:
// package app turns requests into calls on a Service and enforces the route
// levels with the Identity it returns.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// Session lifetimes.
const (
	// IdleTimeout ends a session that made no request for this long.
	IdleTimeout = 12 * time.Hour
	// AbsoluteTimeout ends a session this long after login, however active.
	AbsoluteTimeout = 30 * 24 * time.Hour
	// lastSeenInterval is how often a busy session's last_seen is written: the
	// sliding idle timeout costs at most one database write a minute per session.
	lastSeenInterval = time.Minute
)

// Role is what an account may do.
type Role string

// The two roles.
const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// Errors callers match with errors.Is. Their text is safe to show.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrNotFound           = errors.New("not found")
	ErrLastAdmin          = errors.New("the last administrator cannot be deleted, demoted or disabled")
	ErrUsernameTaken      = errors.New("that username is already taken")
	ErrSetupDone          = errors.New("setup has already been completed")
	ErrBadSetupToken      = errors.New("invalid setup token")
	ErrWrongPassword      = errors.New("the current password is incorrect")
	ErrTooManyAttempts    = errors.New("too many failed attempts; try again later")
)

// User is an account. The password hash never leaves this package.
type User struct {
	ID                 string
	Username           string
	Role               Role
	Disabled           bool
	MustChangePassword bool
	CreatedAt          time.Time
	PasswordChangedAt  time.Time
}

// Session is a login. The token is never part of it.
type Session struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// Identity is who a request is from: the account and the session it came with.
type Identity struct {
	User
	SessionID string
	// CSRF is the token the client must send in X-CSRF-Token on state changes.
	CSRF string
	// ExpiresAt is when the session ends if the user does nothing more (the
	// idle limit, or the absolute one if that comes first).
	ExpiresAt time.Time
	// AbsoluteExpiresAt is the hard limit.
	AbsoluteExpiresAt time.Time
	// Bypass marks the synthetic principal of the local-network bypass: no account,
	// no session (SessionID is empty), role user. Code that needs a real login
	// (password, sessions) must refuse it.
	Bypass bool
}

// BypassUsername is the name shown for, and audited as, the bypass principal.
const BypassUsername = "local-network"

// BypassIdentity is who a request from inside the bypass ranges is: a signed-in
// user with no account behind it. csrf is the stateless token the client sends
// on state changes.
func BypassIdentity(csrf string) *Identity {
	return &Identity{User: User{Username: BypassUsername, Role: RoleUser}, CSRF: csrf, Bypass: true}
}

// Actor is who an audit entry is about: a signed-in account, or "cli" or
// "system", and the client address when there is one.
type Actor struct {
	ID   string
	Name string
	Addr string
	// Via is "bypass" when the local-network bypass acted; it is recorded in the
	// entry's detail.
	Via string
}

// Actor returns the identity as an audit actor.
func (i *Identity) Actor(addr string) Actor {
	a := Actor{ID: i.ID, Name: i.Username, Addr: addr}
	if i.Bypass {
		a.Via = "bypass"
	}
	return a
}

// ClientMeta is what is recorded about the client of a login.
type ClientMeta struct {
	Addr      string
	UserAgent string
}

// Options configure a Service. Everything has a production default; the fields
// exist so tests can control time and cost. They are constructor options, never
// environment variables, so a running server cannot be told to ignore them.
type Options struct {
	// Clock is the time source (default time.Now).
	Clock func() time.Time
	// Params are the Argon2id parameters for new hashes (default DefaultParams).
	Params Params
	// ConfigDir is where the setup token file is written.
	ConfigDir string
	// Logger receives the setup token notice and storage errors (default: discard).
	Logger *slog.Logger
}

// Service is the account system over one database. It is safe for concurrent use.
type Service struct {
	db  *store.DB
	now func() time.Time
	h   *hasher
	th  *throttle
	log *slog.Logger
	dir string
	// dummy is a valid hash of a throwaway password, verified against when a
	// username does not exist, so a login for an unknown name costs the same as
	// one for a real name. It is made at construction, so not even the first
	// such login is faster.
	dummy string

	hasUsers atomic.Bool // once true it stays true: the last admin cannot go

	setupMu    sync.Mutex
	setupToken string
	// beforeSetupTx is nil outside tests, which use it to interleave two setups
	// at the one point where the transaction's own check is what protects.
	beforeSetupTx func()
}

// New returns a Service over db.
func New(db *store.DB, o Options) *Service {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Service{db: db, now: o.Clock, h: newHasher(o.Params), th: newThrottle(o.Clock), log: o.Logger, dir: o.ConfigDir}
	s.dummy, _ = s.h.Hash(context.Background(), newToken())
	s.h.computed.Store(0)
	return s
}

// Healthy reports whether the database answers.
func (s *Service) Healthy(ctx context.Context) error { return s.db.Health(ctx) }

func (s *Service) unix() int64 { return s.now().Unix() }

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (it ends the program)
	return hex.EncodeToString(b[:])
}

// tokenLen is the length of a session token as text (32 bytes, unpadded base64url).
const tokenLen = 43

func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CSRFToken derives a session's CSRF token from its cookie value. Only someone
// holding the cookie can compute it, and the database holds only the SHA-256 of
// the cookie, so a copy of the database yields neither.
func CSRFToken(sessionToken string) string {
	h := sha256.Sum256([]byte("wheelhouse-csrf-v1\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func validTokenShape(token string) bool {
	if len(token) != tokenLen {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// ValidateUsername applies the username rules: 3 to 64 characters, ASCII
// letters, digits and . _ @ -, starting with a letter or digit.
func ValidateUsername(name string) error {
	if !usernameRE.MatchString(name) {
		return &PolicyError{"The username must be 3 to 64 characters: letters, digits and . _ @ - only."}
	}
	if _, bad := reservedUsernames[strings.ToLower(name)]; bad {
		return &PolicyError{"That username is reserved. Choose another."}
	}
	return nil
}

// reservedUsernames are the names the audit log uses for actors that are not
// accounts, so no account can impersonate one. Checked when a name is chosen
// (create, rename), never at login: an existing account with such a name keeps
// working.
var reservedUsernames = map[string]struct{}{"local-network": {}, "cli": {}, "system": {}, "anonymous": {}}

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{2,63}$`)

// ---------------------------------------------------------------- rows

const userCols = "id, username, role, disabled, must_change_password, created_at, password_changed_at"

type scanner interface{ Scan(dest ...any) error }

func scanUser(r scanner) (User, error) {
	var u User
	var role string
	var disabled, must int
	var created, changed int64
	if err := r.Scan(&u.ID, &u.Username, &role, &disabled, &must, &created, &changed); err != nil {
		return User{}, err
	}
	u.Role = Role(role)
	u.Disabled = disabled != 0
	u.MustChangePassword = must != 0
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.PasswordChangedAt = time.Unix(changed, 0).UTC()
	return u, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- setup

// NeedsSetup reports whether no account exists yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	if s.hasUsers.Load() {
		return false, nil
	}
	var n int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		s.hasUsers.Store(true)
		return false, nil
	}
	return true, nil
}

// SetupTokenFile is the name of the file, in the config directory, that holds
// the one-time setup token while no account exists.
const SetupTokenFile = "setup-token"

// PrepareSetup is called once at start. With no account it generates a one-time
// setup token, writes it to <config dir>/setup-token (mode 0600) and logs it
// once, because creating the first administrator needs it: it keeps whoever
// reaches a fresh container first from claiming it. With accounts it removes a
// stale token file. It never returns the token.
func (s *Service) PrepareSetup(ctx context.Context) error {
	need, err := s.NeedsSetup(ctx)
	if err != nil {
		return err
	}
	if !need {
		s.removeTokenFile()
		return nil
	}
	token := newToken()
	s.setupMu.Lock()
	s.setupToken = token
	s.setupMu.Unlock()
	file := ""
	if s.dir != "" {
		if err := writeTokenFile(s.dir, token); err != nil {
			s.log.Warn("could not write the setup token file; use the token in this log line", "error", err)
		} else {
			file = s.dir + "/" + SetupTokenFile
		}
	}
	// The one place the token is logged: the operator has to be able to find it.
	s.log.Warn("setup required: open Wheelhouse in a browser and create the administrator with this one-time setup token. "+
		"Anyone who can read this log line or the token file before setup completes can claim this instance",
		"setup_token", token, "file", file)
	return nil
}

func (s *Service) removeTokenFile() {
	if s.dir != "" {
		removeTokenFile(s.dir)
	}
}

// Setup creates the first administrator. It works only while no account exists
// and only with the setup token, compared in constant time. It does not sign
// anyone in.
func (s *Service) Setup(ctx context.Context, token, username, password string, cm ClientMeta) (User, error) {
	key := addrKey("s", cm.Addr)
	if locked, _ := s.th.locked(key); locked {
		return User{}, ErrBadSetupToken
	}
	need, err := s.NeedsSetup(ctx)
	if err != nil {
		return User{}, err
	}
	if !need {
		return User{}, ErrSetupDone
	}
	s.setupMu.Lock()
	want := s.setupToken
	s.setupMu.Unlock()
	if want == "" || !constantTimeEqual(token, want) {
		if started, _ := s.th.fail(key, setupThreshold); started {
			s.record(ctx, Actor{Name: "anonymous", Addr: cm.Addr}, "setup.lockout", "", nil)
		}
		return User{}, ErrBadSetupToken
	}
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password, username); err != nil {
		return User{}, err
	}
	hash, err := s.h.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	now := s.unix()
	u := User{ID: newID(), Username: username, Role: RoleAdmin, CreatedAt: time.Unix(now, 0).UTC(), PasswordChangedAt: time.Unix(now, 0).UTC()}
	if s.beforeSetupTx != nil {
		s.beforeSetupTx() // tests: another request wins the race here
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupDone
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at)
			VALUES (?, ?, ?, 'admin', ?, ?)`, u.ID, username, hash, now, now); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, Actor{ID: u.ID, Name: username, Addr: cm.Addr}, "setup", username, nil)
	})
	if err != nil {
		return User{}, err
	}
	s.hasUsers.Store(true)
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	s.removeTokenFile()
	return u, nil
}

func constantTimeEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtleEqual(ha[:], hb[:])
}

// ---------------------------------------------------------------- login

// LoginResult is a new session.
type LoginResult struct {
	User    User
	Session Session
	// Token is the cookie value; CSRF the matching X-CSRF-Token value.
	Token string
	CSRF  string
}

type userRow struct {
	User
	hash string
}

func (s *Service) userByName(ctx context.Context, name string) (*userRow, error) {
	if len(name) > 256 {
		return nil, nil
	}
	var row userRow
	r := s.db.QueryRow(ctx, "SELECT "+userCols+", password_hash FROM users WHERE username = ?", name)
	var role string
	var disabled, must int
	var created, changed int64
	err := r.Scan(&row.ID, &row.Username, &role, &disabled, &must, &created, &changed, &row.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row.Role, row.Disabled, row.MustChangePassword = Role(role), disabled != 0, must != 0
	row.CreatedAt, row.PasswordChangedAt = time.Unix(created, 0).UTC(), time.Unix(changed, 0).UTC()
	return &row, nil
}

// Login checks a username and password and, if they are right, starts a
// session. Unknown user, wrong password, disabled account and locked-out
// account or client all return the same ErrInvalidCredentials after about the
// same work: a password hash is always computed, against a dummy when there is
// no such account.
func (s *Service) Login(ctx context.Context, username, password string, cm ClientMeta) (*LoginResult, error) {
	uKey, aKey := accountKey(username), addrKey("a", cm.Addr)
	if len(password) > MaxPasswordBytes {
		password = password[:MaxPasswordBytes] // bounded work; it cannot be right anyway
	}
	u, err := s.userByName(ctx, username)
	if err != nil {
		return nil, err
	}
	if u != nil {
		s.th.resetIfOlder(uKey, u.PasswordChangedAt)
	}
	// Reserve a verification slot on both keys before spending any work. A
	// locked key, or one whose remaining allowance is already taken by
	// attempts in flight, gets the same generic answer without an Argon2 run:
	// nothing observable differs, and a flood against a locked account costs
	// the server almost nothing.
	if !s.th.reserve(uKey, accountThreshold) {
		return nil, ErrInvalidCredentials
	}
	if !s.th.reserve(aKey, addressThreshold) {
		s.th.release(uKey)
		return nil, ErrInvalidCredentials
	}
	hash := s.dummy
	if u != nil {
		hash = u.hash
	}
	// Re-checked once the semaphore is ours, after any wait in the queue.
	stillAllowed := func() bool {
		lu, _ := s.th.locked(uKey)
		la, _ := s.th.locked(aKey)
		return !lu && !la
	}
	match, params, verr := s.h.Verify(ctx, hash, password, stillAllowed)
	if verr != nil && !errors.Is(verr, errBadHash) { // a damaged stored hash counts as a wrong password
		s.th.release(uKey)
		s.th.release(aKey)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrInvalidCredentials // busy or skipped after a lock: the generic answer
	}
	if u == nil || u.Disabled || !match {
		s.loginFailed(ctx, u, uKey, aKey, cm)
		return nil, ErrInvalidCredentials
	}
	s.th.release(aKey) // a success never counts against the address

	if params.Weaker(s.h.params) {
		if nh, herr := s.h.Hash(ctx, password); herr == nil {
			_, _ = s.db.Exec(ctx, "UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?", nh, u.ID, u.hash)
		}
	}
	s.th.reset(uKey)
	s.th.release(uKey)
	res, err := s.startSession(ctx, u.User, cm, "login.ok")
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) loginFailed(ctx context.Context, u *userRow, uKey, aKey string, cm ClientMeta) {
	// Converts the two reservations Login holds into recorded failures.
	startedU, untilU := s.th.failRelease(uKey, accountThreshold)
	startedA, untilA := s.th.failRelease(aKey, addressThreshold)
	actor := Actor{Name: "anonymous", Addr: cm.Addr}
	target, reason := "", "unknown user"
	if u != nil {
		actor.ID, actor.Name, target = u.ID, u.Username, u.Username
		reason = "wrong password"
		if u.Disabled {
			reason = "account disabled"
		}
	}
	s.record(ctx, actor, "login.fail", target, map[string]any{"reason": reason})
	if startedU {
		s.record(ctx, actor, "login.lockout", target, map[string]any{"scope": "account", "seconds": int(untilU.Sub(s.now()).Round(time.Second) / time.Second)})
	}
	if startedA {
		s.record(ctx, actor, "login.lockout", target, map[string]any{"scope": "address", "seconds": int(untilA.Sub(s.now()).Round(time.Second) / time.Second)})
	}
}

// startSession creates a session for u and audits it as action.
func (s *Service) startSession(ctx context.Context, u User, cm ClientMeta, action string) (*LoginResult, error) {
	var res *LoginResult
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if res, err = s.insertSession(ctx, tx, u, cm); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, Actor{ID: u.ID, Name: u.Username, Addr: cm.Addr}, action, u.Username, nil)
	})
	if err != nil {
		return nil, err
	}
	// Expired sessions are swept as a side effect of logins; a failure here matters to no one.
	s.sweepExpired(ctx)
	return res, nil
}

func (s *Service) insertSession(ctx context.Context, tx *sql.Tx, u User, cm ClientMeta) (*LoginResult, error) {
	now := s.unix()
	token := newToken()
	sess := Session{
		ID: newID(), UserID: u.ID,
		CreatedAt: time.Unix(now, 0).UTC(), LastSeen: time.Unix(now, 0).UTC(),
		ExpiresAt: time.Unix(now, 0).Add(AbsoluteTimeout).UTC(),
		IP:        clip(cm.Addr, 64), UserAgent: clean(cm.UserAgent, 200),
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, token_hash, user_id, created_at, last_seen, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sess.ID, hashToken(token), u.ID, now, now, sess.ExpiresAt.Unix(), sess.IP, sess.UserAgent); err != nil {
		return nil, err
	}
	return &LoginResult{User: u, Session: sess, Token: token, CSRF: CSRFToken(token)}, nil
}

func (s *Service) sweepExpired(ctx context.Context) {
	now := s.unix()
	_, _ = s.db.Exec(ctx, "DELETE FROM sessions WHERE expires_at <= ? OR last_seen <= ?", now, now-int64(IdleTimeout/time.Second))
}

// clip truncates to n bytes on a character boundary.
func clip(v string, n int) string {
	if len(v) <= n {
		return v
	}
	v = v[:n]
	for len(v) > 0 && !utf8Valid(v) {
		v = v[:len(v)-1]
	}
	return v
}

// clean drops control characters and truncates, for text a client supplies.
func clean(v string, n int) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	return clip(v, n)
}

// ---------------------------------------------------------------- sessions

// Authenticate resolves a session cookie value to an identity. It returns
// ErrUnauthenticated for an unknown, expired (idle or absolute) or revoked
// session, or one whose account is disabled or gone.
func (s *Service) Authenticate(ctx context.Context, token string) (*Identity, error) {
	if !validTokenShape(token) {
		return nil, ErrUnauthenticated
	}
	var (
		sid                       string
		created, lastSeen, expiry int64
		uid, name, role           string
		disabled, must            int
		ucreated, uchanged        int64
	)
	err := s.db.QueryRow(ctx, `SELECT s.id, s.created_at, s.last_seen, s.expires_at,
			u.id, u.username, u.role, u.disabled, u.must_change_password, u.created_at, u.password_changed_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, hashToken(token)).
		Scan(&sid, &created, &lastSeen, &expiry, &uid, &name, &role, &disabled, &must, &ucreated, &uchanged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	now := s.unix()
	idle := int64(IdleTimeout / time.Second)
	if now >= expiry || now-lastSeen >= idle || disabled != 0 {
		if now >= expiry || now-lastSeen >= idle {
			_, _ = s.db.Exec(ctx, "DELETE FROM sessions WHERE id = ?", sid)
		}
		return nil, ErrUnauthenticated
	}
	if now-lastSeen >= int64(lastSeenInterval/time.Second) {
		if _, err := s.db.Exec(ctx, "UPDATE sessions SET last_seen = ? WHERE id = ? AND last_seen < ?", now, sid, now); err != nil {
			s.log.Warn("could not update a session's last_seen", "error", err)
		}
		lastSeen = now
	}
	_ = created
	return &Identity{
		User: User{
			ID: uid, Username: name, Role: Role(role), MustChangePassword: must != 0,
			CreatedAt: time.Unix(ucreated, 0).UTC(), PasswordChangedAt: time.Unix(uchanged, 0).UTC(),
		},
		SessionID:         sid,
		CSRF:              CSRFToken(token),
		ExpiresAt:         time.Unix(min(lastSeen+idle, expiry), 0).UTC(),
		AbsoluteExpiresAt: time.Unix(expiry, 0).UTC(),
	}, nil
}

// Logout ends a session.
func (s *Service) Logout(ctx context.Context, a Actor, sessionID string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", sessionID); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, a, "logout", a.Name, nil)
	})
}

// ListSessions returns a user's live sessions, newest first.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	now := s.unix()
	rows, err := s.db.Query(ctx, `SELECT id, user_id, created_at, last_seen, expires_at, ip, user_agent FROM sessions
		WHERE user_id = ? AND expires_at > ? AND last_seen > ? ORDER BY created_at DESC, id`, userID, now, now-int64(IdleTimeout/time.Second))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Session{}
	for rows.Next() {
		var x Session
		var c, l, e int64
		if err := rows.Scan(&x.ID, &x.UserID, &c, &l, &e, &x.IP, &x.UserAgent); err != nil {
			return nil, err
		}
		x.CreatedAt, x.LastSeen, x.ExpiresAt = time.Unix(c, 0).UTC(), time.Unix(l, 0).UTC(), time.Unix(e, 0).UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeSession ends one of userID's sessions. It is ErrNotFound for any other
// session, so a user cannot probe for or end someone else's.
func (s *Service) RevokeSession(ctx context.Context, a Actor, userID, sessionID string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE id = ? AND user_id = ?", sessionID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return s.auditTx(ctx, tx, a, "session.revoke", sessionID, map[string]any{"userId": userID})
	})
}

// RevokeAllSessions ends every session of a user and returns how many.
func (s *Service) RevokeAllSessions(ctx context.Context, a Actor, userID string) (int, error) {
	var n int
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.userExists(ctx, tx, userID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
		if err != nil {
			return err
		}
		c, _ := res.RowsAffected()
		n = int(c)
		return s.auditTx(ctx, tx, a, "session.revokeAll", userID, map[string]any{"count": n})
	})
	return n, err
}

func (s *Service) userExists(ctx context.Context, tx *sql.Tx, id string) error {
	var x int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM users WHERE id = ?", id).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---------------------------------------------------------------- password change

// ChangePassword sets a new password for the signed-in user, who must give the
// current one. Every session of the user, including this one, ends and a new
// session replaces it (rotation), so a stolen cookie dies with the old
// password. The attempt counts toward the account's throttle like a login.
func (s *Service) ChangePassword(ctx context.Context, who *Identity, current, next string, cm ClientMeta) (*LoginResult, error) {
	uKey := accountKey(who.Username)
	if !s.th.reserve(uKey, accountThreshold) { // locked, or the allowance is taken by attempts in flight
		return nil, ErrTooManyAttempts
	}
	u, err := s.userByName(ctx, who.Username)
	if err != nil {
		s.th.release(uKey)
		return nil, err
	}
	if u == nil || u.ID != who.ID {
		s.th.release(uKey)
		return nil, ErrUnauthenticated
	}
	if len(current) > MaxPasswordBytes {
		current = current[:MaxPasswordBytes]
	}
	match, _, verr := s.h.Verify(ctx, u.hash, current, func() bool { l, _ := s.th.locked(uKey); return !l })
	if verr != nil && !errors.Is(verr, errBadHash) {
		s.th.release(uKey)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrTooManyAttempts
	}
	actor := who.Actor(cm.Addr)
	if !match {
		if started, until := s.th.failRelease(uKey, accountThreshold); started {
			s.record(ctx, actor, "login.lockout", who.Username, map[string]any{"scope": "account", "seconds": int(until.Sub(s.now()).Round(time.Second) / time.Second)})
		}
		s.record(ctx, actor, "password.change.fail", who.Username, nil)
		return nil, ErrWrongPassword
	}
	s.th.release(uKey)
	if err := ValidatePassword(next, who.Username); err != nil {
		return nil, err
	}
	if next == current {
		return nil, &PolicyError{"The new password must be different from the current one."}
	}
	hash, err := s.h.Hash(ctx, next)
	if err != nil {
		return nil, err
	}
	var res *LoginResult
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.unix()
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, must_change_password = 0 WHERE id = ?`,
			hash, now, u.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID); err != nil {
			return err
		}
		nu := u.User
		nu.MustChangePassword = false
		nu.PasswordChangedAt = time.Unix(now, 0).UTC()
		if res, err = s.insertSession(ctx, tx, nu, cm); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, actor, "password.change", who.Username, nil)
	})
	if err != nil {
		return nil, err
	}
	s.th.reset(uKey)
	return res, nil
}
