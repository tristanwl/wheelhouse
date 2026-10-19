package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
	fake "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent/fakeqbittorrent"
)

var bg = context.Background()

func clientFor(t *testing.T, v fake.Version, scenario fake.Scenario, password string) (dc.Client, *fake.Server) {
	t.Helper()
	srv := fake.Start(t, v, scenario)
	c, err := New(Config{URL: srv.URL, Username: fake.GoodUser, Password: password}, "qbittorrent")
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func good(t *testing.T, v fake.Version, scenario fake.Scenario) (dc.Client, *fake.Server) {
	return clientFor(t, v, scenario, fake.GoodPassword)
}

// realTorrents are the torrents the fake serves for a version: real captures.
func realTorrents(t *testing.T, v fake.Version) []fake.Obj {
	t.Helper()
	var out []fake.Obj
	if err := json.Unmarshal(fake.Fixture("torrents-"+string(v)+".json"), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestContract(t *testing.T) {
	for _, v := range []fake.Version{fake.V5, fake.V4} {
		dc.RunContract(t, "qbittorrent "+string(v), func(t *testing.T) (dc.Client, dc.Protocol) {
			c, _ := good(t, v, fake.Active)
			return c, dc.Torrent
		})
	}
}

func TestStateMapping(t *testing.T) {
	for _, tc := range []struct {
		in     string
		state  dc.State
		detail string
	}{
		{"downloading", dc.StateDownloading, ""},
		{"forcedDL", dc.StateDownloading, ""},
		{"forcedMetaDL", dc.StateDownloading, ""},
		{"metaDL", dc.StateDownloading, "Fetching metadata"},
		{"allocating", dc.StateDownloading, "Allocating"},
		{"stalledDL", dc.StateStalled, ""},
		{"queuedDL", dc.StateQueued, ""},
		{"queuedUP", dc.StateQueued, ""},
		{"checkingDL", dc.StatePostprocessing, "Checking"},
		{"checkingUP", dc.StatePostprocessing, "Checking"},
		{"checkingResumeData", dc.StatePostprocessing, "Checking"},
		{"moving", dc.StatePostprocessing, "Moving"},
		{"uploading", dc.StateSeeding, ""},
		{"forcedUP", dc.StateSeeding, ""},
		{"stalledUP", dc.StateSeeding, ""},
		{"pausedDL", dc.StatePaused, ""},
		{"stoppedDL", dc.StatePaused, ""},
		{"pausedUP", dc.StateCompleted, ""},
		{"stoppedUP", dc.StateCompleted, ""},
		{"error", dc.StateFailed, ""},
		{"missingFiles", dc.StateFailed, "Files missing"},
		// A state it does not know keeps the client's word.
		{"somethingNew", dc.StateDownloading, "somethingNew"},
	} {
		if got := MapState(tc.in); got != (Mapped{tc.state, tc.detail}) {
			t.Errorf("%s -> %+v, want %s %q", tc.in, got, tc.state, tc.detail)
		}
	}
	if MapState("stoppedDL") != MapState("pausedDL") || MapState("stoppedUP") != MapState("pausedUP") {
		t.Error("the v5 and v4 names of a stopped torrent disagree")
	}
}

func TestItems(t *testing.T) {
	t.Run("lists everything except finished, stopped torrents (v5 fixtures from a real client)", func(t *testing.T) {
		c, _ := good(t, fake.V5, fake.Active)
		items, err := c.Items(bg)
		if err != nil {
			t.Fatal(err)
		}
		real := realTorrents(t, fake.V5)
		stoppedUp := 0
		for _, r := range real {
			if r["state"] == "stoppedUP" {
				stoppedUp++
				for _, it := range items {
					if it.ID == r["hash"] {
						t.Error("a finished, stopped torrent is listed")
					}
				}
			}
		}
		if stoppedUp != 1 || len(items) != len(real)-1 {
			t.Errorf("%d items for %d torrents (%d stoppedUP)", len(items), len(real), stoppedUp)
		}
		got := map[dc.State]bool{}
		for _, it := range items {
			got[it.State] = true
		}
		want := map[dc.State]bool{
			dc.StateSeeding: true, dc.StateStalled: true, dc.StateDownloading: true,
			dc.StateQueued: true, dc.StatePostprocessing: true, dc.StatePaused: true,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("states %v", got)
		}
	})

	t.Run("maps a real seeding torrent", func(t *testing.T) {
		c, _ := good(t, fake.V5, fake.Active)
		items, _ := c.Items(bg)
		i := slices.IndexFunc(items, func(i dc.Item) bool { return i.Name == "Example.Movie.2024.1080p.BluRay.x264-GROUP" })
		if i < 0 {
			t.Fatal("not found")
		}
		it := items[i]
		want := dc.Item{
			ID: realTorrents(t, fake.V5)[0]["hash"].(string), Name: it.Name, Protocol: dc.Torrent, State: dc.StateSeeding,
			Progress: 1, SizeBytes: 1073741824, RemainingBytes: 0, Torrent: &dc.TorrentInfo{},
		}
		if !reflect.DeepEqual(it, want) {
			t.Errorf("%+v\nwant %+v", it, want)
		}
	})

	t.Run("a half-downloaded stalled torrent and a magnet still fetching metadata", func(t *testing.T) {
		c, _ := good(t, fake.V5, fake.Active)
		items, _ := c.Items(bg)
		stalled := items[slices.IndexFunc(items, func(i dc.Item) bool { return i.State == dc.StateStalled })]
		if stalled.Progress != 0.5 || stalled.SizeBytes != 262144 || stalled.RemainingBytes != 131072 || stalled.ETASeconds != nil {
			t.Errorf("%+v", stalled)
		}
		meta := items[slices.IndexFunc(items, func(i dc.Item) bool { return i.StateDetail == "Fetching metadata" })]
		if meta.State != dc.StateDownloading || meta.SizeBytes != 0 || meta.RemainingBytes != 0 {
			t.Errorf("%+v", meta)
		}
	})

	t.Run("reads v4's paused names", func(t *testing.T) {
		c, _ := good(t, fake.V4, fake.Active)
		items, err := c.Items(bg)
		if err != nil {
			t.Fatal(err)
		}
		// The v4 capture has one paused download and one paused (finished) upload.
		if len(items) != 1 || items[0].State != dc.StatePaused {
			t.Errorf("%+v", items)
		}
	})

	t.Run("treats eta 8640000 as infinity and passes real ETAs through", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Edit(func(s *fake.State) {
			base := s.Torrents[0]
			s.Torrents = []fake.Obj{
				with(base, "hash", "a", "state", "downloading", "progress", 0.1, "eta", 8640000),
				with(base, "hash", "b", "state", "downloading", "progress", 0.1, "eta", 90),
				with(base, "hash", "c", "state", "downloading", "progress", 0.1, "eta", 0),
			}
		})
		items, err := c.Items(bg)
		if err != nil {
			t.Fatal(err)
		}
		var etas []int64
		for _, it := range items {
			if it.ETASeconds == nil {
				etas = append(etas, -1)
			} else {
				etas = append(etas, *it.ETASeconds)
			}
		}
		if !reflect.DeepEqual(etas, []int64{-1, 90, -1}) {
			t.Errorf("%v", etas)
		}
	})

	t.Run("ratio, seeders and upload speed of a seeding torrent", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Edit(func(s *fake.State) {
			s.Torrents = []fake.Obj{with(s.Torrents[0], "state", "uploading", "ratio", 1.75, "num_seeds", 4, "upspeed", 51200)}
		})
		items, _ := c.Items(bg)
		if items[0].State != dc.StateSeeding || *items[0].Torrent != (dc.TorrentInfo{Ratio: 1.75, Seeders: 4, UploadBytesPerSec: 51200}) {
			t.Errorf("%+v", items[0])
		}
	})

	t.Run("downloading, error and missing files, which a client without peers cannot produce", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Edit(func(s *fake.State) {
			base := s.Torrents[0]
			s.Torrents = []fake.Obj{
				with(base, "hash", "d", "state", "downloading", "progress", 0.25, "size", 1000, "amount_left", 750),
				with(base, "hash", "e", "state", "error", "progress", 0.25, "size", 1000, "amount_left", 750),
				with(base, "hash", "f", "state", "missingFiles", "progress", 1, "size", 1000, "amount_left", 0),
			}
		})
		items, _ := c.Items(bg)
		var states []dc.State
		for _, it := range items {
			states = append(states, it.State)
		}
		if !reflect.DeepEqual(states, []dc.State{dc.StateDownloading, dc.StateFailed, dc.StateFailed}) {
			t.Errorf("%v", states)
		}
		if items[0].SizeBytes != 1000 || items[0].RemainingBytes != 750 || items[0].Progress != 0.25 {
			t.Errorf("%+v", items[0])
		}
	})

	t.Run("odd numbers are clamped", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Edit(func(s *fake.State) {
			s.Torrents = []fake.Obj{with(s.Torrents[0], "hash", "z", "state", "downloading", "progress", 7, "size", 100,
				"amount_left", 500, "ratio", -3, "num_seeds", -1, "eta", -5)}
		})
		items, _ := c.Items(bg)
		it := items[0]
		if it.Progress != 1 || it.RemainingBytes != 100 || it.Torrent.Ratio != 0 || it.Torrent.Seeders != 0 || it.ETASeconds != nil {
			t.Errorf("%+v", it)
		}
	})

	t.Run("an empty client has an empty, non-nil list", func(t *testing.T) {
		c, _ := good(t, fake.V5, fake.Idle)
		items, err := c.Items(bg)
		if err != nil || items == nil || len(items) != 0 {
			t.Errorf("%v %v", items, err)
		}
	})
}

// with copies base and sets key/value pairs.
func with(base fake.Obj, kv ...any) fake.Obj {
	out := fake.Obj{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestStatus(t *testing.T) {
	t.Run("an empty client is idle", func(t *testing.T) {
		c, _ := good(t, fake.V5, fake.Idle)
		s, err := c.Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		want := dc.Status{
			StatusText: "Idle", Version: "5.2.4", Warnings: []dc.Warning{},
			Torrent: &dc.TorrentStatus{},
		}
		if !reflect.DeepEqual(s, want) {
			t.Errorf("%+v", s)
		}
		b, _ := json.Marshal(s)
		if !strings.Contains(string(b), `"warnings":[]`) || !strings.Contains(string(b), `"etaSeconds":null`) {
			t.Errorf("%s", b)
		}
	})

	t.Run("unfinished torrents are the queue; seeding ones are counted separately", func(t *testing.T) {
		c, _ := good(t, fake.V5, fake.Active)
		s, err := c.Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		incomplete := 0
		for _, r := range realTorrents(t, fake.V5) {
			if r["progress"].(float64) < 1 {
				incomplete++
			}
		}
		// stalledUP and forcedUP seed; queuedUP, stoppedUP and the rest do not.
		if s.QueueCount != int64(incomplete) || s.Torrent.Seeding != 2 || s.Paused ||
			s.StatusText != "Seeding" || s.Version != "5.2.4" || s.Usenet != nil {
			t.Errorf("%+v", s)
		}
	})

	t.Run("speeds come from transfer info and give the whole-queue ETA", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Edit(func(s *fake.State) {
			s.Transfer = with(s.Transfer, "dl_info_speed", 1000, "up_info_speed", 250)
			s.Torrents = []fake.Obj{
				with(s.Torrents[0], "state", "downloading", "progress", 0.5, "amount_left", 4000),
				with(s.Torrents[0], "hash", "x", "state", "queuedDL", "progress", 0, "amount_left", 1500),
			}
		})
		s, err := c.Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		if s.StatusText != "Downloading" || s.SpeedBytesPerSec != 1000 || s.QueueCount != 2 ||
			s.ETASeconds == nil || *s.ETASeconds != 6 || s.Torrent.Seeding != 0 || s.Torrent.UploadBytesPerSec != 250 {
			t.Errorf("%+v", s)
		}
	})

	t.Run("paused only when everything still downloading is stopped", func(t *testing.T) {
		c, srv := good(t, fake.V4, fake.Active)
		s, err := c.Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		// The v4 capture: one paused download, one finished and paused.
		if !s.Paused || s.StatusText != "Paused" || s.QueueCount != 1 || s.Version != "4.6.7" {
			t.Errorf("%+v", s)
		}
		srv.Edit(func(st *fake.State) {
			st.Torrents = []fake.Obj{st.Torrents[0], with(st.Torrents[0], "hash", "y", "state", "downloading")}
		})
		if s, _ = c.Status(bg); s.Paused {
			t.Errorf("paused with an active download")
		}
	})
}

func TestSession(t *testing.T) {
	t.Run("TestConnection logs in and returns the version without the leading v", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		if got := c.TestConnection(bg); got != (core.ConnectionResult{OK: true, Version: "5.2.4"}) {
			t.Errorf("%+v", got)
		}
		if srv.Logins() != 1 {
			t.Errorf("logins %d", srv.Logins())
		}
	})

	t.Run("logs in once and reuses the session cookie", func(t *testing.T) {
		c, srv := good(t, fake.V4, fake.Active)
		_, _ = c.Items(bg)
		_, _ = c.Items(bg)
		_, _ = c.Status(bg)
		if srv.Logins() != 1 {
			t.Errorf("logins %d", srv.Logins())
		}
		for _, r := range srv.Requests() {
			if r.Path != "/api/v2/auth/login" && !strings.HasPrefix(r.Cookie, "SID=") {
				t.Errorf("%s sent cookie %q", r.Path, r.Cookie)
			}
		}
	})

	t.Run("v5 keeps its port-suffixed cookie name", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		_, _ = c.Items(bg)
		reqs := srv.Requests()
		if !strings.HasPrefix(reqs[len(reqs)-1].Cookie, "QBT_SID_8080=") {
			t.Errorf("%+v", reqs)
		}
	})

	for _, v := range []fake.Version{fake.V4, fake.V5} {
		t.Run("signs in again, once, when the "+string(v)+" session expires", func(t *testing.T) {
			c, srv := good(t, v, fake.Active)
			if _, err := c.Items(bg); err != nil {
				t.Fatal(err)
			}
			srv.ExpireSessions()
			items, err := c.Items(bg)
			if err != nil || len(items) == 0 {
				t.Fatalf("%v %v", items, err)
			}
			if srv.Logins() != 2 {
				t.Errorf("logins %d", srv.Logins())
			}
			// First call, the expired call (403), then the retry.
			n := 0
			for _, r := range srv.Requests() {
				if r.Path == "/api/v2/torrents/info" {
					n++
				}
			}
			if n != 3 {
				t.Errorf("torrents/info calls %d", n)
			}
		})
	}

	t.Run("the parallel calls of one Status share a single new login", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		_, _ = c.Items(bg)
		srv.ExpireSessions()
		if _, err := c.Status(bg); err != nil {
			t.Fatal(err)
		}
		if srv.Logins() != 2 {
			t.Errorf("logins %d", srv.Logins())
		}
	})

	t.Run("many concurrent first calls share one login", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				if _, err := c.Items(bg); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if srv.Logins() != 1 {
			t.Errorf("logins %d", srv.Logins())
		}
	})

	t.Run("a caller holding a stale session reuses the newer one instead of signing in again", func(t *testing.T) {
		raw, srv := good(t, fake.V5, fake.Active)
		c := raw.(*client)
		_, staleGen, err := c.session(bg)
		if err != nil {
			t.Fatal(err)
		}
		srv.ExpireSessions()
		if _, err := c.renew(bg, staleGen); err != nil { // the first refused call signs in
			t.Fatal(err)
		}
		if _, err := c.renew(bg, staleGen); err != nil { // a slower parallel call finds that done
			t.Fatal(err)
		}
		if srv.Logins() != 2 {
			t.Errorf("logins %d, want 2", srv.Logins())
		}
	})

	for _, v := range []fake.Version{fake.V4, fake.V5} {
		t.Run("a wrong password ("+string(v)+") is a clear message that does not contain it", func(t *testing.T) {
			const secret = "wrong-secret-password-999"
			c, _ := clientFor(t, v, fake.Active, secret)
			const msg = "qbittorrent: login failed (check the username and password)"
			if got := c.TestConnection(bg); got != (core.ConnectionResult{Error: msg}) {
				t.Errorf("%+v", got)
			}
			_, err1 := c.Status(bg)
			_, err2 := c.Items(bg)
			for _, err := range []error{err1, err2} {
				if err == nil || err.Error() != msg {
					t.Errorf("%v", err)
				}
			}
		})
	}

	t.Run("a banned IP (403 on login) says so, even with the right password", func(t *testing.T) {
		bad, srv := clientFor(t, fake.V5, fake.Active, "nope-nope")
		for range 5 {
			bad.TestConnection(bg)
		}
		goodClient, err := New(Config{URL: srv.URL, Username: fake.GoodUser, Password: fake.GoodPassword}, "qbittorrent")
		if err != nil {
			t.Fatal(err)
		}
		got := goodClient.TestConnection(bg)
		if got.OK || got.Error != "qbittorrent: login refused, this IP may be banned after too many failed attempts" {
			t.Errorf("%+v", got)
		}
		if strings.Contains(got.Error, fake.GoodPassword) {
			t.Error("password leaked")
		}
	})

	t.Run("signs in only once more when the session keeps being refused", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Edit(func(s *fake.State) { s.RefuseSessions = true })
		_, err := c.Items(bg)
		if err == nil || err.Error() != "qbittorrent: not authorized after signing in" {
			t.Errorf("%v", err)
		}
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Status != 403 {
			t.Errorf("%#v", err)
		}
		// The first login, plus the single retry.
		if srv.Logins() != 2 {
			t.Errorf("logins %d", srv.Logins())
		}
	})

	t.Run("an unreachable server is reported, not thrown, by TestConnection", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		srv.Close()
		if got := c.TestConnection(bg); got != (core.ConnectionResult{Error: "qbittorrent: connection refused"}) {
			t.Errorf("%+v", got)
		}
	})

	t.Run("a canceled caller does not poison the shared login", func(t *testing.T) {
		c, srv := good(t, fake.V5, fake.Active)
		ctx, cancel := context.WithCancel(bg)
		cancel()
		if res := c.TestConnection(ctx); res.OK || !strings.Contains(res.Error, "canceled") {
			t.Errorf("%+v", res)
		}
		// Whether or not the abandoned login finished, the next call works.
		if res := c.TestConnection(bg); !res.OK {
			t.Errorf("%+v", res)
		}
		if srv.Logins() > 2 {
			t.Errorf("logins %d", srv.Logins())
		}
	})
}

// upstream starts a server running h and a client with tightened limits.
func upstream(t *testing.T, h http.HandlerFunc, tweak func(*core.HTTPOptions)) *client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c, err := newClient(Config{URL: ts.URL, Username: "u", Password: "upstream-pass-123456"}, "qbittorrent", tweak)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// loginThen answers the login and then h.
func loginThen(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=abc; path=/")
			_, _ = w.Write([]byte("Ok."))
			return
		}
		h(w, r)
	}
}

func TestUpstreamFailures(t *testing.T) {
	t.Run("malformed JSON is an error that does not quote the body", func(t *testing.T) {
		c := upstream(t, loginThen(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[{"hash": SECRET-BODY`)) }), nil)
		_, err := c.Items(bg)
		if err == nil || err.Error() != "qbittorrent /api/v2/torrents/info returned invalid JSON" {
			t.Errorf("%v", err)
		}
	})

	t.Run("a response of the wrong shape is an error", func(t *testing.T) {
		c := upstream(t, loginThen(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"a":1}`)) }), nil)
		if _, err := c.Items(bg); err == nil {
			t.Error("Items succeeded")
		}
		if _, err := c.Status(bg); err == nil {
			t.Error("Status succeeded")
		}
	})

	t.Run("an oversized response is refused", func(t *testing.T) {
		c := upstream(t, loginThen(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[` + strings.Repeat(" ", 4096) + `]`))
		}), func(o *core.HTTPOptions) { o.MaxResponseBytes = 1024 })
		_, err := c.Items(bg)
		if err == nil || !strings.Contains(err.Error(), "response larger than 1024 bytes") {
			t.Errorf("%v", err)
		}
	})

	t.Run("a flood of empty elements is refused by its element count, not its size", func(t *testing.T) {
		c := upstream(t, loginThen(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[` + strings.Repeat("{},", core.DefaultMaxElements) + `{}]`))
		}), nil)
		_, err := c.Items(bg)
		if err == nil || !strings.Contains(err.Error(), "too large or too deeply nested") {
			t.Errorf("%v", err)
		}
	})

	t.Run("a slow service times out", func(t *testing.T) {
		release := make(chan struct{})
		c := upstream(t, func(http.ResponseWriter, *http.Request) { <-release },
			func(o *core.HTTPOptions) { o.Timeout = 100 * time.Millisecond })
		t.Cleanup(func() { close(release) }) // runs before the server's Close
		res := c.TestConnection(bg)
		if res.OK || res.Error != "qbittorrent: timed out after 100ms" {
			t.Errorf("%+v", res)
		}
	})

	t.Run("a server error is reported with the status only", func(t *testing.T) {
		c := upstream(t, loginThen(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("trace upstream-pass-123456"))
		}), nil)
		res := c.TestConnection(bg)
		if res.Error != "qbittorrent /api/v2/app/version failed: 500 Internal Server Error" {
			t.Errorf("%+v", res)
		}
	})

	t.Run("a login with no cookie is not retried forever", func(t *testing.T) {
		var logins int
		var mu sync.Mutex
		c := upstream(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				mu.Lock()
				logins++
				mu.Unlock()
				_, _ = w.Write([]byte("Ok."))
				return
			}
			w.WriteHeader(http.StatusForbidden)
		}, nil)
		_, err := c.Items(bg)
		if err == nil || err.Error() != "qbittorrent: not authorized after signing in" || logins != 2 {
			t.Errorf("%v, logins %d", err, logins)
		}
	})
}
