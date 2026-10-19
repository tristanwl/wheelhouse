package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are the Argon2id cost parameters. They are written into every encoded
// hash, so raising them later does not break existing passwords: a hash made
// with weaker parameters still verifies and is replaced by one with the current
// parameters at the user's next login (Params.Weaker).
type Params struct {
	Memory  uint32 // KiB
	Time    uint32 // passes
	Threads uint8
}

// DefaultParams follows RFC 9106's second recommended option (memory-constrained
// environments) with one lane fewer: 64 MiB, 3 passes, 2 lanes, about 100 ms on
// a modern core. Verification is limited to maxConcurrentHashes at a time, so
// a login flood costs at most that many times 64 MiB.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2}

const (
	saltLen = 16
	keyLen  = 32
	// maxConcurrentHashes bounds the memory a flood of login attempts can use.
	maxConcurrentHashes = 3

	// Bounds on parameters read back from the database, so a damaged or hostile
	// value cannot make the server allocate gigabytes or spin.
	maxStoredMemory  = 1 << 20 // 1 GiB in KiB
	maxStoredTime    = 20
	maxStoredThreads = 16
)

// Weaker reports whether p costs less than cur in any dimension.
func (p Params) Weaker(cur Params) bool {
	return p.Memory < cur.Memory || p.Time < cur.Time || p.Threads < cur.Threads
}

// hasher computes and verifies Argon2id hashes, a few at a time.
type hasher struct {
	params Params
	sem    chan struct{}
	// computed counts Argon2 evaluations, so tests can show that every kind of
	// failed login costs exactly one.
	computed atomic.Int64
	// waiting counts verifications queued for a slot; past maxQueued more are
	// refused with errBusy instead of piling up (memory and latency).
	waiting atomic.Int32
}

// maxQueuedVerifies bounds how many verifications may wait for one of the
// maxConcurrentHashes slots. Each waiter holds only a goroutine, but an
// unbounded queue would turn a flood into minutes of latency for everyone.
const maxQueuedVerifies = 32

// errBusy means too many verifications are already waiting.
var errBusy = errors.New("auth: too many password checks are waiting")

func newHasher(p Params) *hasher {
	if p == (Params{}) {
		p = DefaultParams
	}
	return &hasher{params: p, sem: make(chan struct{}, maxConcurrentHashes)}
}

func (h *hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// acquireBounded is acquire for attacker-reachable work: when the queue is
// already maxQueuedVerifies long it fails at once with errBusy.
func (h *hasher) acquireBounded(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	default:
	}
	if h.waiting.Add(1) > maxQueuedVerifies {
		h.waiting.Add(-1)
		return errBusy
	}
	defer h.waiting.Add(-1)
	return h.acquire(ctx)
}

func (h *hasher) release() { <-h.sem }

// Hash returns the encoded Argon2id hash of password with the current parameters
// and a fresh random salt: $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>.
func (h *hasher) Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("random source failed: %w", err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	h.computed.Add(1)
	p := h.params
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, keyLen)
	return encode(p, salt, key), nil
}

func encode(p Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// errSkipped means the caller's proceed check refused the work.
var errSkipped = errors.New("auth: password check skipped")

var errBadHash = errors.New("auth: the stored password hash is not a valid Argon2id hash")

// parseHash splits an encoded hash into its parameters, salt and key.
func parseHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Params{}, nil, nil, errBadHash
	}
	var p Params
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil {
			return Params{}, nil, nil, errBadHash
		}
		switch k {
		case "m":
			p.Memory = uint32(n)
		case "t":
			p.Time = uint32(n)
		case "p":
			if n > maxStoredThreads {
				return Params{}, nil, nil, errBadHash
			}
			p.Threads = uint8(n)
		default:
			return Params{}, nil, nil, errBadHash
		}
	}
	if p.Memory < 8 || p.Memory > maxStoredMemory || p.Time < 1 || p.Time > maxStoredTime || p.Threads < 1 {
		return Params{}, nil, nil, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	key, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) < 8 || len(salt) > 64 || len(key) < 16 || len(key) > 64 {
		return Params{}, nil, nil, errBadHash
	}
	return p, salt, key, nil
}

// Verify reports whether password matches the encoded hash, comparing in
// constant time. params are the hash's own, so the caller can decide to rehash.
// A hash that cannot be parsed never matches (and still costs a full hash, so
// it is not distinguishable by timing from a wrong password).
//
// proceed, when not nil, is asked once a slot is held, immediately before the
// expensive work: if it says no, nothing is computed and errSkipped comes back.
// Login uses it to re-check the lock after the wait in the queue.
func (h *hasher) Verify(ctx context.Context, encoded, password string, proceed func() bool) (ok bool, params Params, err error) {
	p, salt, key, perr := parseHash(encoded)
	if perr != nil {
		// Spend the same time, within the same bounds as a real check.
		if err := h.acquireBounded(ctx); err != nil {
			return false, Params{}, err
		}
		defer h.release()
		if proceed != nil && !proceed() {
			return false, Params{}, errSkipped
		}
		h.computed.Add(1)
		_ = argon2.IDKey([]byte(password), make([]byte, saltLen), h.params.Time, h.params.Memory, h.params.Threads, keyLen)
		return false, Params{}, perr
	}
	if err := h.acquireBounded(ctx); err != nil {
		return false, p, err
	}
	defer h.release()
	if proceed != nil && !proceed() {
		return false, p, errSkipped
	}
	h.computed.Add(1)
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key))) //nolint:gosec // 16 to 64, checked by parseHash
	return subtle.ConstantTimeCompare(got, key) == 1, p, nil
}

// Password policy.
const (
	// MinPasswordLength counts characters (code points).
	MinPasswordLength = 12
	// MaxPasswordBytes bounds the work and memory a request can ask for.
	MaxPasswordBytes = 1024
)

// PolicyError is a password or username a person has to change. Its text is
// meant for the user.
type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

//go:embed common_passwords.txt
var commonPasswordsRaw string

var (
	commonOnce sync.Once
	commonSet  map[string]struct{}
)

func commonPasswords() map[string]struct{} {
	commonOnce.Do(func() {
		commonSet = map[string]struct{}{}
		sc := bufio.NewScanner(strings.NewReader(commonPasswordsRaw))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			commonSet[line] = struct{}{}
		}
	})
	return commonSet
}

// ValidatePassword applies the policy: at least MinPasswordLength characters, at
// most MaxPasswordBytes bytes, valid text, not one of the most common passwords,
// not the username, and more than a couple of different characters. There are
// no composition rules (no "needs a digit"): length matters, the rest only
// teaches people patterns.
func ValidatePassword(password, username string) error {
	switch {
	case len(password) > MaxPasswordBytes:
		return &PolicyError{fmt.Sprintf("The password is too long (at most %d bytes).", MaxPasswordBytes)}
	case !utf8.ValidString(password) || strings.ContainsRune(password, 0):
		return &PolicyError{"The password contains characters that are not allowed."}
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return &PolicyError{fmt.Sprintf("The password must be at least %d characters long.", MinPasswordLength)}
	case strings.EqualFold(password, username):
		return &PolicyError{"The password must not be the same as the username."}
	}
	seen := map[rune]struct{}{}
	for _, r := range password {
		seen[r] = struct{}{}
	}
	if len(seen) < 5 {
		return &PolicyError{"That password is too simple. Use a longer phrase."}
	}
	if _, bad := commonPasswords()[strings.ToLower(password)]; bad {
		return &PolicyError{"That password is on the list of the most commonly used passwords. Choose another."}
	}
	return nil
}

// generatedAlphabet leaves out look-alike characters (0/O, 1/l/I).
const generatedAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random one-time password of 20 characters (about
// 114 bits) for `wheelhouse reset-password`.
func GeneratePassword() (string, error) {
	const n = 20
	out := make([]byte, 0, n)
	limit := byte(256 - 256%len(generatedAlphabet)) // reject the biased tail
	var buf [64]byte
	for len(out) < n {
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("random source failed: %w", err)
		}
		for _, b := range buf {
			if b < limit && len(out) < n {
				out = append(out, generatedAlphabet[int(b)%len(generatedAlphabet)])
			}
		}
	}
	return string(out), nil
}
