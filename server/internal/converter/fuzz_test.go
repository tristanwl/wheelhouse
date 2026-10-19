package converter

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/codecs"
)

// pathFarm is a directory tree built to be attacked: two media roots (one
// reached through a symlink), a folder outside them, and inside the roots
// symlinks that point out, in, around in circles and nowhere, a hard link to a
// file outside, awkward names (spaces, accents, a name of 200 characters) and a
// sibling whose name starts with a root's name.
type pathFarm struct {
	farm, root1, root1Alias, root2, outside string
	roots                                   []string
}

func newPathFarm(tb testing.TB, dir string) *pathFarm {
	tb.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		tb.Fatal(err)
	}
	f := &pathFarm{farm: real, root1: filepath.Join(real, "root1"), root1Alias: filepath.Join(real, "root1-alias"), root2: filepath.Join(real, "root2"), outside: filepath.Join(real, "outside")}
	mk := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			tb.Fatal(err)
		}
	}
	file := func(p string) {
		mk(filepath.Dir(p))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	link := func(target, name string) {
		if err := os.Symlink(target, name); err != nil {
			tb.Skipf("symlinks unavailable: %v", err)
		}
	}
	file(filepath.Join(f.root1, "movie.mkv"))
	file(filepath.Join(f.root1, "sub dir", "ep 1.mkv"))
	file(filepath.Join(f.root1, "ünï côdé", "é.mkv"))
	file(filepath.Join(f.root1, strings.Repeat("x", 200)+".mkv"))
	file(filepath.Join(f.root1, "...", "dots.mkv"))
	file(filepath.Join(f.root1, "-leading-dash.mkv"))
	mk(filepath.Join(f.root1, "empty-dir"))
	file(filepath.Join(f.root2, "inner.mkv"))
	file(filepath.Join(f.outside, "secret.mkv"))
	file(filepath.Join(f.outside, "dir", "deep.mkv"))
	file(f.root1 + "-evil/a.mkv")
	link(f.outside, filepath.Join(f.root1, "link-out"))
	link(filepath.Join(f.outside, "secret.mkv"), filepath.Join(f.root1, "link-file-out.mkv"))
	link("sub dir", filepath.Join(f.root1, "link-in"))
	link(filepath.Join("sub dir", "ep 1.mkv"), filepath.Join(f.root1, "link-file-in.mkv"))
	link(".", filepath.Join(f.root1, "loop"))
	link("b", filepath.Join(f.root1, "a"))
	link("a", filepath.Join(f.root1, "b"))
	link("nowhere.mkv", filepath.Join(f.root1, "dangling.mkv"))
	link("../outside/dir", filepath.Join(f.root1, "relative-out"))
	link(f.root1, f.root1Alias)
	link(f.root2, filepath.Join(f.outside, "to-root2"))
	if err := os.Link(filepath.Join(f.outside, "secret.mkv"), filepath.Join(f.root1, "hardlink.mkv")); err != nil {
		tb.Logf("hard links unavailable: %v", err)
	}
	f.roots = []string{f.root1Alias, f.root2, filepath.Join(f.farm, "not-there"), "relative/root", ""}
	return f
}

// build turns a selector and text into a path in or near the farm.
func (f *pathFarm) build(sel uint8, s string) string {
	switch sel % 7 {
	case 0:
		return s
	case 1:
		return f.farm + "/" + s
	case 2:
		return f.root1 + "/" + s
	case 3:
		return f.root1Alias + "/" + s
	case 4:
		return f.root2 + "/" + s
	case 5:
		return f.outside + "/" + s
	default:
		return f.root1 + s
	}
}

// inside reports whether p is strictly below dir, using filepath.Rel (the code
// under test compares prefixes; this is a different way to ask).
func inside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func realOf(tb testing.TB, p string) string {
	tb.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return r
}

// FuzzResolveInRoots: the containment check that decides which files a
// conversion may touch.
//
// Invariants: a path with a ".." element, a NUL, an empty or relative path is
// always refused; any refusal is exactly ErrOutsideRoots (it says nothing about
// what exists elsewhere); an accepted path is absolute, clean, itself free of
// symlinks, a regular file, strictly inside the real path of one of the roots,
// the very file the input names after every symlink is followed, and never
// inside the folder outside the roots; and a plain path that really is a regular
// file inside a root is not refused.
func FuzzResolveInRoots(f *testing.F) {
	farm := newPathFarm(f, f.TempDir())
	f.Fuzz(func(t *testing.T, sel uint8, s string) {
		p := farm.build(sel, s)
		got, err := ResolveInRoots(farm.roots, p)

		hasDotDot := false
		for _, part := range strings.Split(filepath.ToSlash(p), "/") {
			hasDotDot = hasDotDot || part == ".."
		}
		refusedUpFront := p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) || hasDotDot

		if err != nil {
			if !errors.Is(err, ErrOutsideRoots) || err.Error() != ErrOutsideRoots.Error() || got != "" {
				t.Fatalf("ResolveInRoots(%q) = %q, %v", p, got, err)
			}
			if refusedUpFront {
				return
			}
			// Completeness: a good path must not be refused.
			target := realOf(t, filepath.Clean(p))
			if target == "" {
				return
			}
			info, lerr := os.Lstat(target)
			if lerr != nil || !info.Mode().IsRegular() {
				return
			}
			for _, root := range []string{farm.root1, farm.root2} {
				if inside(root, target) {
					t.Fatalf("a regular file inside %s was refused: %q resolves to %q", root, p, target)
				}
			}
			return
		}

		if refusedUpFront {
			t.Fatalf("ResolveInRoots(%q) accepted a path that is refused up front: %q", p, got)
		}
		if !filepath.IsAbs(got) || filepath.Clean(got) != got {
			t.Fatalf("result %q is not an absolute clean path", got)
		}
		if again := realOf(t, got); again != got {
			t.Fatalf("result %q still has a symlink (real path %q)", got, again)
		}
		if want := realOf(t, filepath.Clean(p)); want != got {
			t.Fatalf("result %q is not what %q resolves to (%q)", got, p, want)
		}
		info, lerr := os.Lstat(got)
		if lerr != nil || !info.Mode().IsRegular() {
			t.Fatalf("result %q is not a regular file", got)
		}
		if !inside(farm.root1, got) && !inside(farm.root2, got) {
			t.Fatalf("result %q is outside the roots (%s, %s)", got, farm.root1, farm.root2)
		}
		if inside(farm.outside, got) && !strings.HasSuffix(got, "hardlink.mkv") {
			t.Fatalf("result %q is in the folder outside the roots", got)
		}
	})
}

// FuzzValidateRoots: the media roots an administrator types.
//
// Invariants: an accepted list has absolute, clean entries that exist and are
// folders; none is, contains or lies inside a system folder or the protected
// folder (after symlinks); none overlaps another; and "/" is never a root.
func FuzzValidateRoots(f *testing.F) {
	farm := newPathFarm(f, f.TempDir())
	protected := filepath.Join(farm.farm, "protected")
	if err := os.MkdirAll(filepath.Join(protected, "inner"), 0o755); err != nil {
		f.Fatal(err)
	}
	system := []string{"/etc", "/proc", "/sys", "/dev", "/root", "/boot", "/bin", "/sbin", "/usr", "/lib", "/lib32", "/lib64", "/libx32", "/run", "/var/lib"}
	f.Fuzz(func(t *testing.T, s1, s2, s3 string, sel uint8) {
		in := []string{farm.build(sel, s1), farm.build(sel>>3, s2), farm.build(sel>>5, s3)}
		if sel&1 == 0 {
			in[2] = s3
		}
		got, err := ValidateRoots(in, []string{protected})
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error is %T", err)
			}
			if len(ve.Msg) > 1200 {
				t.Fatalf("error text of %d bytes", len(ve.Msg))
			}
			return
		}
		if len(got) == 0 || len(got) > len(in) {
			t.Fatalf("%d roots from %d", len(got), len(in))
		}
		reals := make([]string, len(got))
		for i, r := range got {
			if !filepath.IsAbs(r) || filepath.Clean(r) != r || r == "/" {
				t.Fatalf("root %q is not an absolute clean path other than /", r)
			}
			real := realOf(t, r)
			info, serr := os.Stat(real)
			if real == "" || serr != nil || !info.IsDir() {
				t.Fatalf("root %q is not an existing folder", r)
			}
			if real == "/" {
				t.Fatalf("root %q is the file system root", r)
			}
			for _, d := range append([]string{protected}, system...) {
				for _, cand := range []string{r, real} {
					if cand == d || inside(d, cand) || inside(cand, d) {
						t.Fatalf("root %q (real %q) overlaps %s", r, real, d)
					}
				}
			}
			reals[i] = real
		}
		for i := range got {
			for j := i + 1; j < len(got); j++ {
				if got[i] == got[j] || inside(got[i], got[j]) || inside(got[j], got[i]) || reals[i] == reals[j] || inside(reals[i], reals[j]) || inside(reals[j], reals[i]) {
					t.Fatalf("roots %q and %q overlap", got[i], got[j])
				}
			}
		}
	})
}

// FuzzEnvRoots: WHEELHOUSE_MEDIA_ROOTS keeps only absolute, clean paths.
func FuzzEnvRoots(f *testing.F) {
	f.Fuzz(func(t *testing.T, v string) {
		for _, r := range EnvRoots(func(string) string { return v }) {
			if !filepath.IsAbs(r) || filepath.Clean(r) != r {
				t.Fatalf("%q produced %q", v, r)
			}
		}
	})
}

// FuzzArgs: the ffmpeg argument vector.
//
// Invariants: every element other than the input and output paths comes from
// the fixed vocabulary of the builder (settings values, valid or not, only
// ever select a known encoder); the input path is exactly one element, right
// after "-i", and the output path exactly one, last (so a name with spaces,
// quotes, newlines or "=" is never split or reinterpreted); and, as the paths
// are absolute, neither can start with "-" and be read as an option.
func FuzzArgs(f *testing.F) {
	vocabulary := map[string]bool{}
	for _, v := range []string{
		"-nostdin", "-y", "-vaapi_device", vaapiDevice, "-protocol_whitelist", "file", "-i", "-map", "0",
		"-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "hevc_vaapi", "h264_nvenc", "hevc_nvenc", "libx264", "libx265",
		"-qp", "20", "-c:a", "copy", "-c:s", "-progress", "pipe:1", "-nostats", "-cq", "-preset", "p5", "-pix_fmt", "yuv420p",
		"-c", "medium", "-crf",
	} {
		vocabulary[v] = true
	}
	f.Fuzz(func(t *testing.T, backend, family, file, temp string) {
		file, temp = "/"+strings.TrimLeft(file, "/"), "/"+strings.TrimLeft(temp, "/")
		args := Args(Backend(backend), Family(family), file, temp)
		if len(args) < 5 || args[len(args)-1] != temp {
			t.Fatalf("the output path is not the last argument: %q", args)
		}
		inputAt := -1
		for i, a := range args[:len(args)-1] {
			switch {
			case i > 0 && args[i-1] == "-i":
				if a != file || inputAt >= 0 {
					t.Fatalf("argument after -i is %q, want the input %q once", a, file)
				}
				inputAt = i
			case vocabulary[a]:
			default:
				t.Fatalf("argument %d %q is not from the builder's vocabulary (backend %q family %q)", i, a, backend, family)
			}
		}
		if inputAt < 0 {
			t.Fatalf("no input in %q", args)
		}
		for _, p := range []string{args[inputAt], args[len(args)-1]} {
			if strings.HasPrefix(p, "-") {
				t.Fatalf("path %q reads as an option", p)
			}
		}
		// Valid settings select the encoder named for them, and nothing else does.
		want := map[[2]string]string{
			{"software", "h264"}: "libx264", {"software", "hevc"}: "libx265",
			{"vaapi", "h264"}: "h264_vaapi", {"vaapi", "hevc"}: "hevc_vaapi",
			{"nvenc", "h264"}: "h264_nvenc", {"nvenc", "hevc"}: "hevc_nvenc",
		}
		if enc, ok := want[[2]string{backend, family}]; ok {
			found := false
			for i, a := range args {
				if a == "-c:v" && args[i+1] == enc {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s/%s did not select %s: %q", backend, family, enc, args)
			}
		}
	})
}

// FuzzBackupNames: the backup file names.
//
// Invariants: every name the builder makes for the first 99 tries is recognised
// as a backup of that file, only those are, and none of them is another file's.
func FuzzBackupNames(f *testing.F) {
	f.Fuzz(func(t *testing.T, src, other string, n uint8) {
		src = "/" + strings.TrimLeft(src, "/")
		if i := int(n) % 140; i < maxBackupNames {
			if b := backupName(src, i); !isBackupNameOf(src, b) {
				t.Fatalf("%q is not recognised as backup %d of %q", b, i, src)
			}
		} else if isBackupNameOf(src, backupName(src, i)) {
			t.Fatalf("backup number %d of %q is recognised", i, src)
		}
		if isBackupNameOf(src, other) {
			ok := false
			for i := 0; i < maxBackupNames; i++ {
				ok = ok || backupName(src, i) == other
			}
			if !ok {
				t.Fatalf("%q is recognised as a backup of %q but is none of its names", other, src)
			}
		}
	})
}

// FuzzProgressWriter: ffmpeg's -progress stream.
//
// Invariants: every write is consumed whole; only finite numbers are reported
// (NaN and infinities are dropped), whatever the garbage around them; the
// reported sequence does not depend on where the stream was cut into writes; and
// a stream with no newline cannot grow the buffer without bound.
func FuzzProgressWriter(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte, cut1, cut2 uint8) {
		collect := func(chunks ...[]byte) ([]float64, int) {
			var got []float64
			w := &progressWriter{onTime: func(us float64) { got = append(got, us) }}
			for _, c := range chunks {
				n, err := w.Write(c)
				if n != len(c) || err != nil {
					t.Fatalf("Write returned %d, %v for %d bytes", n, err, len(c))
				}
			}
			return got, len(w.buf)
		}
		a, b := int(cut1)%(len(data)+1), int(cut2)%(len(data)+1)
		if a > b {
			a, b = b, a
		}
		whole, _ := collect(data)
		pieces, held := collect(data[:a], data[a:b], data[b:])
		for _, v := range append(append([]float64(nil), whole...), pieces...) {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("reported %v", v)
			}
		}
		if len(data) < 1<<16 {
			if len(whole) != len(pieces) {
				t.Fatalf("whole stream reports %v, cut stream reports %v", whole, pieces)
			}
			for i := range whole {
				if whole[i] != pieces[i] {
					t.Fatalf("whole stream reports %v, cut stream reports %v", whole, pieces)
				}
			}
		}
		if held > len(data) || held > 1<<16+len(data) {
			t.Fatalf("buffer holds %d bytes of a %d byte stream", held, len(data))
		}
		// A stream of one endless line is dropped, not kept.
		if _, held := collect(bytes.Repeat([]byte("x"), 3<<16)); held > 1<<16 {
			t.Fatalf("an unterminated line is held (%d bytes)", held)
		}
	})
}

// FuzzClassifyProbe: HDR classification from ffprobe's JSON.
//
// Invariants: always one of the five valid classes; anything that is not the
// expected document is "unknown", never "none" (an answer that could not be read
// must not let an HDR file through); and for a document that is the expected shape
// the class agrees with an independent reading of it: Dolby Vision side data, the
// PQ transfer, the HLG transfer and BT.2020 primaries with any other transfer are
// the HDR classes, and only an explicitly SDR first stream is "none".
func FuzzClassifyProbe(f *testing.F) {
	f.Fuzz(func(t *testing.T, raw []byte) {
		got := ClassifyProbe(raw)
		if !got.Valid() {
			t.Fatalf("invalid class %q", got)
		}
		var doc struct {
			Streams []map[string]any `json:"streams"`
		}
		if json.Unmarshal(raw, &doc) != nil || len(doc.Streams) == 0 {
			if got != codecs.HDRUnknown {
				t.Fatalf("an unreadable answer %q classified as %q", raw, got)
			}
			return
		}
		// encoding/json matches field names without regard to case; the oracle
		// reads the same way, and skips documents where that is ambiguous.
		fold := func(m map[string]any) map[string]any {
			out := make(map[string]any, len(m))
			for k, v := range m {
				lk := strings.ToLower(k)
				if _, dup := out[lk]; dup {
					t.Skip()
				}
				out[lk] = v
			}
			return out
		}
		s := fold(doc.Streams[0])
		str := func(key string) (string, bool) {
			v, ok := s[key].(string)
			return strings.ToLower(v), ok || s[key] == nil
		}
		transfer, okT := str("color_transfer")
		primaries, okP := str("color_primaries")
		if !okT || !okP {
			if got != codecs.HDRUnknown {
				t.Fatalf("a stream with odd types classified as %q: %q", got, raw)
			}
			return
		}
		dovi, odd := false, false
		if list, ok := s["side_data_list"].([]any); ok {
			for _, e := range list {
				raw, ok := e.(map[string]any)
				if !ok {
					odd = true
					continue
				}
				m := fold(raw)
				switch ty := m["side_data_type"].(type) {
				case string:
					ty = strings.ToLower(ty)
					dovi = dovi || strings.Contains(ty, "dovi") || strings.Contains(ty, "dolby vision")
				case nil:
				default:
					odd = true
				}
			}
		} else if s["side_data_list"] != nil {
			odd = true
		}
		if odd {
			// The decoder refuses a document of the wrong types: unknown, not none.
			if got != codecs.HDRUnknown {
				t.Fatalf("a document with odd types classified as %q: %q", got, raw)
			}
			return
		}
		want := codecs.HDRNone
		switch {
		case dovi:
			want = codecs.HDRDolbyVision
		case transfer == "smpte2084":
			want = codecs.HDRHDR10
		case transfer == "arib-std-b67":
			want = codecs.HDRHLG
		case strings.HasPrefix(primaries, "bt2020"):
			want = codecs.HDRUnknown
		}
		if got != want {
			t.Fatalf("classified as %q, want %q: %q", got, want, raw)
		}
	})
}
