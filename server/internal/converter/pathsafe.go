// Package converter converts library files to another video codec in place
// with ffmpeg: it detects the usable encoders, keeps an in-memory job queue and
// runs one conversion at a time. It mirrors lib/convert-queue.ts, lib/hw-encode.ts
// and lib/hardlink.ts of the Next app.
//
// Safety rules, all enforced here and not in the handlers:
//   - ffmpeg and ffprobe are run directly (never through a shell) with
//     arguments built from validated enum values and an absolute, resolved path.
//   - A file is only touched after its path has been resolved (symlinks
//     included) and found to be inside one of the media roots, and that is
//     checked again just before the job runs and just before the swap.
//   - The converted file is written next to the original under a temporary
//     name, and only a non-empty result replaces the original, which is kept as
//     "<file>.bak".
package converter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMediaRoots are the folders conversion may touch. They are the paths
// Radarr and Sonarr use in the reference container layout; making them
// configurable per install is a later item (issue #31), so this is the one
// place to change.
var DefaultMediaRoots = []string{"/movies", "/tv"}

// RootsEnv overrides the media roots with a list of absolute paths separated
// like PATH (":" on Linux). It exists so tests and the acceptance suite can
// point the converter at scratch folders; the default is DefaultMediaRoots.
const RootsEnv = "WHEELHOUSE_MEDIA_ROOTS"

// RootsFromEnv returns the media roots for getenv (os.Getenv in production).
func RootsFromEnv(getenv func(string) string) []string {
	v := getenv(RootsEnv)
	if v == "" {
		return DefaultMediaRoots
	}
	var roots []string
	for _, r := range filepath.SplitList(v) {
		if filepath.IsAbs(r) {
			roots = append(roots, filepath.Clean(r))
		}
	}
	if len(roots) == 0 {
		return DefaultMediaRoots
	}
	return roots
}

// ErrOutsideRoots is returned for a path that is not a regular file inside a
// media root. It does not say which of those it was, so it cannot be used to
// find out what exists elsewhere on the host.
var ErrOutsideRoots = errors.New("the file is not inside the media folders")

// ResolveInRoots returns the real path of p (every symlink resolved) if it is
// a regular file inside one of roots, and ErrOutsideRoots otherwise. p must be
// absolute and must not contain a ".." element; roots are resolved too, so a
// root that is a symlink works and a symlink inside a root that points out of
// it does not.
func ResolveInRoots(roots []string, p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return "", ErrOutsideRoots
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return "", ErrOutsideRoots
		}
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", ErrOutsideRoots
	}
	info, err := os.Lstat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrOutsideRoots
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		rr, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		if within(rr, real) {
			return real, nil
		}
	}
	return "", ErrOutsideRoots
}

// within reports whether path is strictly below dir (both cleaned and real).
func within(dir, path string) bool {
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return len(path) > len(prefix) && strings.HasPrefix(path, prefix)
}
