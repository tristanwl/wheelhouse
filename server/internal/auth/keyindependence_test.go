package auth

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// docs/operations.md promises that losing the master key does not affect
// accounts or sessions: passwords are Argon2id hashes and session tokens are
// SHA-256 hashes, so nothing in this package is encrypted with the key. This test
// keeps that sentence true: the package must not import internal/secrets, and no
// account table may be a registered secret column.
func TestAccountsNeverDependOnTheMasterKey(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range tree.Imports {
			if strings.Contains(imp.Path.Value, "internal/secrets") {
				t.Errorf("%s imports %s: accounts must not depend on the master key", f, imp.Path.Value)
			}
		}
	}
	for _, c := range store.SecretColumns() {
		switch c.Table {
		case "users", "sessions", "audit_log":
			t.Errorf("%s.%s is registered as a secret column: losing the key would lock people out", c.Table, c.Column)
		}
	}
}
