package converter

import (
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the cases of the converter
// tests, real ffprobe and ffmpeg -progress output and the path tricks of the
// threat model (symlink farms, "..", NUL, long and unicode names):
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/converter
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)

	// Paths relative to the farm: the selector says where they hang (see
	// pathFarm.build): 0 raw, 1 farm, 2 root1, 3 the root1 alias, 4 root2, 5 outside, 6 root1 + text.
	for _, c := range []struct {
		sel uint8
		s   string
	}{
		{2, "movie.mkv"}, {2, "sub dir/ep 1.mkv"}, {2, "ünï côdé/é.mkv"}, {2, strings.Repeat("x", 200) + ".mkv"}, {2, "...//dots.mkv"},
		{2, "-leading-dash.mkv"}, {2, "link-out/secret.mkv"}, {2, "link-file-out.mkv"}, {2, "link-in/ep 1.mkv"}, {2, "link-file-in.mkv"},
		{2, "loop/loop/loop/movie.mkv"}, {2, "a"}, {2, "b/movie.mkv"}, {2, "dangling.mkv"}, {2, "relative-out/deep.mkv"}, {2, "hardlink.mkv"},
		{2, "../outside/secret.mkv"}, {2, "sub dir/../movie.mkv"}, {2, "./movie.mkv"}, {2, "movie.mkv/"}, {2, "movie.mkv/."}, {2, "sub dir//ep 1.mkv"},
		{2, "sub dir/./ep 1.mkv"}, {2, "movie.mkv\x00.png"}, {2, ""}, {2, "empty-dir"}, {2, ".."}, {2, "%2e%2e/outside/secret.mkv"},
		{2, "..\\outside\\secret.mkv"}, {2, "link-out/../movie.mkv"}, {2, "link-out/to-root2/inner.mkv"},
		{3, "movie.mkv"}, {3, "link-out/secret.mkv"}, {4, "inner.mkv"}, {5, "secret.mkv"}, {5, "dir/deep.mkv"}, {5, "to-root2/inner.mkv"},
		{6, "-evil/a.mkv"}, {1, "root1/movie.mkv"}, {1, "root1-alias/movie.mkv"}, {1, "root1-evil/a.mkv"}, {1, "root2/../outside/secret.mkv"},
		{0, "/etc/passwd"}, {0, ""}, {0, "relative/movie.mkv"}, {0, "/"}, {0, "/proc/self/root/etc/passwd"}, {0, "/dev/null"},
	} {
		w.Add("FuzzResolveInRoots", "path", c.sel, c.s)
	}

	for _, c := range [][3]string{
		{"/", "", ""}, {"/etc", "/usr/local", ""}, {"/var/lib/x", "/var", ""}, {"/proc/self", "", ""}, {"/dev/shm", "", ""},
		{"relative", "", ""}, {"", "", ""}, {"/a\x00b", "", ""}, {"/tmp/../etc", "", ""}, {"movie.mkv", "root1/sub dir", "root2"},
		{"root1", "root2", ""}, {"root1", "root1/sub dir", ""}, {"root1-alias", "root1", ""}, {"root1/link-out", "", ""},
		{"root1/loop", "", ""}, {"protected", "", ""}, {"protected/inner", "", ""}, {"root1/movie.mkv", "", ""}, {"outside", "root2", "root1/empty-dir"},
		{"root1/" + strings.Repeat("d/", 2100), "", ""}, {" root2 ", "", ""},
	} {
		w.Add("FuzzValidateRoots", "roots", c[0], c[1], c[2], uint8(1))
		w.Add("FuzzValidateRoots", "roots-under-farm", c[0], c[1], c[2], uint8(0x92))
	}

	for _, v := range []string{"", "/movies:/tv", "/movies", "relative:/abs", ":::", "/a//b/../c:/d/", "/x\x00y", "/movies:/movies"} {
		w.Add("FuzzEnvRoots", "env", v)
	}

	for _, c := range [][4]string{
		{"software", "h264", "/movies/a.mkv", "/movies/.wheelhouse-convert-x.mkv"},
		{"software", "hevc", "/movies/Example Movie 1 (2025).mkv", "/movies/.wheelhouse-convert-x.mkv"},
		{"vaapi", "h264", "/movies/a.mkv", "/movies/.wheelhouse-convert-x.mkv"},
		{"vaapi", "hevc", "/movies/a.mkv", "/movies/.wheelhouse-convert-x.mkv"},
		{"nvenc", "h264", "/movies/a.mkv", "/movies/.wheelhouse-convert-x.mkv"},
		{"nvenc", "hevc", "/movies/a.mkv", "/movies/.wheelhouse-convert-x.mkv"},
		{"", "", "/movies/a.mkv", "/movies/b.mkv"}, {"-y", "-vf", "/movies/a.mkv", "/movies/b.mkv"}, {"Software", "av1", "/x", "/y"},
		{"software", "h264", "/-i", "/-y"}, {"software", "h264", "-i", "-y"}, {"software", "h264", "--", "//-f"},
		{"software", "h264", "/movies/a b\n-i /etc/passwd.mkv", "/movies/c=d,e:f.mkv"}, {"vaapi", "hevc", "/m/'quoted'\"x\".mkv", "/m/$(id).mkv"},
		{"nvenc", "h264", "/m/ü.mkv", "/m/\x00.mkv"}, {"software", "h264", "file:/etc/passwd", "pipe:1"}, {"software", "h264", "/concat:a|b", "/tmp/x.mkv"},
	} {
		w.Add("FuzzArgs", "args", c[0], c[1], c[2], c[3])
	}

	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak", uint8(0))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.1", uint8(1))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.99", uint8(99))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.100", uint8(100))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.01", uint8(2))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.-1", uint8(3))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/other/a.mkv.bak", uint8(4))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.1.bak", uint8(5))
	w.Add("FuzzBackupNames", "name", "/movies/a.mkv", "/movies/a.mkv.bak.+1", uint8(6))

	progress := "frame=100\nfps=25.0\nstream_0_0_q=-1.0\nbitrate=N/A\ntotal_size=N/A\nout_time_us=4000000\nout_time_ms=4000000\nout_time=00:00:04.000000\ndup_frames=0\ndrop_frames=0\nspeed=1.5x\nprogress=continue\nout_time_ms=8000000\nprogress=end\n"
	for _, c := range []struct {
		data []byte
		a, b uint8
	}{
		{[]byte(progress), 17, 90}, {[]byte(progress), 0, 255}, {[]byte("out_time_ms=N/A\nout_time_ms=-9223372036854775808\nout_time_ms=1e999\nout_time_ms=NaN\nout_time_ms=Inf\nout_time_ms= 5 \n"), 5, 40},
		{[]byte("out_time_ms=1000000"), 3, 9}, {[]byte("\n\n\nout_time_ms=\n=\nout_time_ms=1=2\n"), 2, 6}, {[]byte("out_time_ms=0x10\nout_time_ms=1_0\nout_time_ms=+5\n"), 4, 12},
		{[]byte(strings.Repeat("out_time_ms=1\n", 100)), 50, 200}, {[]byte(strings.Repeat("x", 70000) + "\nout_time_ms=3\n"), 200, 250},
	} {
		w.Add("FuzzProgressWriter", "progress", c.data, c.a, c.b)
	}

	for _, doc := range []string{
		`{"streams":[{"color_transfer":"bt709"}]}`,
		`{"streams":[{"color_transfer":"smpte2084","color_primaries":"bt2020"}]}`,
		`{"streams":[{"color_transfer":"smpte2084","color_primaries":"bt2020","side_data_list":[{"side_data_type":"DOVI configuration record"},{"side_data_type":"Dolby Vision RPU Data"}]}]}`,
		`{"streams":[{"color_transfer":"arib-std-b67"}]}`,
		`{"streams":[{"color_transfer":"bt709","color_primaries":"bt2020"}]}`,
		`{"streams":[{"side_data_list":[{"side_data_type":"Dolby Vision RPU Data"}]}]}`,
		`{"streams":[{}]}`, `{"streams":[]}`, `2.000000`, ``, `{"streams":[{"color_transfer":"SMPTE2084"}]}`,
		`{"streams":[{"color_transfer":5}]}`, `{"streams":[{"color_transfer":null,"color_primaries":null}]}`,
		`{"streams":[{"side_data_list":"x"}]}`, `{"streams":[{"side_data_list":[1,"a",{"side_data_type":3}]}]}`,
		`{"streams":[{"color_transfer":"bt709"},{"color_transfer":"smpte2084"}]}`, `{"streams":{"color_transfer":"smpte2084"}}`,
		`{"streams":[{"color_transfer":"bt709","color_transfer":"smpte2084"}]}`, `[{"streams":[]}]`, `{"streams":[{"color_primaries":"BT2020NC"}]}`,
		`{"streams":[{"color_transfer":"smpte2084"}`, "\xff\xfe", `{"streams":[` + strings.Repeat("{", 100),
	} {
		w.Add("FuzzClassifyProbe", "doc", []byte(doc))
	}
}
