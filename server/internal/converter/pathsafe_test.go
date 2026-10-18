package converter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveInRoots(t *testing.T) {
	root, file := library(t, "Example Movie 1 (2025).mkv", "x")
	outside, secret := library(t, "secret.mkv", "s")
	sibling := root + "-evil" // shares the root's name as a prefix
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sibling) })
	siblingFile := filepath.Join(sibling, "a.mkv")
	if err := os.WriteFile(siblingFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Symlinks inside the root: one to a file outside, one to a directory
	// outside, one to a file inside (which is fine).
	linkOut := filepath.Join(root, "link-out.mkv")
	linkDir := filepath.Join(root, "link-dir")
	linkIn := filepath.Join(root, "link-in.mkv")
	for from, to := range map[string]string{linkOut: secret, linkDir: outside, linkIn: file} {
		if err := os.Symlink(to, from); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	// A symlinked root works too.
	rootLink := filepath.Join(t.TempDir(), "rootlink")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}

	roots := []string{root, "/definitely/not/here", "relative"}
	cases := []struct {
		name string
		path string
		ok   bool
	}{
		{"file in root", file, true},
		{"symlink to a file inside", linkIn, true},
		{"unclean but inside", filepath.Join(root, "sub", ".", "..", "x"), false}, // ".." is refused outright
		{"dot-dot escape", root + "/../" + filepath.Base(outside) + "/secret.mkv", false},
		{"dot-dot that stays inside", root + "/sub/../" + filepath.Base(file), false},
		{"symlinked file pointing out", linkOut, false},
		{"file below a symlinked dir pointing out", filepath.Join(linkDir, "secret.mkv"), false},
		{"outside the root", secret, false},
		{"sibling sharing the root's name as a prefix", siblingFile, false},
		{"the root itself", root, false},
		{"a directory", sub, false},
		{"missing", filepath.Join(root, "missing.mkv"), false},
		{"relative", "Example Movie 1 (2025).mkv", false},
		{"empty", "", false},
		{"NUL", file + "\x00.png", false},
		{"/etc/passwd", "/etc/passwd", false},
	}
	for _, c := range cases {
		got, err := ResolveInRoots(roots, c.path)
		if (err == nil) != c.ok {
			t.Errorf("%s: ResolveInRoots(%q) = %q, %v; want ok=%v", c.name, c.path, got, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrOutsideRoots) {
			t.Errorf("%s: error %v leaks more than ErrOutsideRoots", c.name, err)
		}
	}

	got, err := ResolveInRoots([]string{rootLink}, file)
	if err != nil || got != file {
		t.Errorf("symlinked root: %q, %v", got, err)
	}
	got, err = ResolveInRoots(roots, linkIn)
	if err != nil || got != file {
		t.Errorf("a symlink inside resolves to its target: %q, %v", got, err)
	}
	if _, err := ResolveInRoots(nil, file); err == nil {
		t.Error("no roots means nothing is allowed")
	}
}

func TestRootsFromEnv(t *testing.T) {
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == RootsEnv {
				return v
			}
			return ""
		}
	}
	if got := RootsFromEnv(get("")); len(got) != 2 || got[0] != "/movies" || got[1] != "/tv" {
		t.Errorf("default: %v", got)
	}
	if got := RootsFromEnv(get("/a/b:/c/../d:rel")); len(got) != 2 || got[0] != "/a/b" || got[1] != "/d" {
		t.Errorf("custom: %v", got)
	}
	if got := RootsFromEnv(get("rel")); len(got) != 2 {
		t.Errorf("only relative paths fall back to the default: %v", got)
	}
}

func TestLinkCount(t *testing.T) {
	_, file := library(t, "a.mkv", "x")
	if n, ok := LinkCount(file); !ok || n != 1 {
		t.Fatalf("one link: %d %v", n, ok)
	}
	if err := os.Link(file, file+".seed"); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if n, ok := LinkCount(file); !ok || n != 2 {
		t.Fatalf("two links: %d %v", n, ok)
	}
	if _, ok := LinkCount(file + ".missing"); ok {
		t.Fatal("a missing file has no link count")
	}
}
