package fuzzseed_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The fuzz targets, the seed corpora and the scheduled workflow must stay in
// step: a target nobody runs on a schedule, or one whose seeds were deleted (so
// `go test` replays nothing for it), is a hole nobody sees.

type target struct{ pkg, name string }

func (t target) String() string { return t.pkg + " " + t.name }

var fuzzFunc = regexp.MustCompile(`(?m)^func (Fuzz\w+)\(f \*testing\.F\)`)

// declaredTargets finds every fuzz target under server/internal.
func declaredTargets(t *testing.T) []target {
	t.Helper()
	var out []target
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, rerr := os.ReadFile(path) //nolint:gosec // a source file of this repository
		if rerr != nil {
			return rerr
		}
		for _, m := range fuzzFunc.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, target{pkg: "./internal/" + filepath.ToSlash(strings.TrimPrefix(filepath.Dir(path), "..")[1:]), name: m[1]})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 40 {
		t.Fatalf("found only %d fuzz targets", len(out))
	}
	return out
}

func TestEveryFuzzTargetIsInTheScheduledWorkflow(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "fuzz.yml"))
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`- \{ pkg: (\S+), target: (\w+) \}`)
	var listed []string
	for _, m := range entry.FindAllStringSubmatch(string(raw), -1) {
		listed = append(listed, target{m[1], m[2]}.String())
	}
	var declared []string
	for _, tg := range declaredTargets(t) {
		declared = append(declared, tg.String())
	}
	slices.Sort(listed)
	slices.Sort(declared)
	for _, d := range declared {
		if !slices.Contains(listed, d) {
			t.Errorf("fuzz target %s is not in .github/workflows/fuzz.yml (add it to the matrix)", d)
		}
	}
	for _, l := range listed {
		if !slices.Contains(declared, l) {
			t.Errorf(".github/workflows/fuzz.yml lists %s, which does not exist", l)
		}
	}
	if slices.ContainsFunc(listed, func(s string) bool { return strings.Count(s, " ") != 1 }) {
		t.Errorf("malformed matrix entry in %v", listed)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "uses:") && !regexp.MustCompile(`@[0-9a-f]{40}( |$)`).MatchString(line) {
			t.Errorf("action not pinned by commit SHA: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(string(raw), "permissions:\n  contents: read\n") {
		t.Error("the workflow must declare `permissions: contents: read`")
	}
	if strings.Contains(string(raw), "pull_request") || strings.Contains(string(raw), "push:") {
		t.Error("fuzzing must not run on pull requests or pushes")
	}
}

func TestEveryFuzzTargetHasASeedCorpus(t *testing.T) {
	for _, tg := range declaredTargets(t) {
		dir := filepath.Join("..", strings.TrimPrefix(tg.pkg, "./internal/"), "testdata", "fuzz", tg.name)
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			t.Errorf("%s has no seed corpus in %s (regenerate with WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds %s)", tg, dir, tg.pkg)
		}
	}
}
