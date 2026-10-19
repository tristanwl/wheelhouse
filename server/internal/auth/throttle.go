package auth

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Login throttling. State lives in memory, deliberately:
//
//   - A failed login must not need a database write: an attacker could otherwise
//     turn guessing into disk and WAL growth, and a read-only or full disk would
//     disable the lock.
//   - The counters are per process and are lost at a restart. Only the operator
//     restarts the server, an attacker gains one fresh window by that, and the
//     guess rate is capped again at once. The lockouts are short (at most 15
//     minutes) so nothing important is lost by forgetting them.
//   - Keeping them for names that do not exist is what makes a lockout look the
//     same for real and made-up users.
//
// A key is "u:<lowercased name>" for an account, "a:<address>" for a client (an
// IPv6 address counts as its /64) and "s:<address>" for setup-token guesses.
// The first few failures are free, so typing errors cost nothing; after that
// each further failure locks the key for twice as long as the last, from
// lockBase up to lockMax. A success clears the account key (never the address
// key, so one valid login cannot reset a guessing client's count). Failures
// are forgotten after decayAfter without a new one.
const (
	lockBase   = 30 * time.Second
	lockMax    = 15 * time.Minute
	decayAfter = time.Hour

	accountThreshold = 5  // failures before an account locks
	addressThreshold = 20 // failures before a client address locks (households share one)
	setupThreshold   = 10 // wrong setup tokens before that address locks

	// maxEntries bounds memory against a flood of made-up names or addresses.
	maxEntries = 20000
)

type entry struct {
	failures    int
	first, last time.Time
	lockedUntil time.Time
}

type throttle struct {
	now func() time.Time
	mu  sync.Mutex
	m   map[string]*entry
	// inflight counts verifications that hold a reservation per key (see
	// reserve). It is separate from m so that clearing or evicting a failure
	// record can never lose track of a running verification.
	inflight map[string]int
}

func newThrottle(now func() time.Time) *throttle {
	return &throttle{now: now, m: map[string]*entry{}, inflight: map[string]int{}}
}

func accountKey(username string) string {
	u := strings.ToLower(strings.TrimSpace(username))
	if len(u) > 64 {
		u = u[:64]
	}
	return "u:" + u
}

// addrKey turns a remote address into a key. An IPv6 client can pick any
// address in its /64, so the whole /64 shares one key.
func addrKey(kind, addr string) string {
	if ip, err := netip.ParseAddr(addr); err == nil {
		ip = ip.Unmap()
		if ip.Is6() {
			if p, err := ip.Prefix(64); err == nil {
				return kind + ":" + p.String()
			}
		}
		return kind + ":" + ip.String()
	}
	return kind + ":" + addr
}

// locked reports whether key is locked now, and until when.
func (t *throttle) locked(key string) (bool, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[key]
	if e == nil {
		return false, time.Time{}
	}
	if now := t.now(); e.lockedUntil.After(now) {
		return true, e.lockedUntil
	}
	return false, time.Time{}
}

// reserve claims one verification slot for key. It succeeds only when the
// recorded failures plus the verifications already running leave room under
// threshold, and the key is not locked. This is what stops a burst of parallel
// guesses: every one of them used to pass the lock check before any of them
// had failed, so N parallel guesses were all verified. Now at most
// threshold-failures verifications can be running for a key, so the lock
// holds even against a simultaneous burst. A caller that gets true must call
// release (a success or an aborted attempt) or failRelease (a wrong password)
// exactly once.
func (t *throttle) reserve(key string, threshold int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	failures := 0
	if e := t.m[key]; e != nil {
		if e.lockedUntil.After(now) {
			return false
		}
		if now.Sub(e.last) <= decayAfter {
			failures = e.failures
		}
	}
	// After an expired lock the failures are still on record (the next one
	// doubles the lock), and exactly one attempt at a time may probe.
	if t.inflight[key] >= max(threshold-failures, 1) {
		return false
	}
	t.inflight[key]++
	return true
}

// release gives back a reservation without recording a failure.
func (t *throttle) release(key string) {
	t.mu.Lock()
	t.releaseLocked(key)
	t.mu.Unlock()
}

func (t *throttle) releaseLocked(key string) {
	if t.inflight[key] <= 1 {
		delete(t.inflight, key)
		return
	}
	t.inflight[key]--
}

// failRelease converts a reservation into a recorded failure in one step, so
// the slot is never counted twice or not at all.
func (t *throttle) failRelease(key string, threshold int) (started bool, until time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.releaseLocked(key)
	return t.failLocked(key, threshold)
}

// fail records a failure for key and reports whether it started a lockout.
func (t *throttle) fail(key string, threshold int) (started bool, until time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failLocked(key, threshold)
}

func (t *throttle) failLocked(key string, threshold int) (started bool, until time.Time) {
	now := t.now()
	e := t.m[key]
	if e != nil && now.Sub(e.last) > decayAfter && !e.lockedUntil.After(now) {
		delete(t.m, key)
		e = nil
	}
	if e == nil {
		if len(t.m) >= maxEntries {
			t.evictLocked(now)
			if len(t.m) >= maxEntries {
				return false, time.Time{} // all slots are live lockouts; do not grow
			}
		}
		e = &entry{first: now}
		t.m[key] = e
	}
	e.failures++
	e.last = now
	if e.failures >= threshold {
		d := lockBase
		for i := threshold; i < e.failures && d < lockMax; i++ {
			d *= 2
		}
		d = min(d, lockMax)
		e.lockedUntil = now.Add(d)
		return true, e.lockedUntil
	}
	return false, time.Time{}
}

// evictLocked drops every entry that is not an active lockout. Called with the
// lock held when the table is full.
func (t *throttle) evictLocked(now time.Time) {
	for k, e := range t.m {
		if !e.lockedUntil.After(now) {
			delete(t.m, k)
		}
	}
}

func (t *throttle) reset(key string) {
	t.mu.Lock()
	delete(t.m, key)
	t.mu.Unlock()
}

// resetIfOlder forgets key when its failures began before cutoff: a password
// reset after the failures started (an admin, or `wheelhouse reset-password`
// run by the operator) lifts the lock the guessing caused, so locking someone
// out can never also lock them out of recovery.
func (t *throttle) resetIfOlder(key string, cutoff time.Time) {
	t.mu.Lock()
	if e := t.m[key]; e != nil && e.first.Before(cutoff) {
		delete(t.m, key)
	}
	t.mu.Unlock()
}
