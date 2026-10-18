package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/jellyfin/jellyfintest"
)

const version = jellyfintest.Version

var ctx = context.Background()

func create(t *testing.T, cfg Config) mediaserver.Client {
	t.Helper()
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get("jellyfin")
	raw := map[string]any{"url": cfg.URL}
	if cfg.APIKey != "" {
		raw["apiKey"] = cfg.APIKey
	}
	decoded, err := d.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d.Create(decoded, core.ProviderContext{InstanceID: "jellyfin", InstanceName: "Jellyfin"}).(mediaserver.Client)
}

// clientFor starts a fake. key "" means no key.
func clientFor(t *testing.T, sc jellyfintest.Scenario, key string) (mediaserver.Client, *jellyfintest.Server) {
	t.Helper()
	srv := jellyfintest.Start(t, sc)
	return create(t, Config{URL: srv.URL, APIKey: key}), srv
}

func status(t *testing.T, c mediaserver.Client) mediaserver.Status {
	t.Helper()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func sessions(t *testing.T, st mediaserver.Status) int {
	t.Helper()
	if st.ActiveSessions == nil {
		t.Fatalf("no session count in %+v", st)
	}
	return *st.ActiveSessions
}

func TestContract(t *testing.T) {
	mediaserver.RunContract(t, "jellyfin", func(t *testing.T) mediaserver.Client {
		c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.Playing}, jellyfintest.GoodKey)
		return c
	})
}

func TestStatusHealthyAuthenticated(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.Playing}, jellyfintest.GoodKey)
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Name != "Example Jellyfin" || st.Version != version ||
		!st.Authenticated || st.Notice != "" || sessions(t, st) != 1 {
		t.Errorf("got %+v", st)
	}
	raw, _ := json.Marshal(st)
	if string(raw) != `{"state":"ok","name":"Example Jellyfin","version":"12.2.0","activeSessions":1,"authenticated":true}` {
		t.Errorf("json = %s", raw)
	}
}

// activeSessions counts only sessions that are playing something; Jellyfin
// lists every connected client as a session.
func TestActiveSessionsCountsOnlySessionsWithANowPlayingItem(t *testing.T) {
	for name, tc := range map[string]struct {
		sessions jellyfintest.Sessions
		want     int
	}{
		"nobody connected":                 {jellyfintest.None, 0},
		"a connected client, not playing":  {jellyfintest.Idle, 0},
		"one playing":                      {jellyfintest.Playing, 1},
		"one idle and one playing (mixed)": {jellyfintest.Mixed, 1},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: tc.sessions}, jellyfintest.GoodKey)
			st := status(t, c)
			if got := sessions(t, st); got != tc.want {
				t.Errorf("sessions = %d, want %d", got, tc.want)
			}
			raw, _ := json.Marshal(st)
			if !strings.Contains(string(raw), `"activeSessions":`) {
				t.Errorf("a zero count must be present in JSON: %s", raw)
			}
		})
	}
}

func TestActiveSessionsIgnoresNullNowPlayingItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/System/Info/Public":
			_, _ = w.Write(jellyfintest.Fixture("info-public.json"))
		case "/System/Info":
			_, _ = w.Write(jellyfintest.Fixture("info.json"))
		default:
			_, _ = w.Write([]byte(`[{"NowPlayingItem":null},{},{"NowPlayingItem":{"Name":"x"}},{"NowPlayingItem":{"Name":"y"}}]`))
		}
	}))
	defer srv.Close()
	st := status(t, create(t, Config{URL: srv.URL, APIKey: "key-1234567"}))
	if got := sessions(t, st); got != 2 {
		t.Errorf("sessions = %d", got)
	}
}

func TestStatusNeedsSetupWhenTheWizardHasNotRunWithoutAskingForAKey(t *testing.T) {
	c, srv := clientFor(t, jellyfintest.Scenario{SetupCompleted: false, Sessions: jellyfintest.None}, "")
	st := status(t, c)
	if st.State != mediaserver.StateNeedsSetup || st.Authenticated || st.Version != version {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)setup isn't finished`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
	if paths := srv.Paths(); len(paths) != 1 || paths[0] != "/System/Info/Public" {
		t.Errorf("paths = %v", paths)
	}
}

func TestStatusOKButUnauthenticatedWithoutAKey(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.Playing}, "")
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Authenticated || st.Version != version || st.ActiveSessions != nil {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)no jellyfin api key`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
}

func TestStatusSaysTheKeyWasRejectedOn401(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.Playing}, "stale-key-value")
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Authenticated || st.ActiveSessions != nil {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)api key was rejected`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "stale-key-value") {
		t.Errorf("leak in %s", raw)
	}
}

func TestStatusStaysOKWhenDetailsFailForAnotherReason(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.None, AuthedFailure: 500}, jellyfintest.GoodKey)
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Authenticated {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)couldn't read jellyfin details`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
	mediaserver.CheckNotice(t, st.Notice)
}

func TestKeyIsSentInTheAuthorizationHeaderOnlyAndNeverToThePublicEndpoint(t *testing.T) {
	c, srv := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.None}, jellyfintest.GoodKey)
	status(t, c)
	var others int
	for _, r := range srv.Requests() {
		if r.Path == "/System/Info/Public" {
			if r.Key != "" {
				t.Error("key sent to the public endpoint")
			}
			continue
		}
		others++
		if r.Key != jellyfintest.GoodKey || r.EmbyToken != "" {
			t.Errorf("%s: key %q, X-Emby-Token %q", r.Path, r.Key, r.EmbyToken)
		}
	}
	if others != 2 {
		t.Errorf("authenticated requests = %d", others)
	}
}

func TestAuthorizationHeaderIsExactlyTheMediaBrowserForm(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/Info" {
			got = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	create(t, Config{URL: srv.URL, APIKey: "key-1234567"}).TestConnection(ctx)
	if got != `MediaBrowser Token="key-1234567"` {
		t.Errorf("Authorization = %q", got)
	}
}

func TestKeyNeverAppearsInAQueryString(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RawQuery)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	create(t, Config{URL: srv.URL, APIKey: jellyfintest.GoodKey}).TestConnection(ctx)
	for _, q := range seen {
		if q != "" {
			t.Errorf("query %q", q)
		}
	}
}

func TestStatusThrowsWhenTheServerCannotBeReachedAtAll(t *testing.T) {
	srv := jellyfintest.Start(t, jellyfintest.Scenario{SetupCompleted: true})
	url := srv.URL
	srv.Close()
	_, err := create(t, Config{URL: url, APIKey: jellyfintest.GoodKey}).Status(ctx)
	if err == nil || err.Error() != "jellyfin: connection refused" {
		t.Fatalf("err = %v", err)
	}
	var pe *core.ProviderError
	if !errors.As(err, &pe) {
		t.Errorf("not a ProviderError: %T", err)
	}
}

func TestTestConnectionOKWithAGoodKey(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true}, jellyfintest.GoodKey)
	if got := c.TestConnection(ctx); got != (core.ConnectionResult{OK: true, Version: version}) {
		t.Errorf("got %+v", got)
	}
}

func TestTestConnectionOKWithoutAKeyEvenBeforeSetup(t *testing.T) {
	c, srv := clientFor(t, jellyfintest.Scenario{SetupCompleted: false}, "")
	if got := c.TestConnection(ctx); !got.OK {
		t.Errorf("got %+v", got)
	}
	if len(srv.Paths()) != 1 {
		t.Errorf("paths = %v", srv.Paths())
	}
}

func TestTestConnectionFailsClearlyWithARejectedKey(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true}, "wrong-key-value")
	want := core.ConnectionResult{Version: version, Error: "jellyfin: API key rejected"}
	if got := c.TestConnection(ctx); got != want {
		t.Errorf("got %+v", got)
	}
}

func TestTestConnectionReportsOtherFailuresWithoutTheKey(t *testing.T) {
	c, _ := clientFor(t, jellyfintest.Scenario{SetupCompleted: true, AuthedFailure: 503}, jellyfintest.GoodKey)
	got := c.TestConnection(ctx)
	if got.OK || got.Error != "jellyfin /System/Info failed: 503 Service Unavailable" {
		t.Errorf("got %+v", got)
	}
}

func TestTestConnectionFailsClearlyWhenUnreachable(t *testing.T) {
	srv := jellyfintest.Start(t, jellyfintest.Scenario{SetupCompleted: true})
	url := srv.URL
	srv.Close()
	if got := create(t, Config{URL: url}).TestConnection(ctx); got != (core.ConnectionResult{Error: "jellyfin: connection refused"}) {
		t.Errorf("got %+v", got)
	}
}

// --- hostile and broken upstreams ---

func serveRaw(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func tuned(t *testing.T, url, key string, tune func(*core.HTTPOptions)) *client {
	t.Helper()
	c, err := newClient(Config{URL: url, APIKey: key}, "jellyfin", tune)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMalformedJSONIsAnErrorWithoutTheBody(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{<html>SECRET-BODY")) })
	_, err := tuned(t, url, "key-1234567", nil).Status(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") || strings.Contains(err.Error(), "SECRET-BODY") {
		t.Errorf("err = %v", err)
	}
	if res := tuned(t, url, "", nil).TestConnection(ctx); res.OK || strings.Contains(res.Error, "SECRET-BODY") {
		t.Errorf("res = %+v", res)
	}
}

func TestMalformedAuthenticatedJSONKeepsTheServerOK(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/Info/Public" {
			_, _ = w.Write(jellyfintest.Fixture("info-public.json"))
			return
		}
		_, _ = w.Write([]byte(`{"not":"a list"`))
	})
	st := status(t, tuned(t, url, "key-1234567", nil))
	if st.State != mediaserver.StateOK || st.Authenticated || !strings.Contains(st.Notice, "Couldn't read Jellyfin details") {
		t.Errorf("got %+v", st)
	}
	if strings.Contains(st.Notice, "not") && strings.Contains(st.Notice, "a list") {
		t.Errorf("body echoed: %q", st.Notice)
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
	})
	c := tuned(t, url, "", func(o *core.HTTPOptions) { o.MaxResponseBytes = 1024 })
	_, err := c.Status(ctx)
	if err == nil || !strings.Contains(err.Error(), "larger than 1024 bytes") {
		t.Errorf("err = %v", err)
	}
}

func TestTimeoutIsReportedNotHung(t *testing.T) {
	release := make(chan struct{})
	url := serveRaw(t, func(_ http.ResponseWriter, _ *http.Request) { <-release })
	defer close(release)
	c := tuned(t, url, "", func(o *core.HTTPOptions) { o.Timeout = 100 * time.Millisecond })
	start := time.Now()
	_, err := c.Status(ctx)
	if err == nil || !strings.Contains(err.Error(), "timed out after 100ms") {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("did not time out promptly")
	}
}

// --- provider definition, config and the runtime ---

func TestDefinition(t *testing.T) {
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get("jellyfin")
	if d.Role != core.RoleMediaServer || d.DisplayName != "Jellyfin" {
		t.Errorf("def = %+v", d)
	}
	if keys := d.SecretKeys(); len(keys) != 1 || keys[0] != "apiKey" {
		t.Errorf("secret keys = %v", keys)
	}
	if caps, ok := d.Capabilities.(mediaserver.Capabilities); !ok || !caps.Sessions {
		t.Errorf("capabilities = %#v", d.Capabilities)
	}
	if _, err := d.Decode(map[string]any{"url": "http://jellyfin:8096"}); err != nil {
		t.Errorf("a missing key must be accepted: %v", err)
	}
	if _, err := d.Decode(map[string]any{"apiKey": "k"}); err == nil || !strings.Contains(err.Error(), "url") {
		t.Errorf("a missing url must be rejected: %v", err)
	}
	if _, err := d.Decode(map[string]any{"url": "http://jellyfin:8096", "apiKey": ""}); err == nil {
		t.Error("an empty key must be rejected")
	}
}

type staticSource []core.Instance

func (s staticSource) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func TestThroughTheRuntime(t *testing.T) {
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	srv := jellyfintest.Start(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.None})
	mk := func(config map[string]any) *core.Runtime {
		return core.NewRuntime(reg, staticSource{{ID: "jellyfin", ProviderID: "jellyfin", Name: "Jellyfin", Enabled: true, Config: config}})
	}
	list, _ := mk(map[string]any{"url": "http://jellyfin:8096"}).ListInstances(ctx)
	if list[0].Status != core.StatusReady {
		t.Errorf("status = %s", list[0].Status)
	}
	list, _ = mk(map[string]any{"apiKey": "k"}).ListInstances(ctx)
	if list[0].Status != core.StatusInvalidConfig || !strings.Contains(list[0].Error, "url") {
		t.Errorf("bad = %+v", list[0])
	}
	clients, err := mk(map[string]any{"url": srv.URL, "apiKey": jellyfintest.GoodKey}).Clients(ctx, core.RoleMediaServer)
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	if st := status(t, clients[0].Client.(mediaserver.Client)); st.State != mediaserver.StateOK || !st.Authenticated {
		t.Errorf("status = %+v", st)
	}
}

func TestEnvironmentMappingFeedsThisProvider(t *testing.T) {
	reg, _ := core.NewRegistry(Definition())
	srv := jellyfintest.Start(t, jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.Mixed})
	src := core.NewEnvConfigSource(map[string]string{"JELLYFIN_URL": srv.URL, "JELLYFIN_API_KEY": jellyfintest.GoodKey})
	clients, err := core.NewRuntime(reg, src).Clients(ctx, core.RoleMediaServer)
	if err != nil || len(clients) != 1 || clients[0].Instance.ID != "jellyfin" {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	if got := sessions(t, status(t, clients[0].Client.(mediaserver.Client))); got != 1 {
		t.Errorf("sessions = %d", got)
	}
}

// The fake's fixtures must stay byte for byte the real captures the Next app's
// tests use.
func TestFixturesMatchTheTypeScriptCopies(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "..", "lib", "providers", "media-server", "__fixtures__", "jellyfin")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("TypeScript fixtures not in this checkout")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		want, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // a fixture directory in this repo
		if err != nil {
			t.Fatal(err)
		}
		if got := jellyfintest.Fixture(e.Name()); !bytes.Equal(got, want) {
			t.Errorf("%s differs from the TypeScript copy", e.Name())
		}
	}
}
