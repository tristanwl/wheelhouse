package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// FuzzValidateUsername compares the username rules with an independent
// statement of them.
//
// Invariants: a name is accepted only if it is 3 to 64 ASCII letters, digits and
// . _ @ -, starts with a letter or a digit, and (in any letter case) is not one
// of the names the audit log uses for actors that are not accounts; nothing
// outside ASCII (full-width letters, the Kelvin sign, a trailing newline,
// zero-width characters) is accepted; a refusal is a *PolicyError that does not
// repeat the name.
func FuzzValidateUsername(f *testing.F) {
	f.Fuzz(func(t *testing.T, name string) {
		want := true
		switch {
		case len(name) < 3 || len(name) > 64:
			want = false
		default:
			for i := 0; i < len(name); i++ {
				c := name[i]
				alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
				if !alnum && (i == 0 || !strings.ContainsRune("._@-", rune(c))) {
					want = false
				}
			}
		}
		if want {
			switch strings.ToLower(name) {
			case "local-network", "cli", "system", "anonymous":
				want = false
			}
		}
		err := ValidateUsername(name)
		if (err == nil) != want {
			t.Fatalf("ValidateUsername(%q) = %v, want accepted=%v", name, err, want)
		}
		if err != nil {
			var pe *PolicyError
			if !errors.As(err, &pe) {
				t.Fatalf("error is %T", err)
			}
			if len(name) >= 5 && strings.Contains(err.Error(), name) {
				t.Fatalf("the error repeats the name: %q", err)
			}
		}
	})
}

var commonList = func() map[string]bool {
	raw, err := os.ReadFile("common_passwords.txt")
	if err != nil {
		panic(err)
	}
	m := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			m[line] = true
		}
	}
	return m
}()

// FuzzValidatePassword compares the policy with an independent statement of it.
//
// Invariants: length is counted in code points (not bytes: twelve emoji pass,
// eleven do not), at most 1024 bytes, valid UTF-8 without NUL, not the username
// in any letter case, at least five different characters, and not on the common
// list in lower case; a refusal is a *PolicyError that never echoes the password
// or the username.
func FuzzValidatePassword(f *testing.F) {
	f.Fuzz(func(t *testing.T, pw, user string) {
		var want bool
		distinct := map[rune]bool{}
		for _, r := range pw {
			distinct[r] = true
		}
		switch {
		case len(pw) > 1024, !utf8.ValidString(pw), strings.ContainsRune(pw, 0):
		case utf8.RuneCountInString(pw) < 12:
		case strings.EqualFold(pw, user):
		case len(distinct) < 5:
		case commonList[strings.ToLower(pw)]:
		default:
			want = true
		}
		err := ValidatePassword(pw, user)
		if (err == nil) != want {
			t.Fatalf("ValidatePassword(%q, %q) = %v, want accepted=%v", pw, user, err, want)
		}
		if err != nil {
			var pe *PolicyError
			if !errors.As(err, &pe) {
				t.Fatalf("error is %T", err)
			}
			if (len(pw) >= 6 && strings.Contains(err.Error(), pw)) || (len(user) >= 6 && strings.Contains(err.Error(), user)) {
				t.Fatalf("the error echoes its input: %q", err)
			}
		}
	})
}

// FuzzParseHash: a stored password hash is bounded or refused before any work is
// done with it.
//
// Invariants: it never panics; an accepted hash has parameters within the
// stored-parameter bounds (so a damaged or hostile row cannot ask for gigabytes
// or minutes), a salt of 8 to 64 bytes and a key of 16 to 64 bytes, and its own
// re-encoding parses to the same values.
func FuzzParseHash(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		p, salt, key, err := parseHash(s)
		if err != nil {
			if !errors.Is(err, errBadHash) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		if p.Memory < 8 || p.Memory > maxStoredMemory || p.Time < 1 || p.Time > maxStoredTime || p.Threads < 1 || p.Threads > maxStoredThreads {
			t.Fatalf("accepted parameters %+v", p)
		}
		if len(salt) < 8 || len(salt) > 64 || len(key) < 16 || len(key) > 64 {
			t.Fatalf("accepted salt %d and key %d bytes", len(salt), len(key))
		}
		p2, salt2, key2, err := parseHash(encode(p, salt, key))
		if err != nil || p2 != p || string(salt2) != string(salt) || string(key2) != string(key) {
			t.Fatalf("re-encoding changed the hash: %v", err)
		}
	})
}

// FuzzCleanClip: the text a client supplies (user agent, names, targets) is
// bounded and free of control characters before it is stored or logged.
//
// Invariants: clip returns a prefix of at most n bytes and keeps valid UTF-8
// valid; clean also removes C0 controls and DEL, and cleaning twice changes
// nothing.
func FuzzCleanClip(f *testing.F) {
	f.Fuzz(func(t *testing.T, v string, n uint8) {
		c := clip(v, int(n))
		if len(c) > int(n) || !strings.HasPrefix(v, c) || (utf8.ValidString(v) && !utf8.ValidString(c)) {
			t.Fatalf("clip(%q, %d) = %q", v, n, c)
		}
		cl := clean(v, int(n))
		if len(cl) > int(n) || strings.ContainsFunc(cl, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Fatalf("clean(%q, %d) = %q", v, n, cl)
		}
		if utf8.ValidString(v) && !utf8.ValidString(cl) {
			t.Fatalf("clean broke valid text: %q", cl)
		}
		if again := clean(cl, int(n)); again != cl {
			t.Fatalf("clean is not idempotent: %q then %q", cl, again)
		}
	})
}

// authFixture is a service with one administrator and one live session.
type authFixture struct {
	svc      *Service
	clk      *clock
	username string
	password string
	token    string
}

func newAuthFixture(tb testing.TB, dir string) *authFixture {
	tb.Helper()
	db, err := store.Open(context.Background(), filepath.Join(dir, "wheelhouse.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	clk := &clock{t: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	svc := New(db, Options{Clock: clk.now, Params: cheap, ConfigDir: dir})
	fx := &authFixture{svc: svc, clk: clk, username: "fuzz-admin", password: "correct-battery-staple-1"}
	if _, err := svc.CreateUser(context.Background(), Actor{Name: "test"}, NewUser{Username: fx.username, Password: fx.password, Role: RoleAdmin}); err != nil {
		tb.Fatal(err)
	}
	res, err := svc.Login(context.Background(), fx.username, fx.password, ClientMeta{Addr: "192.0.2.1", UserAgent: "fuzz"})
	if err != nil {
		tb.Fatal(err)
	}
	fx.token = res.Token
	return fx
}

// FuzzAuthenticate: session cookie values.
//
// Invariants: the one real token authenticates; no other string does (a prefix,
// a different case, padding, a SQL fragment, a token of the right shape that was
// never issued); a failure is always ErrUnauthenticated and never a database
// error; the CSRF token derived from a session token is deterministic, 43
// characters, different for different tokens and contains no part of the token.
func FuzzAuthenticate(f *testing.F) {
	fx := newAuthFixture(f, f.TempDir())
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, token string) {
		id, err := fx.svc.Authenticate(ctx, token)
		if token == fx.token {
			if err != nil || id == nil || id.CSRF != CSRFToken(token) {
				t.Fatalf("the real token was refused: %v", err)
			}
			return
		}
		if !errors.Is(err, ErrUnauthenticated) || id != nil {
			t.Fatalf("token %q: err %v identity %v", token, err, id)
		}
		if validTokenShape(token) != (len(token) == tokenLen && strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") == "") {
			t.Fatalf("validTokenShape(%q) disagrees with its definition", token)
		}
		c1, c2 := CSRFToken(token), CSRFToken(token)
		if c1 != c2 || len(c1) != 43 || c1 == CSRFToken(fx.token) || (len(token) >= 8 && strings.Contains(c1, token)) {
			t.Fatalf("CSRFToken(%q) = %q", token, c1)
		}
	})
}

// FuzzLogin: arbitrary credentials and client metadata.
//
// Invariants: a login succeeds only for the administrator's exact password and a
// spelling of the name that the database treats as the same account; every
// failure is ErrInvalidCredentials (nothing says whether the name exists, was
// disabled or locked); the error never contains the password; a name the
// database resolves to the account has the same throttle key as the account's
// own, so a different spelling cannot dodge the lockout; and the stored user
// agent and address are bounded and, for the agent, free of control characters.
func FuzzLogin(f *testing.F) {
	fx := newAuthFixture(f, f.TempDir())
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, username, password, addr, ua string) {
		fx.clk.advance(48 * time.Hour) // forget lockouts; long-dead sessions are swept
		res, err := fx.svc.Login(ctx, username, password, ClientMeta{Addr: addr, UserAgent: ua})
		if err != nil {
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("login failed with %v", err)
			}
			if len(password) >= 6 && strings.Contains(err.Error(), password) {
				t.Fatal("the error echoes the password")
			}
		} else {
			if !strings.EqualFold(username, fx.username) {
				t.Fatalf("logged in as %q with name %q", fx.username, username)
			}
			if truncated := password; truncated != fx.password {
				t.Fatalf("logged in with password %q", password)
			}
			if len(res.Session.UserAgent) > 200 || strings.ContainsFunc(res.Session.UserAgent, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				t.Fatalf("stored user agent %q", res.Session.UserAgent)
			}
			if len(res.Session.IP) > 64 {
				t.Fatalf("stored address of %d bytes", len(res.Session.IP))
			}
		}
		if u, err := fx.svc.userByName(ctx, username); err == nil && u != nil && accountKey(username) != accountKey(fx.username) {
			t.Fatalf("%q resolves to the account but throttles under %q, not %q", username, accountKey(username), accountKey(fx.username))
		}
	})
}
