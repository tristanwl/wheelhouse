package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/converter"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
)

type codecEnv struct {
	h          http.Handler
	root, file string
	configDir  string
	ffmpegArgs string
	acc        *accounts
	tools      string // holds the fake ffmpeg and ffprobe, and stream.json for the HDR class
}

// fakeFfmpeg writes a POSIX sh stand-in for ffmpeg: it records its arguments,
// prints -progress lines and writes "converted" to its last argument.
func fakeFfmpeg(t *testing.T, dir, body string) (ffmpeg, ffprobe, argsFile string) {
	t.Helper()
	argsFile = filepath.Join(dir, "args.txt")
	ffmpeg = filepath.Join(dir, "ffmpeg")
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\"; done > '%s'\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\n%s\n", argsFile, body)
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ffprobe = filepath.Join(dir, "ffprobe")
	// The stream query (HDR classification) answers from stream.json when the
	// test wrote one, an SDR stream otherwise; any other call is the duration probe.
	probe := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *stream=*) if [ -f '%s/stream.json' ]; then cat '%s/stream.json'; else echo '{\"streams\":[{\"color_transfer\":\"bt709\"}]}'; fi; exit 0;; esac\necho 2.0\n", dir, dir)
	if err := os.WriteFile(ffprobe, []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	return ffmpeg, ffprobe, argsFile
}

const ffmpegOK = `echo "out_time_ms=1000000"; echo "progress=continue"; printf converted > "$out"; echo "progress=end"`
const ffmpegSlow = `printf partial > "$out"; echo "out_time_ms=1000000"; exec sleep 30`

func newCodecEnv(t *testing.T, vars env, ffmpegBody string) codecEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "Example Movie 1 (2025).mkv")
	if err := os.WriteFile(file, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	ffmpeg, ffprobe, args := fakeFfmpeg(t, tools, ffmpegBody)
	configDir := filepath.Join(t.TempDir(), "config")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	acc := newAccounts(t)
	settings, err := converter.NewSettings(ctx, acc.DB, []string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(Options{
		Logger:  slog.New(slog.DiscardHandler),
		Auth:    acc.Svc,
		Source:  core.NewEnvConfigSource(vars),
		Ffmpeg:  func() bool { return true },
		Context: ctx,
		Converter: ConverterOptions{
			Settings: settings, Store: acc.DB, ConfigDir: configDir, Ffmpeg: ffmpeg, Ffprobe: ffprobe,
		},
	})
	return codecEnv{h: acc.As(h), root: root, file: file, configDir: configDir, ffmpegArgs: args, tools: tools, acc: acc}
}

func (c codecEnv) do(method, path, body string) (int, map[string]any, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c.h.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

func (c codecEnv) convertBody(mut map[string]any) string {
	m := map[string]any{
		"instanceId": "radarr", "itemId": 1, "filePath": c.file, "title": "Example Movie 1",
		"originalSize": 8, "backend": "software", "targetFamily": "h264",
	}
	for k, v := range mut {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

func (c codecEnv) waitJob(t *testing.T, id, status string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		code, job, raw := c.do("GET", "/api/codec-analysis/convert/"+id, "")
		if code != 200 {
			t.Fatalf("%d %s", code, raw)
		}
		if job["status"] == status {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job never became %s", status)
	return nil
}

func TestCodecAnalysisMoviesAndSeries(t *testing.T) {
	radarr := mmtest.StartRadarr(t)
	sonarr := mmtest.StartSonarr(t)
	c := newCodecEnv(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"SONARR_URL": sonarr.URL, "SONARR_API_KEY": mmtest.GoodKey,
	}, ffmpegOK)

	code, body, raw := c.do("GET", "/api/codec-analysis?kind=movies", "")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	files := append(body["candidates"].([]any), body["matching"].([]any)...)
	if int(body["totalCount"].(float64)) != len(files) || len(files) == 0 {
		t.Errorf("totalCount %v vs %d files", body["totalCount"], len(files))
	}
	if int(body["totalCount"].(float64))+int(body["skippedCount"].(float64)) != len(radarr.Movies()) {
		t.Errorf("counted + skipped != library: %s", raw)
	}
	prefs := body["preferences"].(map[string]any)
	if prefs["hd"] != "h264" || prefs["uhd"] != "hevc" {
		t.Errorf("preferences %v", prefs)
	}
	if _, has := body["problems"]; has {
		t.Errorf("no problems expected: %s", raw)
	}
	for _, f := range files {
		m := f.(map[string]any)
		if m["instanceId"] != "radarr" || m["filePath"] == "" || m["title"] == "" || m["tier"] == nil {
			t.Errorf("file %v", m)
		}
	}

	code, body, raw = c.do("GET", "/api/codec-analysis?kind=series", "")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	files = append(body["candidates"].([]any), body["matching"].([]any)...)
	if len(files) == 0 {
		t.Fatal("no episodes")
	}
	for _, f := range files {
		title := f.(map[string]any)["title"].(string)
		if !strings.Contains(title, " - S") && !strings.Contains(title, " - Season ") {
			t.Errorf("episode title %q", title)
		}
	}
	// Anything but "series" means movies.
	if _, b, _ := c.do("GET", "/api/codec-analysis?kind=whatever", ""); b["totalCount"] == nil {
		t.Error("unknown kind should analyse movies")
	}
}

func TestCodecAnalysisProblemsAndNothingConfigured(t *testing.T) {
	none := newCodecEnv(t, env{}, ffmpegOK)
	code, body, _ := none.do("GET", "/api/codec-analysis?kind=movies", "")
	if code != 404 || body["error"] != "No Radarr instance is configured" {
		t.Errorf("%d %v", code, body)
	}
	if code, body, _ = none.do("GET", "/api/codec-analysis?kind=series", ""); code != 404 || body["error"] != "No Sonarr instance is configured" {
		t.Errorf("%d %v", code, body)
	}

	good := mmtest.StartRadarr(t)
	bad := mmtest.StartRadarr(t, mmtest.WithAPIKey("another-key-0123456789"))
	mixed := newCodecEnv(t, env{
		"RADARR_URL": bad.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"RADARR_4K_URL": good.URL, "RADARR_4K_API_KEY": mmtest.GoodKey,
	}, ffmpegOK)
	code, body, raw := mixed.do("GET", "/api/codec-analysis?kind=movies", "")
	problems, _ := body["problems"].([]any)
	if code != 200 || len(problems) != 1 || problems[0].(map[string]any)["instanceId"] != "radarr" ||
		!strings.Contains(strings.ToLower(problems[0].(map[string]any)["error"].(string)), "api key incorrect") {
		t.Fatalf("%d %s", code, raw)
	}
	for _, f := range body["candidates"].([]any) {
		if f.(map[string]any)["instanceId"] != "radarr-4k" {
			t.Errorf("a file from the broken instance: %v", f)
		}
	}

	allBad := newCodecEnv(t, env{"RADARR_URL": bad.URL, "RADARR_API_KEY": mmtest.GoodKey}, ffmpegOK)
	code, body, raw = allBad.do("GET", "/api/codec-analysis?kind=movies", "")
	if code != 502 || body["error"] == nil || len(body["problems"].([]any)) != 1 {
		t.Errorf("%d %s", code, raw)
	}
	if strings.Contains(raw, mmtest.GoodKey) {
		t.Errorf("the API key leaked: %s", raw)
	}
}

func TestCodecSettings(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	if code, body, _ := c.do("GET", "/api/codec-analysis/settings", ""); code != 200 || body["hd"] != "h264" || body["uhd"] != "hevc" {
		t.Errorf("defaults: %d %v", code, body)
	}
	bad := []string{"not json", "{}", `{"hd":"h264"}`, `{"hd":"x265","uhd":"hevc"}`, `{"hd":"h264","uhd":1}`, `null`, `[]`, `"hevc"`}
	for _, b := range bad {
		if code, body, _ := c.do("PUT", "/api/codec-analysis/settings", b); code != 400 || body["error"] == nil {
			t.Errorf("%q: %d %v", b, code, body)
		}
	}
	if _, err := os.Stat(c.configDir); err == nil {
		t.Error("a refused request must not write anything")
	}
	code, body, raw := c.do("PUT", "/api/codec-analysis/settings", `{"hd":"hevc","uhd":"h264","extra":1}`)
	if code != 200 || body["hd"] != "hevc" || body["uhd"] != "h264" {
		t.Fatalf("%d %s", code, raw)
	}
	if _, body, _ = c.do("GET", "/api/codec-analysis/settings", ""); body["hd"] != "hevc" {
		t.Errorf("not persisted: %v", body)
	}

	// An unwritable config directory is a 500 with advice, and the error text
	// does not echo anything but the system's reason.
	blocked := newCodecEnv(t, env{}, ffmpegOK)
	if err := os.WriteFile(blocked.configDir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, body, _ = blocked.do("PUT", "/api/codec-analysis/settings", `{"hd":"h264","uhd":"h264"}`)
	if code != 500 || !strings.Contains(body["error"].(string), "Could not save settings") || !strings.Contains(body["error"].(string), "/config volume") {
		t.Errorf("%d %v", code, body)
	}
}

func TestEncodersEndpoint(t *testing.T) {
	c := newCodecEnv(t, env{}, `case "$*" in *libx*) exit 0;; esac; echo "no such encoder" >&2; exit 1`)
	code, body, raw := c.do("GET", "/api/codec-analysis/encoders", "")
	encoders, _ := body["encoders"].([]any)
	if code != 200 || len(encoders) != 6 {
		t.Fatalf("%d %s", code, raw)
	}
	first := encoders[0].(map[string]any)
	if first["backend"] != "software" || first["family"] != "h264" || first["available"] != true || first["label"] != "Software (x264)" {
		t.Errorf("%v", first)
	}
	for _, e := range encoders[2:] {
		m := e.(map[string]any)
		if m["available"] != false || m["reason"] == nil {
			t.Errorf("hardware option should be unavailable with a reason: %v", m)
		}
	}
}

func TestConvertRefusesBadRequests(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	for _, body := range []string{
		"not json", "{}", "null", "[]",
		c.convertBody(map[string]any{"instanceId": 1}),
		c.convertBody(map[string]any{"itemId": "1"}),
		c.convertBody(map[string]any{"itemId": 1.5}),
		c.convertBody(map[string]any{"filePath": 5}),
		c.convertBody(map[string]any{"title": nil}),
		c.convertBody(map[string]any{"originalSize": "big"}),
		c.convertBody(map[string]any{"backend": "quicksync"}),
		c.convertBody(map[string]any{"backend": "-y"}),
		c.convertBody(map[string]any{"targetFamily": "av1"}),
	} {
		code, resp, raw := c.do("POST", "/api/codec-analysis/convert", body)
		if code != 400 || resp["error"] == nil {
			t.Errorf("%s: %d %s", body, code, raw)
		}
	}
	if _, body, _ := c.do("GET", "/api/codec-analysis/convert", ""); len(body["jobs"].([]any)) != 0 {
		t.Errorf("nothing may be queued: %v", body)
	}
}

func TestConvertRefusesPathsOutsideTheMediaRoots(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	other := t.TempDir()
	outsideFile := filepath.Join(other, "secret.mkv")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(c.root, "escape.mkv")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for name, path := range map[string]string{
		"outside":       outsideFile,
		"dot-dot":       c.root + "/../" + filepath.Base(other) + "/secret.mkv",
		"symlink out":   link,
		"relative":      "Example Movie 1 (2025).mkv",
		"missing":       filepath.Join(c.root, "nope.mkv"),
		"a directory":   c.root,
		"etc":           "/etc/passwd",
		"empty":         "",
		"dot-dot inner": c.root + "/x/../Example Movie 1 (2025).mkv",
	} {
		code, resp, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"filePath": path, "acknowledgeHardlink": true}))
		if code != 400 || resp["error"] == nil {
			t.Errorf("%s (%q): %d %s", name, path, code, raw)
		}
		if strings.Contains(raw, other) || strings.Contains(raw, "secret") {
			t.Errorf("%s: the response says too much: %s", name, raw)
		}
	}
	if _, body, _ := c.do("GET", "/api/codec-analysis/convert", ""); len(body["jobs"].([]any)) != 0 {
		t.Errorf("nothing may be queued: %v", body)
	}
	if _, err := os.Stat(c.ffmpegArgs); err == nil {
		t.Error("ffmpeg ran for a refused path")
	}
	if got, _ := os.ReadFile(outsideFile); string(got) != "secret" {
		t.Error("the file outside was touched")
	}
}

func TestConvertHardlinkNeedsConfirmation(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	if err := os.Link(c.file, filepath.Join(c.root, "seeding-copy.mkv")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	code, body, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil))
	if code != 409 || body["code"] != "hardlinked" || body["linkCount"] != float64(2) ||
		!strings.Contains(strings.ToLower(body["error"].(string)), "hard link") {
		t.Fatalf("%d %s", code, raw)
	}
	if _, jobs, _ := c.do("GET", "/api/codec-analysis/convert", ""); len(jobs["jobs"].([]any)) != 0 {
		t.Error("nothing may be queued before the user confirms")
	}
	if _, err := os.Stat(c.ffmpegArgs); err == nil {
		t.Error("ffmpeg ran before the user confirmed")
	}
	// Anything but a literal true is not a confirmation.
	for _, v := range []any{"true", 1, false, nil} {
		if code, _, _ := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHardlink": v})); code != 409 {
			t.Errorf("acknowledgeHardlink=%v: %d", v, code)
		}
	}
	code, job, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHardlink": true}))
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	c.waitJob(t, job["id"].(string), "success")
	// The other link keeps the original data; the library path has the new file.
	if got, _ := os.ReadFile(filepath.Join(c.root, "seeding-copy.mkv")); string(got) != "original" {
		t.Errorf("the seeding copy changed: %q", got)
	}
	if got, _ := os.ReadFile(c.file); string(got) != "converted" {
		t.Errorf("library file: %q", got)
	}
}

func TestConvertEndToEnd(t *testing.T) {
	radarr := mmtest.StartRadarr(t)
	c := newCodecEnv(t, env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey}, ffmpegOK)

	code, job, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil))
	if code != 200 || job["status"] == nil || job["id"] == nil {
		t.Fatalf("%d %s", code, raw)
	}
	for k, want := range map[string]any{
		"instanceId": "radarr", "itemId": float64(1), "filePath": c.file, "title": "Example Movie 1",
		"originalSize": float64(8), "backend": "software", "targetFamily": "h264",
	} {
		if job[k] != want {
			t.Errorf("%s = %v, want %v", k, job[k], want)
		}
	}
	id := job["id"].(string)
	done := c.waitJob(t, id, "success")
	if done["progressPercent"] != float64(100) || done["backupPath"] != c.file+".bak" || done["newSize"] != float64(9) {
		t.Errorf("%v", done)
	}
	if got, _ := os.ReadFile(c.file + ".bak"); string(got) != "original" {
		t.Errorf(".bak: %q", got)
	}
	// The media manager was asked to rescan the item.
	cmd, ok := radarr.Find("POST", "/api/v3/command")
	if !ok || fmt.Sprint(cmd.Body) != "map[movieId:1 name:RescanMovie]" {
		t.Errorf("rescan: %v %v", cmd.Body, ok)
	}
	// The job list shows it, and cancelling a finished job is refused.
	if _, list, _ := c.do("GET", "/api/codec-analysis/convert", ""); len(list["jobs"].([]any)) != 1 {
		t.Errorf("%v", list)
	}
	if code, body, _ := c.do("POST", "/api/codec-analysis/convert/"+id+"/cancel", ""); code != 400 || body["error"] != "job is not cancelable" {
		t.Errorf("%d %v", code, body)
	}
}

func TestConvertCancelAndUnknownJobs(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegSlow)
	code, job, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil))
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	id := job["id"].(string)
	c.waitJob(t, id, "running")

	// The same file again reattaches to the running job.
	if _, again, _ := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil)); again["id"] != id {
		t.Errorf("a second job was started: %v", again)
	}

	code, body, raw := c.do("POST", "/api/codec-analysis/convert/"+id+"/cancel", "")
	if code != 200 || body["id"] != id {
		t.Fatalf("%d %s", code, raw)
	}
	done := c.waitJob(t, id, "canceled")
	if !strings.Contains(done["log"].(string), "Canceled.") {
		t.Errorf("log %v", done["log"])
	}
	if got, _ := os.ReadFile(c.file); string(got) != "original" {
		t.Errorf("original: %q", got)
	}

	for _, p := range []string{"GET /api/codec-analysis/convert/nope", "GET /api/codec-analysis/convert/%2e%2e"} {
		method, path, _ := strings.Cut(p, " ")
		if code, body, _ := c.do(method, path, ""); code != 404 || body["error"] != "job not found" {
			t.Errorf("%s: %d %v", p, code, body)
		}
	}
	if code, body, _ := c.do("POST", "/api/codec-analysis/convert/nope/cancel", ""); code != 404 || body["error"] != "job not found" {
		t.Errorf("%d %v", code, body)
	}
}

func TestConvertFailureIsReported(t *testing.T) {
	c := newCodecEnv(t, env{}, `echo "boom" >&2; exit 7`)
	_, job, _ := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil))
	done := c.waitJob(t, job["id"].(string), "failed")
	if done["error"] != "ffmpeg exited with code 7" || !strings.Contains(done["log"].(string), "boom") {
		t.Errorf("%v", done)
	}
	if got, _ := os.ReadFile(c.file); string(got) != "original" {
		t.Errorf("original: %q", got)
	}
	if _, err := os.Stat(c.file + ".bak"); err == nil {
		t.Error("no backup is made for a failed conversion")
	}
}

func TestCodecRoutesDoNotLeakConfiguredSecrets(t *testing.T) {
	const canary = "canary-key-do-not-leak-0123456789"
	radarr := mmtest.StartRadarr(t, mmtest.WithAPIKey(canary))
	c := newCodecEnv(t, env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": canary}, "exit 1")
	for _, p := range []string{
		"/api/codec-analysis?kind=movies", "/api/codec-analysis/settings", "/api/codec-analysis/encoders",
		"/api/codec-analysis/convert", "/api/codec-analysis/convert/nope",
	} {
		rec := httptest.NewRecorder()
		c.h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if strings.Contains(rec.Body.String(), canary) || strings.Contains(fmt.Sprint(rec.Header()), canary) {
			t.Errorf("%s leaks the key", p)
		}
	}
}

// With no working ffmpeg nothing can be encoded, so no encoder is offered
// (software included): the same answer /api/features gives for Codecs.
func TestEncodersWithoutFfmpegOffersNone(t *testing.T) {
	c := newCodecEnv(t, env{}, "exit 127")
	code, body, raw := c.do("GET", "/api/codec-analysis/encoders", "")
	encoders, _ := body["encoders"].([]any)
	if code != 200 || len(encoders) != 6 {
		t.Fatalf("%d %s", code, raw)
	}
	for _, e := range encoders {
		m := e.(map[string]any)
		if m["available"] != false || m["reason"] == nil {
			t.Errorf("no encoder may be available without ffmpeg: %v", m)
		}
	}
}
