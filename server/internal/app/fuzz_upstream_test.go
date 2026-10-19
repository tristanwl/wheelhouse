package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent/fakeqbittorrent"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd/fakesabnzbd"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/jellyfin/jellyfintest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex/plextest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

// startTamperedUpstreams is startUpstreams with a hostiletest proxy in front of
// each fake service.
func startTamperedUpstreams(tb testing.TB, tamper *hostiletest.Tamper) *fuzzUpstreams {
	radarrUp, sonarrUp := &hostiletest.Upstream{}, &hostiletest.Upstream{}
	sab := fakesabnzbd.Start(tb, fakesabnzbd.Active)
	qbit := fakeqbittorrent.Start(tb, fakeqbittorrent.V5, fakeqbittorrent.Active)
	plex := plextest.Start(tb, plextest.Scenario{Claimed: true, Active: true})
	jf := jellyfintest.Start(tb, jellyfintest.Scenario{SetupCompleted: true})
	tm := tmdbtest.Start(tb, tmdbtest.Options{})
	p := func(u string) string { return hostiletest.Proxy(tb, u, tamper) }
	return &fuzzUpstreams{radarr: radarrUp, sonarr: sonarrUp, env: map[string]string{
		"RADARR_URL": hostiletest.ProxyTo(tb, radarrUp, tamper), "RADARR_API_KEY": mmtest.GoodKey,
		"SONARR_URL": hostiletest.ProxyTo(tb, sonarrUp, tamper), "SONARR_API_KEY": mmtest.GoodKey,
		"SABNZBD_URL": p(sab.URL), "SABNZBD_API_KEY": fakesabnzbd.GoodKey,
		"QBITTORRENT_URL": p(qbit.URL), "QBITTORRENT_USERNAME": fakeqbittorrent.GoodUser, "QBITTORRENT_PASSWORD": fakeqbittorrent.GoodPassword,
		"PLEX_URL": p(plex.URL), "PLEX_TOKEN": plextest.GoodToken,
		"JELLYFIN_URL": p(jf.URL), "JELLYFIN_API_KEY": jellyfintest.GoodKey,
		"TMDB_API_KEY": tmdbtest.GoodKey, "TMDB_BASE_URL": p(tm.URL) + "/3",
	}}
}

// upstreamRoutes lists a request for every route of the router table that a
// signed-in administrator can make (with a body where the route needs one), the
// ids filled in so they name something the fakes know.
func upstreamRoutes(rt *Router) []struct{ method, target, body string } {
	var out []struct{ method, target, body string }
	for _, r := range rt.Routes() {
		if r.Level == Public {
			continue
		}
		method, path, _ := strings.Cut(r.Pattern, " ")
		inst := "radarr"
		if strings.Contains(path, "/seasons") || strings.Contains(path, "kind=series") {
			inst = "sonarr"
		}
		path = strings.NewReplacer("{instanceId}", inst, "{id}", "1", "{jobId}", "x", "{season}", "1", "{sessionId}", "x").Replace(path)
		switch {
		case strings.HasPrefix(path, "/api/users"), strings.HasPrefix(path, "/api/auth/"), strings.HasPrefix(path, "/api/audit"):
			continue // accounts, not upstreams
		case path == "/api/explore/1":
			path = "/api/explore/603"
		case path == "/api/explore":
			path = "/api/explore?category=popular"
		case path == "/api/codec-analysis":
			path = "/api/codec-analysis?kind=movies"
		case path == "/api/search":
			path = "/api/search?q=example"
		case strings.HasPrefix(method+" "+path, "DELETE /api/library"):
			path += "?confirm=true"
		case path == "/api/admin/instances/test":
			out = append(out, struct{ method, target, body string }{"POST", path, `{"id":"radarr","providerId":"radarr","config":{}}`})
			continue
		case path == "/api/admin/instances/radarr/import":
			continue
		}
		body := ""
		if b := seedBodies[r.Pattern]; len(b) > 0 && method != http.MethodGet {
			body = b[0]
		}
		if r.Pattern == "POST /api/add" {
			body = `{"instanceId":"radarr","id":603,"qualityProfileId":1,"rootFolderPath":"/movies","monitored":true,"searchNow":false}`
		}
		out = append(out, struct{ method, target, body string }{method, path, body})
	}
	return out
}

// FuzzHostileUpstream runs every API route as an administrator against the whole
// stack of fake services with exactly one upstream answer replaced by fuzzed
// bytes and a fuzzed status (hostiletest.Tamper), so every adapter, mapper and
// route meets a hostile answer in the middle of an otherwise valid conversation.
//
// Invariants, for every response: no panic (recovery is not installed); no 500 or
// 503 (the upstream's failure is a 502 or 504 at most, or the route degrades and
// answers 200 with the instance marked down); a JSON body; no configured secret
// in the response or the log; an error answer never echoes a long token of the
// hostile body (what the upstream said is data, never an error message); the
// response is bounded; and it arrives in a few seconds.
func FuzzHostileUpstream(f *testing.F) {
	tamper := &hostiletest.Tamper{}
	up := startTamperedUpstreams(f, tamper)
	planted := plantedSecrets(up)
	fx, err := newHTTPFixture(up)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(fx.close)
	f.Fuzz(func(t *testing.T, at, statusSel uint8, body []byte) {
		// Fresh Radarr and Sonarr fakes (the routes add and delete) and fresh
		// provider clients for every input.
		up.radarr.Set(mmtest.StartRadarr(t).URL)
		up.sonarr.Set(mmtest.StartSonarr(t).URL)
		fx.rebuild()
		tamper.Reset(int(at)%56, statusSel, body)
		tokens := longAlnumTokens(body)

		for _, r := range upstreamRoutes(fx.rt) {
			req, ok := fx.buildRequest(r.method, r.target, "Content-Type: application/json", []byte(r.body), adminOK, 0)
			if !ok {
				t.Fatalf("cannot build %s %s", r.method, r.target)
			}
			rec := httptest.NewRecorder()
			start := time.Now()
			fx.h.ServeHTTP(rec, req)
			elapsed := time.Since(start)
			desc := fmt.Sprintf("%s %s -> %d", r.method, r.target, rec.Code)
			logs := fx.logs.take()

			if strings.Contains(logs, "panic") {
				t.Fatalf("%s: a panic was logged: %s", desc, logs)
			}
			if rec.Code == http.StatusInternalServerError || rec.Code == http.StatusServiceUnavailable || rec.Code >= 505 {
				t.Fatalf("%s: server error: %.300s", desc, rec.Body.String())
			}
			if rec.Body.Len() > 0 && (!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || !json.Valid(rec.Body.Bytes())) {
				t.Fatalf("%s: not JSON: %.200q", desc, rec.Body.String())
			}
			if form := leakedIn(rec.Body.String()+logs, planted); form != "" {
				t.Fatalf("%s: the planted secret %q leaked", desc, form)
			}
			if rec.Code >= 400 {
				for _, tok := range tokens {
					if strings.Contains(rec.Body.String(), tok) {
						t.Fatalf("%s: the error echoes the upstream body (%q): %.300s", desc, tok, rec.Body.String())
					}
				}
			}
			if rec.Body.Len() > 16<<20 || elapsed > 10*time.Second {
				t.Fatalf("%s: %d bytes in %s", desc, rec.Body.Len(), elapsed)
			}
		}
	})
}

func longAlnumTokens(body []byte) []string {
	var out []string
	start := -1
	for i := 0; i <= len(body); i++ {
		alnum := i < len(body) && ((body[i] >= 'a' && body[i] <= 'z') || (body[i] >= 'A' && body[i] <= 'Z') || (body[i] >= '0' && body[i] <= '9'))
		switch {
		case alnum && start < 0:
			start = i
		case !alnum && start >= 0:
			if i-start >= 14 {
				out = append(out, string(body[start:i]))
			}
			start = -1
		}
	}
	return out
}

// TestUpstreamRouteCoverage keeps the hostile-upstream target honest: the routes
// it runs must really reach the fakes (a conversation of several dozen requests,
// not a wall of 404s).
func TestUpstreamRouteCoverage(t *testing.T) {
	tamper := &hostiletest.Tamper{}
	up := startTamperedUpstreams(t, tamper)
	up.radarr.Set(mmtest.StartRadarr(t).URL)
	up.sonarr.Set(mmtest.StartSonarr(t).URL)
	fx, err := newHTTPFixture(up)
	if err != nil {
		t.Fatal(err)
	}
	defer fx.close()
	tamper.Reset(1<<30, 0, nil)
	ok := 0
	for _, r := range upstreamRoutes(fx.rt) {
		req, _ := fx.buildRequest(r.method, r.target, "Content-Type: application/json", []byte(r.body), adminOK, 0)
		rec := httptest.NewRecorder()
		fx.h.ServeHTTP(rec, req)
		if rec.Code < 300 {
			ok++
		}
		if rec.Code >= 500 {
			t.Errorf("%s %s: %d %s", r.method, r.target, rec.Code, rec.Body.String())
		}
	}
	t.Logf("%d routes answered 2xx; %d upstream requests", ok, tamper.Requests())
	if ok < 10 || tamper.Requests() < 30 {
		t.Fatalf("only %d routes succeeded and %d upstream requests were made", ok, tamper.Requests())
	}
}
