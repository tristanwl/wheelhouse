// Package guards holds source-level tests: rules of the threat model that are
// about how the code is written rather than what one function returns, so a
// future change that breaks them fails in CI instead of in review. Each test
// names the threat-model row it enforces (docs/threat-model.md).
package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// serverDir is the module root (this file is in server/internal/guards).
const serverDir = "../.."

// production parses every non-test Go file under the server module.
func production(t *testing.T, visit func(path string, fset *token.FileSet, f *ast.File)) {
	t.Helper()
	n := 0
	err := filepath.WalkDir(serverDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		n++
		visit(filepath.ToSlash(path), fset, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 50 {
		t.Fatalf("parsed only %d Go files; is serverDir right?", n)
	}
}

func selector(call *ast.CallExpr) (pkg, name string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		return id.Name, sel.Sel.Name
	}
	return "", sel.Sel.Name
}

func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

var sqlVerb = regexp.MustCompile(`(?i)^\s*(select|insert|update|delete|create|drop|alter|pragma)\b`)

// E4 (SQL injection): statements use bound parameters. The only SQL assembled
// with fmt.Sprintf is in store/secrets.go, where table and column names come
// from the fixed store.SecretColumns list and are checked against a pattern;
// nothing may build a statement by concatenation, and no other file may format
// SQL text.
func TestSQLIsNeverBuiltFromStringsOutsideTheSecretColumnCode(t *testing.T) {
	const allowed = "internal/store/secrets.go"
	dbCalls := map[string]bool{"Exec": true, "ExecContext": true, "Query": true, "QueryContext": true, "QueryRow": true, "QueryRowContext": true, "Prepare": true, "PrepareContext": true}
	production(t, func(path string, fset *token.FileSet, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkg, name := selector(call)
			if pkg == "fmt" && name == "Sprintf" && len(call.Args) > 0 {
				if s, ok := literal(call.Args[0]); ok && sqlVerb.MatchString(s) && !strings.HasSuffix(path, allowed) {
					t.Errorf("%s: SQL formatted with Sprintf; use bound parameters (%s)", fset.Position(call.Pos()), s)
				}
			}
			if dbCalls[name] {
				for _, a := range call.Args {
					if bin, ok := a.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
						if _, isLit := literal(bin.X); isLit {
							t.Errorf("%s: SQL built by concatenation; use bound parameters", fset.Position(call.Pos()))
						}
					}
				}
			}
			return true
		})
	})
}

// T-8 and E6: external programs are started with fixed arguments and never
// through a shell, and only the converter and the feature probe start any.
func TestNoShellIsEverStartedAndOnlyFfmpegCodeRunsPrograms(t *testing.T) {
	allowed := []string{"internal/converter/", "internal/features/features.go"}
	shells := map[string]bool{"sh": true, "bash": true, "/bin/sh": true, "/bin/bash": true, "cmd": true, "cmd.exe": true, "powershell": true, "zsh": true, "ash": true, "busybox": true}
	production(t, func(path string, fset *token.FileSet, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkg, name := selector(call)
			if pkg == "syscall" && (name == "Exec" || name == "ForkExec") {
				t.Errorf("%s: syscall.%s is not allowed", fset.Position(call.Pos()), name)
			}
			if pkg != "exec" || (name != "Command" && name != "CommandContext") {
				return true
			}
			ok2 := false
			for _, a := range allowed {
				if strings.Contains(path, a) {
					ok2 = true
				}
			}
			if !ok2 {
				t.Errorf("%s: %s starts a program outside the converter", fset.Position(call.Pos()), path)
			}
			for _, a := range call.Args {
				if s, isLit := literal(a); isLit && (shells[s] || s == "-c") {
					t.Errorf("%s: a shell (%q) must never be started", fset.Position(call.Pos()), s)
				}
			}
			return true
		})
	})
}

// S6: TLS verification is always on, and there is no switch to turn it off.
func TestTLSVerificationIsNeverDisabled(t *testing.T) {
	production(t, func(path string, fset *token.FileSet, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "InsecureSkipVerify" {
					t.Errorf("%s: InsecureSkipVerify must not be set", fset.Position(kv.Pos()))
				}
			}
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "InsecureSkipVerify" {
				t.Errorf("%s: InsecureSkipVerify must not be used", fset.Position(sel.Pos()))
			}
			return true
		})
	})
}

// "No way to turn security off" (plan principle 1): no environment variable or
// flag is named like a switch that disables authentication or the checks that
// protect it.
func TestThereIsNoSwitchToDisableAuthentication(t *testing.T) {
	bad := regexp.MustCompile(`(?i)(disable|skip|no|insecure|bypass)_?(auth|csrf|login|origin|tls|verify)|(auth|csrf|login)_?(disable|off|none)`)
	production(t, func(path string, fset *token.FileSet, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			_, name := selector(call)
			if (name == "Getenv" || name == "LookupEnv" || name == "getenv") && len(call.Args) > 0 {
				if s, ok := literal(call.Args[0]); ok && bad.MatchString(s) {
					t.Errorf("%s: environment variable %q looks like a security switch", fset.Position(call.Pos()), s)
				}
			}
			return true
		})
	})
	// And the documented variable list has none either.
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile("`(WHEELHOUSE_[A-Z_]+)`").FindAllStringSubmatch(string(readme), -1) {
		if bad.MatchString(m[1]) {
			t.Errorf("README names %s, which looks like a security switch", m[1])
		}
	}
}
