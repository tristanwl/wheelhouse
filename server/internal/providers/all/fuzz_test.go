package all

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent/fakeqbittorrent"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd/fakesabnzbd"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/mmtest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/jellyfin/jellyfintest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex/plextest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

// upstream is one provider under test: its fake service, behind a proxy that
// corrupts one answer, and the settings that point a client at the proxy.
type upstream struct {
	id     string
	config map[string]any
	// secrets are the credentials in config: none may appear in an error.
	secrets []string
}

// FuzzUpstreamResponses runs every call of every adapter against its fake
// service, with exactly one of the service's answers replaced by fuzzed bytes
// and a fuzzed status. The seeds are the real fixtures of the fakes, so the
// fuzzer starts from answers each adapter understands and bends them: truncated,
// odd-typed, nested, huge numbers, other shapes, binary.
//
// Invariants, for each call: it returns (no panic, no hang); an error is a
// *core.ProviderError, whose text is short, free of every configured secret
// (in any encoding), and free of any long token of the hostile body (an adapter
// never echoes what the upstream said); a connection test never returns an error
// but a result whose text obeys the same; and whatever values come back are
// normalized: they encode as JSON (no NaN or infinity), download progress is
// within 0..1, sizes, counts and speeds are not negative, states are in the shared
// enum.
func FuzzUpstreamResponses(f *testing.F) {
	tamper := &hostiletest.Tamper{}
	radarr, sonarr := mmtest.StartRadarr(f), mmtest.StartSonarr(f)
	sab := fakesabnzbd.Start(f, fakesabnzbd.Active)
	qbit := fakeqbittorrent.Start(f, fakeqbittorrent.V5, fakeqbittorrent.Active)
	plex := plextest.Start(f, plextest.Scenario{Claimed: true, Active: true})
	jf := jellyfintest.Start(f, jellyfintest.Scenario{SetupCompleted: true})
	tm := tmdbtest.Start(f, tmdbtest.Options{})

	ups := []upstream{
		{"radarr", map[string]any{"url": hostiletest.Proxy(f, radarr.URL, tamper), "apiKey": mmtest.GoodKey}, []string{mmtest.GoodKey}},
		{"sonarr", map[string]any{"url": hostiletest.Proxy(f, sonarr.URL, tamper), "apiKey": mmtest.GoodKey}, []string{mmtest.GoodKey}},
		{"sabnzbd", map[string]any{"url": hostiletest.Proxy(f, sab.URL, tamper), "apiKey": fakesabnzbd.GoodKey}, []string{fakesabnzbd.GoodKey}},
		{"qbittorrent", map[string]any{"url": hostiletest.Proxy(f, qbit.URL, tamper), "username": fakeqbittorrent.GoodUser, "password": fakeqbittorrent.GoodPassword}, []string{fakeqbittorrent.GoodPassword}},
		{"plex", map[string]any{"url": hostiletest.Proxy(f, plex.URL, tamper), "token": plextest.GoodToken}, []string{plextest.GoodToken}},
		{"jellyfin", map[string]any{"url": hostiletest.Proxy(f, jf.URL, tamper), "apiKey": jellyfintest.GoodKey}, []string{jellyfintest.GoodKey}},
		{"tmdb", map[string]any{"url": hostiletest.Proxy(f, tm.URL, tamper) + "/3", "apiKey": tmdbtest.GoodKey}, []string{tmdbtest.GoodKey}},
	}
	reg, err := Registry()
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, prov, at, statusSel uint8, body []byte) {
		up := ups[int(prov)%len(ups)]
		def, ok := reg.Get(up.id)
		if !ok {
			t.Fatalf("no provider %s", up.id)
		}
		cfg, err := def.Decode(up.config)
		if err != nil {
			t.Fatal(err)
		}
		tamper.Reset(int(at)%48, statusSel, body)
		client := def.Create(cfg, core.ProviderContext{InstanceID: up.id, InstanceName: up.id})

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tokens := longTokens(body)
		check := func(what string, err error) {
			if err == nil {
				return
			}
			var pe *core.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("%s %s: error is %T: %v", up.id, what, err, err)
			}
			checkText(t, up, what, err.Error(), tokens)
		}
		onTest := func(r core.ConnectionResult) {
			// The version is the service's own text (Definition.TestConnection
			// redacts it before it leaves); qBittorrent answers it as plain text, so
			// anything that is not a version number is refused there.
			checkText(t, up, "TestConnection result", r.Error, tokens)
			if up.id == "qbittorrent" && r.OK && !qbitVersion.MatchString(r.Version) {
				t.Fatalf("qbittorrent accepted %q as a version", r.Version)
			}
		}

		for _, call := range calls(t, client, onTest) {
			start := time.Now()
			err := call.do(ctx)
			if time.Since(start) > 10*time.Second {
				t.Fatalf("%s %s took %s", up.id, call.name, time.Since(start))
			}
			if call.name == "TestConnection" {
				if err != nil { // the media managers' list reports the result as an error
					checkText(t, up, call.name, err.Error(), tokens)
				}
				continue
			}
			check(call.name, err)
		}
	})
}

var qbitVersion = regexp.MustCompile(`^[0-9]+(\.[0-9A-Za-z_+-]+){0,4}$`)

// longTokens are the runs of 14 or more letters and digits in the body: the
// kind of text a secret or an identifier is made of.
func longTokens(body []byte) []string {
	var out []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= 14 {
			out = append(out, string(body[start:end]))
		}
		start = -1
	}
	for i, c := range body {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(body))
	return out
}

func checkText(t *testing.T, up upstream, what, text string, tokens []string) {
	t.Helper()
	if len(text) > 400 {
		t.Fatalf("%s %s: text of %d bytes: %.200s", up.id, what, len(text), text)
	}
	for _, s := range up.secrets {
		for _, form := range hostiletest.SecretForms(s) {
			if strings.Contains(text, form) {
				t.Fatalf("%s %s: the secret leaked as %q in %q", up.id, what, form, text)
			}
		}
	}
	for _, tok := range tokens {
		if strings.Contains(text, tok) {
			t.Fatalf("%s %s: the text echoes the upstream body (%q): %q", up.id, what, tok, text)
		}
	}
}

type call struct {
	name string
	do   func(ctx context.Context) error
}

// calls lists what to run against a client of any role, with a check of the
// normalized values each returns.
func calls(t *testing.T, client any, onTest func(core.ConnectionResult)) []call {
	marshal := func(what string, v any) {
		t.Helper()
		if _, err := json.Marshal(v); err != nil {
			t.Fatalf("%s: the result does not encode: %v", what, err)
		}
	}
	switch c := client.(type) {
	case mm.Manager:
		out := []call{}
		for _, op := range mmtest.Calls(c) {
			out = append(out, call{op.Name, op.Do})
		}
		read := func(name string, f func(ctx context.Context) (any, error)) {
			out = append(out, call{"values/" + name, func(ctx context.Context) error {
				v, err := f(ctx)
				if err == nil {
					marshal(name, v)
				}
				return err
			}})
		}
		read("Status", func(ctx context.Context) (any, error) { return c.Status(ctx) })
		read("SystemStatus", func(ctx context.Context) (any, error) { return c.SystemStatus(ctx) })
		read("Health", func(ctx context.Context) (any, error) { return c.Health(ctx) })
		read("QueueSummary", func(ctx context.Context) (any, error) { return c.QueueSummary(ctx) })
		read("RootFolders", func(ctx context.Context) (any, error) { return c.RootFolders(ctx) })
		read("Lookup", func(ctx context.Context) (any, error) { return c.Lookup(ctx, "example") })
		if m, ok := mm.AsMovies(c); ok {
			read("Movies", func(ctx context.Context) (any, error) { return m.Movies(ctx) })
			read("MovieDetail", func(ctx context.Context) (any, error) { return m.MovieDetail(ctx, 1) })
		}
		if s, ok := mm.AsSeries(c); ok {
			read("Series", func(ctx context.Context) (any, error) { return s.Series(ctx) })
			read("SeriesDetail", func(ctx context.Context) (any, error) { return s.SeriesDetail(ctx, 1) })
			read("Episodes", func(ctx context.Context) (any, error) { return s.Episodes(ctx, 1) })
			read("EpisodeFiles", func(ctx context.Context) (any, error) { return s.EpisodeFiles(ctx, 1) })
			read("Seasons", func(ctx context.Context) (any, error) { return s.Seasons(ctx, 1) })
		}
		return out
	case dc.Client:
		return []call{
			{"TestConnection", func(ctx context.Context) error { onTest(c.TestConnection(ctx)); return nil }},
			{"Status", func(ctx context.Context) error {
				st, err := c.Status(ctx)
				if err != nil {
					return err
				}
				marshal("Status", st)
				if st.SpeedBytesPerSec < 0 || st.QueueCount < 0 || st.Warnings == nil || (st.ETASeconds != nil && *st.ETASeconds < 0) {
					t.Fatalf("status is not normalized: %+v", st)
				}
				if st.Torrent != nil && (st.Torrent.Seeding < 0 || st.Torrent.UploadBytesPerSec < 0) {
					t.Fatalf("torrent status is not normalized: %+v", st.Torrent)
				}
				return nil
			}},
			{"Items", func(ctx context.Context) error {
				items, err := c.Items(ctx)
				if err != nil {
					return err
				}
				marshal("Items", items)
				for _, it := range items {
					if math.IsNaN(it.Progress) || it.Progress < 0 || it.Progress > 1 || it.SizeBytes < 0 || it.RemainingBytes < 0 ||
						(it.ETASeconds != nil && *it.ETASeconds < 0) || !knownState(it.State) {
						t.Fatalf("item is not normalized: %+v", it)
					}
					if it.Torrent != nil && (it.Torrent.Seeders < 0 || it.Torrent.UploadBytesPerSec < 0 || math.IsNaN(it.Torrent.Ratio) || math.IsInf(it.Torrent.Ratio, 0)) {
						t.Fatalf("torrent item is not normalized: %+v", it.Torrent)
					}
				}
				return nil
			}},
		}
	case mediaserver.Client:
		return []call{
			{"TestConnection", func(ctx context.Context) error { onTest(c.TestConnection(ctx)); return nil }},
			{"Status", func(ctx context.Context) error {
				st, err := c.Status(ctx)
				if err != nil {
					return err
				}
				marshal("Status", st)
				if st.ActiveSessions != nil && *st.ActiveSessions < 0 {
					t.Fatalf("negative sessions: %+v", st)
				}
				if st.State != mediaserver.StateOK && st.State != mediaserver.StateNeedsSetup {
					t.Fatalf("unknown state: %+v", st)
				}
				return nil
			}},
		}
	case *tmdb.Client:
		return []call{
			{"TestConnection", func(ctx context.Context) error { onTest(c.TestConnection(ctx)); return nil }},
			{"Popular", func(ctx context.Context) error {
				r, err := c.Popular(ctx, 20, 1)
				if err == nil {
					marshal("Popular", r)
				}
				return err
			}},
			{"NowPlaying", func(ctx context.Context) error { _, err := c.NowPlaying(ctx, 20, 1); return err }},
			{"RecentPhysicalReleases", func(ctx context.Context) error { _, err := c.RecentPhysicalReleases(ctx, 20, 1); return err }},
			{"MovieDetail", func(ctx context.Context) error {
				d, err := c.MovieDetail(ctx, 603)
				if err == nil {
					marshal("MovieDetail", d)
				}
				return err
			}},
		}
	}
	t.Fatalf("no calls for %T", client)
	return nil
}

func knownState(s dc.State) bool {
	for _, k := range dc.States {
		if s == k {
			return true
		}
	}
	return false
}
