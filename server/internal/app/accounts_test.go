package app

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// cheapParams keeps Argon2 fast in tests; the production parameters are tested
// in internal/auth and by the acceptance suite against the real binary.
var cheapParams = auth.Params{Memory: 8, Time: 1, Threads: 1}

const (
	testAdminName     = "admin-user"
	testPassword      = "correct-battery-staple-1"
	testOtherPassword = "another-long-passphrase-2"
)

// fakeClock is the injectable time source of the account service.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// accounts is an account service over a temporary database, with one signed-in
// administrator, for tests of the routes.
type accounts struct {
	Svc    *auth.Service
	DB     *store.DB
	Clock  *fakeClock
	Dir    string
	Admin  auth.User
	Cookie *http.Cookie
	CSRF   string
}

func newAccounts(t *testing.T) *accounts {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "wheelhouse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := newClock()
	svc := auth.New(db, auth.Options{Clock: clk.Now, Params: cheapParams, ConfigDir: dir})
	admin, err := svc.CreateUser(context.Background(), auth.Actor{Name: "test"}, auth.NewUser{Username: testAdminName, Password: testPassword, Role: auth.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	a := &accounts{Svc: svc, DB: db, Clock: clk, Dir: dir, Admin: admin}
	a.Cookie, a.CSRF = a.login(t, testAdminName, testPassword)
	return a
}

// login starts a session directly in the service and returns its cookie and CSRF token.
func (a *accounts) login(t *testing.T, name, password string) (*http.Cookie, string) {
	t.Helper()
	res, err := a.Svc.Login(context.Background(), name, password, auth.ClientMeta{Addr: "192.0.2.1", UserAgent: "test"})
	if err != nil {
		t.Fatalf("login %s: %v", name, err)
	}
	return &http.Cookie{Name: cookieName, Value: res.Token}, res.CSRF
}

// As returns h with the administrator's cookie and CSRF token added to every
// request, like a signed-in client of the API.
func (a *accounts) As(h http.Handler) http.Handler {
	return withSession(h, a.Cookie, a.CSRF)
}

func withSession(h http.Handler, c *http.Cookie, csrf string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.AddCookie(c)
		if r.Header.Get("X-CSRF-Token") == "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		h.ServeHTTP(w, r)
	})
}
