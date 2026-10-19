package secrets

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Environment variables that supply the master key, and the file name used when
// neither is set.
const (
	EnvKeyFile = "WHEELHOUSE_SECRET_KEY_FILE"
	EnvKey     = "WHEELHOUSE_SECRET_KEY"

	// GeneratedKeyName is created inside the config directory on first use.
	GeneratedKeyName = "secret.key"
)

// Key is a master key. It prints as "[redacted]" with every fmt verb, in slog
// and in JSON. The zero Key is invalid.
type Key struct{ b []byte }

// NewKey wraps exactly KeySize bytes (copied).
func NewKey(b []byte) (Key, error) {
	if len(b) != KeySize {
		return Key{}, fmt.Errorf("secrets: a key must be exactly %d bytes, not %d", KeySize, len(b))
	}
	if bytes.Equal(b, make([]byte, KeySize)) {
		return Key{}, errors.New("secrets: a key of all zero bytes is refused")
	}
	return Key{b: bytes.Clone(b)}, nil
}

// GenerateKey returns a new random key.
func GenerateKey() (Key, error) {
	b := make([]byte, KeySize)
	if _, err := rand.Read(b); err != nil {
		return Key{}, fmt.Errorf("secrets: random source failed: %w", err)
	}
	return Key{b: b}, nil
}

// String implements fmt.Stringer.
func (Key) String() string { return redacted }

// GoString keeps %#v from printing the key.
func (Key) GoString() string { return redacted }

// Format redacts every fmt verb, including %x.
func (Key) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// LogValue keeps slog from recording the key.
func (Key) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON keeps the key out of any JSON.
func (Key) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText keeps the key out of text encodings.
func (Key) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Equal reports whether two keys are the same, in constant time.
func (k Key) Equal(o Key) bool {
	return len(k.b) == KeySize && len(o.b) == KeySize && subtle.ConstantTimeCompare(k.b, o.b) == 1
}

// Source says where a key came from.
type Source int

// The key sources, in the order Load tries them.
const (
	SourceKeyFile   Source = iota + 1 // WHEELHOUSE_SECRET_KEY_FILE
	SourceEnv                         // WHEELHOUSE_SECRET_KEY
	SourceGenerated                   // <config dir>/secret.key, made by Wheelhouse
)

// String names the source for messages ("" never contains the key).
func (s Source) String() string {
	switch s {
	case SourceKeyFile:
		return EnvKeyFile
	case SourceEnv:
		return EnvKey
	case SourceGenerated:
		return GeneratedKeyName + " in the config directory"
	}
	return "unknown"
}

// ErrNoKey means no key is configured and none exists yet.
var ErrNoKey = errors.New("secrets: no master key: set " + EnvKeyFile + " or " + EnvKey + ", or let Wheelhouse create " + GeneratedKeyName + " in its config directory")

// Loaded is a master key and where it came from.
type Loaded struct {
	Key    Key
	Source Source
	// Path is the key file for SourceKeyFile and SourceGenerated.
	Path string
}

// LoadOptions says where to look.
type LoadOptions struct {
	// Getenv reads the environment (os.Getenv in production).
	Getenv func(string) string
	// ConfigDir holds the generated secret.key.
	ConfigDir string
	// Generate allows creating secret.key when no key is configured and the
	// file does not exist. Only the commands that own the key (and the tests)
	// set it; nothing creates a key just by starting the server.
	Generate bool
}

// Load finds the master key, in this order:
//
//  1. WHEELHOUSE_SECRET_KEY_FILE: a file holding exactly 32 raw bytes, mode 0600 or 0400.
//  2. WHEELHOUSE_SECRET_KEY: the key, base64 encoded (32 bytes).
//  3. <ConfigDir>/secret.key, created with 32 random bytes and mode 0600 when
//     it does not exist and opts.Generate is set.
//
// The first source that is set decides: a set but unusable source is an error,
// never a silent fall through to the next one (that could encrypt under a key
// the operator did not mean).
func Load(opts LoadOptions) (Loaded, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if p := getenv(EnvKeyFile); p != "" {
		k, err := ReadKeyFile(p)
		if err != nil {
			return Loaded{}, fmt.Errorf("%s: %w", EnvKeyFile, err)
		}
		return Loaded{Key: k, Source: SourceKeyFile, Path: p}, nil
	}
	if v := getenv(EnvKey); v != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
		if err != nil {
			return Loaded{}, fmt.Errorf("%s must be base64 (standard alphabet) of exactly %d bytes", EnvKey, KeySize)
		}
		k, err := NewKey(raw)
		clear(raw)
		if err != nil {
			return Loaded{}, fmt.Errorf("%s must be base64 of exactly %d bytes (non-zero)", EnvKey, KeySize)
		}
		return Loaded{Key: k, Source: SourceEnv}, nil
	}
	if opts.ConfigDir == "" {
		return Loaded{}, ErrNoKey
	}
	path := filepath.Join(opts.ConfigDir, GeneratedKeyName)
	k, err := ReadKeyFile(path)
	switch {
	case err == nil:
		return Loaded{Key: k, Source: SourceGenerated, Path: path}, nil
	case !errors.Is(err, fs.ErrNotExist):
		return Loaded{}, err
	case !opts.Generate:
		return Loaded{}, ErrNoKey
	}
	k, err = GenerateKeyFile(path)
	if errors.Is(err, fs.ErrExist) { // another process created it first
		k, err = ReadKeyFile(path)
	}
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{Key: k, Source: SourceGenerated, Path: path}, nil
}

// ReadKeyFile reads a key file. It refuses anything that is not a regular file
// owned-readable only (no group or other permission bits at all) or not exactly
// 32 bytes, with a message that says how to fix it. Symbolic links are followed
// (secret mounts use them) and the checks apply to the file they lead to.
func ReadKeyFile(path string) (Key, error) {
	f, err := os.Open(path) //nolint:gosec // the operator chooses the key file
	if err != nil {
		return Key{}, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Key{}, err
	}
	if !st.Mode().IsRegular() {
		return Key{}, fmt.Errorf("key file %s is not a regular file", path)
	}
	if err := checkPrivate(st.Mode()); err != nil {
		return Key{}, fmt.Errorf("key file %s: %w", path, err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, KeySize+2))
	if err != nil {
		return Key{}, err
	}
	defer clear(raw)
	if len(raw) != KeySize {
		return Key{}, fmt.Errorf("key file %s must hold exactly %d raw bytes (no hex or base64, no trailing newline); make one with: head -c %d /dev/urandom > %s", path, KeySize, KeySize, path)
	}
	k, err := NewKey(raw)
	if err != nil {
		return Key{}, fmt.Errorf("key file %s: %w", path, err)
	}
	return k, nil
}

// GenerateKeyFile creates path with a new random key, mode 0600, failing with
// fs.ErrExist if the file is already there (it never overwrites a key). The key
// is flushed to disk before it is returned.
func GenerateKeyFile(path string) (Key, error) {
	k, err := GenerateKey()
	if err != nil {
		return Key{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is the config dir or an operator-chosen output
	if err != nil {
		return Key{}, err
	}
	_, werr := f.Write(k.b)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(path)
		return Key{}, fmt.Errorf("writing key file %s: %w", path, err)
	}
	syncDir(filepath.Dir(path))
	return k, nil
}

// PromoteKeyFile atomically moves a staged key file over the live one and makes
// the rename durable. Rekey stages a new key as <path>.next, commits the
// re-encrypted values, then promotes it.
func PromoteKeyFile(staged, live string) error {
	if err := os.Rename(staged, live); err != nil {
		return err
	}
	syncDir(filepath.Dir(live))
	return nil
}

// checkPrivate refuses a file that group or others can read, write or run.
func checkPrivate(mode fs.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil // permission bits are not meaningful there
	}
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("permissions %04o are too open: only the owner may have access; run: chmod 600 <file>", mode.Perm())
	}
	return nil
}

// syncDir makes a new directory entry durable; best effort.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil { //nolint:gosec // the config directory
		_ = d.Sync()
		_ = d.Close()
	}
}
