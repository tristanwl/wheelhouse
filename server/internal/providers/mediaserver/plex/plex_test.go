package plex

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
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex/plextest"
)

const version = "1.43.3.10828-00f62d37d"

var ctx = context.Background()

func create(t *testing.T, cfg Config) mediaserver.Client {
	t.Helper()
	def := Definition()
	reg, err := core.NewRegistry(def)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get("plex")
	raw := map[string]any{"url": cfg.URL}
	if cfg.Token != "" {
		raw["token"] = cfg.Token
	}
	decoded, err := d.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d.Create(decoded, core.ProviderContext{InstanceID: "plex", InstanceName: "Plex"}).(mediaserver.Client)
}

// clientFor starts a fake. token "" means no token.
func clientFor(t *testing.T, sc plextest.Scenario, token string) (mediaserver.Client, *plextest.Server) {
	t.Helper()
	srv := plextest.Start(t, sc)
	return create(t, Config{URL: srv.URL, Token: token}), srv
}

func status(t *testing.T, c mediaserver.Client) mediaserver.Status {
	t.Helper()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestContract(t *testing.T) {
	mediaserver.RunContract(t, "plex", func(t *testing.T) mediaserver.Client {
		c, _ := clientFor(t, plextest.Scenario{Claimed: true, Active: true}, plextest.GoodToken)
		return c
	})
}

func TestStatusHealthyAuthenticated(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true, Active: true}, plextest.GoodToken)
	got := status(t, c)
	want := mediaserver.Status{
		State: mediaserver.StateOK, Name: "Example Plex", Version: version,
		ActiveSessions: mediaserver.Ptr(2), Authenticated: true,
	}
	if got.State != want.State || got.Name != want.Name || got.Version != want.Version ||
		got.Authenticated != want.Authenticated || got.Notice != "" ||
		got.ActiveSessions == nil || *got.ActiveSessions != 2 {
		t.Errorf("got %+v", got)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `{"state":"ok","name":"Example Plex","version":"`+version+`","activeSessions":2,"authenticated":true}` {
		t.Errorf("json = %s", raw)
	}
}

func TestStatusIdleIsZeroSessions(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true}, plextest.GoodToken)
	st := status(t, c)
	if st.ActiveSessions == nil || *st.ActiveSessions != 0 {
		t.Errorf("sessions = %v", st.ActiveSessions)
	}
	raw, _ := json.Marshal(st)
	if !strings.Contains(string(raw), `"activeSessions":0`) {
		t.Errorf("zero must be present in JSON: %s", raw)
	}
}

func TestStatusNeedsSetupWhenUnclaimedWithoutAskingForAToken(t *testing.T) {
	c, srv := clientFor(t, plextest.Scenario{Claimed: false}, "")
	st := status(t, c)
	if st.State != mediaserver.StateNeedsSetup || st.Authenticated {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)not claimed`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
	if paths := srv.Paths(); len(paths) != 1 || paths[0] != "/identity" {
		t.Errorf("paths = %v", paths)
	}
}

func TestStatusUnclaimedEvenWithATokenOnlyAsksIdentity(t *testing.T) {
	c, srv := clientFor(t, plextest.Scenario{Claimed: false}, plextest.GoodToken)
	if st := status(t, c); st.State != mediaserver.StateNeedsSetup {
		t.Errorf("state = %q", st.State)
	}
	if paths := srv.Paths(); len(paths) != 1 {
		t.Errorf("paths = %v", paths)
	}
}

func TestStatusOKButUnauthenticatedWithoutAToken(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true, Active: true}, "")
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Authenticated || st.Version != version {
		t.Errorf("got %+v", st)
	}
	if st.ActiveSessions != nil {
		t.Errorf("sessions = %d", *st.ActiveSessions)
	}
	if !regexp.MustCompile(`(?i)no plex token`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
}

func TestStatusSaysTheTokenWasRejectedOn401WithAnHTMLPage(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true, Active: true}, "stale-token-value")
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Authenticated || st.ActiveSessions != nil {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)token was rejected`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
	if st.Version != version {
		t.Errorf("version = %q", st.Version)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "stale-token-value") || strings.Contains(string(raw), "Unauthorized") {
		t.Errorf("leak in %s", raw)
	}
}

func TestStatusStaysOKWhenDetailsFailForAnotherReason(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true, AuthedFailure: 500}, plextest.GoodToken)
	st := status(t, c)
	if st.State != mediaserver.StateOK || st.Authenticated {
		t.Errorf("got %+v", st)
	}
	if !regexp.MustCompile(`(?i)couldn't read plex details`).MatchString(st.Notice) {
		t.Errorf("notice = %q", st.Notice)
	}
	if !strings.Contains(st.Notice, "500") {
		t.Errorf("notice should carry the status: %q", st.Notice)
	}
	mediaserver.CheckNotice(t, st.Notice)
}

func TestTokenIsSentAsAHeaderAndNeverToIdentity(t *testing.T) {
	c, srv := clientFor(t, plextest.Scenario{Claimed: true}, plextest.GoodToken)
	status(t, c)
	reqs := srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %v", reqs)
	}
	for _, r := range reqs {
		switch {
		case r.Path == "/identity" && r.Token != "":
			t.Errorf("token sent to /identity")
		case r.Path != "/identity" && r.Token != plextest.GoodToken:
			t.Errorf("%s without the token: %q", r.Path, r.Token)
		}
	}
}

func TestTokenNeverAppearsInAQueryString(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RawQuery)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := create(t, Config{URL: srv.URL, Token: plextest.GoodToken})
	c.TestConnection(ctx)
	for _, q := range seen {
		if q != "" {
			t.Errorf("query %q", q)
		}
	}
}

func TestStatusThrowsWhenTheServerCannotBeReachedAtAll(t *testing.T) {
	srv := plextest.Start(t, plextest.Scenario{Claimed: true})
	url := srv.URL
	srv.Close()
	c := create(t, Config{URL: url, Token: plextest.GoodToken})
	_, err := c.Status(ctx)
	if err == nil || err.Error() != "plex: connection refused" {
		t.Fatalf("err = %v", err)
	}
	var pe *core.ProviderError
	if !errors.As(err, &pe) {
		t.Errorf("not a ProviderError: %T", err)
	}
}

func TestTestConnectionOKWithAGoodToken(t *testing.T) {
	c, srv := clientFor(t, plextest.Scenario{Claimed: true}, plextest.GoodToken)
	if got := c.TestConnection(ctx); got != (core.ConnectionResult{OK: true, Version: version}) {
		t.Errorf("got %+v", got)
	}
	if paths := srv.Paths(); len(paths) != 2 || paths[0] != "/identity" || paths[1] != "/" {
		t.Errorf("paths = %v", paths)
	}
}

func TestTestConnectionOKWithoutAToken(t *testing.T) {
	c, srv := clientFor(t, plextest.Scenario{Claimed: false}, "")
	if got := c.TestConnection(ctx); !got.OK {
		t.Errorf("got %+v", got)
	}
	if len(srv.Paths()) != 1 {
		t.Errorf("paths = %v", srv.Paths())
	}
}

func TestTestConnectionFailsClearlyWithARejectedToken(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true}, "wrong-token-value")
	want := core.ConnectionResult{OK: false, Version: version, Error: "plex: token rejected"}
	if got := c.TestConnection(ctx); got != want {
		t.Errorf("got %+v", got)
	}
}

func TestTestConnectionReportsOtherFailuresWithoutTheToken(t *testing.T) {
	c, _ := clientFor(t, plextest.Scenario{Claimed: true, AuthedFailure: 503}, plextest.GoodToken)
	got := c.TestConnection(ctx)
	if got.OK || got.Error != "plex / failed: 503 Service Unavailable" {
		t.Errorf("got %+v", got)
	}
}

func TestTestConnectionFailsClearlyWhenUnreachable(t *testing.T) {
	srv := plextest.Start(t, plextest.Scenario{Claimed: true})
	url := srv.URL
	srv.Close()
	c := create(t, Config{URL: url})
	if got := c.TestConnection(ctx); got != (core.ConnectionResult{Error: "plex: connection refused"}) {
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

func tuned(t *testing.T, url, token string, tune func(*core.HTTPOptions)) *client {
	t.Helper()
	c, err := newClient(Config{URL: url, Token: token}, "plex", tune)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMalformedJSONFromIdentityIsAnErrorWithoutTheBody(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{<html>SECRET-BODY")) })
	_, err := tuned(t, url, "tok-1234567", nil).Status(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") || strings.Contains(err.Error(), "SECRET-BODY") {
		t.Errorf("err = %v", err)
	}
	if res := tuned(t, url, "", nil).TestConnection(ctx); res.OK || strings.Contains(res.Error, "SECRET-BODY") {
		t.Errorf("res = %+v", res)
	}
}

func TestJSONWithoutAMediaContainerIsAnError(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"hello":"world"}`)) })
	_, err := tuned(t, url, "", nil).Status(ctx)
	if err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Errorf("err = %v", err)
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

func TestCanceledContextStopsTheCall(t *testing.T) {
	release := make(chan struct{})
	url := serveRaw(t, func(_ http.ResponseWriter, _ *http.Request) { <-release })
	defer close(release)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := tuned(t, url, "", nil).Status(cctx)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("err = %v", err)
	}
}

// --- provider definition, config and the runtime ---

func TestDefinition(t *testing.T) {
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get("plex")
	if d.Role != core.RoleMediaServer || d.DisplayName != "Plex" {
		t.Errorf("def = %+v", d)
	}
	if keys := d.SecretKeys(); len(keys) != 1 || keys[0] != "token" {
		t.Errorf("secret keys = %v", keys)
	}
	if caps, ok := d.Capabilities.(mediaserver.Capabilities); !ok || !caps.Sessions {
		t.Errorf("capabilities = %#v", d.Capabilities)
	}
	if _, err := d.Decode(map[string]any{"url": "http://plex:32400"}); err != nil {
		t.Errorf("a missing token must be accepted: %v", err)
	}
	if _, err := d.Decode(map[string]any{"token": "t"}); err == nil || err.Error() != "URL is required" {
		t.Errorf("a missing url must be rejected: %v", err)
	}
	if _, err := d.Decode(map[string]any{"url": "http://plex:32400", "token": ""}); err == nil {
		t.Error("an empty token must be rejected")
	}
	if _, err := d.Decode(map[string]any{"url": "nope"}); err == nil {
		t.Error("a bad url must be rejected")
	}
	raw, _ := json.Marshal(d)
	if !strings.Contains(string(raw), `"capabilities":{"sessions":true}`) || !strings.Contains(string(raw), `"x-secret":true`) {
		t.Errorf("description = %s", raw)
	}
}

type staticSource []core.Instance

func (s staticSource) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func TestThroughTheRuntime(t *testing.T) {
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	srv := plextest.Start(t, plextest.Scenario{Claimed: true})

	mk := func(config map[string]any) *core.Runtime {
		return core.NewRuntime(reg, staticSource{{ID: "plex", ProviderID: "plex", Name: "Plex", Enabled: true, Config: config}})
	}
	list, _ := mk(map[string]any{"url": "http://plex:32400"}).ListInstances(ctx)
	if list[0].Status != core.StatusReady {
		t.Errorf("status = %s", list[0].Status)
	}
	list, _ = mk(map[string]any{"token": "t"}).ListInstances(ctx)
	if list[0].Status != core.StatusInvalidConfig || list[0].Error != "URL is required" {
		t.Errorf("bad = %+v", list[0])
	}

	clients, err := mk(map[string]any{"url": srv.URL, "token": plextest.GoodToken}).Clients(ctx, core.RoleMediaServer)
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	client := clients[0].Client.(mediaserver.Client)
	if st := status(t, client); st.State != mediaserver.StateOK || !st.Authenticated {
		t.Errorf("status = %+v", st)
	}
}

func TestEnvironmentMappingFeedsThisProvider(t *testing.T) {
	reg, _ := core.NewRegistry(Definition())
	srv := plextest.Start(t, plextest.Scenario{Claimed: true, Active: true})
	src := core.NewEnvConfigSource(map[string]string{"PLEX_URL": srv.URL, "PLEX_TOKEN": plextest.GoodToken})
	clients, err := core.NewRuntime(reg, src).Clients(ctx, core.RoleMediaServer)
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	if clients[0].Instance.ID != "plex" {
		t.Errorf("id = %q", clients[0].Instance.ID)
	}
	if st := status(t, clients[0].Client.(mediaserver.Client)); *st.ActiveSessions != 2 {
		t.Errorf("sessions = %v", st.ActiveSessions)
	}
}

// The fake's fixtures must stay byte for byte the real captures in
// acceptance/fakes, which are the source of truth.
func TestFixturesMatchTheAcceptanceCopies(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "..", "acceptance", "fakes", "plex")
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
		if got := plextest.Fixture(e.Name()); !bytes.Equal(got, want) {
			t.Errorf("%s differs from the acceptance copy", e.Name())
		}
	}
}
