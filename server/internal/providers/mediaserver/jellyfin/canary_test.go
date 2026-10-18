package jellyfin

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/jellyfin/jellyfintest"
)

// The canary sweep: plant a secret as the key (and as URL credentials), make
// Jellyfin fail in every way it can, and prove the secret appears in no status,
// connection result, error or log line the package produced.
func TestCanarySweep(t *testing.T) {
	s := hostiletest.NewSweep(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tune := func(o *core.HTTPOptions) {
		o.Logger = logger
		o.Timeout = 300 * time.Millisecond
	}
	// The hostile upstreams also get a tiny size limit; the real fakes answer
	// with bodies larger than that.
	tuneSmall := func(o *core.HTTPOptions) {
		tune(o)
		o.MaxResponseBytes = 64
	}
	record := func(label string, c *client) {
		for i := range 2 { // a second pass proves nothing is remembered or cached
			st, err := c.Status(ctx)
			s.AddErr(fmt.Sprintf("%s status %d", label, i), err)
			s.AddJSON(fmt.Sprintf("%s status %d", label, i), st)
			s.Add(fmt.Sprintf("%s fmt %d", label, i), fmt.Sprintf("%v|%+v", st, st))
			res := c.TestConnection(ctx)
			s.AddJSON(fmt.Sprintf("%s test %d", label, i), res)
		}
	}

	// Upstreams that echo the canary in every place a server can.
	for _, behaviour := range hostiletest.Behaviours {
		addr := hostiletest.Hostile(t, behaviour)
		base := "http://user:" + url.QueryEscape(hostiletest.Pass) + "@" + addr + "/jellyfin-" + url.PathEscape(hostiletest.Key)
		c, err := newClient(Config{URL: base, APIKey: hostiletest.Key}, "jellyfin-"+behaviour, tuneSmall)
		if err != nil {
			t.Fatal(err)
		}
		record(behaviour, c)
	}

	// The real fake with a canary key: the 401 path, and a 500.
	for _, tc := range []struct {
		sc   jellyfintest.Scenario
		want string // the path the scenario must exercise
	}{
		{jellyfintest.Scenario{SetupCompleted: true, Sessions: jellyfintest.Mixed}, "API key was rejected"},
		{jellyfintest.Scenario{SetupCompleted: true, AuthedFailure: 500}, "Couldn't read Jellyfin details"},
		{jellyfintest.Scenario{SetupCompleted: false}, "Setup isn't finished"},
	} {
		srv := jellyfintest.Start(t, tc.sc)
		c, _ := newClient(Config{URL: srv.URL, APIKey: hostiletest.Key}, "jellyfin", tune)
		record("fake", c)
		if st, _ := c.Status(ctx); !strings.Contains(st.Notice, tc.want) {
			t.Fatalf("scenario %+v: notice %q, want %q (the sweep would prove nothing)", tc.sc, st.Notice, tc.want)
		}
		// The key reaches the server only in the Authorization header, never
		// to the public endpoint.
		for _, r := range srv.Requests() {
			if r.Path == "/System/Info/Public" && r.Key != "" {
				t.Error("key sent to the public endpoint")
			}
		}
	}

	// Unreachable, and a name that does not resolve, both carrying the canary.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	for _, base := range []string{
		"http://user:" + hostiletest.Pass + "@" + dead,
		"http://user:" + hostiletest.Pass + "@canary-key.invalid",
	} {
		c, _ := newClient(Config{URL: base, APIKey: hostiletest.Key}, "jellyfin", tune)
		record("dead", c)
	}

	// A base URL that cannot be used: the error must not echo it.
	_, err := newClient(Config{URL: "ftp://user:" + hostiletest.Pass + "@h/" + hostiletest.Key, APIKey: hostiletest.Key}, "jellyfin", tune)
	s.AddErr("bad base", err)
	if err == nil {
		t.Fatal("expected an error for a bad base URL")
	}
	b := &brokenClient{err: err}
	st, err := b.Status(ctx)
	s.AddErr("broken", err)
	s.AddJSON("broken", st)
	s.AddJSON("broken test", b.TestConnection(ctx))

	s.Add("log", logs.String())
	if !strings.Contains(logs.String(), "upstream request failed") {
		t.Fatal("expected the failures to be logged")
	}
	s.Check()
}

// Config (with the token) must not leak through formatting of the instance.
func TestConfigStaysOutOfFormattedInstances(t *testing.T) {
	inst := core.Instance{ID: "jellyfin", ProviderID: "jellyfin", Name: "Jellyfin", Enabled: true,
		Config: map[string]any{"url": "http://h", "apiKey": hostiletest.Key}}
	s := hostiletest.NewSweep(t)
	s.Add("fmt", fmt.Sprintf("%v|%+v|%#v", inst, inst, inst))
	s.AddJSON("json", inst)
	s.Check()
}

func TestTheDetectorCanFail(t *testing.T) {
	if hostiletest.Leaked("x "+hostiletest.Key) == "" {
		t.Fatal("detector missed the plain canary")
	}
}
