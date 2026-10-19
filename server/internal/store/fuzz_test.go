package store

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// FuzzLoadMigrations: the migration file set.
//
// Invariants: a set is accepted only if every file is named NNNN_name.sql (four
// digits, lower-case letters, digits and underscores) and the versions run from
// 0001 without a gap or a duplicate; accepted migrations carry the file's exact
// bytes and a checksum of them; nothing else (a directory, another extension,
// an upper-case name, a path with a separator) is ever accepted.
func FuzzLoadMigrations(f *testing.F) {
	re := regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)
	f.Fuzz(func(t *testing.T, n1, n2, n3, body string, asDir bool) {
		fsys := fstest.MapFS{}
		var names []string
		for _, n := range []string{n1, n2, n3} {
			if n == "" || strings.ContainsAny(n, "/\\\x00") || n == "." || n == ".." {
				continue
			}
			if _, dup := fsys["m/"+n]; dup {
				continue
			}
			fsys["m/"+n] = &fstest.MapFile{Data: []byte(body + n)}
			names = append(names, n)
		}
		if asDir && len(names) > 0 {
			fsys["m/"+names[0]] = &fstest.MapFile{Mode: 0o20000000000 /* fs.ModeDir */}
		}
		ms, err := loadMigrations(fsys, "m")
		if err != nil {
			return
		}
		// Re-derive the expectation: sorted names, each matching, versions 1..n.
		if len(ms) != len(names) {
			t.Fatalf("%d migrations from %d files %q", len(ms), len(names), names)
		}
		for i, m := range ms {
			sub := re.FindStringSubmatch(m.name + ".sql")
			if sub == nil {
				t.Fatalf("accepted the name %q", m.name)
			}
			if v, _ := strconv.Atoi(sub[1]); v != i+1 || m.version != i+1 {
				t.Fatalf("migration %q has version %d at position %d", m.name, m.version, i+1)
			}
			if m.sql != body+m.name+".sql" || len(m.sum) != 64 {
				t.Fatalf("migration %q lost its content or checksum", m.name)
			}
		}
	})
}

// FuzzDSN: the connection string made from a database path.
//
// Invariants: the path (absolute and clean, as Open passes it) can neither add a
// parameter nor end itself early: the string parses as a "file:" URL whose path is
// the given one and whose host is empty, and the only query parameters are the ones the code sets (the pragmas
// that harden the connection, _txlock and _defensive), whatever characters the
// path has (?, #, %, &, =, spaces, NUL, newlines, unicode).
func FuzzDSN(f *testing.F) {
	allowed := map[string]bool{"_pragma": true, "_txlock": true, "_defensive": true}
	f.Fuzz(func(t *testing.T, p string, writer bool) {
		p = filepath.Clean("/" + p) // Open hands dsn the absolute, cleaned path
		d := dsn(p, writer)
		u, err := url.Parse(d)
		if err != nil {
			t.Fatalf("dsn(%q) = %q does not parse: %v", p, d, err)
		}
		if u.Scheme != "file" {
			t.Fatalf("dsn(%q) = %q has scheme %q", p, d, u.Scheme)
		}
		for k := range u.Query() {
			if !allowed[k] {
				t.Fatalf("dsn(%q) = %q carries the parameter %q", p, d, k)
			}
		}
		wantPragmas := 6 // busy_timeout, foreign_keys, secure_delete, trusted_schema, cell_size_check, query_only
		if writer {
			wantPragmas = 7 // journal_mode and synchronous instead of query_only
		}
		if got := len(u.Query()["_pragma"]); got != wantPragmas {
			t.Fatalf("dsn(%q): %d pragmas, want %d", p, got, wantPragmas)
		}
		if writer == (u.Query().Get("_txlock") == "") {
			t.Fatalf("dsn(%q, writer=%v): _txlock %q", p, writer, u.Query().Get("_txlock"))
		}
		if u.Query().Get("_defensive") != "1" {
			t.Fatalf("dsn(%q) is not defensive", p)
		}
		if u.Path != filepath.ToSlash(p) || u.Host != "" {
			t.Fatalf("dsn(%q) = %q reads back the path %q on host %q", p, d, u.Path, u.Host)
		}
	})
}
