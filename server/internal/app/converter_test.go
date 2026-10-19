package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
)

func putSettings(c codecEnv, body string) (int, map[string]any, string) {
	return c.do("PUT", "/api/admin/converter", body)
}

func TestConverterSettingsContract(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	code, body, raw := c.do("GET", "/api/admin/converter", "")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	// Exactly these four keys, with these types: the web page is built against them.
	if len(body) != 4 || body["source"] != "environment" || body["includeHdr"] != false {
		t.Fatalf("shape: %s", raw)
	}
	for _, k := range []string{"mediaRoots", "effectiveRoots"} {
		l, ok := body[k].([]any)
		if !ok || len(l) != 1 || l[0] != c.root {
			t.Errorf("%s = %v", k, body[k])
		}
	}

	// Save a database root: it wins over the environment one.
	other := t.TempDir()
	other, _ = filepath.EvalSymlinks(other)
	code, body, raw = putSettings(c, `{"mediaRoots":["`+other+`"],"includeHdr":true}`)
	if code != 200 || body["source"] != "database" || body["includeHdr"] != true ||
		body["effectiveRoots"].([]any)[0] != other || body["mediaRoots"].([]any)[0] != other {
		t.Fatalf("%d %s", code, raw)
	}
	// It applies without a restart: the old root's file is now refused, a file in
	// the new root is accepted.
	if code, _, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil)); code != 400 {
		t.Errorf("a file outside the new roots: %d %s", code, raw)
	}
	inNew := filepath.Join(other, "Example Movie 2 (2025).mkv")
	if err := os.WriteFile(inNew, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, job, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"filePath": inNew}))
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	c.waitJob(t, job["id"].(string), "success")

	// The audit log has the change, with the roots and no more.
	entries, err := c.acc.Svc.ListAudit(context.Background(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == "converter.update" {
			found = true
			if !strings.Contains(string(e.Detail), other) {
				t.Errorf("detail: %s", e.Detail)
			}
		}
	}
	if !found {
		t.Error("no converter.update audit entry")
	}

	// An empty list removes the saved roots; the environment applies again.
	code, body, raw = putSettings(c, `{"mediaRoots":[]}`)
	if code != 200 || body["source"] != "environment" || body["effectiveRoots"].([]any)[0] != c.root || body["includeHdr"] != true {
		t.Errorf("%d %s", code, raw)
	}
}

func TestConverterSettingsValidation(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	file := c.file
	for name, body := range map[string]string{
		"not json":        `nope`,
		"empty object":    `{}`,
		"unknown field":   `{"mediaRoots":["/tmp"],"x":1}`,
		"wrong type":      `{"mediaRoots":"/tmp"}`,
		"relative":        `{"mediaRoots":["movies"]}`,
		"dotdot":          `{"mediaRoots":["` + c.root + `/../etc"]}`,
		"slash":           `{"mediaRoots":["/"]}`,
		"etc":             `{"mediaRoots":["/etc"]}`,
		"proc":            `{"mediaRoots":["/proc"]}`,
		"inside usr":      `{"mediaRoots":["/usr/share"]}`,
		"missing":         `{"mediaRoots":["` + c.root + `/nope"]}`,
		"a file":          `{"mediaRoots":["` + file + `"]}`,
		"nested":          `{"mediaRoots":["` + c.root + `","` + c.root + `/sub"]}`,
		"hdr wrong type":  `{"includeHdr":"yes"}`,
		"null byte":       `{"mediaRoots":["` + c.root + `\u0000"]}`,
		"empty string":    `{"mediaRoots":[""]}`,
		"var lib parent":  `{"mediaRoots":["/var"]}`,
		"home of nothing": `{"mediaRoots":["/nonexistent-wheelhouse-root"]}`,
	} {
		code, resp, raw := putSettings(c, body)
		if code != 400 || resp["error"] == nil {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	// Nothing changed by any of that.
	if _, body, _ := c.do("GET", "/api/admin/converter", ""); body["source"] != "environment" || body["includeHdr"] != false {
		t.Errorf("state changed: %v", body)
	}
	// Duplicates of the same path are merged, not refused.
	code, body, raw := putSettings(c, `{"mediaRoots":["`+c.root+`","`+c.root+`/"]}`)
	if code != 200 || len(body["effectiveRoots"].([]any)) != 1 {
		t.Errorf("%d %s", code, raw)
	}
}

func TestConvertHDRPolicy(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	stream := filepath.Join(c.tools, "stream.json")
	write := func(doc string) {
		if err := os.WriteFile(stream, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"hdr10":        `{"streams":[{"color_transfer":"smpte2084","color_primaries":"bt2020"}]}`,
		"hlg":          `{"streams":[{"color_transfer":"arib-std-b67","color_primaries":"bt2020"}]}`,
		"dolby-vision": `{"streams":[{"color_transfer":"smpte2084","side_data_list":[{"side_data_type":"DOVI configuration record"}]}]}`,
		"unknown":      `not json at all`,
	}
	for class, doc := range cases {
		write(doc)
		// Excluded by default.
		code, body, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHdr": true}))
		if code != 409 || body["code"] != "hdr-excluded" || body["hdr"] != class {
			t.Fatalf("%s excluded: %d %s", class, code, raw)
		}
	}
	if _, err := os.Stat(c.ffmpegArgs); err == nil {
		t.Fatal("ffmpeg ran for an excluded HDR file")
	}

	// Allowed: now it needs the explicit acknowledgement, and only a literal true counts.
	if code, _, raw := putSettings(c, `{"includeHdr":true}`); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	write(cases["dolby-vision"])
	for _, v := range []any{"true", 1, false, nil} {
		code, body, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHdr": v}))
		if code != 409 || body["code"] != "hdr" || body["hdr"] != "dolby-vision" || !strings.Contains(body["error"].(string), "SDR 8-bit") {
			t.Errorf("acknowledgeHdr=%v: %d %s", v, code, raw)
		}
	}
	if _, jobs, _ := c.do("GET", "/api/codec-analysis/convert", ""); len(jobs["jobs"].([]any)) != 0 {
		t.Fatal("nothing may be queued before the acknowledgement")
	}
	code, job, raw := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHdr": true}))
	if code != 200 || job["hdr"] != "dolby-vision" || job["acknowledgedHdr"] != true {
		t.Fatalf("%d %s", code, raw)
	}
	done := c.waitJob(t, job["id"].(string), "success")
	if done["hdr"] != "dolby-vision" || done["owner"] != c.acc.Admin.ID || done["ownerName"] != testAdminName {
		t.Errorf("%v", done)
	}

	// An SDR file needs no acknowledgement and records none.
	write(`{"streams":[{"color_transfer":"bt709"}]}`)
	if err := os.WriteFile(c.file, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(c.file + ".bak")
	code, job, raw = c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHdr": true}))
	if code != 200 || job["hdr"] != "none" || job["acknowledgedHdr"] != false {
		t.Fatalf("%d %s", code, raw)
	}
	c.waitJob(t, job["id"].(string), "success")
}

func TestHDRSwitchedOffWhileQueuedFailsTheJob(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegSlow)
	// A first job holds the queue while the HDR job waits behind it.
	other := filepath.Join(c.root, "Example Movie 3 (2025).mkv")
	if err := os.WriteFile(other, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(c.tools, "stream.json"), []byte(`{"streams":[{"color_transfer":"smpte2084"}]}`), 0o644)
	if code, _, raw := putSettings(c, `{"includeHdr":true}`); code != 200 {
		t.Fatal(raw)
	}
	_, first, _ := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"filePath": other, "acknowledgeHdr": true}))
	c.waitJob(t, first["id"].(string), "running")
	_, second, _ := c.do("POST", "/api/codec-analysis/convert", c.convertBody(map[string]any{"acknowledgeHdr": true}))
	if code, _, raw := putSettings(c, `{"includeHdr":false}`); code != 200 {
		t.Fatal(raw)
	}
	if code, _, raw := c.do("POST", "/api/codec-analysis/convert/"+first["id"].(string)+"/cancel", ""); code != 200 {
		t.Fatal(raw)
	}
	failed := c.waitJob(t, second["id"].(string), "failed")
	if !strings.Contains(failed["error"].(string), "HDR") {
		t.Errorf("%v", failed)
	}
	if got, _ := os.ReadFile(c.file); string(got) != "original" {
		t.Errorf("the file changed: %q", got)
	}
}

func TestCodecAnalysisHidesHDRUnlessAllowed(t *testing.T) {
	movie := func(id int, title, rng, typ string) mmtest.Doc {
		return mmtest.Doc{
			"id": id, "title": title, "hasFile": true,
			"movieFile": mmtest.Doc{"size": 5000 + id, "path": "/movies/" + title + ".mkv", "mediaInfo": mmtest.Doc{
				"videoCodec": "x265", "resolution": "1920x1080", "videoDynamicRange": rng, "videoDynamicRangeType": typ,
			}},
		}
	}
	radarr := mmtest.StartRadarr(t, mmtest.WithMovies([]mmtest.Doc{
		movie(1, "Example Movie 1", "", ""),
		movie(2, "Example Movie 2", "HDR", "HDR10"),
		movie(3, "Example Movie 3", "HDR", "DV HDR10"),
	}))
	c := newCodecEnv(t, env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey}, ffmpegOK)

	titles := func(body map[string]any) map[string]string {
		out := map[string]string{}
		for _, f := range body["candidates"].([]any) {
			m := f.(map[string]any)
			out[m["title"].(string)] = m["hdr"].(string)
		}
		return out
	}
	_, body, raw := c.do("GET", "/api/codec-analysis?kind=movies", "")
	got := titles(body)
	if len(got) != 1 || got["Example Movie 1"] != "none" || body["hdrExcluded"] != float64(2) {
		t.Fatalf("by default: %s", raw)
	}
	if body["totalCount"] != float64(3) {
		t.Errorf("the library totals still count every file: %s", raw)
	}
	if code, _, raw := putSettings(c, `{"includeHdr":true}`); code != 200 {
		t.Fatal(raw)
	}
	_, body, raw = c.do("GET", "/api/codec-analysis?kind=movies", "")
	got = titles(body)
	if len(got) != 3 || got["Example Movie 2"] != "hdr10" || got["Example Movie 3"] != "dolby-vision" || body["hdrExcluded"] != float64(0) {
		t.Fatalf("allowed: %s", raw)
	}
}

func TestConvertOwnerAndAuditAreRecorded(t *testing.T) {
	c := newCodecEnv(t, env{}, ffmpegOK)
	_, job, _ := c.do("POST", "/api/codec-analysis/convert", c.convertBody(nil))
	done := c.waitJob(t, job["id"].(string), "success")
	b, _ := json.Marshal(done)
	if done["owner"] != c.acc.Admin.ID {
		t.Errorf("owner: %s", b)
	}
	// A job's final state is visible a moment before its finish audit row is
	// written (the queue reports it after publishing the state), so wait for it.
	var actions map[string]bool
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		entries, _ := c.acc.Svc.ListAudit(context.Background(), 50, 0)
		actions = map[string]bool{}
		for _, e := range entries {
			if e.Target == job["id"] {
				actions[e.Action] = true
				if e.Action == "conversion.finish" && e.ActorID != c.acc.Admin.ID {
					t.Errorf("finish actor %q", e.ActorID)
				}
			}
		}
		if (actions["conversion.start"] && actions["conversion.finish"]) || time.Now().After(deadline) {
			break
		}
	}
	if !actions["conversion.start"] || !actions["conversion.finish"] {
		t.Errorf("audit actions for the job: %v", actions)
	}
}

func TestOwnerOf(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	if id, name := ownerOf(req); id != "cli" || name != "system" {
		t.Errorf("no identity: %q %q", id, name)
	}
	byp := req.WithContext(context.WithValue(req.Context(), identityKey{}, auth.BypassIdentity("x")))
	if id, name := ownerOf(byp); id != "bypass" || name != auth.BypassUsername {
		t.Errorf("bypass: %q %q", id, name)
	}
}
