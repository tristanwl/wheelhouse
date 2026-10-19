// Package fuzzseed is test support for the fuzz targets: it writes seed corpus
// files in the "go test fuzz v1" format that "go test" replays as ordinary
// tests, so a seed that was made from a real input (a fixture of a fake
// service, a hostile case from an existing test) is committed under
// testdata/fuzz/<FuzzName>/ and runs on every build. It is only imported from
// tests.
package fuzzseed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Env turns the generators on: "WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run
// TestWriteFuzzSeeds ./internal/<package>" (re)writes the seed files of that
// package. Without it every generator is skipped.
const Env = "WHEELHOUSE_WRITE_FUZZ_SEEDS"

// Enabled reports whether the generators should write files.
func Enabled() bool { return os.Getenv(Env) != "" }

// Writer writes seed files below one package's testdata/fuzz directory.
type Writer struct {
	t   testing.TB
	dir string
}

// New returns a Writer for the current package, or skips the test when the
// generators are off.
func New(t testing.TB) *Writer {
	t.Helper()
	if !Enabled() {
		t.Skipf("set %s=1 to write the fuzz seed files", Env)
	}
	return &Writer{t: t, dir: filepath.Join("testdata", "fuzz")}
}

// Add writes one corpus entry for the target (for example "FuzzDecodeJSON")
// with the given argument values, which must match the target's parameters in
// order. The file name is derived from the content, so rewriting is stable and
// duplicates collapse. label is a short human hint kept in the file name.
func (w *Writer) Add(target, label string, args ...any) {
	w.t.Helper()
	var b strings.Builder
	b.WriteString("go test fuzz v1\n")
	for _, a := range args {
		b.WriteString(encode(w.t, a))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	name := sanitize(label) + "-" + hex.EncodeToString(sum[:4])
	dir := filepath.Join(w.dir, target)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// Fixtures reads every file matching the glob patterns (relative to the
// package directory), keyed by the directory's name and the file's base name.
// It fails the test when a pattern matches nothing, so a moved fixture
// directory cannot silently empty a corpus.
func Fixtures(t testing.TB, patterns ...string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("fuzzseed: no fixtures match %q", pat)
		}
		for _, m := range matches {
			raw, err := os.ReadFile(m) //nolint:gosec // a repository fixture
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(filepath.Dir(m)) + "-" + strings.TrimSuffix(filepath.Base(m), filepath.Ext(m))
			out[name] = raw
		}
	}
	return out
}

func sanitize(s string) string {
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "seed"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}

func encode(t testing.TB, v any) string {
	t.Helper()
	switch x := v.(type) {
	case string:
		return "string(" + strconv.Quote(x) + ")"
	case []byte:
		return "[]byte(" + strconv.Quote(string(x)) + ")"
	case bool:
		return fmt.Sprintf("bool(%t)", x)
	case int:
		return fmt.Sprintf("int(%d)", x)
	case int64:
		return fmt.Sprintf("int64(%d)", x)
	case uint8:
		return fmt.Sprintf("uint8(%d)", x)
	case uint16:
		return fmt.Sprintf("uint16(%d)", x)
	case uint32:
		return fmt.Sprintf("uint32(%d)", x)
	default:
		t.Fatalf("fuzzseed: unsupported argument type %T", v)
		return ""
	}
}
