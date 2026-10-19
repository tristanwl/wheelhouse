package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent/fakeqbittorrent"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd/fakesabnzbd"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex/plextest"
)

// The SABnzbd fixture's newest warning is at 1792300000 (seconds since the epoch).
var sabNow = time.Unix(1792300000, 0).Add(time.Hour)

type env = map[string]string

// stack serves the app over an injected environment source, for a signed-in administrator.
func stack(t *testing.T, e env, now time.Time) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	acc := newAccounts(t)
	h := Handler(Options{
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Source: core.NewEnvConfigSource(e),
		Ffmpeg: func() bool { return false },
		Now:    func() time.Time { return now },
		Auth:   acc.Svc,
	})
	return acc.As(h), &logs
}

func getJSON(t *testing.T, h http.Handler, path string) (int, map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("%s: not JSON: %v: %q", path, err, rec.Body.String())
	}
	return rec.Code, m, rec.Body.String()
}

func byID(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, raw := range body["instances"].([]any) {
		i := raw.(map[string]any)
		out[i["id"].(string)] = i
	}
	return out
}

func TestStatusAndDownloadsWithNothingConfigured(t *testing.T) {
	h, _ := stack(t, env{}, sabNow)
	for path, want := range map[string]string{
		"/api/status":    `{"instances":[]}`,
		"/api/downloads": `{"instances":[],"items":[]}`,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
			t.Errorf("%s: %d %q, want %s", path, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestStatusFullStack(t *testing.T) {
	radarr := mmtest.StartRadarr(t)
	sonarr := mmtest.StartSonarr(t)
	sab := fakesabnzbd.Start(t, fakesabnzbd.Active)
	plex := plextest.Start(t, plextest.Scenario{Claimed: false})
	h, _ := stack(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey,
		"SONARR_URL": sonarr.URL, "SONARR_API_KEY": mmtest.GoodKey,
		"SABNZBD_URL": sab.URL, "SABNZBD_API_KEY": fakesabnzbd.GoodKey,
		"PLEX_URL": plex.URL, "PLEX_TOKEN": plextest.GoodToken,
		"TMDB_API_KEY": "tmdb-key-0123456789",
	}, sabNow)

	code, body, raw := getJSON(t, h, "/api/status")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	got := byID(t, body)
	if len(got) != 4 {
		t.Fatalf("want 4 instances (TMDB is not a status role), got %s", raw)
	}
	var roles []string
	for _, raw := range body["instances"].([]any) {
		roles = append(roles, raw.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "mediaManager,mediaManager,downloadClient,mediaServer" {
		t.Errorf("roles are not grouped managers, clients, servers: %v", roles)
	}
	// Configured order inside a role: Sonarr before Radarr.
	if body["instances"].([]any)[0].(map[string]any)["id"] != "sonarr" {
		t.Errorf("configured order lost: %s", raw)
	}
	// The unclaimed Plex needs setup; the SABnzbd fixture has a recent error.
	if got["plex"]["state"] != "needs-setup" {
		t.Errorf("plex: %v", got["plex"])
	}
	if got["sabnzbd"]["state"] != "degraded" {
		t.Errorf("sabnzbd with a recent error: %v", got["sabnzbd"]["state"])
	}
	for _, id := range []string{"sonarr", "radarr", "sabnzbd", "plex"} {
		i := got[id]
		if i["version"] == nil || i["detail"] == nil || i["error"] != nil {
			t.Errorf("%s: %v", id, i)
		}
	}
	// Plex's web UI lives under /web; the others at the root.
	plexPort := mustPort(t, plex.URL)
	link := got["plex"]["link"].(map[string]any)
	if link["protocol"] != "http" || link["path"] != "/web" || int(link["port"].(float64)) != plexPort {
		t.Errorf("plex link: %v", link)
	}
	if got["radarr"]["link"].(map[string]any)["path"] != "/" {
		t.Errorf("radarr link: %v", got["radarr"]["link"])
	}
}

func mustPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A warning older than 24 hours is history: it no longer makes the client degraded.
func TestSabnzbdWarningsFollowThe24HourRule(t *testing.T) {
	sab := fakesabnzbd.Start(t, fakesabnzbd.Active)
	e := env{"SABNZBD_URL": sab.URL, "SABNZBD_API_KEY": fakesabnzbd.GoodKey}
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"within a day of the last error", time.Unix(1792297332, 0).Add(24 * time.Hour), "degraded"},
		{"just past a day", time.Unix(1792297332, 0).Add(24*time.Hour + time.Second), "ok"},
	} {
		h, _ := stack(t, e, tc.now)
		_, body, _ := getJSON(t, h, "/api/status")
		if got := byID(t, body)["sabnzbd"]["state"]; got != tc.want {
			t.Errorf("%s: state %v, want %s", tc.name, got, tc.want)
		}
	}
}

func TestStatusFailuresAreIsolatedAndRedacted(t *testing.T) {
	const canary = "CANARY-KEY-9f8e7d6c5b4a"
	radarr := mmtest.StartRadarr(t)
	sab := fakesabnzbd.Start(t, fakesabnzbd.Active)
	gone := fakesabnzbd.Start(t, fakesabnzbd.Active)
	goneURL := gone.URL
	gone.Close()
	creds := strings.Replace(goneURL, "http://", "http://CANARY-USER:CANARY-PASS@", 1) + "/sub/path?token=" + canary

	h, logs := stack(t, env{
		"RADARR_URL": radarr.URL, "RADARR_API_KEY": canary, // rejected
		"SABNZBD_URL": sab.URL, "SABNZBD_API_KEY": fakesabnzbd.GoodKey,
		"PLEX_URL": creds, "PLEX_TOKEN": canary, // unreachable, with credentials in the URL
		"JELLYFIN_URL": "not a url", "JELLYFIN_API_KEY": canary, // invalid config
	}, sabNow)
	code, body, raw := getJSON(t, h, "/api/status")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	got := byID(t, body)
	for _, id := range []string{"radarr", "plex", "jellyfin"} {
		if got[id]["state"] != "down" || got[id]["error"] == nil || got[id]["detail"] != nil {
			t.Errorf("%s should be down with a reason: %v", id, got[id])
		}
	}
	if got["sabnzbd"]["state"] == "down" {
		t.Errorf("the healthy client was affected: %v", got["sabnzbd"])
	}
	// The link carries scheme, port and path prefix, never credentials or query.
	plexLink := got["plex"]["link"].(map[string]any)
	if plexLink["path"] != "/sub/path/web" {
		t.Errorf("plex link path: %v", plexLink)
	}
	for _, secret := range []string{canary, "CANARY-USER", "CANARY-PASS"} {
		if strings.Contains(raw, secret) || strings.Contains(logs.String(), secret) {
			t.Errorf("%q leaked:\nbody %s\nlogs %s", secret, raw, logs)
		}
	}
}

func TestDownloadsNormalizesAndKeepsPartialFailures(t *testing.T) {
	sab := fakesabnzbd.Start(t, fakesabnzbd.Active)
	qbit := fakeqbittorrent.Start(t, fakeqbittorrent.V5, fakeqbittorrent.Active)
	h, _ := stack(t, env{
		"SABNZBD_URL": sab.URL, "SABNZBD_API_KEY": fakesabnzbd.GoodKey,
		"QBITTORRENT_URL": qbit.URL, "QBITTORRENT_USERNAME": fakeqbittorrent.GoodUser,
		"QBITTORRENT_PASSWORD": fakeqbittorrent.GoodPassword,
	}, sabNow)
	code, body, raw := getJSON(t, h, "/api/downloads")
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	insts := body["instances"].([]any)
	if len(insts) != 2 || insts[0].(map[string]any)["instanceId"] != "sabnzbd" ||
		insts[0].(map[string]any)["protocol"] != "usenet" || insts[1].(map[string]any)["protocol"] != "torrent" {
		t.Fatalf("instances: %s", raw)
	}
	for _, i := range insts {
		m := i.(map[string]any)
		if m["up"] != true || m["paused"] == nil || m["speedBytesPerSec"] == nil {
			t.Errorf("up client lacks fields: %v", m)
		}
	}
	items := body["items"].([]any)
	var order []string
	for _, it := range items {
		m := it.(map[string]any)
		order = append(order, m["instanceId"].(string))
		if m["id"] == nil || m["state"] == nil || m["etaSeconds"] == nil && !hasKey(m, "etaSeconds") {
			t.Errorf("row: %v", m)
		}
	}
	if len(items) == 0 || order[0] != "sabnzbd" || order[len(order)-1] != "qbittorrent" {
		t.Errorf("rows are not in configured order: %v", order)
	}

	// Now the torrent client rejects the login: it is listed as down, SABnzbd's rows stay.
	h, _ = stack(t, env{
		"SABNZBD_URL": sab.URL, "SABNZBD_API_KEY": fakesabnzbd.GoodKey,
		"QBITTORRENT_URL": qbit.URL, "QBITTORRENT_USERNAME": fakeqbittorrent.GoodUser,
		"QBITTORRENT_PASSWORD": "CANARY-wrong-password",
	}, sabNow)
	code, body, raw = getJSON(t, h, "/api/downloads")
	insts = body["instances"].([]any)
	if code != 200 || len(insts) != 2 {
		t.Fatalf("%d %s", code, raw)
	}
	q := insts[1].(map[string]any)
	if q["up"] != false || q["error"] == nil || q["paused"] != nil {
		t.Errorf("broken client: %v", q)
	}
	if len(body["items"].([]any)) == 0 || strings.Contains(raw, "CANARY-wrong-password") {
		t.Errorf("healthy rows lost or secret leaked: %s", raw)
	}
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func TestFeaturesFollowTheConfiguredStack(t *testing.T) {
	radarr := mmtest.StartRadarr(t)
	h, _ := stack(t, env{"RADARR_URL": radarr.URL, "RADARR_API_KEY": mmtest.GoodKey, "TMDB_API_KEY": "tmdb-key-0123456789"}, sabNow)
	_, body, raw := getJSON(t, h, "/api/features")
	avail := map[string]bool{}
	for _, f := range body["features"].([]any) {
		m := f.(map[string]any)
		avail[m["id"].(string)] = m["available"].(bool)
	}
	// Codecs still needs ffmpeg, which the test pins to absent.
	if !avail["dashboard"] || !avail["library"] || !avail["explore"] || avail["codecs"] {
		t.Errorf("features: %s", raw)
	}
	if !strings.Contains(raw, `"missing":["ffmpeg"]`) {
		t.Errorf("codecs should say ffmpeg is missing: %s", raw)
	}

	// A half-configured Radarr (a URL without its key) is not ready, so it satisfies nothing.
	h, _ = stack(t, env{"RADARR_URL": radarr.URL}, sabNow)
	_, _, raw = getJSON(t, h, "/api/features")
	if strings.Count(raw, `"available":true`) != 1 {
		t.Errorf("an unready Radarr enabled a feature: %s", raw)
	}
}
